# implement.md — group-channel-name-display(子任务 C)

> 设计依据:`design.md`

## 执行清单

1. **`internal/model/group.go`** — `GroupItem` 加 `ChannelName string`/`ChannelEnabled bool`,均 `gorm:"-"`,带合适 json tag。
2. **`internal/op/group.go` `GroupList`** — 改为返回前填充:遍历缓存 group,为每个 group 重建 Items slice,每条 item 用 `channelCache.Get(item.ChannelID)` 填 `ChannelName`/`ChannelEnabled`,渠道不存在则空+false。**重建 slice,不污染缓存**。
3. **`web/src/api/endpoints/group.ts`** — `GroupItem` 接口加 `channel_name?: string`、`channel_enabled?: boolean`。
4. **`web/src/components/modules/group/Card.tsx`** — `displayMembers`:`channel_name` 取 `item.channel_name ?? ''`(传给 ItemList 由其决定占位);删 `Channel ${id}` 兜底;保留 `enabledByKey` 维度的 `enabled`。
5. **`web/src/components/modules/group/ItemList.tsx:120`** — 渲染逻辑:channel_name 非空显名+(channel_enabled===false 显"已禁用"角标);channel_name 空显"(渠道已删除)"灰文。
6. **i18n** — 若用新文案 key,在 `web/messages/zh.json`/`en.json`(或对应结构)的 `group` 节点补 `channelDeleted`/`channelDisabled`;先确认现有文件。

## 验证命令(后端 Docker 单独编译)

```bash
# 后端编译验证(本子任务只动 internal/op/group.go + internal/model/group.go)
mkdir -p static/out && echo '<!--ph-->' > static/out/.placeholder
docker run --rm -v "$PWD":/src -w /src golang:1.25 sh -c "go build -tags=jsoniter ./internal/... && echo OK"
rm -rf static/out
```

## 前端类型/lint 检查

```bash
cd web && pnpm install && pnpm run lint && pnpm run build
```
(统一在最后整体验证时跑,前端构建产物供 static embed)

## Review Gate

- 后端 `GroupList` 填充不污染 `groupCache`(slice 重建);路由用 `GroupGetEnabledMap` 不受影响。
- 前端删渠道 → "(渠道已删除)";渠道禁用 → 渠道名 + "(已禁用)";正常 → 渠道名;改名 → 跟随更新。
- `enabled`(模型级)与 `channel_enabled`(渠道级)语义不混淆。

## 验证(Docker 统一在 A/C/B 全完成后)

完整构建:`bash scripts/build.sh build linux amd64` 或直接 `docker compose build` 起容器,造删渠道/禁用/改名三种态验证。
