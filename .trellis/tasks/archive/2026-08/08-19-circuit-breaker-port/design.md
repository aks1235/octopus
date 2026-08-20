# Design — ⑦ 熔断器(X+ 混合方案)

> 用户已确认粒度方案 = **X+**:保留上游「分组活动渠道」模型,叠加熔断器,并在活动渠道被熔断时自动推进到下一个优先级渠道。
> 不移植 dev 的 `balancer.go`/`iterator.go`/`session.go`,不做负载均衡。

## 1. 方案边界(X+)

**移植**: 仅 `internal/relay/balancer/circuit.go`(状态机,~190 行),B1 改造为两段 key。
**新增**: `op.GroupAdvanceActiveItem`(活动渠道熔断时推进)。
**接入**: `internal/relay/execution.go` 三处 + `internal/model/setting.go` 三个 key。

**不做**:
- 不移植 `balancer.go`(4 策略)/`iterator.go`(编排)/`session.go`(粘性),不引入 `GroupMode`/`Weight`/`SessionKeepTime`。
- 不改 `model.Group`/`model.GroupItem` 字段(⑤ 前端零返工)。
- 不碰 `resolveTarget` 的禁用/无 key 错误路径(配置态,靠人工切;不属于熔断范畴)。
- 不新增 `AttemptCircuitBreak` 状态常量,熔断跳过复用 ⑥ 的 `AttemptSkipped`(error message 区分)。

## 2. 两个部件如何配合

- **熔断器(circuit.go)** = 决策"这个 channelID:modelName 现在能不能用"。连续失败 K 次(threshold)→ Open(隔离冷却),冷却到期 → HalfOpen 放一个试探;试探成功 → Closed,失败 → Open(tripCount++ 冷却翻倍)。保留 dev 全部语义(含 30s HalfOpen 挂死回退)。
- **自动推进(GroupAdvanceActiveItem)** = 当 `executeAttempt` 查到活动渠道被熔断(Open/cooling)时,把 `ActiveItemID` 推进到组内下一个 priority 渠道,复用上游已有的 `notifyGroupActiveItemChanged` —— `execution.go` 主循环本就在 `<-activeItemChanged` 上等待,推进后自动唤醒、`resolveTarget` 取到新渠道。

**关键决策:只在熔断器 trip 后推进,不在每次失败推进。**
- 保留对瞬时抖动的 K 次容忍(同一渠道连续失败 < threshold 时只重试同渠道,和 dev 语义一致)。
- 只有持续失败达 threshold → trip → 下次 `executeAttempt` 查到 Open → 推进。避免"一次网络抖动就踢掉好渠道"。

## 3. circuit.go 改造点(B1,相对 dev 原实现)

仅改签名与 key 拼装,状态机逻辑原样保留:

| 函数 | dev(三段) | X+(两段) |
|---|---|---|
| `circuitKey` | `(channelID, keyID int, modelName string)` → `"%d:%d:%s"` | `(channelID int, modelName string)` → `"%d:%s"` |
| `IsTripped` | `(channelID, keyID int, modelName string)` | `(channelID int, modelName string)` |
| `RecordSuccess` | 同上 | 同上 |
| `RecordFailure` | 同上 | 同上 |

其余(`CircuitState`/`circuitEntry`/`getOrCreateEntry`/`getThreshold`/`GetCooldown`/三态 switch/指数退避 `base<<(tripCount-1)` shift≤20/30s HalfOpen 回退)逐行照搬 dev,不改。

依赖不变: `internal/model`(SettingKey)、`internal/op`(SettingGetInt)、`internal/utils/log`。包名 `balancer`,路径 `internal/relay/balancer/`。

## 4. setting.go 改造(加回三 key)

照搬 dev `internal/model/setting.go` 的对应三行:

- const 块加 `SettingKeyCircuitBreakerThreshold = "circuit_breaker_threshold"` 等 3 个(注释照 dev)。
- `DefaultSettings()` 加 3 项默认值(threshold=5 / cooldown=60 / maxCooldown=600)。
- `Validate()` 的整数校验 case 加这 3 个 key(并入现有 `SettingKeyModelInfoUpdateInterval, ...` 列表)。

## 5. GroupAdvanceActiveItem 设计(op 层)

新增于 `internal/op/group.go`:

