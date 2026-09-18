# 选路与渠道禁用契约

> 转发链如何选择分组成员,以及渠道/凭据禁用如何在选路层生效。2026-09-09 由缺陷修复 09-09-v2-fix-channel-disable-routing 沉淀(此前禁用仅有标记作用,不阻断流量)。

---

## 契约:ChannelGrantGet 是转发可用性的唯一裁决点

**What**:`op.ChannelGrantGet(id)` 返回错误当且仅当:授权不存在、渠道模型缺失、凭据缺失、**渠道不存在或 `Enabled=false`**、凭据 `Enabled=false`。取到的授权必然可直接转发,调用方无需再逐项检查。

**Why**:转发链(handler 循环)与选路层(route 包)都依赖它判断成员可选性。若渠道级 Enabled 不在校验内,禁用就只剩标记/展示作用,健康检查自动禁用(连续失败达阈值)无法止损。

**调用方**(改动前先查全):
- `internal/relay/handler.go` Forward 循环:pick 之后取授权。
- `internal/relay/route.go` `selectableGroupItems`:选路前过滤(见下)。

## 契约:禁用成员在选路层过滤,与冷却同级

**What**:`pickGroupItem` 选路前经 `selectableGroupItems` 剔除不可选成员(即 `ChannelGrantGet` 失败者),再进入手动/亲和/冷却/探测逻辑。

**语义边界**:
- **禁用 ≠ 删除**:禁用渠道的成员仍留在分组里,正则吸纳(op/group.go `syncRegexGroupItems`)不看启停;界面以 `Available` 展示口径(`channel.Enabled && channelKey.Enabled`,groupSnapshot)标记不可用。过滤只影响选路,不影响分组内容。
- **不选、不探测、不计数**:禁用成员不产生失败计数(不走 `recordRouteFailure`),不占用恢复探测名额(ProbeItemID),与冷却到期探测是两条路径。
- **亲和立即失效**:亲和期内当前成员被禁用,`pickGroupItem` 清 CurrentItemID/AffinityUntil 并立即重选,不等亲和期自然结束——这是"人工禁用后实时请求立即改走其他成员"的保证。
- **全员不可用 → 零值**:pick 返回零值,handler 走既有 wait 循环(等待重新启用/补成员),不 panic、不记 attempts、不刷日志;手动模式激活成员被禁用同此语义。
- **恢复即回选**:渠道重新启用(人工或健康检查自动解禁,两者都就地更新 `channelCache`)后,下一轮选路即恢复参与,残留冷却按正常探测逻辑放行。

**Bad/Good**:
- Bad:在 handler 循环里对禁用渠道 wait+continue —— pick 每轮仍选同一最高优先级成员,低优先级成员永远得不到机会(禁用者堵死全组)。
- Bad:把禁用上报为失败进冷却 —— 污染失败统计与渠道健康计数。
- Good:选路前用同一 `ChannelGrantGet` 口径过滤,禁用语义一处定义。

## 已知坑:布尔列 `default:true` 与 gorm Create 零值

`Channel.Enabled`/`ChannelKey.Enabled` 带 `gorm:"default:true"`。**Create 时传 `false` 会被 gorm 忽略、落库仍为 `true`**(零值字段走列默认值)。测试播种或迁移脚本须先建后 `Update("enabled", false)`,或用 `Select("*")` 强制包含该列。见 `internal/relay/route_disable_test.go` 的播种方式。

---

## 模式:管理端"测试/探测"类请求经 buildOutbound 合成临时 grant 复用转发构请求路径

**问题**:管理端要做"按凭据×模型发真实请求验证连通性"一类功能时,若另写 HTTP 客户端,测到的地址拼接/凭据注入/代理选择/参数覆盖与真实转发不一致,验证结果失真。

