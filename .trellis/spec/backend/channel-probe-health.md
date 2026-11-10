# 渠道探测与可用性降权契约

> 2026-11-10 定案（grill 收口）。公益渠道大面积失效但无可见性：115 个渠道，用户不知道自己接了哪些死渠道。
> 结论：**放弃失败率统计**，改为「复用分组成员测试做定时探测 + 白名单硬判 + grant 级熔断降权」。

## 背景：为什么不做失败率（勿回退到比率口径）

2026-11-10 生产库实测（`data.db`，近 7 天）：

| 指标 | 值 |
|---|---|
| 渠道 / 启用 | 115 / 46 |
| 授权 grant | 1925 |
| 分组成员 | 268 |
| 产生过真实尝试的渠道 | **13（11%）**，其中 **7 个零成功** |
| 所在渠道从未被试过的成员 | **134（50%）** |
| 7 天累计尝试次数 | 283 |

两个事实决定了口径：

1. **283 次尝试撑不起任何比率统计**，与 `ab9fe58` 定下的「别用绝对条数挑责任方」同源。
2. **被动统计永远覆盖不到低优先级渠道**：`routeStartIndex` 非轮询恒返回 0，failover 永远从最高优先级起，后面的成员根本没机会被选中。半数成员零数据不是巧合，是选路机制的必然。

one-api（`monitor/channel.go`）与 new-api（`model/ability.go`、`model/cache.go`）都不做失败率，做的是**三态 status + 单次测试结果 + 错误码白名单**。本契约沿用同一模型。

## 契约一：探测单元 = 复用 `relay.TestGroupMembers`

**What**：定时探测直接调用既有 `relay.TestGroupMembers(ctx, group)`（`internal/relay/keytest.go:276`），即「用户手点全部测试」的同一实现。逐成员直连该成员的（渠道, 凭据, 模型），**不走分组选路**；报文 `1+1等于几？`、`max_tokens 8192`、超时 15s。

**判据（三层，缺一不可）**：
1. 2xx；
2. 响应可按所测协议解析；
3. **有正文或工具调用**——`emptyAnswerReasonWith(format, body, false)`。

第 3 条是探测**特有**的严格口径：转发链走 `emptyAnswerReason(..., true)`，把「命中输出上限 / 未完成 / 内容过滤」都视为正常截断；探测不容忍，因为探测问的是「能不能给出答案」，8192 上限还没吐正文说明它在思考里空转，标成可用就是假绿（2026-11-10 实测该形态真实存在：content 空 + `reasoning_content` 满 + `finish_reason=length` 的 200）。空正文的错误文本是 `upstream returned no answer: ...`，不命中判死白名单，故走**计数类**失败（连续 3 次才判死），避免偶发截断直接毙掉成员。

**Why**：端到端探测（发 `model=<分组名>` 走真实选路）一轮只发 15 次请求，但 prio1 成功即停，**后段成员全程未被测到**——而降权需要的恰恰是「每个成员各自死活」。成员粒度一轮 268 次请求，换来每个成员都有确定结论；并发 4 + 每渠道限 2 的保护是现成的。

**边界**：
- **分组间串行**。`TestGroupMembersStream` 的 `channelSlots`（`keytest.go:295-300`）是函数内局部 map，跨组不共享；15 个组并行会让同一渠道（如腾讯云在 8 个组里）同时挨打。串行下整轮约十几分钟，间隔 6–12 小时，可接受。
- 探测**只读** grant/渠道配置，不改 `enabled`、不改 `auto_disabled`、不写成员冷却（见契约四）。
- 成员不可用（渠道或凭据被禁用）时 `testGroupMemberWithTimeout` 不发请求、直接以原因记 `unavailable`——这类结果**不是**探测结论，不得据此降权。

## 契约二：判据是白名单，不是阈值

**What**：一次探测失败是否判死，只由错误形态决定：

| 形态 | 判死 | 说明 |
|---|---|---|
| 401 / 403 / `authentication_error` / `permission_error` | 是 | 凭据失效 |
| `insufficient_credits` / `insufficient_quota` / `billing_not_active` / 429 配额类 | 是 | 额度耗尽 |
| 404 / 410 / `model_not_found` / `unknown provider for model` | 是 | 模型不存在或已下线 |
| 400 参数类（含 `unknown provider` 变体） | 是 | 该模型在该渠道不可用 |
| 5xx / 502 / 503 网关类 | 否（计数） | 上游抖动，连续 N 次才判死 |
| 超时 / 空正文 | 否（计数） | 同上 |
| 协议解析失败（2xx 但形状不对） | 否（计数） | 偶发转换问题 |

