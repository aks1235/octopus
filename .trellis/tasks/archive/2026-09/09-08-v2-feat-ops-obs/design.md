# 技术设计:渠道健康检查 + 分组正则成员

基线:dev-v2 @ `34cf007`(上游 v0.13.2 + 资产 + 迁移脚本)。引用的结构均已在基线核实。

## 一、渠道健康检查+失败自动禁用(R1)

### 1.1 数据模型(`internal/model/channel.go`)

`Channel` 顶层(不在 `ChannelConfig`,健康状态非用户可编辑配置)新增 4 列,AutoMigrate 增量添加:

```go
HealthFailCount int    `json:"-" gorm:"not null;default:0"` // 连续健康检查失败次数。
LastHealthError string `json:"-" gorm:"not null;default:''"` // 最近一次健康检查失败原因; 成功后清空。
LastHealthAt    int64  `json:"-" gorm:"not null;default:0"`  // 最近一次健康检查完成的 unix 秒。
AutoDisabled    bool   `json:"-" gorm:"not null;default:0"`  // 是否因连续健康检查失败被自动禁用。
```

出 JSON 不走 Channel(避免混入编辑形态 `ChannelDetail` 的语义),走 `ChannelStats`(列表形态):

```go
// ChannelStats 追加 4 个只读字段, json 同名
HealthFailCount int    `json:"health_fail_count"`
LastHealthError string `json:"last_health_error"`
LastHealthAt    int64  `json:"last_health_at"`
AutoDisabled    bool   `json:"auto_disabled"`
```

`op.ChannelStatsList()` 从 channelCache 填充(fork `8185f12` 的 DTO 教训:填充必须重建元素,不能就地改缓存对象)。

### 1.2 设置项(`internal/model/setting.go`)

- `SettingKeyHealthCheckInterval = "health_check_interval"`,默认 `"30"`(分钟,`"0"`=关闭;`task.Register` 对 interval≤0 天然不注册)
- `SettingKeyHealthFailThreshold = "health_fail_threshold"`,默认 `"3"`

接线方式对齐 `price_update` 现状:任务名=设置键,设置页修改后经 handlers 的 task.Update 生效(实现时照抄 price/stats 的现成接线)。

### 1.3 探测包 `internal/probe`(新包)

`handlers/channel.go` 的 `fetchModel` 已有探测逻辑(http client 构建、代理、`fetchOpenAIModels`/`fetchAnthropicModels`/`modelsURL`/`decodeModelList`)。抽出到 `internal/probe`,handlers 与 task 共用,避免 task→handlers 的反向依赖:

```go
// internal/probe/models.go
func FetchModels(ctx, config model.ChannelConfig, keys []model.ChannelKeyConfig) (ok bool, err error)
```

- 对渠道**每个 enabled key × OpenAI/Anthropic 两个 models 端点**逐一探测,**任一 200 即健康**(宽容判定:多 KEY 渠道剩一个活 KEY 不误杀;单侧端点不通的渠道另一侧通也不误杀)
- 超时/代理行为与 fetchModel 完全一致(复用同一 client 构建代码)
- 错误信息截断(≤200 字符)后作为 LastHealthError

### 1.4 健康检查任务(`internal/task/health.go`)

- `Init()` 注册 `Register(SettingKeyHealthCheckInterval, interval, false, ChannelHealthCheckTask)`,runOnStart=false(启动即探测 113 渠道会拖慢启动观测,交给首个周期)
- 任务逻辑:
  1. 遍历 channelCache,候选 = `Enabled || AutoDisabled`(自动禁用的要继续探测以便解禁)
  2. 并发探测(每渠道一个 goroutine + WaitGroup;单渠道内部串行端点),逐渠道回调落库
  3. 失败:`ChannelUpdateHealth(id, count+1, err, now, false)`;达阈值(threshold 每轮实时读设置)→ 置 `enabled=false, auto_disabled=true`
  4. 成功:计数清零、错误清空;若 `auto_disabled && !enabled` → 置 `enabled=true, auto_disabled=false`
  5. 人工禁用渠道(`!enabled && !auto_disabled`)不在候选,永不误翻

### 1.5 op 层(`internal/op/channel.go`)

```go
// 轻量健康状态更新, 不混入 ChannelUpdate 的用户编辑语义(fork 55ffcbf 同款教训)
func ChannelUpdateHealth(id int, fn func(ch *model.Channel) ) // 或显式参数, 实现时按包内风格定
```

- 更新 DB 行 + channelCache 副本(对齐 `ChannelEnabled` 的"改库→改缓存"顺序)
- `ChannelEnabled` 与 `ChannelUpdate`:当结果为 enabled=true 时清 `AutoDisabled`(运维接管清自动标记,人工禁用不清计数、计数由探测归零)

### 1.6 前端