**解法**:`buildOutbound(channel, grant, channelKey, want)` 只消费 `grant.Protocols`(位掩码)与 `grant.ID`(仅错误文案)。合成临时 `ChannelGrant{Protocols: <位>}` 即可按指定协议拿到出站转换器——不查库不落库,零改动 relay 既有文件。凭据探测(probe)与凭据连通性测试(keytest)共用此路径。协议位来源:优先按「模型×凭据」实际授权位(表单 grants),查不到/位 0 回退全协议位。

```go
// internal/relay/keytest.go
grant := model.ChannelGrant{Protocols: model.ProtocolAnthropicMessage | ...}
outbound, _, _, err := buildOutbound(channel, grant, channelKey, protocol)
```

**边界**:合成 grant 只用于构请求,不得进入选路/统计/日志的 grant 语义(那些仍走 DB 真实对象)。参考实现 `internal/relay/keytest.go` + `keytest_test.go`(含授权位过滤与回退用例)。

---

## 相关

- 健康检查自动禁用/解禁落库与缓存改写:`op.ChannelHealthFail` / `op.ChannelHealthSuccess` / `op.ChannelEnabled`(只动健康状态与启停两列)
- 转发日志持久化(任务4)→ [relay-log-persistence.md](./relay-log-persistence.md)

## 模式:自定义请求头的 `{client_header:NAME}` 占位符

**What**:`applyChannelConfig`(`internal/relay/channel.go`)应用渠道自定义 Header 时,值中的 `{client_header:NAME}` 片段替换为**客户端请求**同名头的实际值(`request.Headers.Get(NAME)`,此时出站请求头已并入客户端原始终头),取不到替换为空串。占位符正则包级预编译。

**Why**:部分上游(如 Cloudflare 站点)只放行带浏览器 UA 的请求——fork 曾因客户端缺 UA 遭 CF 1010 拦截(ADR-0004),该占位符让上游看到客户端真实 UA 而非网关默认值。

**边界**:
- 敏感 Header 的跳过判断在替换**之前**,不受占位符影响。
- 探测/keytest 走合成请求无客户端头,占位符替换为空串(上游 v0.13.3 同款行为)。
- 单测:`internal/relay/channel_test.go`(真实替换/缺失置空/无占位符零变化/多占位符/敏感头不被动)。

> 2026-09-11 随任务 09-11-port-upstream-0134 自上游 v0.13.3(1c48ee5)移植。

## 契约:客户端请求头透传与 UA 指纹(WAF 403 排障路径)

**What**:同协议透传(`buildPassthroughRequest` → `httpclient.MergeInboundRequest`)把客户端请求头并入上游请求,过滤名单仅三类:认证类、库自管类(Content-Length/Transfer-Encoding/Accept-Encoding/Host)、逐跳类(Connection/Keep-Alive/Te/Upgrade/X-Forwarded-For 等)。**`User-Agent` 不在名单里,原样透传**——上游 WAF 看到的 UA 是打到 octopus 的客户端的 UA,不是网关自己的。

**Why**:2026-09-15 实证:同一 Key 同一渠道,`Agents/Python 0.18.0` UA 成功、`OpenAI/Python 2.44.0` UA 连续 `403 Forbidden: Your request was blocked`。公益站前置 WAF 按 UA 指纹拦截,裸 OpenAI SDK 的 UA 是常见拦截目标。

**排障路径**(看到 upstream 403 "blocked" 时):
1. 查 `relay_logs.user_agent`(记录的是**客户端** UA,非上游所见——两者一致因透传);
2. 对比同时段同渠道成功/失败请求的 UA 分布;
3. 缓解:渠道自定义 Header 加 `User-Agent: <放行的 UA>`——UA 非敏感头,自定义值会**覆盖**透传值(`applyChannelConfig` 只对「已存在且敏感」的头跳过)。

