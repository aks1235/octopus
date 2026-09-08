# 数据库与缓存一致性指南

> GORM 模式、缓存层约定、迁移机制。本文件聚焦 Octopus 后端「内存缓存 + DB」双层结构的可执行契约。

---

## 概览

Octopus 大量热路径读内存缓存(`op` 包内 `cache.New[K,V]`),写路径落库后刷新缓存。**缓存与 DB 的一致性由刷新函数保证**,刷新遗漏或就地改缓存对象会导致下一周期读到脏值。

- ORM:GORM,AutoMigrate 增量加列(`internal/db/db.go:55-79`)
- 缓存:`internal/utils/cache/cache.go` `cache[K,V]`,`Get`/`GetAll` 返回值拷贝(泛型 V 值类型)
- DB 驱动:SQLite(默认)/MySQL/PostgreSQL,`OCTOPUS_DATABASE_TYPE` 切换

---

## 缓存填充与轮询代价契约

### 契约:GroupList 必须按用途分流

**What**:`op.GroupList` 返回前会遍历每个 group 的 Items,用 `channelCache` 填 `ChannelName`/`ChannelEnabled`(非持久化字段 `gorm:"-"`)。**该填充有 O(全量 GroupItem) 开销**。

**Why**:HTTP `getGroupList`(`internal/server/handlers/group.go:42-49`)需要渠道名展示,必须填充。但内部逻辑(`ChannelAutoGroup`)只关心 group 名称/匹配规则,不需要渠道名,误用会导致同步热路径重复无谓遍历。

**签名**:
```go
// internal/op/group.go
func GroupList(ctx context.Context) ([]model.Group, error)     // 填充 ChannelName/ChannelEnabled,供 HTTP
func GroupListRaw(ctx context.Context) ([]model.Group, error)  // 不填充,供内部热路径
```

**Good/Base/Bad**:
- Good:HTTP handler 调 `GroupList`(`handlers/group.go:43`)。
- Good:`ChannelAutoGroup` 调 `GroupListRaw`(`helper/channel.go:68`)。
- Bad:内部热路径调 `GroupList` → 每次 AutoSync 渠道同步都重建全量 items slice 填无效字段。

### 契约:填充必须重建 slice,不污染缓存

**What**:`GroupList` 填充时**必须**对 `group.Items` 重建新 slice 并逐条拷贝赋值,不能就地修改缓存对象。

**Why**:`groupCache.GetAll()` 返回的 `model.Group` 是 map 值拷贝,但其 `Items []GroupItem` slice header 仍指向缓存底层数组。就地改 `item.ChannelName`(非持久化 `gorm:"-"` 字段)会写回缓存对象内部 slice,下次读取带脏值。

**正确**(internal/op/group.go:17-37):
```go
items := make([]model.GroupItem, len(group.Items))
for i, item := range group.Items {
    item := item            // 局部拷贝
    if ch, ok := channelCache.Get(item.ChannelID); ok {
        item.ChannelName = ch.Name
        item.ChannelEnabled = ch.Enabled
    } else {
        item.ChannelName = ""      // 渠道已删除
        item.ChannelEnabled = false
    }
    items[i] = item
}
group.Items = items                 // 整体替换 slice header
```

### 契约:relay_logs 历史查询走 DB,不走 relayLogCache

**What**:`op.RelayLogList` 走「内存 `relayLogCache` 优先,不足补 DB」(`op/log.go:197`),但 `relayLogCache` 只是 **≤20 条的 flush 缓冲**(`relayLogMaxSize=20` at `op/log.go:17`):新日志累计到 20 条触发 `relayLogFlushToDB` 批量写库后清空(`op/log.go:88`)。它**不**是「保留期日志的内存镜像」。

**Why**:保留期(`relay_log_keep_period`,默认 7 天)是 **DB 的删除线**(`relayLogCleanup` 删 `time < cutoff`,`op/log.go:179`),与缓存无关。任何「按历史/按渠道查 attempts 明细」类需求若误以为缓存里有历史,或照搬 `RelayLogList` 的缓存优先模式,会只拿到最近约 20 条、丢掉 7 天历史大头。

**契约**:
- 需要保留期历史数据的查询 → **直接查 `relay_logs` 表**(经 `db.GetDB().WithContext(ctx)`),**不碰 `relayLogCache`**。
- 只有「最近一屏实时日志」才适合 `RelayLogList` 的缓存优先模式。
- `relay_log_keep_period` 通过 `SettingGetInt(model.SettingKeyRelayLogKeepPeriod)` 读取(单位:天),`<=0` 表示无时间过滤(但仍受其它上界保护)。照 `relayLogCleanup` 的 `cutoff = now - keep*24h` 写法。

