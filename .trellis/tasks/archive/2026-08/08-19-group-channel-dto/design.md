# Design — ⑤ 分组渠道名 DTO 下发

> 技术设计。需求/验收见 `prd.md`,执行清单见 `implement.md`。
> 参考 dev `8185f12` 的后端逻辑,前端按上游 Vite 组件现状适配,有意省略 dev 的 `GroupListRaw` 拆分(见 §3)。

## 1. 现状(已核验)

### 后端
- `internal/model/group.go` `GroupItem`:`ID/GroupID/ChannelID/ModelName/Priority`。**无** Weight、无 ChannelName/ChannelEnabled。
- `internal/op/group.go`:
  - `GroupList`(line 23-29):遍历 `groupCache.GetAll()` 直接 append,**无渠道名填充**。唯一调用方是 `internal/server/handlers/group.go:42 getGroupList` HTTP handler。
  - `GroupItemListAll`(line 390-399):② 已加,全量查询带 `Order("priority ASC")`。⑤ 不动。
  - `channelCache` 定义在 `internal/op/channel.go:15`(`cache.New[int, model.Channel]`),同 `op` 包,`group.go` 可直接访问。
- `internal/model/channel.go` `Channel`:`Name string`、`Enabled bool` 在(① 的 sync 字段也在,与本任务无关)。
- `internal/helper/channel.go`:上游只剩 `ChannelHttpClient`,**无 `ChannelAutoGroup`**(全仓 `rg AutoGroup` 无匹配)。

### 前端(上游 Vite + React 19)
- `web/src/api/group.ts` `GroupItem`(line 6-12):`id/group_id/channel_id/model_name/priority`,无 channel_name/enabled。
- `web/src/components/modules/group/Card.tsx`:
  - line 13 import `buildChannelNameByModelKey, modelChannelKey` from `./utils`
  - line 75 `channelNameByKey = useMemo(() => buildChannelNameByModelKey(modelChannels), ...)`
  - line 92 `channel_name: channelNameByKey.get(...) ?? \`Channel ${item.channel_id}\`` ← ⑤ 删兜底
  - line 90 `enabled: enabledByKey.get(...) ?? true` ← 模型级,保留
  - `displayMembers` deps `[group.items, channelNameByKey, enabledByKey]`
- `web/src/components/modules/group/ItemList.tsx`:
  - `SelectedMember extends LLMChannel { id; item_id? }`(line 17-20)
  - `MemberItem` line 56 `const t = useTranslations('group')` 已在
  - line 59 `isDisabled = member.enabled === false`(模型级灰显)
  - line 57 `const { Icon, className: iconClassName } = getModelIcon(member.name)` ← 上游签名,别照搬 dev 的 `{ Avatar }`
  - line 126 `<span ...>{member.channel_name}</span>` ← 裸渠道名,无状态区分
- `web/src/components/modules/group/utils.ts`:`buildChannelNameByModelKey`(line 20-26)死代码,⑤ 删。`LLMChannel` import 仍被 `memberKey` 用,保留。
- `web/src/locales/{en,zh_hans,zh_hant}.json`:`group.card`(en line 317-321)含 `empty/active/activate`,无 channelDeleted/channelDisabled。`web/out/locale/` 是构建产物,不碰。

## 2. 改动设计

### 2.1 `internal/model/group.go` — GroupItem 加非持久化字段

在 `Priority` 字段后追加:

```go
type GroupItem struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	GroupID   int    `json:"group_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ChannelID int    `json:"channel_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ModelName string `json:"model_name" gorm:"not null;index:idx_group_channel_model,unique"`
	Priority  int    `json:"priority"`

	// 以下字段非持久化:仅由 op.GroupList 填充后返回前端,用于分组列表正确展示渠道名与启停态。
	// 渠道已删除时 ChannelName 留空、ChannelEnabled=false,前端据此显示占位文案。
	ChannelName    string `json:"channel_name,omitempty" gorm:"-"`
	ChannelEnabled bool   `json:"channel_enabled" gorm:"-"`
}
```

- `gorm:"-"`:不进库,AutoMigrate 不生成列(与 ① 的持久化 sync 字段不同,后者要进库)。
- `channel_name,omitempty`:渠道已删时空串被省略,前端 `?? ''` → 空串 → `!channel_name` 触发已删除文案。渠道名 gorm `unique;not null`,真实渠道不空名,故空名=已删信号,可靠。
- `channel_enabled` 无 omitempty:始终下发;前端 `?? true` 防御旧数据。

### 2.2 `internal/op/group.go` — GroupList 填充 + 重建 slice

替换 `GroupList`(line 23-29):

```go
func GroupList(ctx context.Context) ([]model.Group, error) {
	res := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		// 重建 Items slice 并填充渠道名/启停态,避免就地修改污染缓存对象底层数组。
		// 渠道已删除(不在 channelCache)时 ChannelName 留空、ChannelEnabled=false,前端据此显示占位。
		items := make([]model.GroupItem, len(group.Items))
		for i, item := range group.Items {
			item := item
			if ch, ok := channelCache.Get(item.ChannelID); ok {
				item.ChannelName = ch.Name
				item.ChannelEnabled = ch.Enabled
			} else {
				item.ChannelName = ""
				item.ChannelEnabled = false
			}
			items[i] = item
		}
		group.Items = items
		res = append(res, group)
	}
	return res, nil
}
```

**缓存安全论证**(关键,dev 已踩坑):
- `groupCache.GetAll()` 返回 `[]model.Group`(值切片)。`for _, group := range` 中 `group` 是每个 Group 的**副本**(值类型)。
- 但 `group.Items` 是 slice header 副本,指向**同一底层数组**。若就地 `group.Items[i].ChannelName = ...` 会写穿到缓存底层数组,污染缓存对象。
- 解法:新建 `items` slice,填好后 `group.Items = items` 把副本的 Items 字段指向新数组,缓存对象的 Items 仍指原数组,不受影响。`item := item` 再加一层 loop-var 副本(上游 go 1.26 已 per-iteration,冗余但无害,与 dev 一致、防御性)。
- `group.Items = items` 不会改缓存:缓存存的是 `model.Group` 值(`groupCache.Set(id, *group)` 指针解引用存值),`GetAll()` 返回的是其内部 slice 的副本;副本上改字段赋值不影响缓存存的 Group 值。

**不抽 `GroupListRaw`**(见 §3)。

### 2.3 前端 `web/src/api/group.ts`

`GroupItem` 接口(line 6-12)末尾追加:

```ts
    priority: number;
    // 以下字段非持久化,由后端 GroupList 填充,用于分组列表正确展示渠道名/启停态。
    // channel_name 缺失表示渠道已删除,channel_enabled 表示渠道级启停(与模型级 enabled 区分)。
    channel_name?: string;
    channel_enabled?: boolean;
}
```

### 2.4 前端 `web/src/components/modules/group/Card.tsx`

- line 13 import:`import { buildChannelNameByModelKey, modelChannelKey } from './utils';` → `import { modelChannelKey } from './utils';`(`modelChannelKey` 仍用于 id 与 enabledByKey 键)。
- 删 line 75 `channelNameByKey` useMemo 整行。
- line 92 `channel_name: channelNameByKey.get(modelChannelKey(item.channel_id, item.model_name)) ?? \`Channel ${item.channel_id}\`,` →
  ```ts
                // channel_name 来自后端 DTO;为空表示渠道已删除,由 ItemList 渲染占位文案。
                // channel_enabled 为渠道级启停,用于显示已禁用角标。
                channel_name: item.channel_name ?? '',
                channel_enabled: item.channel_enabled ?? true,
```
- displayMembers deps line 95 `[group.items, channelNameByKey, enabledByKey]` → `[group.items, enabledByKey]`。
- `modelChannels`/`enabledByKey` 保留(模型级 enabled 仍需本地映射)。

### 2.5 前端 `web/src/components/modules/group/ItemList.tsx`

- `SelectedMember`(line 17-20)加字段:
  ```ts
export interface SelectedMember extends LLMChannel {
    id: string;
    item_id?: number;
    // 渠道级启停(与 LLMChannel.enabled 模型级启停区分):false 表示整个渠道被禁用。
    channel_enabled?: boolean;
}
  ```
- `MemberItem` 在 `isDisabled`(line 59)后加渠道级状态派生:
  ```ts
    const isDisabled = member.enabled === false;
    // 渠道级状态:channel_name 缺失表示渠道已删除;channel_enabled=false 表示渠道被禁用(含 ① 的自动禁用)。
    const channelDeleted = !member.channel_name;
    const channelDisabled = member.channel_enabled === false;
  ```