**边界**:
- `relay_logs` 里 response_content 含关键词的搜索会命中会话自身内容(假阳性),排障用 `attempts` JSON 里的 msg 精确定位。
- failover 大轮转 × 短冷却会在 WAF 眼里变成高频异常请求,愈撞愈拦(2026-09-15 单请求 111 次尝试实例)。

## 契约:failover/roundrobin 的请求内游标(v2.2.0 起,对齐 v1 语义)

**What**:转发请求的成员遍历由**请求作用域游标**(`routeWalk.lastItemID`)驱动,规则:粘住(当前成员未进冷却就重复返回,同成员重试)→ 向下走(从上一位找第一个未冷却成员)→ 到尾绕回头部。冷却只做闸门,**高优先级成员中途恢复不插队**,等游标绕回。全员冷却时沿用既有 `wait(MemberRetryIntervalSeconds)` 循环(无上限、无新报错,冷却到期自然续走)。

**Why**:旧实现失败后 `CurrentItemID=0` 从队头重扫(抢占式),成员多、冷却短时同一请求反复回头撞刚失败的渠道(2026-09-15 实证:单请求 111 次,K API 被 12 轮回炉)。游标语义 = v1(`v1.5.3 relay/balancer`)的用户确认对齐:**回炉是特性,「请求内试过即排除」已被否决,勿再提**。

**模式起点差异**:
- failover:亲和窗口内从 `CurrentItemID` 起(亲和保留,语义不变),否则队头。
- roundrobin(v2.2.0 新增,per-group `sync.Map` 原子计数器):起点按成员数取模旋转;**不参与亲和**——起点不看亲和,`recordRouteFailure` 的亲和武装仅对 failover 门控(轮询组 runtime 不残留 `affinity_until`)。
- manual:不走游标,`active_item_id` 原逻辑。

**游标用 itemID 定位而非下标**:分组每轮重读、成员集合会变,成员被删后 `indexOfItem == -1` 落回轮询计数器位,语义仍正确。

**中止不计失败**(R9):pre-commit 取消轮次**不追加** `AttemptFailed`(append 在 `ctx.Err()` 检查之后);流式中途取消不写 `RequestFailed` 渠道统计。中止不算渠道故障;冷却/探测本就不受中止影响。注意:人工主动中止(round cancel)仍记 AttemptFailed,与改动前一致。

**测试**:`internal/relay/route_cursor_test.go`(粘住/向下不插队/绕圈/全员冷却零新增/探测单飞/亲和起点/轮询旋转与隔离/轮询不武装亲和/渠道连续性/中止不计失败×2);渠道连续性由 priority 合成排序保证(同渠道成员连续,见 [group-channel-order.md](./group-channel-order.md))。

## 已知问题

- SQLITE_BUSY:5 分钟兜底重算与吸纳撞锁,日志每 5 分钟告警,待根治。
- 渠道健康恢复等其他路径仍受亲和窗口影响(顺序端点已重置,见 [group-channel-order.md](./group-channel-order.md));游标语义下若要"恢复立即接管"需另行设计。

## 契约:成员冷却的指数退避梯度(v2.2.0 起,对齐 v1 熔断)

- 同一成员**连续**触发冷却时,时长 = `min(member_cooldown_seconds × 2^(连续次数-1), member_max_cooldown_seconds)`;首次为 base,封顶默认 600s。连续次数存 `RouteState.trips`(未导出,与 Cooldowns 同生命周期;成功一次清零,成员删除时随 `groupRouteLocked` 清理)。
- 公式实现 `route.go cooldownSeconds`:移位上限 20 + 封顶双重防溢出(`base > max>>shift` 时直接取上限)。
- 新旋钮 `member_max_cooldown_seconds`(分组级,默认 600);旧分组加载时经 `groupRefreshCache → NormalizeGroupRelayConfig` 收敛 0 值,保证 ≥ base。
- 语义不变项:manual 无冷却;探测单飞、亲和、请求内游标不受影响;`RouteStateOf` 只暴露 deadline,档位不出 JSON。