**Why**：生产失败原文高度集中在确定性形态（410 Gone 11 次、400 `unknown provider` 16 次、`503 model_not_found` 4 次、`429 insufficient_credits` 8 次）。这类错误重试一万次也不会好，按比率阈值反而延迟判定；而 5xx/超时是真抖动，一次就判死会误杀。

**边界**：
- 白名单以**错误码 + 错误文本关键词**双条件匹配，与 one-api `ShouldDisableChannel()` 同构；关键词命中表集中在一处常量，勿分散。
- **状态码只在"像状态行"的位置才算**：前有 `status`/`responded`，或后接 `Gone`/`Unauthorized`/`Forbidden`/`Payment`/`Not`/`Bad` 等短语。否则 token 计数里的 `410` 会被误判成 `410 Gone`（实现时被 `TestProbeFailureIsFatal` 的 "used 4102 of 410 tokens" 用例抓到）。
- 阈值只用于「计数类」错误：连续 3 次（与 `health_fail_threshold` 默认值同量级）判死。
- **不做「7 天成功率」**。若将来确需展示，只能作为「附加信息」并强制附带样本数，不得参与判死。

## 契约三：状态挂 grant，不挂渠道

**What**：在 `model.ChannelGrant` 上新增四列（AutoMigrate 直接增列，无需迁移文件；只有重建表才要 `internal/db/migrate/014.go`）：

| 列 | 类型 | 含义 |
|---|---|---|
| `probe_status` | string，空=未探测 | `ok` / `dead` |
| `probe_reason` | string | 判死时的错误摘要（截断 200 字符，复用 `truncateKeyTestError`） |
| `probe_at` | int64 | 最近一次探测完成时间（unix 秒） |
| `probe_fail_count` | int | 计数类错误的连续失败次数 |

