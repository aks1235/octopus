# GroupItem 孤儿对账定时任务(子任务 B)

> 父任务:`07-30-channel-group-model-sync`

## 背景

`ChannelAutoGroup`(`internal/helper/channel.go:61-141`)只增不减(OnConflict DoNothing)。除「删渠道」(`ChannelDel:387-470` 已级联清理)和「AutoSync 渠道同步 diff」(`SyncModelsTask:69-78` 按 `(channelID,modelName)` 删)两条线外,以下路径产生的孤儿 GroupItem 无兜底清理:

- `AutoSync=false` 渠道被人工 `ChannelUpdate` 改 `Model` 字段后,旧模型对应 GroupItem 残留。
- 手动 `GroupItemAdd` 加入的模型,渠道侧后续删除该模型(非 AutoSync 路径)→ GroupItem 残留。
- 任何未来遗漏路径。

全局无周期对账。

## Goal

新增周期对账定时任务,扫描全部 GroupItem,清理「渠道已不存在」或「模型已不在该渠道的 `Model+CustomModel` 列表中」的孤儿引用,并刷新受影响 Group 缓存。作为所有遗漏路径的统一兜底。

## Requirements

- 新增任务函数(建议 `internal/task/reconcile.go`,注册于 `internal/task/init.go`),周期由 setting `SettingKeyGroupReconcileInterval` 控制(单位分钟,默认 60)。
- 对账逻辑(单次执行):
  1. `op.GroupItemList(ctx)` 取全量 GroupItem(若现有无全量查询,在 `internal/op/group.go` 增补)。
  2. `op.ChannelList(ctx)` 取全量渠道,构建 `map[channelID]→ model set`(由 `Model`+`CustomModel` 拆分并集)。
  3. 遍历每个 GroupItem:
     - 若 `channel_id` 不在 map 中 → 标记删除(渠道已删,理论上 ChannelDel 应已清,这里兜底)。
     - 若 `channel_id` 在但 `model_name` 不在该渠道 model set 中 → 标记删除(模型已下架)。
  4. 按 `(channelID, modelName)` 分组批量调 `op.GroupItemBatchDelByChannelAndModels`(已存在,`group.go:312-347`),或新增按 `group_item.id` 批删的函数(避免逐条删)。
  5. 对受影响 `group_id` 集合调 `groupRefreshCacheByIDs`(已存在)。
- 不与 `SyncModelsTask` 的删除冲突:对账任务只删"渠道已删"和"模型不在渠道列表"两类,AutoSync 渠道正常同步产生的删除是它的子集,重复无害(幂等)。
- 日志:执行结束打印扫描总数、清理数、受影响 group 数;清理数为 0 时低级别日志(避免刷屏)。
- 不改 `ChannelDel`、不改 `ChannelAutoGroup` 的"只增不减"语义(对账任务就是它的反向补丁)。

## Acceptance Criteria

- [ ] 容器内存在孤儿 GroupItem(渠道已删 / 模型已下架两类各造一条)→ 触发一次对账任务(可手动触发或缩周期)→ 孤儿被清理,受影响 Group 缓存刷新。
- [ ] AutoSync 渠道正常模型仍在、渠道仍存 → 对账任务不误删。
- [ ] 对账幂等:连续两次执行,第二次清理数为 0。
- [ ] setting 周期可调。
- [ ] Docker 构建通过,容器内 `octopus start` 后对账任务按周期注册并执行(日志可见)。

## Notes

- 与子任务 A 的关系:A 置 `Enabled=false` 不删渠道,对账任务**不**因 `Enabled=false` 而清理其 GroupItem(禁用≠删除,只是路由不选)。对账只按"渠道行存在性"和"模型在列表中"判断。
- 验证一律 Docker 构建。
