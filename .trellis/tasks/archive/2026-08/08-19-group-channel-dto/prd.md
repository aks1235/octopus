# ⑤ 分组渠道名 DTO 下发

## Goal

分组列表里的每个成员(GroupItem)当前只回 `channel_id`,前端靠 `channel_id + model_name` 双键在本地 `modelChannels` 里反查渠道名,
查不到就兜底成 `Channel {id}`。渠道改名、被禁用或被删除后,前端显示的是无意义名或过期名,无法区分「渠道还在但禁用了」与「渠道已删除」。

本子任务把渠道名与启停态改为**后端 DTO 下发**:后端在 `op.GroupList` 返回前用 `channelCache` 填充 `ChannelName/ChannelEnabled`(非持久化字段),
前端直接按 DTO 渲染,渠道已删除时显示占位文案、渠道被禁用时显示角标。

来源:dev `8185f12`(2026-07-31)「分组列表渠道名由后端 DTO 下发,禁用/已删除态明确区分」。后端逻辑参考 dev,前端按上游 Vite 组件现状重做(不复用 dev 的胶水)。

## Requirements

### 后端

- `model.GroupItem` 加两个**非持久化**字段:`ChannelName string`、`ChannelEnabled bool`,均 `gorm:"-"`,不进库、不 migrate。
- `op.GroupList` 返回前用 `channelCache` 按 `item.ChannelID` 填充这两个字段:
  - 渠道存在 → `ChannelName = ch.Name`、`ChannelEnabled = ch.Enabled`
  - 渠道已删除(不在缓存)→ `ChannelName = ""`、`ChannelEnabled = false`
- 填充必须**重建 Items slice 再赋值**,不能就地改缓存对象的底层数组(否则污染 `groupCache` 缓存对象)。

### 前端(Vite,按上游组件现状)

- `GroupItem` TS 接口加 `channel_name?: string`、`channel_enabled?: boolean`。
- `Card.tsx`:删掉本地反查逻辑(`buildChannelNameByModelKey` + `Channel {id}` 兜底),`channel_name/channel_enabled` 直接取 DTO(缺失时 `''` / `true`)。`enabled`(模型级启停,用于灰显)仍保留本地映射,与 `channel_enabled`(渠道级)区分。
- `ItemList.tsx` `MemberItem`:
  - `channel_name` 为空 → 渠道已删除,显示占位文案 `card.channelDeleted`(destructive 色)。
  - `channel_enabled === false` → 渠道已禁用,在渠道名后追加角标 `card.channelDisabled`(amber 色)。覆盖 ① 的自动禁用场景。
- `utils.ts`:`buildChannelNameByModelKey` **保留**——`log/Item.tsx`(日志详情页,数据源 `RelayLogOverview` 仅 channel_id,不走 group DTO)仍用它反查渠道名,非死代码;Card.tsx 改用 DTO 后不再调用它(dev ⑤ 能删是因为其 log/Item.tsx 未引用,上游引用了,属上游结构差异)。
- 三语 i18n:在 `group.card` 下加 `channelDeleted` / `channelDisabled` 两个 key(en / zh_hans / zh_hant)。

### 与 dev `8185f12` 的有意差异(KISS/YAGNI)

- **不搬 `GroupListRaw`**:dev ⑤ 抽 `GroupListRaw` 是为了让 `helper.ChannelAutoGroup` 热路径绕开渠道名填充。上游已整体移除 auto-group 功能(全仓无 `AutoGroup`/`ChannelAutoGroup`),`op.GroupList` 唯一调用方是 `getGroupList` HTTP handler,不存在热路径开销,拆分多余。
- **不碰 `internal/helper/channel.go`**:同上理由,上游该文件只剩 `ChannelHttpClient`,与 ⑤ 无关。

### 不在范围

- 不改 `GroupItemList` / `GroupItemListAll`(② 已加,单 group / 全量查询,列表页不经过它们)。
- 不改 `GroupGet` / `GroupGetByName`(详情/路由用,缓存直出,前端编辑器走 `displayMembers` 已含 DTO)。
- 不碰 `internal/relay/log.go`(那是 ④⑥ 的共享文件)。
- 不做容器起服务 + UI smoke(留父任务 Step 8 集成验收)。

## Acceptance Criteria

- [ ] `model.GroupItem` 含 `ChannelName/ChannelEnabled` 非持久化字段,`go build ./...` 通过
- [ ] `op.GroupList` 对存在渠道填真名+真启停态,对已删渠道填空名+false,且不污染 `groupCache` 缓存
- [ ] 前端 `GroupItem` 接口含两字段;`Card.tsx` 不再 import/调用 `buildChannelNameByModelKey`,无 `Channel {id}` 兜底
- [ ] `ItemList.tsx` 渠道已删除显示 `channelDeleted` 文案,渠道已禁用追加 `channelDisabled` 角标
- [ ] `utils.ts` 保留 `buildChannelNameByModelKey`(log/Item.tsx 仍用);Card.tsx 不再 import/调用它
- [ ] `web/src/locales/{en,zh_hans,zh_hant}.json` 的 `group.card` 含 `channelDeleted/channelDisabled` 三语
- [ ] `go build ./...` + `go test -tags=jsoniter ./...` 通过(容器 `golang:1.25 -e GOTOOLCHAIN=auto`)
- [ ] `cd web && pnpm build` 通过(容器 `node:22-alpine`);自改文件 `pnpm lint` 无新增 error(49 baseline 不修,ItemList.tsx line 157 三元、log/Item.tsx line 60 `Date.now` 均为既有 baseline)

## Notes

- `channel_name,omitempty`:渠道已删时 ChannelName 为空串被 omitempty 省略,前端 `?? ''` 兜底成空串,`!channel_name` 为 true → 触发已删除文案。渠道名 gorm 标签 `unique;not null`,真实渠道不可能空名,故空名即已删除信号,可靠。
- `channel_enabled` 不加 omitempty:始终下发(false 表已删/已禁用),前端 `?? true` 做防御。
- `enabled`(模型级,LLMChannel.enabled)与 `channel_enabled`(渠道级)是两个维度:前者控制成员整体灰显(`isDisabled`),后者只控制渠道名后角标。两者独立,别混用。
- ② 已在 `op/group.go` 加好 `GroupItemListAll`,⑤ 不重复加;dev ⑤ diff 里那段 `GroupItemListAll` 是 dev 自己加的,上游由 ② 承担,别照搬导致重复定义。
