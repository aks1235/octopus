# Research: dev-v2 relay 主流程与日志持久化挂钩点调研

- **Query**: dev-v2(上游 v0.13.2)relay 主流程、RequestState 终态、attempts 重建、settings/定时任务机制调研
- **Scope**: internal(当前工作区 dev-v2 分支,commit 86e521a)
- **Date**: 2026-09-09

---

## 1. internal/relay/ 文件清单与主转发循环

### 文件清单(共 6 个文件)

| 文件 | 行数 | 职责 |
|---|---|---|
| `internal/relay/handler.go` | 360 | `Forward` 主入口:解析请求、登记状态、**主转发循环**、终态定稿 |
| `internal/relay/state.go` | 313 | `RequestState` 进程内状态、SSE 日志流、内存日志裁剪 |
| `internal/relay/route.go` | 275 | 分组路由状态:选路、冷却(熔断)、亲和、探测 |
| `internal/relay/channel.go` | 86 | 出站转换器构造 `buildOutbound`、渠道参数覆盖 `applyChannelConfig` |
| `internal/relay/protocol.go` | 169 | 流事件终态判定、透传请求构造、Responses 终态校验 |
| `internal/relay/upstream.go` | 246 | 上游请求发送:同协议透传 `sendPassthrough`、跨协议 `sendConverted` |

### 主转发循环位置

`internal/relay/handler.go:27` 的 `Forward(format llm.APIFormat)` 返回 gin handler,主循环是其闭包内的匿名 `for {}`(**handler.go:82-347**)。

### 请求从进来到终态定稿的完整调用链

```
POST /v1/chat/completions | /v1/responses | /v1/messages
  挂载点: internal/server/handlers/relay.go:13-26 (router.NewGroupRouter("/v1").Use(APIKeyAuth()))
  ↓
middleware.APIKeyAuth (internal/server/middleware/auth.go:32-74)
  解析 x-api-key / Authorization Bearer (auth.go:36-40)
  → op.APIKeyGetByAPIKey (auth.go:48 → internal/op/apikey.go:63)
  → 校验 Enabled/ExpireAt/MaxCost (auth.go:54-69)
  → c.Set("api_key_id", apiKeyObj.ID) (auth.go:71)
  ↓
relay.Forward(format) (handler.go:27)
  httpclient.ReadHTTPRequest 读完整请求体 (handler.go:44,保留全部请求头)
  解析 model/stream 元数据 (handler.go:51-58)
  API key 模型白名单检查 (handler.go:61-66)
  op.GroupGetByName 定位分组 (handler.go:70 → op/group.go:61)
  newRequestState 登记请求状态 (handler.go:77 → state.go:62)
  ↓
for 主循环 (handler.go:82):
  ctx 取消检查 → markCanceled (handler.go:83-86)
  每轮重读分组 op.GroupGetByName (handler.go:89)
  pickGroupItem 选成员 (handler.go:99 → route.go:68)
  op.ChannelGrantGet 取授权+模型+凭据 (handler.go:109 → op/channel.go:442)
  op.ChannelGet 取渠道 (handler.go:120 → op/channel.go:431)
  sjson 改写 body 的 model / stream_options (handler.go:129,137)
  buildOutbound 选协议+构造出站转换器 (handler.go:147 → channel.go:22)
  request.startRound 登记本轮目标 (handler.go:155 → state.go:82)
  sendPassthrough / sendConverted 请求上游 (handler.go:174-178)
  ├─ 失败: finishRound (handler.go:197) + 渠道统计 (211-214)
  │        + failures 计数 (217-222) + recordRouteFailure 冷却 (224 → route.go:161)
  │        + request.wait 退避重试 (227 → state.go:124)
  └─ 成功: finishRound("") (handler.go:233) + recordRouteSuccess (236 → route.go:121)
       ├─ 非流式: markCommitted (255) + Write (256) + markSucceeded/markFailed/markCanceled (262-268)
       └─ 流式: 逐事件转发循环 (282-314) + AggregateStreamChunks 聚合 (322)
                + 渠道统计 (327-336) + markSucceeded/markFailed/markCanceled (339-345)
  ↓
终态定稿 finishLocked (state.go:181-218):
  写 Usage/Cost/Duration → usageMetrics (state.go:221)
  → op.StatsTotalUpdate/StatsHourlyUpdate/StatsDailyUpdate/StatsAPIKeyUpdate (state.go:196-201)
  → publishRequestLocked SSE 推送 (state.go:202)
  → 裁剪内存历史 maxFinished=50 (state.go:52, 204-217)
```

