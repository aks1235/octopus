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

## 相关

- 健康检查自动禁用/解禁落库与缓存改写:`op.ChannelHealthFail` / `op.ChannelHealthSuccess` / `op.ChannelEnabled`(只动健康状态与启停两列)
- 转发日志持久化(任务4)→ [relay-log-persistence.md](./relay-log-persistence.md)
