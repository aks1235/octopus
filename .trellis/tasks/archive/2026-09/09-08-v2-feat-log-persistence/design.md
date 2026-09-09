# 设计:日志持久化包(dev-v2)

> 依据:`research/upstream-relay-hook.md`、`research/frontend-log-modules.md`(两侧全链路调研,带 file:line);ADR-0002/0005。

## 1. 总体形态:双数据形状并存,互不改造

| | RequestState(上游,不动) | RelayLog(本任务,新增) |
|---|---|---|
| 生命周期 | 请求进行中实时演进,终态后内存最多留 50 条 | 请求终态后定稿落库,按保留期留存 |
| 消费方 | 日志页实时区(SSE `overview/stream`)、中止、body 拉取 | 日志页历史区(/list 筛选分页)、详情(/:id)、渠道调用详情 |
| ID | 进程内 atomic 序号(uint64) | Snowflake(int64) |

两套 ID 互不映射;实时区与历史区是两个独立视图。**上游 5 个既有接口与 state.go 零改动**(白拿项回归即 AC5)。

## 2. 数据模型与迁移

### 2.1 RelayLog 列形状 = fork 完整形状

`internal/model/log.go` 自 dev 平移:RelayLog(全部列,含 `user_agent`/`client_name`/`reasoning_effort`/`cached_tokens`/`cache_creation_tokens`/`debug_content`/`attempts` JSON serializer/`total_attempts`)、ChannelAttempt、ChannelAttemptDetail、RelayLogDebugContent。

- **列全量保留的理由**:迁移走「列取交集」,fork 库 1284 条零丢弃(AC6);user_agent/client_name/reasoning_effort 本任务只建列不写入(空值),任务5 补逻辑。
- **AttemptStatus 四枚举全保留**(success/failed/circuit_break/skipped):迁移来的 attempts JSON 含后两态,少枚举会反序列化失败或展示缺失;v2 新写入只产生 success/failed(见 §7)。

### 2.2 migrate/009 拦截

`internal/db/migrate/009.go`:删除 `DropTable("relay_logs")` 段(27-31 行),保留 `base_urls` 列删除;注释注明 dev-v2 恢复日志持久化、引 ADR-0005。

- 不换表名(转换脚本与 fork 数据都认 `relay_logs`);不加 Version>9 迁移(多一段无意义的迁移历史)。
- 幂等性:全新库 AutoMigrate 建表 → 改后 009 不碰该表;已跑过 009 的演练库有 migration_records 记录、不会重跑,建表同样由 AutoMigrate 补齐。

### 2.3 建表与 ID

- `internal/db/db.go:55` AutoMigrate 列表追加 `&model.RelayLog{}`。
- Snowflake:上游无生成器,平移 fork `internal/utils/snowflake`。

## 3. 写库管线

### 3.1 attempts 插桩(handler.go Forward 闭包)

`internal/relay/handler.go:82` 的 `for{}` 循环是单 goroutine,闭包顶部加本地切片 `attempts []model.ChannelAttempt`(无锁)。每轮在拿到上游结果后(handler.go:195 `if err != nil` 分支及其成功对侧)append 一条:

- 字段来源全在闭包作用域:`channel`(120 行)、`channelKey`/`channelModel`(116-117 行经 grant)、`channelModel.Name`(129 行写入 raw.Body 的真实模型)。
- status:成功轮=success;失败轮=failed;msg=err.Error();duration=自 `roundStartedAt`(157 行);attempt_num=len(attempts)+1。
- 等待型 continue(grant/channel 缺失,109-126 行)不产生 attempt——该成员未被实际尝试。
- key 名:`channelKey.Name`(fork 语义的 channel_key_remark)。

### 3.2 终态组装:defer finalize

Forward 闭包顶部 `defer` 一个 finalize 函数(替换在 10 个终态调用点逐点插桩——点太多易漏):

- 读 `request.Status`:running/committed 理论不可达(defer 时必有终态或 panic 路径);success/failed/canceled 全部落库(fork 同语义:失败与取消也记)。
- 字段组装(handler/relay 包内可直接访问 RequestState 私有字段,同包):
  - time/use_time:request.StartedAt / Duration(已定稿)
  - token/cost:request.Usage、request.Cost(finishLocked 已算好,不重算);cached_tokens 取 Usage.PromptTokensDetails
  - request_content / response_content:`raw.Body` 原始入站 / `request.responseBody`(聚合终稿)
  - request_model_name / protocol 上下文:metadata.Model;channel_id/channel_name/actual_model_name:attempts 最后一次成功尝试,无成功取最后一次尝试(平移 fork saveLog 语义,`internal/relay/metrics.go` dev 版 380-392 行)
  - request_api_key_name:`op.APIKeyGet(apiKeyID)`(handler 入口已有 apiKeyID;apikey 走缓存)
  - user_agent:`c.Request.UserAgent()`(列建好,client_name 留空——任务5)
  - reasoning_effort:留空(任务5;上游 axonhub 转换层的 effort 提取不在本任务)
  - ftut:成功轮 result 返回时刻 − StartedAt 的近似值(上游无首字时间戳,近似语义写入代码注释)
  - debug_content:空(上游 pipeline 无 fork 的差异记录钩子;列保留,后续按需)
