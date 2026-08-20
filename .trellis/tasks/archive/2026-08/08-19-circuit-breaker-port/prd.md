# ⑦ 熔断器(改造移植)

## Goal

将 dev `internal/relay/balancer/circuit.go` 的熔断器状态机移植到上游基底(feat/my-features,含 ①–⑥),
接入上游 `internal/relay/execution.go`,使连续失败的上游渠道被短期隔离,冷却后放行试探请求,
试探成功恢复通行,试探失败重新熔断并加倍冷却。粒度按 B1 决策降级为 `channelID:modelName`(两段,上游无 keyID)。

## 背景

- **上游事实**: `d279e1d` 已删除熔断器(-176 行),`internal/relay/balancer/` 在 upstream/master 不存在。
- **上游负载模型**: 「分组活动渠道」——一个分组同时只有一个活动渠道(`group.ActiveItemID`),
  失败按 `group.RetryInterval` 等待重试,靠 `GroupActiveItemChangeSignal()` 通知切换。无渠道级熔断。
- **dev 原实现**: `balancer/` 包含 `circuit.go`(状态机)+ `balancer.go`(4 策略选择)+ `iterator.go`(编排)+ `session.go`(粘性会话),key 维度 `channelID:keyID:modelName`(三段)。
- **B1 决策**: 上游无 keyID 概念 → 降级为 `channelID:modelName`(两段),熔断器其余能力不变。
- **⑥ 已完成的接入点事实**(current execution.go): `handleAttemptFailure:205-206` 设 `AttemptFailed`+`Duration`;
  `commitAttempt:235-236` 设 `AttemptSuccess`+`Duration`;`recordUnavailableTarget:282` 设 `AttemptSkipped`。
  ⑦ 接入必须在这些终态设值点**追加** RecordFailure/RecordSuccess 调用,不得覆盖 ⑥ 已设字段。

## Requirements

### 功能需求

- 三态状态机: `Closed`(正常通行) / `Open`(熔断,拒绝请求) / `HalfOpen`(半开,仅放行单个试探请求)。
- 连续失败计数: 达阈值(`SettingKeyCircuitBreakerThreshold`,默认 5)由 Closed→Open,`TripCount++`。
- 指数退避冷却: 冷却 = `baseCooldown * 2^(tripCount-1)`,上限 `maxCooldown`(默认 base=60s / max=600s)。
- HalfOpen 试探: Open 冷却到期后转 HalfOpen 放行一次;试探成功→Closed(全状态归零);试探失败→Open(`TripCount++`,冷却翻倍)。
- HalfOpen 挂死保护: 进入 HalfOpen 超 30s 仍未完成,回退 Open 重新冷却,避免渠道永久不可用。
- 全局存储: 进程内 `sync.Map`,key=`channelID:modelName`,重启清空(与 dev 一致)。
- 接入上游 execution.go: `executeAttempt` 前查熔断(Open 则跳过该渠道),`handleAttemptFailure` 记失败,`commitAttempt` 记成功。

### 配置项(setting.go)

- `SettingKeyCircuitBreakerThreshold` = `circuit_breaker_threshold`(默认 5)
- `SettingKeyCircuitBreakerCooldown` = `circuit_breaker_cooldown`(默认 60)
- `SettingKeyCircuitBreakerMaxCooldown` = `circuit_breaker_max_cooldown`(默认 600)
- 三者纳入 `Validate()` 整数校验 + `DefaultSettings()` 默认值。

### B1 改造点(相对 dev 原实现)

- `circuitKey(channelID, keyID int, modelName string)` → `circuitKey(channelID int, modelName string)`,去掉 keyID。
- `IsTripped` / `RecordSuccess` / `RecordFailure` 签名同步去 keyID,改 `(channelID int, modelName string)`。
- 其余状态机逻辑(三态/退避/HalfOpen/30s 保护)原样移植,不改语义。

## 接入方式硬关卡(X/Y,规划阶段与用户确认)

粒度方案 B1 已定(key 维度),但「如何接入上游负载模型」有两选项,见 design.md §硬关卡:

- **选项 X(叠加,推荐)**: 仅移植 `circuit.go`,保留上游「分组活动渠道」模型,熔断器作 overlay。
- **选项 Y(移植负载均衡)**: 移植整个 `balancer/` 包,替换上游活动渠道模型为多候选选择。

> 本 prd 的需求与验收标准对 X/Y 通用(两种方案都要满足三态/退避/HalfOpen/接入 execution.go)。
> X/Y 仅影响接入深度与是否恢复多渠道并行,不影响熔断器本身的能力。

## Acceptance Criteria

- [ ] `internal/relay/balancer/circuit.go` 存在,三态 Closed/Open/HalfOpen 语义与 dev 一致。
- [ ] `circuitKey` 为两段 `channelID:modelName`,无 keyID。
- [ ] 指数退避: `base << (tripCount-1)`,上限 maxCooldown,防溢出(shift≤20)。
- [ ] HalfOpen 试探: 成功→Closed 归零,失败→Open tripCount++,冷却到期转 HalfOpen,30s 挂死回退 Open。
- [ ] `setting.go` 加回三个 `CircuitBreaker*` 常量 + 默认值 + 整数校验。
- [ ] `execution.go` 接入: executeAttempt 前查熔断(Open 跳过),handleAttemptFailure 记失败,commitAttempt 记成功。
- [ ] 接入点不覆盖 ⑥ 已设的 `Status`/`Duration` 终态字段(追加调用,不重写)。
- [ ] `go build ./...` 通过;`go test ./...` 通过(`-tags=jsoniter`)。
- [ ] 集成测试: 模拟连续失败→Open→冷却→HalfOpen→成功恢复 Closed / 失败重回 Open。
- [ ] X/Y 粒度方案经用户确认(本规划阶段硬关卡)。

## Notes

- 不引入 dev 的 `balancer.go`/`iterator.go`/`session.go` 除非选 Y。
- AttemptStatus 复用 ⑥ 的 `AttemptSkipped` 表示熔断跳过(error message 区分),不为 ⑦ 新增 `AttemptCircuitBreak` 常量(KISS,避免动 ⑥ log model);若选 Y 且需区分可再议。
- 全局 `sync.Map` 重启清空,不持久化熔断状态(与 dev 一致,熔断是短时态)。
- 本任务是路线 A 最后一项(⑦),完成后一次性 rebase feat/my-features 到 upstream/master 最新。
