# 渠道-分组模型一致性同步与级联清理(父任务)

## 背景

当前 Octopus 的「渠道 → 分组」模型一致性链路存在三处缺陷,运维难以感知上游失效,且分组里会残留失效引用:

1. **同步失败静默跳过** —— `SyncModelsTask`(`internal/task/sync.go:38-42`)在 `FetchModels` 返回 err 时只 `log.Warnf` 后 `continue`,既不清渠道模型、也不落任何失败标记,`model.Channel` 结构上无失败状态字段。运维只能扒日志才能知道某渠道上游挂了。
2. **弱孤儿无兜底** —— `ChannelAutoGroup`(`internal/helper/channel.go:61-141`)只 `GroupItemBatchAdd`(OnConflict DoNothing),从不删除。`AutoSync=false` 的渠道被人工改 `Channel.Model` 后,旧 GroupItem 不会被清理;删渠道路径(`ChannelDel:387-470`)虽已级联清理,但全局无对账兜底。
3. **前端渠道名映射回退** —— 后端 `model.GroupItem` 只回 `channel_id`(`internal/model/group.go:23-29`,handler `group.go:42-49` 直接返缓存),前端 `web/src/components/modules/group/Card.tsx:102` 用 `useModelChannelList()` 按 `channel_id+model_name` 双键映射渠道名,映射查空即回退 `Channel ${item.channel_id}`,导致渠道改名/失效后分组里显示成 `Channel 1 / Channel 2` 这类无意义兜底名。

## Goal(父任务)

让「渠道模型同步 → 分组引用」这条链路具备失效感知、孤儿收敛、可读展示三件事,使分组中显示的每一条 GroupItem 都对应一个真实可用且名称正确的渠道模型,运维无需扒日志即可感知上游失效。

## 子任务拆分

父任务本身不做实现,下属三个可独立规划/实现/验证/Docker 构建验证的子任务:

- **子任务 A:`sync-fail-disable`** —— 同步失败标记 + 连续失败自动临时禁用渠道
- **子任务 B:`group-item-reconcile`** —— GroupItem 孤儿对账定时任务(覆盖所有遗漏路径)
- **子任务 C:`group-channel-name-display`** —— 前端渠道名展示修正(后端 DTO 补 channel_name/enabled + 前端按 DTO 渲染,兜底区分"渠道已删除")
- 依赖关系:C 不依赖 A/B 的实现产物,但都面向同一链路,可并行规划。若 A 先落地(渠道被禁用),C 的"渠道已禁用"展示态需要识别 A 落下的 `Enabled=false`,顺序上 A 的字段约定先定下来再写 C 更稳——已在 C 的 prd 里写明该约定。

## 跨子任务验收标准(父级)

- [ ] 同步连续失败超过阈值时,渠道被自动置 `Enabled=false`,前端在分组列表中明确区分"渠道已禁用",不再静默。
- [ ] 全局存在周期对账任务,任何渠道删除/改名/模型下架后,残留 GroupItem 在一个对账周期内被清理或正确展示。
- [ ] 分组列表中每条 GroupItem 的渠道名来自后端 DTO,不再出现 `Channel {id}` 兜底名;渠道已删除时显示统一的"渠道已删除"占位。
- [ ] 全程 Docker 构建运行验证(非本机 go run),验证步骤写入各子任务 implement.md。

## 约束

- 向后兼容:不破坏现有 API 契约。新增字段用 GORM AutoMigrate 增量加列,不删列、不改列语义。
- `internal/helper/channel.go` 的 `ChannelAutoGroup` 现有"只增不减"语义在子任务 A/B 中按各自 prd 调整,不跨任务改同一函数。
- 验证一律 Docker 构建(`docker compose build && docker compose up`),不在本机 `go run` 验证。

## Notes

- 见各子任务目录下的 `prd.md`。
- 父任务不进入 `task.py start`;实现轮到对应子任务时再 start 该子任务。