- error:failed/canceled 终态的 request.Error。

### 3.3 缓冲与落盘(op 层)

`internal/op/log.go` 自 dev 平移并裁剪:

- `RelayLogAdd`:缓存 append;启用 keep_enabled 时满 20 条 flush DB(CreateInBatches),未启用裁剪至 100 条(仅内存实时查询)。**裁掉 fork 的 subscribers/notify/token 部分**(行级 SSE 不移植,PRD 非目标)。
- `RelayLogSaveDBTask`(flush + relayLogCleanup 按 keep_period 删过期行)挂上游定时框架 `internal/task`(仿 `task/init.go:39-45` TaskStatsSave + `op.StatsSaveDBTask` 模式)。
- 查询函数平移:RelayLogList(缓存+DB 合并、四类筛选、Omit 大字段)、RelayLogGet、RelayLogClear、RelayLogAttemptsByChannel(LIKE `%"channel_id":N%` 粗筛 + Go 精确过滤 + 5000 上界 + truncated)。
- RelayLogExists 不带(用途已被 v2 路由形态覆盖,YAGNI)。

## 4. 接口层

`internal/server/handlers/log.go` 在现有 Auth 组追加:

| 路由 | 方法 | 说明 |
|---|---|---|
| `/api/v1/log/list` | GET | 分页+筛选(page/page_size/start_time/end_time/has_error/api_key_names/model_names) |
| `/api/v1/log/:id` | GET | 详情按需加载(含大字段与 attempts) |
| `/api/v1/log/channel-attempts` | GET | 渠道调用明细(channel_id/page/page_size) |
| `/api/v1/log/history/clear` | DELETE | **清库+缓存**(独立路径,避开上游 `/clear` 清内存语义;上游 `/clear` 不动) |

- gin 路由树:`/list`、`/channel-attempts`、`/history/clear` 为静态段,与既有 `/:id/request-body` 等参数段共存无冲突(静态优先);`GET /:id` 与 `GET /:id/request-body` 前缀树一致,合法。
- fork 的 `/stream-token`、`/stream`、`/active` 不移植。

## 5. 设置

- `internal/model/setting.go`:常量 `relay_log_keep_enabled`(默认 "true")、`relay_log_keep_period`(默认 "7");DefaultSettings 注册;Validate 将 keep_period 归入 int 组(fork 版 setting.go:50 同款)。
- 设置页 UI:日志区块两个控件(开关 + 天数),fork 605bfa3 形态对齐上游设置页组件体系。

## 6. 前端方案

布局(依据前端调研结论 C):**同页分区**,不动 NavItem/路由注册链。

- `web/src/components/modules/log/index.tsx`(现 49 行壳)改造:tab 两页(上游现成 `ui/tabs.tsx`)——「实时」页 = 现有 SSE 列表零改动;「历史」页 = 新建 `HistoryPanel`(VirtualizedGrid onReachEnd 无限滚动 + MultiSelect 筛选 + LogCard 行)。
- 平移清单(fork → dev-v2):`channel/detail-store.ts`(零改动)、`channel/CallDetail.tsx`(微调 import)、`log/MultiSelect.tsx`;`RequestDetailDialog.tsx` 需适配——dev-v2 无 `ui/dialog.tsx`,用已有 radix-ui meta 包零依赖新建;JsonContent 复用上游 `Item.tsx:90-135`(无 context 依赖,导出即用),**保留 fork 的自含 Dialog 绕 morph 设计**(上游同款 morphing-dialog 门控,理由成立)。
- API 层:新建历史 hooks(`api/endpoints/log.ts` 增 list infinite query / detail / channelAttempts / clearHistory),queryKey 独立前缀 `['log-history']`,与上游 clear 的 invalidate 互不误伤。
- **模型筛选选项来源 = 分组名列表**(`groupsQueryOptions`):request_model_name 两侧语义均为「客户端请求的模型名=分组名」(上游 state.go:33 自注释);不用 modelListQueryOptions(那是渠道侧真实模型名,语义不符)。
- API Key 筛选选项:现有 apikey 列表 query。
- channel 模块 Card 加「调用详情」入口(setView detail,zustand 自路由,fork 原方案)。
- i18n:en/zh_hans/zh_hant 三语,剔除不移植的 client/activeRequest key 后约每语言 +40~50 keys。

## 7. 行为差异(与 fork 相比)与已知缺陷