注:入口级失败(读请求体失败、model 未传、分组不存在等)走 `rejectRequest`(handler.go:352-359),**不创建 RequestState**,这类请求对任何挂在 RequestState 上的钩子不可见(handler.go:46,56,63,72 四处)。

---

## 2. 终态定稿调用点与可获取的上下文

### 定义处(均在 state.go,全部持有全局锁 mu)

| 函数 | 定义 | 说明 |
|---|---|---|
| `markSucceeded` | `state.go:144-152` | 成功终态,清空 Error,写 responseBody |
| `markFailed` | `state.go:155-165` | 失败终态,Error=err.Error() |
| `markCanceled` | `state.go:168-178` | 取消终态(客户端断开/人工中止) |
| `finishLocked` | `state.go:181-218` | 三者共同的收尾:用量/费用/统计/SSE/裁剪 |

### 调用点(全部在 handler.go,另有 state.go:127)

| 调用点 | 场景 | 此刻循环作用域内的渠道上下文 |
|---|---|---|
| `handler.go:84` | 循环头顶层 ctx 取消 | 无(尚未选目标) |
| `handler.go:131` | sjson 写 model 失败 | 有:channel/channelModel/channelKey/grant/item |
| `handler.go:139` | sjson 写 stream_options 失败 | 同上 |
| `handler.go:201` | 本轮上游失败后发现客户端已取消 | 有 |
| `handler.go:262` | 非流式写响应时客户端取消 | 有 |
| `handler.go:264` | 非流式写响应失败 | 有 |
| `handler.go:268` | 非流式成功 | 有 |
| `handler.go:339` | 流式结束但客户端已取消 | 有 |
| `handler.go:341` | 流式失败 | 有 |
| `handler.go:345` | 流式成功 | 有 |
| `state.go:127` | `wait` 退避期间客户端断开 | 无渠道上下文(退避期无目标) |

### 各类上下文的可得性

| 上下文 | 可得性 | 位置 |
|---|---|---|
| 客户端请求体 | **可得** | `RequestState.body` 私有字段(state.go:45),newRequestState 时存入原始 body(handler.go:77)。注意 handler 里 `raw.Body` 每轮被 sjson 覆写 model 字段(handler.go:129),原始 body 已另存 |
| 最终响应体 | **可得** | markXxx 的 responseBody 参数(非流式= result.body;流式= inbound.AggregateStreamChunks 聚合结果,handler.go:322) |
| usage(llm.Usage) | **可得** | markXxx 的 usage 参数;finishLocked 写入 r.Usage(state.go:184-186) |
| 轮次历史(每轮渠道/模型/协议/错误) | **不可得** | RequestState 只保留最新一轮(见第 3 节) |
| API Key 名称 | **半可得** | RequestState 只存 apiKeyID(state.go:47)。名称可在终态组装时查缓存:`op.APIKeyGet(id).Name`(op/apikey.go:55-61;`model.APIKey.Name` 定义于 model/apikey.go:5),纯内存缓存查询无 DB 开销 |
| User-Agent | **handler 层可得,state 层不可得** | `ReadHTTPRequest` 原样保留 `rawReq.Header`(axonhub `httpclient/utils.go:25` `Headers: rawReq.Header`),Forward 闭包内 `raw.Headers.Get("User-Agent")` 全程可用;但 RequestState 未存 UA,markXxx 内拿不到 |
| 客户端 IP | 额外可用 | `raw.ClientIP`(axonhub ReadHTTPRequest 解析 XFF/X-Real-IP/RemoteAddr,utils.go:126-144) |

