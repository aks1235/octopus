# design.md — group-channel-name-display(子任务 C)

> 父任务:`07-30-channel-group-model-sync`,依赖 A 的 `AutoDisabled`/`Enabled` 约定(A 已落地)

## 目标

分组列表渠道名由后端 DTO 权威下发,前端按 DTO 渲染,渠道已删/已禁用两种态明确区分,根除 `Channel {id}` 兜底名。

## 后端

`internal/model/group.go` `GroupItem` 加两列非持久化字段:
```go
ChannelName    string `json:"channel_name,omitempty" gorm:"-"`
ChannelEnabled bool   `json:"channel_enabled" gorm:"-"`
```
`gorm:"-"` 不入库、不被 AutoMigrate。

`internal/op/group.go` `GroupList`(当前 17-23 直接返缓存):改为返回前对每个 group 填充 items 的渠道名/启停态。
```
for each group in cache:
    副本 = shallow copy group;副本.Items = make new slice
    for each item:
        newItem = item
        if ch, ok := channelCache.Get(item.ChannelID); ok:
            newItem.ChannelName = ch.Name
            newItem.ChannelEnabled = ch.Enabled
        else:
            newItem.ChannelName = ""      // 渠道已删
            newItem.ChannelEnabled = false
        append
    append 副本
```
**关键:必须对 slice 重建拷贝**,不能就地改缓存对象的 item(GORM 的 `gorm:"-"` 字段若直接写到缓存对象上,下次仍带脏值)。`channelCache` 同包可访问。

`GroupGetEnabledMap`(路由用,41-61)不动——它只过滤启停,不需要渠道名,且不带 NON-persist 字段不影响。

## 前端

`web/src/api/endpoints/group.ts` `GroupItem`:
```ts
channel_name?: string;
channel_enabled?: boolean;
```

`web/src/components/modules/group/Card.tsx`:
- `displayMembers`(94-107):`channel_name` 用 `item.channel_name`(空则置占位文案,见下);删除 `` `Channel ${item.channel_id}` `` 兜底。`enabled`(模型级,决定拖拽灰显)仍用 `enabledByKey`(保留 modelChannels 维度的 enabled)。新增 `channel_enabled`(渠道级)取 `item.channel_enabled` 默认 true——但渠道已删时 DTO 给的是 false,前端需区分"渠道已删"(channel_name 空)与"渠道在但禁用"。

`web/src/components/modules/group/ItemList.tsx:120`:渠道名渲染逻辑改为:
- `channel_name` 非空 → 显示 `channel_name`,`channel_enabled===false` 时追加 "(已禁用)" 角标。
- `channel_name` 空 → 显示占位"(渠道已删除)"灰色文案。
- `isDisabled` 仍按 `member.enabled===false`(模型级)判定灰显,不变。

文案走 `next-intl` `useTranslations('group')`——若 i18n keys 未定义需补(zh/en json)。先确认现有 i18n 文件结构。

## 兼容性

- 后端 DTO 新增两可选字段,前端旧版本忽略不影响。
- `GroupList` 填充逻辑不落库、不改 `GroupGetEnabledMap` 路由逻辑。
- `channelCache.Get` O(1),遍历全部 items 总成本 = GroupItem 总数,可接受。

## 验证(Docker,统一在最后)

容器内:删一个渠道 → 分组列表该 GroupItem 显示"(渠道已删除)";置渠道 Enabled=false → 显示渠道名 + "(已禁用)";渠道正常 → 仅渠道名。渠道改名 → 列表渠道名随之变。