1. **attempts 无 skipped/circuit_break 新数据**:上游冷却跳过发生在 `pickGroupItem` 内部(route.go:98),handler 不感知、无事件可记;fork 的禁用跳过同理。枚举保留仅为迁移数据展示。渠道详情页对这两态的渲染保留。
2. **已知缺陷(任务3 范围,本任务不修)**:渠道级禁用(人工+健康检查自动禁用)在选路转发链无任何检查——`pickGroupItem` 不过滤(op/group.go:349 缓存构建也不剔除,`groupSnapshot` 的 Available 仅展示 op/group.go:401),`ChannelGrantGet` 只查 key 级 Enabled(op/channel.go:455),handler 照常转发。route.go:67 注释声称的「调用方发现并作为失败上报」未实现(key 级是 wait 不上报,渠道级未查)。→ **自动禁用无实际止损效果,仅有标记/展示作用**。已单独报告用户,建议独立小任务处置(涉及缓存链改动,非一行);本任务 attempts 语义按现状定义不受影响(禁用渠道被选中时照常产生 failed/success 尝试记录)。
3. ftut 为近似值(§3.2);debug_content 初期为空。
4. fork 行级 SSE 实时追加体验不移植:历史区靠翻页/手动刷新;实时需求由上游 SSE 实时区覆盖。

## 8. 兼容与回滚

- **转换脚本零改动**:模板库出现 relay_logs 表后,`CONDITIONAL_TABLES` 自动走「转换」分支(列交集=全列,零丢弃)。演练重跑:data 副本 → 新模板 → 转换 → 对账 1284 条 → 容器加载查历史页(AC6)。
- **回滚注意**:revert 本任务会恢复 009 的 DROP 段——已有 relay_logs 数据的库再启动将**删表**。回滚操作规程:先备份库文件/导出 relay_logs,再 revert。写入 implement.md 回滚点。
- 对 migrate 演练的影响说明(父任务要求):仅新增 relay_logs 表(fork 完整列),其余表形状不变。

## 9. 测试与验收映射

- 后端单测:attempts 组装(成功/失败/多轮/等待轮不记)、RelayLogList 筛选组合与缓存+DB 合并分页、cleanup cutoff、AttemptsByChannel 粗筛精确过滤、009 改后幂等(有表/无表)。
- 容器冒烟:真实请求(成功/失败/流式/非流式)→ relay_logs 行核验;四接口 curl;SSE 实时区回归;迁移库历史页可见 1284 条。
- AC 映射:AC1←§2.2;AC2←§3;AC3←§4;AC4←§4/§6;AC5←§1;AC6←§8;AC7←§3.3/§5;AC8←verify skill 全流程。

## 10. 实现偏离记录(2026-09-09 收口检查核准)

以下为实现与本文承诺的偏差,均经检查阶段逐项核实,行为安全、有意为之:

1. **§8「转换脚本零改动」→ 实际有改动**:`scripts/migrate_v1_to_v2.py` 的 `SETTINGS_INTERSECTION` 增补 `relay_log_keep_enabled` / `relay_log_keep_period` 两键(fork 侧用户保留偏好随之带走),`DROPPED_FIELDS_SUMMARY` 文案同步 8 键→6 键。安全性:交集键仅当 fork 库存在时才 upsert(`ON CONFLICT(key) DO UPDATE`),v2 侧两键已入 DefaultSettings,值域("true"/"false"、非负整数)与 Validate 兼容;演练实测通过。relay_logs 表本身仍走既有 `CONDITIONAL_TABLES` 机制,零改动。
2. **§6 JsonContent 复用 → 未复用**:Item.tsx 的 `JsonContent` 未导出(非 export),RequestDetailDialog 改为自含同款 JsonView 渲染(相同主题/折叠参数)。理由:保持上游 `Item.tsx` 零改动(AC5 白拿项零回归承诺优先于去重),代码注释已交代取舍;代价是约 35 行近似重复。
3. **§6 文件路径**:MultiSelect 落 `components/common/MultiSelect.tsx`(design 写 `log/MultiSelect.tsx`);API hooks 落 `web/src/api/log-history.ts`(design 写 `api/endpoints/log.ts` 为 fork 布局,dev-v2 为扁平 `api/` 惯例)。分组列表实际名 `groupListQueryOptions`(design 写 `groupsQueryOptions`,同一语义)。
4. **§6 queryKey 前缀补全**(检查阶段修复):RequestDetailDialog 的详情查询原用 `['log-detail']` 独立前缀,已改为 `['log-history', 'detail', id]`,使 `useClearLogHistory` 的 `invalidateQueries(['log-history'])` 能一并失效缓存详情。
5. **§9 对账数**:迁移演练实测 relay_logs 1285 条(规划期快照 1284,演练时 fork 副本新增 1 行),零丢列。