**JSON 字段展开查询**(attempts 是 `gorm:"serializer:json"`,无法 SQL 直接展开):
- 「先限缩再展开」:SQL `attempts LIKE '%"channel_id":<id>%'` 粗筛收窄行集(`serializer:json` 序列化无空格、整数字段无引号,模板须匹配 jsoniter 实际字节),再 Go 层 `json.Unmarshal` 或直接遍历 GORM 已反序列化的 `[]ChannelAttempt` 做精确过滤(`ChannelID == int`)。
- 三库统一走 LIKE 粗筛 + Go 展开,不依赖 dialect jsonb(SQLite 无 jsonb,PG/MySQL 即便有也避免 dialect 分叉)。
- 粗筛可能误匹配前缀相同的大 ID(如查 90 误匹配 900),由 Go 层精确过滤兜底——粗筛只求缩小范围。
- 上界保护:粗筛行数设上限(如 5000),超则 `truncated=true` 标志,前端提示「仅展示最近 N 条」,不静默截断。

**实例**:`RelayLogAttemptsByChannel(ctx, channelID, page, pageSize)`(`op/log.go`)按渠道查 attempts 明细;`RelayLogAttemptsByChannel` 走 DB 直查,展开过滤后内存分页,`total=len(matches)` 精确。

---

## 渠道-分组模型一致性三层维护契约

「渠道 → 分组(GroupItem)」模型一致性由三层协同维护,缺一不可。每层职责互不重叠。

### 第 1 层:同步失败标记 + 自动禁用/解禁(子任务 A)

**场景**:上游失效时 `FetchModels` 返回 err,原实现静默 `continue`(`internal/task/sync.go`),运维无感知。

**签名**:
```go
// internal/op/channel.go:393
func ChannelUpdateSyncStatus(ctx context.Context, id int, syncFailCount int,
    lastSyncError string, lastSyncAt *time.Time, enabled *bool, autoDisabled *bool) error
```
`enabled`/`autoDisabled` 为 `nil` 表示不更新该字段。

**Channel 结构新增字段**(`internal/model/channel.go:36-40`,`gorm:"-"` 之外的持久化列):
| 字段 | 类型 | gorm | 语义 |
|---|---|---|---|
| `SyncFailCount` | `int` | `default:0` | 连续失败次数,成功归零 |
| `LastSyncError` | `string` | `type:text` | 最近失败错误(截断 500 字符) |
| `LastSyncAt` | `*time.Time` | — | 最近同步尝试时间 |
| `AutoDisabled` | `bool` | `default:false` | 是否因连续失败被自动禁用 |

**设置键**(`internal/model/setting.go:22,44,52`):
- `SettingKeySyncFailThreshold = "sync_fail_threshold"`,默认 `"3"`,整数校验。

**验证与错误矩阵**:
| 条件 | 行为 |
|---|---|
| `FetchModels` 返回 err | `SyncFailCount+1`、写 `LastSyncError`/`LastSyncAt`;`failCount>=threshold` 时 `enabled=false`+`auto_disabled=true` |
| 同步成功 | `SyncFailCount=0`、`LastSyncError=""`;若 `channel.AutoDisabled && !channel.Enabled` 则 `enabled=true`+`auto_disabled=false`(自动解禁) |
| `SettingGetInt` 失败 | threshold 取默认 3,不阻断同步主流程 |
| 运维手动 `ChannelUpdate{Enabled}` 或 `ChannelEnabled()` | 同步把 `auto_disabled=false`(运维接管清自动标记) |

**关键不变量**:
- **自动解禁只对 `AutoDisabled && !Enabled` 的渠道生效**。运维手动禁用的渠道(`AutoDisabled=false`)绝不被同步任务自动翻活。见 `internal/task/sync.go:95`。
- 运维启停路径(`ChannelUpdate` 的 `req.Enabled` 分支 `op/channel.go:246-250`、`ChannelEnabled` `op/channel.go:375-389`)都必须清 `auto_disabled=false`。

**错误** vs **正确**:
```go
// ❌ 错误:解禁用 !channel.Enabled 判定,会误翻活运维手动禁用的渠道
if !channel.Enabled { enabledPtr = &t }

// ✅ 正确:同时要求 AutoDisabled=true,只解禁被自动禁用的渠道
if channel.AutoDisabled && !channel.Enabled { enabledPtr = &t; autoDisabledPtr = &f }
```

### 第 2 层:孤儿对账兜底(子任务 B)

**场景**:`ChannelAutoGroup` 只增不减(`internal/helper/channel.go:61-141`,OnConflict DoNothing)。`AutoSync=false` 渠道被人工改 `Channel.Model` 后,旧 GroupItem 无清理路径。删渠道(`ChannelDel` `op/channel.go:413`)虽级联清 GroupItem,但全局无兜底。

**签名**:
```go
// internal/task/reconcile.go:17  (新建文件)
func GroupItemReconcileTask()
// internal/op/group.go:376  (新增全量查询)
func GroupItemListAll(ctx context.Context) ([]model.GroupItem, error)
```

**设置键**:`SettingKeyGroupReconcileInterval = "group_reconcile_interval"`(`setting.go:23,44,52`),默认 `"60"` 分钟,整数校验。`runOnStart=true`(`task/init.go:75`)启动即清存量。