**Why**：死的是「这个渠道的这个模型这把 key」，与它在哪个分组无关。渠道级会互相污染——生产现成反例：`测试`(#149) 直连全 402、`key-2`(#154) 全 200，挂在渠道级就分不出谁死。new-api 的 `MultiKeyStatusList`（逐 key 状态）是同一结论。

**边界**：
- **状态列用字符串不用布尔**，避开 `gorm default:true` 零值坑（见 `relay-routing.md`）：空字符串天然表示「未探测」，无需区分「false」与「没写过」。
- 一个 grant 被多个分组引用时，状态全局共用，探测一次全体生效。
- 探测结果**不**回写 `channel_grants.protocols`，不触发 `ChannelModelReconcile`，不对账。

## 契约四：熔断跳过与恢复

**What**：选路在候选过滤阶段（`selectableGroupItems` / `scanGroupItems` 所在层）跳过满足下列全部条件的成员：

1. `probe_status = 'dead'`
2. `now - probe_at ≤ 24h`（**状态过期就不跳过**）
3. 该成员不是人工禁用（人工禁用本来就已被选路层过滤，此处不重复处理）

探测成功时置 `probe_status='ok'`、`probe_fail_count=0`，立即可选。

**Why**：用户要的是「别每次都先撞一次死渠道白等 15 秒」，跳过才有价值；排到末尾救不了这个痛点。这对应 new-api 的「熔断 + 探活恢复」，而不是「改权重」。

**边界**：
- **绝不改 `group_items` 顺序**：顺序即优先级，`group/update` 是整体替换，自动改序会与手工排序互相覆盖。
- **绝不自动改 `enabled` / `auto_disabled`**：一是会跨分组误杀，二是自动启用会覆盖人工禁用意图。因此「人工禁用的成员永不自动启用」是本契约的**结构性保证**——探测根本不碰这两个字段。
- **绝不移出分组**：`op.GroupManualAbsorb` 5 分钟兜底会把成员加回来，且只增不减（见 `group-manual-absorb.md`）。
- **全员判死必须回退**：过滤后候选为空时，退回「可用但未过滤」的集合。否则一旦整组被判死（例如整段时间网络不通导致全员连错三次），分组会变成零成员、请求卡在等待循环里——熔断就升级成了「分组不可用」。宁可白试一次。
- 24 小时过期是**功能停摆护栏**：探测关掉或崩溃后，最多一天全部成员自动回到可选，不会永久隐身。
- 探测失败**不写成员冷却**（`recordRouteFailure` 属真实转发路径）。现有 `testGroupMemberWithTimeout` 只在成功时 `clearMemberCooldown`（`keytest.go:394`），失败什么都不做——照抄，勿"补全"。

## 契约五：调度是随机间隔，不用 `task.Register`

**What**：独立自调度 goroutine：跑完一轮 → `time.After(6h + rand(6h))` → 下一轮。启动时**不**立即跑（避免重启风暴）。开关与周期走设置项（默认关闭；开启后忽略固定周期）。

**Why**：`internal/task/task.go` 的 `Register` 基于固定 `time.Ticker`，语义是恒定间隔 + 可 `Update`，与「6–12 小时随机」不兼容；硬塞需要每次 tick 再掷骰子，反而更难读。

**边界**：
- 单实例假设：多副本会重复探测。当前部署单容器，不为此加分布式锁。
- **开关是设置项 `group_probe_enabled`（默认 `false`）**：探测会消耗真实上游配额，必须人工开启。协程常驻，关闭时每 10 分钟重读一次设置（设置变更没有回调）。
- **首轮唤醒**：从关闭切到开启后先等 1 分钟（`probeFirstRoundDelay`）再跑，不让人守着看几小时；之后才是 6–12 小时随机。
- **手动触发**：`POST /api/v1/group/probe/run` 后台跑一轮并立即返回 `{started}`；已在跑返回 `started=false` 而不是 409（幂等语义：撞车不算错误）。用途是首次开启前先观察上游反应。
- **单组超时** 10 分钟（`probeRoundTimeout`）：成员多且全失败时避免一轮挂死在某组上。
- 手动触发端点与定时探测**共用同一把闸**，见契约七。

## 契约六：探测历史（`grant_probe_records`）

**What**：每次落库结论时同事务外写一行历史：`grant_id / at / success / reason / use_time / protocol / source`（`source` = `manual` / `scheduled`）。保留期由设置项 `grant_probe_keep_days`（默认 90，0 表示不清理）控制，清理挂在探测轮开头——不写就没必要清，也不必另立定时任务。

**Why**：`channel_grants.probe_*` 只存最新结论，每次覆盖；用户要的是"这一周它挂过几次"。转发日志里其实有原始记录，但它受 `relay_log_keep_period`（默认 7 天）约束，**回看一周正好卡在清理边界**，且无法按探测来源筛选。专表的保留期与前两者解耦。

**边界**：
- **历史写失败只告警，不回滚结论**：成员判死/恢复是选路要用的强一致数据，历史是审计数据，丢一条不该让结论失效。
- 不可用（渠道/凭据被禁用）与取消的成员**不写历史**——它们根本没发上游请求。
- 一行 = 一次探测。同一 grant 在多个分组里时，每轮会被测多次，就记多行；这是事实，不做跨组去重。
- 分组汇总只含该分组**当前持有**的成员；成员被移出后历史仍在表里，但不再出现在该组视图。
- **不做趋势折线图**：6–12 小时一轮，一个成员一周只有 14–28 个点，曲线是插值幻觉。只做"事件列表 + 窗口内成败计数"。
- 查询接口：`GET /api/v1/group/probe/history/:grant_id?days=90`（单成员时间线）、`GET /api/v1/group/probe/summary/:id?days=30`（分组内按成员汇总，失败多的在前）。
- 界面入口：成员行「历史」按钮（弹窗）+ 分组编辑页第三个标签页「探测历史」（汇总表，点行看明细）。

## 契约七：日志与统计口径

**What**：探测写 `relay_logs`，`client_name` 用新常量 **`定时探测`**（已有 `面板测试`、`分组测试`）。

**Why**：现有 `keyTestLogFinalize` 就是「同表 + 靠 client_name 区分」，日志页可直接筛；不新建表。

**边界**：
- 探测**不进** `stats_*` 累加：`op.ChannelStatsUpdate` / `ChannelModelStatsUpdate` / `ChannelKeyStatsUpdate` 只在 `internal/relay/handler.go:254-256` 的真实转发路径调用，`keyTestLogFinalize` 不调。**勿在探测路径补调**，否则渠道统计被探测污染。
- 任何「按渠道聚合成功率」的查询必须排除三个测试 `client_name`，否则探测日志会稀释失败率。
- 探测日志同样受 `relay_log_keep_period`（默认 7 天）清理，历史明细不可长期依赖。

## 契约八：并发闸必须共用

**What**：闸已从 `internal/server/handlers/group.go` 的私有 `groupTests` 上移到 `internal/relay/group_test_lock.go`，导出 `relay.GroupTestAcquire(id)` / `relay.GroupTestRelease(id)`。手动测试（非流式 / 流式）与定时探测（`internal/task/probe.go`）共用同一把闸。

**Why**：原先是 handlers 包内未导出变量，`internal/task` 侧看不到它。不共用会出现「用户手点测试的同时定时探测也在打同一分组」，成员请求数翻倍，且两边互相污染冷却与日志。放在 relay 而不是让 task 反向依赖 handlers，是避免包依赖成环的唯一干净解（task 依赖 op/relay，handlers 依赖 task 与 relay，无环）。

**边界**：
- 闸只能覆盖「同一分组」，跨分组的不同时性靠契约一的分组间串行保证。
- 抢锁失败的一方**跳过该分组继续下一个**，不排队、不重试。

## 前端契约：成员列表可见性（需求 3）

**What**：
- 成员列表（`web/src/components/modules/group/ItemList.tsx`）把已禁用成员从主列表移出，收进底部**只读折叠区**：默认收起，按钮恒显示「已禁用成员 N 个」，展开后按原有内部相对顺序平铺，不可拖拽。
- 拖拽排序只在可用成员之间进行。渲染列表被过滤，故重排必须按**身份锚定**（`reorderVisible`：把被拖动成员插到目标成员之前/之后），不得用渲染下标直接对完整数组 `splice` —— 隐藏项会让下标整体前移，静默写错优先级。
- 编辑页候选侧（`Editor.tsx` 的 `ModelPickerSection`）**不折叠**，只给 `available=false` 的候选加「不可用」标签：添加一个当前转不出去的成员是合法操作，折叠会把用户想加的东西藏起来。
- 每个成员行标注探测结论：`probe_status=dead` 时显示「探测失败」徽标，tooltip 给错误摘要与探测时间；未探测过的不标注（**不要**用绿色暗示「已验证可用」）。

**Why**：用户原话是「禁用渠道还在上面显示，要往下翻才能找到」。诉求是「别让死成员挡路」，不是「删掉它们」——删掉会因 `GroupManualAbsorb` 回归，且丢失重启用后的原优先级。

**边界**：
- **提交仍提交全量成员**，一个不删；前端折叠只是渲染方式。
- 折叠区成员保留移除按钮与探测徽标，只是不可拖：把它们拖到前面没有意义（选路本来就会跳过）。
- 状态为「未探测」的成员不显示任何健康色；灰色只用于「已禁用」与「已判死」两类，两类的文案必须可区分（前者是「不可用」标签，后者是「探测失败」徽标）。

## 测试锚点

- `internal/op/grant_probe_test.go`：判据白名单（含「token 计数里的 410 不算 410 Gone」反例）；状态机（抖动→未定型→第 3 次判死→成功清零→确定性一次判死）；不可用/取消不落库；未知 item 不误伤。
- `internal/relay/route_probe_test.go`：判死且未过期 → 跳过；判死但超 24h → 不跳过；全员判死 → 回退不过滤；凭据禁用时任何探测结论都不放行。
- `internal/task/probe_test.go`：随机周期落在 6–12 小时；空库跑一轮不 panic 且不留运行标记。
- `internal/server/handlers/group_stream_test.go`：`/probe/run` 路由已注册（拼错只在此暴露，空库下异步任务是空转）。
- 前端：`item_id → 原始序号` 映射的纯函数单测（过滤 + 重排 + 提交全量三条断言）。**当前仓库无前端测试框架，此项欠账。**
- 回归红线：`TestGroupMembers` / `TestGroupMembersStream` 现有行为与测试零改动；`relay_logs` 写入路径不变；`stats_*` 不新增调用点。

## 明确不做（避免后续反复）

- **不做失败率排行榜**，不做健康度综合评分。
- **不做独立健康看板页**：只在成员行 / 渠道成员行标注。
- **不做端到端 `model=<分组名>` 探测**（覆盖后段成员不足，且与成员粒度重复）。
- **不做自动移出分组 / 自动禁用渠道**（分别被 `GroupManualAbsorb` 与跨分组误杀否掉）。
- **不做上下文长度元数据**（2026-11-10 用户放弃：`/v1/models` 不加 `context_length`。Hermes 空白成因已查明为「网关不暴露该字段 + 模型名是分组名、公开注册表匹配不到」）。
