# 冷却梯度:连续熔断指数退避(v1 对齐)

## Goal

对齐 v1 熔断器的**指数退避**语义:成员每次被冷却(熔断触发)时,冷却时长按 `base × 2^(连续触发次数-1)` 升级并封顶;该成员成功一次即清零重来。解决 v2 固定冷却导致的"坏渠道恢复慢、好渠道被反复短冷却拖累"。

## Background(2026-09-18 用户要求,v1 公式已核实)

- v1(`v1.5.3 internal/relay/balancer/circuit.go` `GetCooldown`):`cooldown = base << (tripCount-1)`,`tripCount` 每次熔断触发递增(Closed→Open 或 HalfOpen→Open 失败),封顶 `maxCooldown`;成功(`StateClosed`)时 `TripCount = 0`。默认 base=60s,max=600s,均为设置项。
- v2 现状:`recordRouteFailure` 固定 `now + MemberCooldownSeconds`,无梯度、无历史记忆(每次冷却时长一样)。
- v2 的冷却状态在 `RouteState.Cooldowns map[itemID]deadline`,亲和/探测同属该结构。

## Requirements

- R1 **梯度**:同一成员连续触发冷却时,时长 = `min(member_cooldown_seconds × 2^(连续次数-1), 上限)`;首次冷却为 base。
- R2 **封顶**:新分组配置项 `member_max_cooldown_seconds`(默认 600,`binding:"omitempty,min=1"`,`NormalizeGroupRelayConfig` 补齐并保证 ≥ base)。
- R3 **清零**:该成员成功一次(recordRouteSuccess)即把连续次数清零,下次冷却回到 base。
- R4 计数存 RouteState(进程内,与冷却同生命周期):成员被删除/分组重置时随 RouteState 一并清理。
- R5 只影响 failover/roundrobin 的成员冷却;manual 零变化;探测单飞、亲和、游标语义不动。
- R6 前端:分组编辑器旋钮区加「冷却上限(秒)」;i18n 三语。
- R7 连续次数溢出防御:shift 上限(如 20)与封顶双重保护。

## Non-goals

- 不改熔断阈值/尝试次数语义(MemberMaxAttempts 不变)。
- 不做半开探测状态机(v2 的探测单飞 + 冷却到期即候选已是等价简化)。
- 不做全局设置项(base/max 都是分组级,与 v2 现有六旋钮同层)。

## Acceptance Criteria

- [ ] AC1 同一成员连续三次冷却:时长依次 base、2×base、4×base(封顶内)
- [ ] AC2 超过封顶按 max 取值;shift 溢出有防护
- [ ] AC3 该成员成功后连续次数清零,再冷却回到 base
- [ ] AC4 成员被删除/分组路由重置后计数一并清理,不泄漏
- [ ] AC5 manual 模式、游标、亲和、探测语义零回归
- [ ] AC6 前端旋钮 + i18n 三语;octopus-verify 通过

## Notes

- 轻量任务,PRD-only。改动面:model/group.go(配置项)、relay/route.go(计数与公式)、前端 Editor 旋钮 + i18n、route 单测。
- 2026-09-16 游标任务曾将其列为非目标("观察游标语义落地后再定"),现用户明确要求补齐。