```go
// GroupAdvanceActiveItem 在当前活动渠道被熔断时,把活动项推进到下一个优先级的渠道。
// fromItemID 用于 CAS:仅当当前 ActiveItemID 仍等于 fromItemID 时才推进,避免并发请求重复跳过渠道。
// 返回是否推进成功(无可推进/ CAS 失败均返回 false,不报错)。
func GroupAdvanceActiveItem(groupID, fromItemID int, ctx context.Context) bool {
    group, ok := groupCache.Get(groupID)
    if !ok || len(group.Items) == 0 { return false }
    items := make([]model.GroupItem, len(group.Items))
    copy(items, group.Items)
    sort.Slice(items, func(i, j int) bool { return items[i].Priority < items[j].Priority })
    cur := -1
    for i, it := range items { if it.ID == fromItemID { cur = i; break } }
    if cur < 0 { return false }
    next := items[(cur+1)%len(items)]
    if next.ID == fromItemID { return false } // 只有一个项
    res := db.GetDB().WithContext(ctx).Model(&model.Group{}).
        Where("id = ? AND active_item_id = ?", groupID, fromItemID).
        Update("active_item_id", next.ID)
    if res.Error != nil || res.RowsAffected == 0 { return false }
    groupRefreshCacheByID(groupID, ctx)
    notifyGroupActiveItemChanged()
    return true
}
```

**要点**:
- CAS(`WHERE active_item_id = fromItemID`):并发请求同时查到 A 熔断,只有第一个推进 A→B,其余看到 active≠A 返回 0 行,不重复跳。
- next = priority 升序的下一项,末尾回绕到第 0 项(全熔断时 A→B→A 循环,但每步间隔 retryInterval,非紧循环,且有冷却→HalfOpen 探打破局)。
- 组只有 1 个渠道:推进返回 false,请求等冷却→HalfOpen 探测(无替代渠道,必须等)。正确。
- 推进后 `notifyGroupActiveItemChanged()` 唤醒 execution.go 主循环的 `<-activeItemChanged`。
- 不报错(返回 bool),失败时调用方照常 skip+return,主循环等 retryInterval 重试。
- `sort` 新增 import。

## 6. execution.go 接入(三处,不覆盖 ⑥ 终态)

`executeAttempt`、`handleAttemptFailure`、`commitAttempt` 均在 ⑥ 已设 `Status`/`Duration` **之后**追加调用,不改 ⑥ 字段。

### 6.1 executeAttempt 顶部 — 查熔断 + 推进(IsTripped)

在 `func (e *execution) executeAttempt(...)` 第一行(client 构建之前)插入:

```go
// ⑦ 熔断器:活动渠道被熔断时推进到下一个渠道,本次跳过(复用 ⑥ recordUnavailableTarget)
if tripped, remaining := balancer.IsTripped(channel.ID, item.ModelName); tripped {
    op.GroupAdvanceActiveItem(item.GroupID, item.ID, ctx)
    err := fmt.Errorf("circuit breaker open, %v remaining", remaining)
    e.recordUnavailableTarget(item, channel, err)
    return false, err
}
```

- `item.GroupID`/`item.ID` 均为 GroupItem 已有字段(op/group.go:314 用过)。
- 复用 ⑥ 的 `recordUnavailableTarget`(设 `AttemptSkipped`+Error),不碰其字段。
- 返回 err → 主循环 `retryErr != nil` → 等 retryInterval → 重试,`resolveTarget` 取到推进后的新活动渠道。
- execution.go 新增 import:`fmt`、`github.com/bestruirui/octopus/internal/relay/balancer`。

### 6.2 handleAttemptFailure 末尾 — 记失败(仅真实失败分支)

⑥ 的 handleAttemptFailure 有三出口:interrupted(line 210)、canceled(219)、真实失败(222-229)。
**只在真实失败分支**末尾(`e.emit` 之后、`return false, result.err` 之前,约 line 228)加:

```go
// ⑦ 熔断器:真实上游失败才计数(取消/中断不计)
balancer.RecordFailure(channel.ID, item.ModelName)
```

排除 interrupted/canceled 的理由:它们是客户端行为,非渠道健康问题,不应拉低渠道熔断计数。
含 `errUnsupportedTarget`(走 222 分支但跳 metrics):计入失败。设计权衡见 §7。

### 6.3 commitAttempt 顶部 — 记成功(进入即代表上游成功)

进入 commitAttempt 表示 `executeUpstream` 已成功返回响应(渠道健康)。在 ⑥ 设 `Status=AttemptSuccess`+`Duration`+`emit(ResponseCommitted)` 之后(约 line 237 后)、`result.response.Commit` 之前加:

```go
// ⑦ 熔断器:上游成功 → 重置熔断状态(HalfOpen 探测成功 → Closed)
balancer.RecordSuccess(channel.ID, item.ModelName)
```

放在 Commit 之前:即使后续客户端写回失败(commit.err),渠道本身是健康的,应记成功。