**清理判定**(只按两条,不据启停):
1. 渠道行不存在(`channel_id` 不在 `channelCache`)→ 孤儿。
2. `model_name` 不在该渠道 `Model+CustomModel` 拆分集合内 → 孤儿。

**关键不变量**:
- **不禁用渠道**:A 置 `Enabled=false` 的渠道,**对账不清理其 GroupItem**(`ChannelList` 不过滤 Enabled,禁用渠道仍在 `existChID` 集合中)。禁用 ≠ 删除。
- 幂等:连续两次执行,第二次 `toDel` 为空。
- 用 `GroupItemBatchDelByChannelAndModels`(`op/group.go:328`)批删,内部已 `groupRefreshCacheByIDs`。

### 第 3 层:GroupItem DTO 填充(子任务 C)

**场景**:后端 `model.GroupItem` 只回 `channel_id`,前端本地映射兜底成 `Channel ${id}`,渠道改名/失效后显示无意义名。

**契约**:`GroupItem` 加非持久化字段(`internal/model/group.go:31-33`):
```go
ChannelName    string `json:"channel_name,omitempty" gorm:"-"`
ChannelEnabled bool   `json:"channel_enabled" gorm:"-"`
```
`op.GroupList` 填充(见上文缓存契约)。渠道已删 → `ChannelName=""`+`ChannelEnabled=false`。

**前端契约**(见前端 spec):按 DTO `channel_name`/`channel_enabled` 渲染,**禁止**本地 `useModelChannelList` 双键映射 + `Channel ${id}` 兜底。

---

## 迁移约定

- 新增列**必须**带 `default` 或可空,避免旧数据 NOT NULL 报错。示例:`AutoDisabled bool ... gorm:"default:false"`。
- `gorm:"-"` 字段不入库、不进 AutoMigrate,仅运行时填充。
- 复合唯一索引用 GORM tag:`gorm:"not null;index:idx_group_channel_model,unique"`(见 `model/group.go:25-27`)。
- AutoMigrate 仅 ADD COLUMN,不删列、不改列语义——向后兼容硬约束。

---

## 设置键命名约定

- 常量 `SettingKeyXxx = "xxx"`,蛇形小写,注释标单位(小时/分钟/秒/次数)。
- 默认值进 `DefaultSettings()`(`setting.go:29-45`),每条带行尾注释。
- 整数型键**必须**加入 `Validate()` 的 `strconv.Atoi` 校验 case(`setting.go:46-55`)。
- 新增键后,`task/init.go` 用 `op.SettingGetInt` 读取,err 时取默认值不阻断主流程。

---

## 常见错误

### 错误:就地改缓存对象的非持久化字段

**症状**:GroupList 填充后,下次读取缓存 group 的 items 带上次的 `ChannelName` 脏值。
**原因**:`groupCache.GetAll()` 返回的 Group 的 `Items` slice header 指向缓存底层数组,就地改会写回。
**修复**:重建 slice + 局部拷贝赋值(见上文"填充必须重建 slice")。
**预防**:任何对缓存返回对象的 slice 字段做填充,都必须重建 slice。

### 错误:把展示填充用在热路径

**症状**:同步任务每渠道都遍历全量 items 重建。
**原因**:`ChannelAutoGroup` 误调填充版 `GroupList`。
**修复**:调 `GroupListRaw`。

### 错误:解禁条件不区分运维手动禁用

**症状**:运维手动禁用的渠道被同步任务自动翻活。
**原因**:`!channel.Enabled` 判定解禁,无 `AutoDisabled` 标记区分。
**修复**:解禁条件 `AutoDisabled && !Enabled`;运维启停一律清 `auto_disabled=false`。

### 错误:channels.base_url 带 `/v1` 后缀(2026-09-08 迁移实测)

**症状**:渠道请求全部失败,但客户端表现为**无限挂起不报错**;渠道 `request_failed` 统计持续增长。
**原因**:axonhub 出站转换器对 `BaseURL + EndpointPath` **朴素拼接**;v2 三个协议路径列(openai_chat_completion_path 等)自带 `/v1` 前缀,base_url 再带 `/v1` 后缀即拼出 `/v1/v1/chat/completions`,上游 404。叠加 relay 的设计(全成员失败后无限等待重试,不向客户端返回错误,见 `internal/relay/handler.go` 的 for 循环),故障被掩盖成挂死。
**修复**:base_url 存**站点根**(如 `https://new.xkool.cfd`),版本前缀只存在于路径列。迁移脚本 `scripts/migrate_v1_to_v2.py` 已做归一化(去结尾 `/v1`)。
**预防**:新增/编辑渠道时校验 base_url 不得以 `/v1` 结尾;排查"客户端挂死"先看渠道 `request_failed` 是否在涨,再用本地 echo 服务器承接出站请求看实际拼接路径。

---

## 相关

- 前端渲染契约 → 前端 spec「分组列表渠道展示」
- 熔断器(已有,`balancer` 包):运行时熔断 vs 本文档持久层自动禁用——两者正交,熔断是内存级临时摘除,自动禁用是持久层 `Enabled=false`。