- `web/src/api/endpoints/channel.ts`:ChannelStats 类型加 4 字段
- 渠道 Card(`web/src/components/modules/channel/`):状态徽标三态——正常(不显眼/灰)、失败中(橙:`失败 N 次`,tooltip 显示 LastHealthError)、已自动禁用(红:`已自动禁用`,tooltip 同上);tooltip 附最近检查时间
- 设置页:两个数字输入(健康检查间隔分钟、失败阈值),对齐现有 stats/model_info 设置项的表单模式
- i18n:en/zh_hans/zh_hant 三语新增 key

## 二、分组正则成员(R2)

> 上游现状修正(2026-09-09 核实):上游编辑器有 autoAdd 按钮——按分组名对全部渠道模型做大小写不敏感**子串匹配**一键加入成员(`web/src/components/modules/group/Editor.tsx` handleAutoAdd + `utils.ts` matchesGroupName)。局限:规则不留存、无正则、无后续同步。本方案 = 把这条规则持久化(member_regex,正则)+ 自动同步;手动组的 autoAdd 交互保持原样。

### 2.1 数据模型(`internal/model/group.go`)

- `Group.MemberRegex string`(`json:"member_regex" gorm:"not null;default:''"`),空=纯手动组(全部存量组默认值,行为不变)
- `GroupCreateRequest.MemberRegex string`(omitempty,创建时可选)
- `GroupUpdateRequest.MemberRegex *string`(nil=不改;空串=改回手动组,已吸纳成员保留转为手动成员)
- 校验:非空时 `regexp2.Compile(re, regexp2.ECMAScript)`(与渠道 match_regex 校验对齐,handlers/group.go)

### 2.2 重算逻辑(`internal/op/group.go`)

```go
// GroupRegexSync 重算全部(或单个)正则分组的成员
func GroupRegexSync(ctx) / groupRegexSyncOne(tx, groupID)
```

- 匹配对象:全部渠道的全部 ChannelModel 名(不看渠道 Enabled——禁用≠删除,Available 已在展示层表达)
- 命中模型的**全部 grant** 纳入(多凭据渠道全部成员化,凭据禁用态由 Available 表达)
- 目标成员顺序:(channel_id, model_name, key_name) 稳定升序,priority=序号
- **复用 `syncGroupItems(tx, groupID, desiredInputs)`** 做整体替换——它按授权主键匹配保留既有成员的主键与统计,语义完全吻合
- 正则组内成员失配即删;ActiveItemID 指向已删成员时由 syncGroupItems 现有逻辑清空(已核实 GroupUpdate 注释)
- 每次重算后刷 groupCache

### 2.3 触发点

1. 渠道 `ChannelCreate` / `ChannelUpdate` / `ChannelEnabled` / `ChannelDel`(op 尾部,事务外同步调用,重算量级 ~正则组数×渠道模型数,毫秒级)
2. `GroupCreate`/`GroupUpdate` 携带 member_regex 时,对该组即时重算(响应即含吸纳后的成员)
3. 启动兜底 + 定时兜底:`Register("group_regex_sync", 5min, runOnStart=true, ...)`(常量间隔,不暴露设置,KISS)

### 2.4 前端

- `web/src/api/endpoints/group.ts`:Group/创建/更新类型加 member_regex
- 分组 Editor:正则输入框(可选,占位示例 `^deepseek-v4-pro$`);非空时成员选择区切换为只读列表+提示「成员由正则自动维护」,并保留一键吸纳按钮(按正则立即吸纳,衔接上游 autoAdd 习惯)
- ItemList 无需改动(渠道名/禁用态渲染是上游原生能力,即⑤)
- i18n 三语

## 三、②⑤ 原生覆盖验证(R3)

smoke 容器(docker-compose.smoke.yml,8081)实测并记录 `research/native-coverage-verification.md`:

- ② 建 2 成员分组 → 删其中一个渠道 → 该成员随外键级联消失,另一成员保留
- ⑤ 分组页成员渲染 channel_name·key_name;禁用渠道后 Available=false 的禁用态展示(截图/步骤留档)

## 四、权衡与风险

| 决策 | 取舍 |
|---|---|
| 探测任一成功即健康 | 宽容判定避免误杀(单侧端点不通/多 KEY 部分失效);代价:检测不到"部分 KEY 失效",那是 KEY 级健康管理,超出本任务 |
| member_regex 物化成员(非选路时动态解析) | 复用 GroupItem 全套结构/UI/统计保留,不碰选路热路径;代价:5 分钟内新增渠道的入组有延迟(渠道 CRUD 已有即时触发,兜底仅兜意外) |
| 正则组禁止手动编辑成员 | 手动增删会被重算覆盖,允许编辑是假象;UI 只读+提示,语义诚实 |
| 健康字段只进 ChannelStats 不进 ChannelDetail | 编辑形态与只读状态分离,避免客户端提交只读字段;代价:详情页要显示健康状态需复用列表数据(前端渠道模块本就同时持有) |
| 不写 RegisterAfterAutoMigration 迁移 | gorm AutoMigrate 自动加列不删列;存量库零影响 |

## 五、回滚

单 commit 交付(3.4),回滚 = `git revert`。DB 新列为增量列,revert 后残留列不影响旧代码运行(gorm 不删列),无需数据回滚。