- line 126 渲染替换:
  ```tsx
                    <span className="text-[10px] text-muted-foreground truncate leading-tight flex items-center gap-1">
                        {channelDeleted ? (
                            <span className="text-destructive/70">{t('card.channelDeleted')}</span>
                        ) : (
                            <>
                                <span className="truncate">{member.channel_name}</span>
                                {channelDisabled && (
                                    <span className="shrink-0 text-amber-500/80">{t('card.channelDisabled')}</span>
                                )}
                            </>
                        )}
                    </span>
  ```
  `t` 已在 line 56 定义,无需新增。`getModelIcon` 保持上游 `{ Icon, className }` 签名。

### 2.6 前端 `web/src/components/modules/group/utils.ts`

**保留** `buildChannelNameByModelKey`(加注释说明保留原因)。与 dev ⑤ 的差异:dev 删了它,但上游 `web/src/components/modules/log/Item.tsx:20,185` 仍 import 并用它(日志详情页数据源 `RelayLogOverview` 只有 channel_id,不经 group DTO,需本地反查渠道名)。实现时漏查全仓调用方导致首次 `pnpm build` tsc 报 `Item.tsx(20,10) TS2305`,已修正:保留函数,Card.tsx 仍改用 DTO 不受影响。`log/Item.tsx` 属 ⑥ 领域,⑤ 不改。保留 `LLMChannel` import(memberKey 与本函数共用)。

### 2.7 i18n `web/src/locales/{en,zh_hans,zh_hant}.json`

在 `group.card` 块(en line 317-321,三语同构)的 `"activate": "..."` 行后加逗号,追加两 key:

| key | en | zh_hans | zh_hant |
|---|---|---|---|
| channelDeleted | Channel deleted | 渠道已删除 | 渠道已刪除 |
| channelDisabled | Disabled | 已禁用 | 已停用 |

文案与 dev `8185f12` 一致。`web/out/locale/*` 是构建产物,pnpm build 会重新生成,不手改。

## 3. 与 dev `8185f12` 的有意差异

dev ⑤ 抽了 `GroupListRaw` 并把 `helper.ChannelAutoGroup` 的 `GroupList` 调用改 `GroupListRaw`,理由:ChannelAutoGroup 是每渠道同步触发的热路径,不能承受全量 items 遍历重建 slice 的开销。

上游已**整体移除 auto-group 功能**:
- `model.Channel` 无 `AutoGroup` 字段
- `internal/helper/channel.go` 无 `ChannelAutoGroup` 函数
- 全仓 `rg AutoGroup` 无匹配

`op.GroupList` 上游唯一调用方是 HTTP handler `getGroupList`(每次列表请求一次,非热路径)。故 `GroupListRaw` 拆分在上游是**纯多余抽象**(违反 YAGNI),不搬。相应 `helper/channel.go` 也不改。

这符合父任务 prd「移植时只搬核心逻辑,不引入 chicring/dev 的胶水代码」。

## 4. 缓存一致性边界

- `GroupList` 只读 `groupCache` + `channelCache`,不写。填充发生在返回前的副本上,缓存零副作用。
- `groupRefreshCache*` 全量/单 ID 刷新时 `Preload("Items")` 直出 DB,不含 ChannelName/ChannelEnabled(`gorm:"-"` 不会被 Preload 填),缓存对象干净。下一次 `GroupList` 再临时填充。✓
- 编辑器(Editor/Create)新增成员走 `modelChannels` picker,`channel_name` 是真名、`channel_enabled` undefined → `channelDeleted=false`、`channelDisabled=false`,不误显角标。已有成员经 `displayMembers` 带 DTO,角标正确。✓

## 5. 验证设计

- 后端:`go build ./...` + `go test -tags=jsoniter ./...`(上游测试用 jsoniter tag;容器 `golang:1.25 -e GOTOOLCHAIN=auto` 自动拉 1.26.4)。
- 前端:`cd web && pnpm build`(容器 `node:22-alpine`,corepack 启用 pnpm)。`pnpm lint` 全量 49 errors(5 warnings)为 baseline 不修;自改文件无新增——ItemList.tsx line 157 `showConfirmDelete ? ... : ...` 三元作语句、log/Item.tsx line 60 `Date.now()` 均为既有 baseline(⑤ 未碰 onClick 与日志页),改前改后总数持平。
- 容器起服务 + UI 手动看分组卡片禁用/已删除态:留父任务 Step 8。
- 手动单测补充:无(上游 group op 无现成单测骨架,手写 table 测试 ROI 低,靠 build + 集成 smoke 兜底)。

## 6. 回滚

单 commit,`git revert` 即回。无 DB 迁移(非持久化字段),无不可逆变更。
