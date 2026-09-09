# 修复:渠道禁用(人工/自动)在选路转发链不生效

## Goal

渠道级 `Enabled=false`(人工禁用或健康检查自动禁用)后,该渠道的成员不应再被选中转发。当前选路转发链全程不看渠道级 Enabled,禁用仅有标记/展示作用,健康检查自动禁用(任务3)因此无实际止损效果。

## 缺陷事实链(2026-09-09 任务4 规划期核实,均带 file:line)

- `internal/relay/route.go:66-68`:`pickGroupItem` 注释自陈「渠道禁用或缺少密钥由调用方发现并作为一轮失败上报,该成员随即进入冷却」——**该设计未实现**。
- `internal/op/group.go:349` `groupRefreshCache`:分组缓存构建不剔除禁用渠道成员;`internal/op/group.go:401` `groupSnapshot` 的 `Available` 仅为展示字段(注释:「仍列出该成员但标记不可用」)。
- `internal/relay/handler.go:109` `op.ChannelGrantGet`:仅校验 key 级 Enabled(`internal/op/channel.go:455`),渠道级不查;校验失败走 wait+continue,**不作为失败上报、不进冷却**。
- `internal/relay/handler.go:120` `op.ChannelGet` 取渠道后无 Enabled 检查,照常 `buildOutbound` 转发。
- 后果:人工禁用渠道继续吃流量;任务3 自动禁用(连续失败达阈值)同样不阻断流量。

## Requirements

- 渠道级禁用(Enabled=false)后,该渠道的授权成员在选路时被跳过(不再被选中、不再发起上游请求)。
- 修复位置须最小化并贴合上游「选路层过滤」设计意图(与 route.go:67 注释对齐,或在 handler 循环 ChannelGrantGet/ChannelGet 处检查),**不引入新的缓存副本**;注意与 `Available` 展示口径(op/channel.go:485)和正则分组吸纳(op/group.go:275 注释:禁用不等于删除)的语义边界——禁用渠道的成员仍在分组里(展示「不可用」),只是不被选路。
- 跳过方式与「冷却」机制的交互要明确:禁用成员应视为不可选(优先级同冷却),不占用探测名额、不产生失败计数。
- key 级禁用现状( grant 校验失败 → wait 循环)是否也纳入本次修复,规划时裁定(倾向:一并理顺,避免禁用语义两套)。

## Acceptance Criteria

- [ ] 人工禁用渠道后,实时请求立即改走其他成员(容器实测,relay_logs/SSE 日志可证)。
- [ ] 健康检查自动禁用后,该渠道不再接收新请求(任务3 功能闭环补全)。
- [ ] 自动解禁/人工重新启用后恢复选路。
- [ ] 全组唯一成员被禁用时:请求行为符合预期(等待/明确报错,不 panic 不死循环刷日志),与手动模式语义一致。
- [ ] octopus-verify 闭环通过。

## Notes

- 来源:任务4(09-08-v2-feat-log-persistence)规划期发现,与日志持久化无耦合,独立修复独立验证。
- 建议排在任务7(冒烟矩阵+切换)之前完成;不影响任务4 实施。
