# 分组渠道名展示修正(子任务 C)

> 父任务:`07-30-channel-group-model-sync`

## 背景

后端 `model.GroupItem`(`internal/model/group.go:23-29`)只回 `channel_id`,handler `getGroupList`(`internal/server/handlers/group.go:42-49`)直接返 `op.GroupList` 缓存,不关联 channel 表。前端 `web/src/components/modules/group/Card.tsx:102` 用 `useModelChannelList()` 按 `channel_id+model_name` 双键映射渠道名,查空即回退 `` `Channel ${item.channel_id}` ``,导致渠道改名/失效后分组里显示成 `Channel 1 / Channel 2`。

## Goal

渠道名由后端 DTO 权威下发,前端不再本地映射兜底;渠道已删除/已禁用两种态在分组列表中明确区分展示,不再出现 `Channel {id}` 无意义兜底名。

## Requirements

### 后端

- `op.GroupList` 返回前(或 handler 层)为每个 GroupItem 附带 `ChannelName`/`ChannelEnabled` 两个非持久化字段(加到 `model.GroupItem` 上,`gorm:"-"` 或用单独 DTO):
  - 渠道存在 → `ChannelName=channel.Name`、`ChannelEnabled=channel.Enabled`。
  - 渠道已删 → `ChannelName=""`、`ChannelEnabled=false`(或用专门标志)。
- 渠道信息来源:复用内存 channel 缓存/`op.ChannelList`,避免每次 GroupList 都全表扫;若缓存结构不便,在 handler 层取一次 channel map 后填充。
- 不破坏 `model.GroupItem` 持久化结构(新字段 `gorm:"-"`)。

### 前端

- `web/src/api/endpoints/group.ts:8-15` 的 GroupItem 类型补 `channel_name?: string`、`channel_enabled?: boolean`。
- `Card.tsx:102` 移除 `` `Channel ${item.channel_id}` `` 兜底与 `buildChannelNameByModelKey` 本地映射依赖;改用 DTO 的 `channel_name`:
  - `channel_name` 非空 → 显示渠道名(并附加禁用角标:若 `channel_enabled===false` 显示"已禁用")。
  - `channel_name` 为空 → 显示统一占位"渠道已删除"(灰色/置灰样式),不再用 id 兜底。
- 如仍需渠道名做其它用途,保留 `useModelChannelList` 但不在分组列表用它做渠道名渲染。

### 与子任务 A 的约定

- A 落下 `Enabled=false`(因连续同步失败禁用)。本任务的 `channel_enabled===false` 展示态"已禁用"需覆盖该情况——即 A 禁用的渠道在分组列表里也得标"已禁用"。A 的字段(`Enabled` 已存在,无需新增)约定满足本任务需求,无需额外协调。

## Acceptance Criteria

- [ ] 删除某渠道后,分组列表中对应 GroupItem 显示"渠道已删除"占位,不再出现 `Channel 1/2` 兜底名。
- [ ] 渠道改名后,分组列表渠道名随之更新(因走后端 DTO,不再受前端本地映射缓存影响)。
- [ ] 渠道被置 `Enabled=false`(无论是子任务 A 自动禁用还是手动禁用)→ 分组列表显示渠道名 + "已禁用"角标。
- [ ] 渠道正常 → 显示渠道名,无角标。
- [ ] Docker 构建前后端均通过,容器内验证三种态(正常/已禁用/已删除)展示正确。

## Notes

- 实施顺序:建议在 A 之后(或与 A 并行但读取 A 的 `Enabled` 字段约定),确保"已禁用"态对 A 落下的渠道生效。
- 若 dto 改造影响 `GroupDel`/`GroupCreate` 等路径,需回归测试分组 CRUD 不受影响。
- 验证一律 Docker 构建(含前端 `pnpm build` 静态导出嵌入)。