## 7. 设计权衡与边界

| 点 | 决策 | 理由 |
|---|---|---|
| 推进时机 | 仅 trip 后(非每次失败) | 保留 K 次瞬时容忍,避免抖动踢好渠道 |
| errUnsupportedTarget 计入失败 | 是 | 该渠道确实不能服务此模型,隔离+推进是对的;阈值 5 内仅首请求付 K×retryInterval 成本,trip 后续请求即快速推进 |
| 全渠道熔断 | A→B→A 循环推进,每步等 retryInterval | 非紧循环,冷却到 HalfOpen 探打破局;客户端可 ctx 取消 |
| RecordFailure 排除取消/中断 | 是 | 客户端行为非渠道健康问题 |
| AttemptStatus 复用 AttemptSkipped | 不新增 AttemptCircuitBreak | KISS,避免动 ⑥ log model;error message 区分语义 |
| 熔断状态不持久化 | 进程内 sync.Map,重启清空 | 与 dev 一致;熔断是短时态,重启后重新探测即可 |
| HalfOpen 探测挂死 30s 回退 Open | 保留 dev 逻辑 | 避免渠道永久不可用 |

## 8. 依赖图(无循环)

`relay(execution.go)` → `relay/balancer(circuit.go)` → `{model, op, utils/log}`
`relay` → `op(GroupAdvanceActiveItem)` → `{model, cache, db}`
balancer 不 import relay;op 不 import balancer/relay。与 dev 原依赖图一致,无新环。

## 9. 测试计划

### 9.1 新写 `internal/relay/balancer/circuit_test.go`(纯单元,无 DB)

dev 无独立 circuit_test.go(测在 balancer_test.go 里)。新写,沿用 dev 的**直接改 entry 时间字段**手法(不 sleep):

- `TestCircuitBreaker_TripsAfterThreshold`:5×RecordFailure → tripped;RecordSuccess → closed。(移植 dev,改 2 参)
- `TestCircuitBreaker_ModelScoped`:gpt-4 tripped 而 gpt-3.5 不受影响。(移植,改 2 参)
- `TestCircuitBreaker_HalfOpenTimeoutFallback`:手设 `HalfOpenTime = now-31s` → IsTripped 回退 Open + remaining>0。(移植,改 2 参)
- `TestCircuitBreaker_CooldownToHalfOpen`(新):手设 `LastFailureTime = now-(cooldown+1)` → IsTripped 返回 false(转 HalfOpen);随后 RecordFailure → Open(tripCount++),RecordSuccess → Closed。
- `TestCircuitBreaker_ExponentialBackoff`(新):tripCount=1/2/3 时 GetCooldown 应为 base/base*2/base*4,且封顶 maxCooldown。

key 用 `"1:gpt-4"`(两段),不再有 keyID。每个 test 用 `globalBreaker.Delete(key)` 清理。

### 9.2 GroupAdvanceActiveItem + execution.go 接入

上游 `internal/op/` 无 _test.go 基建,op 单元测试需从零搭 test DB,成本高。**改为父任务 Step 8 smoke 验证**:起容器 → 配多渠道分组 → 模拟首渠道连续失败 → 观察活动渠道自动推进 + 熔断冷却 + HalfOpen 恢复。验收在父任务集成层。

## 10. 兼容性与回滚

- **向后兼容**:setting 三 key 随迁移入库(默认值),旧库升级自动补;execution.go 接入是叠加,熔断器无记录时 IsTripped 返回 false(透明,不影响现有 ①–⑥ 行为)。
- **回滚点**:circuit.go 是新增独立文件,删 `internal/relay/balancer/` + revert execution.go 三处 + revert setting.go 三 key + 删 GroupAdvanceActiveItem 即完全回退,无数据迁移。
- **不破坏上游能力**:图片生成/DeepSeek thinking/SSE 日志流均不走熔断路径(它们经 execution.go,熔断器无记录时透明放行)。

## 11. 与 ⑥ 的接入点冲突复核

⑥ 终态设值点 → ⑦ 追加位置(均在其后,不覆盖):
- `handleAttemptFailure:205-206`(Failed+Duration)→ ⑦ 在 228 后加 RecordFailure。
- `commitAttempt:235-236`(Success+Duration)→ ⑦ 在 237 后加 RecordSuccess。
- `recordUnavailableTarget:282`(Skipped)→ ⑦ 复用调用,不改其内部。
- executeAttempt 顶部 ⑦ 插入在 ⑥ 的 startAttempt(182)之前,不触及 ⑥ 的 attempt 字段。

无冲突。