**结论:组装 RelayLog 应在 handler.go 的终态调用点(或收敛后的 defer)做,而不是在 state.go 的 markXxx/finishLocked 内部**——渠道/凭据/UA 上下文都在 handler 闭包作用域,state 层拿不到;且 finishLocked 持全局锁(state.go:56 mu),DB 写入不应进锁。

---

## 3. 轮次信息留存现状与 attempts 链重建

### 现状:只保留最新一轮,无 ChannelAttempt 等价物

- `startRound`(state.go:82-95)每轮**覆盖** `Round++/TargetChannel/TargetModel/TargetProtocol/Sending/Error`(字段定义 state.go:38-43)。历史轮次信息即丢,SSE 日志流推的也是覆盖后的快照。
- `finishRound`(state.go:98-106)只写回 Sending/Error。
- **没有任何数组/链式结构保存每轮记录**;fork 的 `ChannelAttempt`(每轮 channel_id/channel_name/key_id/model/status/duration/msg)在上游不存在等价物。
- 进程内也只有最近 50 条已结束请求(state.go:52 maxFinished,裁剪逻辑 state.go:204-217),重启全丢。

### 重建 attempts 链需要的插桩点

主循环是单 goroutine 顺序执行,在 Forward 闭包加一个本地切片(handler.go:79 `failedItemID`/`failures` 旁边)**天然无并发,无需锁**。每轮全部所需字段在 handler.go:109-157 的作用域内:

| attempts 字段 | 来源 |
|---|---|
| channel_id / channel_name | `channel.ID` / `channel.Name`(handler.go:120 取得) |
| key_id / key_name | `channelKey.ID` / `channelKey.Name`(handler.go:117,来自 grant.ChannelKey) |
| model | `channelModel.Name`(handler.go:116) |
| 协议 | `targetProtocol`(handler.go:147 buildOutbound 返回) |
| 开始时间 | `roundStartedAt`(handler.go:157) |
| duration | `time.Since(roundStartedAt)` |
| status/msg | 见下表 |

插桩事件点:

| 事件 | 位置 | 记什么 |
|---|---|---|
| 本轮开始 | handler.go:155 startRound 之后 | push 一条 running 记录(或暂存,结束时定稿) |
| 本轮上游失败 | handler.go:197 finishRound 失败分支 | status=failed,msg=err.Error(),duration |
| 超时覆盖 | handler.go:183-192(result 被舍弃的分支) | 归入该轮 failed, msg=timeoutErr |
| 客户端取消(失败后发现) | handler.go:199-203 | 该轮 status=canceled |
| 人工中止本轮不计失败 | handler.go:205-208 | 该轮 status=interrupted(可选) |
| 本轮成功 | handler.go:233 finishRound("") | status=success,duration |
| 非流式写失败/取消 | handler.go:256-268 | 该轮已 success 但请求终态 failed/canceled(按 fork 语义对齐) |
| 流式事件错误/写失败 | handler.go:282-314, 337-345 | 同上 |

**未成轮的跳过不进 Round 序号**:`Round` 只在 startRound 递增(state.go:86)。下列等待分支不产生轮次、RequestState 完全不可见(handler.go:100-104 无可用成员、110-115 授权缺失/凭据禁用、121-126 渠道删除、91-95 分组消失),全部走 `request.wait` 退避后 `continue`。若 attempts 需要记录"空转等待",须另行插桩(建议不记,与 fork 对齐:fork 只记真实上游调用)。

---

## 4. 渠道选择 / 失败重试(failover)逻辑

### 选路与重试位置

- **选路**: `pickGroupItem`(route.go:68-118)。手动模式按 `group.ActiveItemID`(route.go:69-76);failover 模式按 Priority 升序遍历成员,跳过冷却中的成员(route.go:97-99),冷却到期成员只放行一个探测请求(route.go:102-109),亲和期内沿用当前成员(route.go:88-90)。
- **重试决策**: handler.go:195-231。真实失败(排除客户端取消与人工中止)计入渠道/模型/凭据三级统计(handler.go:211-214);`failures` 按成员累计(handler.go:217-222);`recordRouteFailure`(handler.go:224 → route.go:161-191)达到 `MemberMaxAttempts` 后把成员打入 `MemberCooldownSeconds` 冷却并立即换目标;未达次数则 `wait(MemberRetryIntervalSeconds)` 后重试(handler.go:227)。
- **成功上报**: `recordRouteSuccess`(handler.go:236 → route.go:121-157),解除冷却/探测占用,故障切换后首次成功开始亲和。
- 退避机制 `request.wait`(state.go:124-132):select ctx.Done / time.After,退避期间断开→markCanceled。

### 单渠道多 KEY:每个 key 尝试**不分开**

- 上游的转发最小单位是 `ChannelGrant` = (渠道模型, 渠道凭据) 固定组合(model/channel.go:85-92)。一个 `GroupItem` 引用一个 grant(handler 拿到的 key 固定,handler.go:109-117),**轮内不会自动换 key**。
- 多 key 的表达方式是组内多个成员(不同 grant);正则分组会把同渠道同模型的所有凭据全部纳入为成员(op/group.go:275-330,注释「命中模型的全部授权纳入成员, 同渠道同模型的多个凭据一并纳入」)。换 key = 换成员 = 走冷却/优先级切换。
- 单渠道多 KEY 支持来自上游提交 6e736b4「feat: 新增单渠道多KEY支持」。

### 被跳过的轮次在哪标记

| 跳过类型 | 机制 | 标记位置 | 是否产生轮次 |
|---|---|---|---|
| 熔断(冷却) | pickGroupItem 跳过 | Cooldowns 写入:recordRouteFailure(route.go:179);到期探测占用 ProbeItemID(route.go:106) | 否 |
| 无可用成员 | item.ID==0 | handler.go:100-104,仅退避 | 否 |
| 凭据禁用/授权缺失 | ChannelGrantGet 报错(key.Enabled 检查在 op/channel.go:455-457) | handler.go:110-115,仅退避,**不计失败不进冷却** | 否 |
| 渠道删除 | ChannelGet 报错(op/channel.go:431-437 只查缓存存在) | handler.go:121-126,仅退避 | 否 |
| 分组消失 | GroupGetByName 报错 | handler.go:90-95,仅退避 | 否 |

**重要现状(与注释不符,移植时须知)**:relay 包内**没有任何 `channel.Enabled` 检查**(grep "Enabled"/"Available" 在 internal/relay/*.go 零命中)。`ChannelGet`(op/channel.go:431-437)不检查 Enabled,`ChannelGrantGet`(op/channel.go:442-461)只检查 key.Enabled 不检查渠道 Enabled。即:**渠道被禁用(含健康检查自动禁用)后,已配置成员仍会被选中并实际转发**;禁用态只体现在 UI 的 Available 字段(op/group.go:401 groupSnapshot)。route.go:67 注释「渠道禁用或缺少密钥由调用方发现并作为一轮失败上报」中,渠道禁用的发现逻辑在 handler.go 调用方实际不存在(凭据禁用也只是退避重选,并未「作为一轮失败上报」)。

### 任务 3(渠道健康检查)与重试循环的交互点

- 健康检查是**独立后台任务**,与 relay 循环无直接调用关系:
  - 定时注册:`task.Init`(internal/task/init.go:58-63)读 `SettingKeyHealthCheckInterval`,`Register(..., ChannelHealthCheckTask)`;`task.RUN`(internal/task/task.go:81)起协程,cmd/start.go:51-52 启动。
  - 执行:`ChannelHealthCheckTask`(internal/task/health.go:23-46)→ `op.ChannelHealthCandidates` 取候选(op/channel.go:347-373)→ `probe.FetchModels` 探测(internal/probe/models.go)→ 并发每渠道一协程(health.go:38-44)。
  - 落库:`op.ChannelHealthFail`(op/channel.go:271-305,达阈值置 enabled=false+auto_disabled=true)/`op.ChannelHealthSuccess`(op/channel.go:310-342,自动解禁判定)。
- 与 relay 的**真实同步点只有一个**:健康检查改写 channelCache 时持有 `channelStatsNeedUpdateLock`(op/channel.go:289-290, 326-327),与 relay 失败路径的 `op.ChannelStatsUpdate`(op/stats.go:298-309,持同一把锁)互斥,防止丢统计累加。
- 理论上禁用渠道应让 pickGroupItem/ChannelGrantGet 跳过,但如上,当前代码未接通(见上文「重要现状」)。做日志持久化时 attempts 里的渠道禁用跳过记录,当前没有现成事件可挂。

---

## 5. API Key 名称

- 入口解析:`middleware.APIKeyAuth`(middleware/auth.go:32-74)优先 `x-api-key` 头,否则 `Authorization: Bearer`(auth.go:36-40);`op.APIKeyGetByAPIKey`(op/apikey.go:63-69)走 `apiKeyIDMap` 字符串索引缓存 → `apiKeyCache` 按主键缓存。
- 传递:仅 `c.Set("api_key_id", apiKeyObj.ID)`(auth.go:71);Forward 处 `c.GetInt("api_key_id")` 传给 newRequestState(handler.go:77)→ `RequestState.apiKeyID`(state.go:47)。
- **终态拿名称**:完全可以。`op.APIKeyGet(r.apiKeyID).Name`(op/apikey.go:55-61)是纯缓存查询(进程启动时 `apiKeyRefreshCache` 灌满,op/apikey.go:90-100);`Name` 字段在 model/apikey.go:5。建议在 handler 终态组装时查一次并作为快照存入日志(中途改名时日志仍显示当时的名称;fork 若存 ID+名称快照,语义一致)。

---

## 6. Snowflake ID:没有

- 全仓库 `grep -i snowflake` 零命中;`internal/utils/` 只有 4 个子包:`cache/`(分片缓存)、`diff/`、`shutdown/`、`xstrings/`,无任何 ID 生成器。
- go.mod 无 ID 生成库(间接依赖有 github.com/google/uuid,但项目代码未使用)。
- 现有 ID 机制:
  - 请求 ID:`relay/state.go:55` `idSeq atomic.Uint64` 进程内严格递增,**重启归零,不落库**;
  - 业务主键:全部 gorm int 自增(model 各实体 `gorm:"primaryKey"`);
- 若 RelayLog 需要全局唯一 ID:用 int 自增主键即可(与现有 stats 表一致);确需分布式 ID 则要自行移植 snowflake,当前仓库无现成代码。

---

## 7. settings 机制与新增 key 的改动面

### 组织方式

- 常量:`model.SettingKey` 字符串类型 + 常量块(model/setting.go:9-18)。
- 默认值:`model.DefaultSettings()`(model/setting.go:25-34)。**必须加**:`settingRefreshCache`(op/setting.go:92-124)只补种 DefaultSettings 里列出的缺失 key,且 `SettingSetString`/`SettingSetInt` 对缓存中不存在的 key 直接报 "setting not found"(op/setting.go:36-38, 71-73)。
- 校验:`Setting.Validate()`(model/setting.go:36-80),int 型 key 各有 Atoi case。
- 读取:`SettingGetInt`(op/setting.go:53)/`SettingGetBool`(op/setting.go:61),未定义返回 error。
- 写入 API:`POST /api/v1/setting/set`(handlers/setting.go:51-83),保存后按 key switch 联动 `task.Update`(handlers/setting.go:65-81)。

### 新增 log_keep_enabled / log_keep_period_days 两个 key 要动的文件

| 文件 | 改动 |
|---|---|
| `internal/model/setting.go` | 常量(:11-18)+ DefaultSettings(:25-34,如 "false"/"7")+ Validate 的 int/bool case(:36-80) |
| `internal/task/init.go` | Init 里读设置并 Register 日志落库/清理任务(仿 stats_save,init.go:39-45) |
| `internal/server/handlers/setting.go` | 若改 keep_period_days 需实时生效,加 task.Update 分支(:65-81);enabled 开关若只在执行时读值判断则不必 |
| 前端设置页 `web/src/...` | 展示新设置项(本次未查前端,实现时补) |

---

## 8. 定时任务机制

### 上游有自研任务框架:internal/task/task.go

- `Register(name, interval, runOnStart, fn)`(task.go:27-50):interval<=0 不注册(即关闭);重名跳过。
- `Update(name, interval)`(task.go:54-78):运行中改间隔;interval<=0 删除任务。
- `RUN()`(task.go:81-90):为每个任务起 `runTask` 协程,自身 `select{}` 永久阻塞(所以 cmd/start.go:52 用 `go task.RUN()`)。
- `runTask`(task.go:92-113):`time.NewTicker` 驱动,**每次到点 `go entry.fn()`**(fn 自身可能并发,若上一轮未跑完)。

### 启动与现有任务

- 启动:cmd/start.go:51-52 `task.Init()` + `go task.RUN()`;阻塞交给 `shutdown.Listen()`(start.go:53)。
- `task.Init`(internal/task/init.go:24-64)注册 4 个任务:price_update(:32)、**stats_save**(:45,`op.StatsSaveDBTask`)、group_regex_sync(:48)、health_check_interval(:63,`ChannelHealthCheckTask`)。

### fork 的 RelayLogSaveDBTask 完全可复用此框架

- 参照 `TaskStatsSave` 注册方式(task/init.go:39-45):间隔从设置读,fork 里定时调用写库。
- 参照 `op.StatsSaveDBTask`(op/stats.go:40-52)的成熟模式:内存缓存/队列 + 定时批量落库 + 失败恢复 dirty 集合(`restoreStatsDirty`,op/stats.go:104-109)。
- 关停兜底:`op.SaveCache`(op/cache.go:33-40)已注册进 shutdown(cmd/start.go:38),shutdown 倒序执行(utils/shutdown/shutdown.go:33-37);日志队列的退出刷盘可挂同一机制(shutdown.Register 一个 flush 函数)。
- 注意:task 框架没有优雅停止(RUN 不可停),进程退出依赖 shutdown.Listen;批量落库任务要容忍被中途杀死(fork 的日志丢失窗口 = 一个落库周期)。

---

## 写库挂钩点建议

### 1. RelayLog 组装位置:handler.go 终态调用点收敛,不在 state.go 内部

- **推荐**:在 `Forward` 闭包内(handler.go:79 附近)准备上下文,终态 10 个 return 点(handler.go:84,131,139,201,262,264,268,339,341,345)收敛。最干净的做法是** defer 一个 finalize**(闭包变量记录最后状态),覆盖所有 return 路径,包括 `wait` 退避期的取消(state.go:127 → handler.go:91/101/111/122/227 的 return,此时无渠道上下文,attempts 为已积累的部分)。
- 理由:
  - 渠道/凭据/模型/item/UA/raw 全在 handler 闭包作用域;state.go 的 markXxx 拿不到这些;
  - markXxx/finishLocked 持全局 `mu`(state.go:56),DB 写入不能进锁。finishLocked 里 `StatsDailyUpdate` 在跨天时已会发生同步 DB 写(op/stats.go:237 → statsSaveDBWithDailyOverride),这是既有现状,新增日志不应加重它;
  - 组装完的 RelayLog **enqueue 到带锁/带缓冲的队列**,由定时任务批量落库(复用 task 框架 + StatsSaveDBTask 模式),终态路径零 DB I/O。
- 与 fork 对齐:fork 的出口是 `metrics.Save → op.RelayLogAdd`;上游等价物是在终态处调 `op` 层新函数(如 `op.RelayLogEnqueue`),而非在 finishLocked 里做。
- 入口级拒绝(rejectRequest,handler.go:46/56/63/72)**没有 RequestState**——fork 是否记录这类请求需对照 fork 行为决定;若要记录,需单独插桩。

### 2. attempts 链重建

- Forward 闭包加 `attempts []model.ChannelAttempt`(与 failedItemID/failures 并列,handler.go:79-80);单 goroutine 顺序写,无锁。
- 事件点:startRound 后(handler.go:155)登记本轮四元组(channel/channelKey/channelModel/targetProtocol);finishRound 失败(handler.go:197)与成功(handler.go:233)定稿 status/msg/duration;取消/中止分支(handler.go:199-208)按语义标 canceled/interrupted;非流式与流式的写失败/取消(handler.go:256-268, 282-345)更新最终轮状态。
- 未成轮的退避(无目标/凭据禁用/渠道删除/冷却跳过)不产生轮次也不进 attempts,与 fork 只记真实上游调用对齐;若产品上要记录「空转等待」,插桩点在 handler.go:100-126 的各 wait 分支(当前完全无痕)。

### 3. 风险点

- **锁**:全局 `mu`(state.go:56)保护 requests/watchers;队列交接用独立 mutex + slice 或 buffered channel(参照 `channelStatsNeedUpdate` 模式,op/stats.go:27-34),绝不在 markXxx/finishLocked 锁内做 I/O。
- **goroutine**:task.runTask 每次 `go fn()`(task.go:104),落库任务可能与请求 goroutine 并发取队列→队列必须有锁;进程退出时 task 无停止机制,靠 shutdown.Register flush(utils/shutdown/shutdown.go:19-22);`finishLocked` 可能被 `Interrupt`(state.go:109-121,管理端人工中止)间接触发,队列写入要幂等/一次性(finalize 只执行一次,用 sync.Once 或状态判断)。
- **ctx**:终态处客户端 ctx 可能已取消,**落库一律用 `context.Background()`**(finishLocked 里 StatsDailyUpdate 已如此,state.go:198);流式聚合用 `context.WithoutCancel(ctx)`(handler.go:322)可参考。
- **relay_logs 表名陷阱(最大的坑)**:迁移 009(internal/db/migrate/009.go:27-31)在 **AfterAutoMigrate** 阶段无条件 DROP TABLE `relay_logs`(已执行过的库按 MigrationRecord 跳过,migrate.go:95-100)。迁移顺序 = BeforeAutoMigrate → db.AutoMigrate(db.go:55-71,注册即建表)→ AfterAutoMigrate **按 Version 升序**(migrate.go:62-64)。全新库上:AutoMigrate 先建 relay_logs → 009 立刻删掉它 → 表不存在。**规避**:新表换名(如 `relay_logs`→其他),或新增 Version>9 的 AfterAutoMigrate 迁移在 009 之后重建(两者都可行,换名最简单);已发布迁移 009 本身不可修改(legacy.go:1-5 的约定)。
- **内存/体积**:RequestState 的 body/responseBody 都在内存(state.go:45-46),RelayLog 若存请求/响应全文,DB 体积会快速膨胀——对齐 fork 的字段取舍;内存侧 maxFinished=50 裁剪(state.go:204-217)与持久化不冲突,但 SSE 日志页(handlers/log.go)读的是内存,持久化查询需另开 API。
- **依赖**:op 层新模式若进 `db.AutoMigrate`(internal/db/db.go:55-71)记得同步 `model/backup.go` 的 DBDump(model/backup.go:7-25,导入导出是否带日志需产品决定,fork 通常不带)。

## Caveats / Not Found

- axonhub 库:go.mod 锁定 `v0.0.0-20260901162339-94e0d7c781e4`,但本地 module cache 只解出了 `v0.0.0-20260810024229-4495aa3cda46`;`ReadHTTPRequest` 保留请求头的结论验证自缓存中的旧版(utils.go:19-31),两个版本签名未变,行为应一致,构建时以实际版本为准。
- 前端设置页(web/src)未调研,第 7 节前端改动列为待补。
- fork dev 分支的具体代码(metrics.go / RelayLogAdd / ChannelAttempt 字段)不在本工作区,本文件只对照任务描述中的转述,字段对齐以 fork 实际代码为准。
