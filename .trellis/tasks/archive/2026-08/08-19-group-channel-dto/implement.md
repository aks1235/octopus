# Implement — ⑤ 分组渠道名 DTO 下发

> 执行清单。需求见 `prd.md`,技术设计见 `design.md`。
> 本会话无 Codex CLI,代码直写 Write/Edit/Bash,记 `CODEX_FALLBACK`(理由:工具不可用)。
> 验证只到 build/test + pnpm build 关卡,容器+UI smoke 留父任务 Step 8。

## 前置确认

- [ ] 当前分支 `feat/my-features`,基底 8d04257(父任务锁定),④ 已在 `internal/relay/log.go` 加 user_agent/client_name(⑤ 不碰该文件)
- [ ] ② 已在 `internal/op/group.go` 加 `GroupItemListAll`(line 390),⑤ 不重复加
- [ ] task.py current 为 ⑤(执行前 `task.py start` 后再做改动)

## Step 1 — 后端:GroupItem 加非持久化字段

- [ ] 1.1 编辑 `internal/model/group.go` `GroupItem` 结构体,在 `Priority` 后加 `ChannelName string`(json `channel_name,omitempty`,gorm `-`)与 `ChannelEnabled bool`(json `channel_enabled`,gorm `-`),附中文注释
- [ ] 1.2 `go build ./...` 冒烟(容器 golang:1.25 -e GOTOOLCHAIN=auto)

## Step 2 — 后端:GroupList 填充 + 重建 slice

- [ ] 2.1 编辑 `internal/op/group.go` `GroupList`:遍历 `groupCache.GetAll()`,对每个 group 重建 `items` slice,用 `channelCache.Get(item.ChannelID)` 填 `ChannelName/ChannelEnabled`(缺失则空名+false),`group.Items = items`,append 到 `res`。**不抽 `GroupListRaw`**(见 design §3)
- [ ] 2.2 `go build ./...` + `go test -tags=jsoniter ./...`(容器)
- [ ] 2.3 gate:无缓存污染逻辑——填充在副本、`group.Items = items` 指向新数组,缓存对象不受影响(design §4 已论证)

## Step 3 — 前端:TS 接口

- [ ] 3.1 编辑 `web/src/api/group.ts` `GroupItem` 接口加 `channel_name?: string` + `channel_enabled?: boolean` + 注释

## Step 4 — 前端:Card.tsx 去 local 反查

- [ ] 4.1 `import { buildChannelNameByModelKey, modelChannelKey }` → `import { modelChannelKey }`
- [ ] 4.2 删 `channelNameByKey` useMemo 整行
- [ ] 4.3 `displayMembers` 里 `channel_name` 改 `item.channel_name ?? ''`,加 `channel_enabled: item.channel_enabled ?? true`
- [ ] 4.4 deps `[group.items, channelNameByKey, enabledByKey]` → `[group.items, enabledByKey]`

## Step 5 — 前端:ItemList.tsx 状态区分

- [ ] 5.1 `SelectedMember` 加 `channel_enabled?: boolean` + 注释
- [ ] 5.2 `MemberItem` 在 `isDisabled` 后加 `channelDeleted = !member.channel_name`、`channelDisabled = member.channel_enabled === false`
- [ ] 5.3 line 126 渠道名 span 改为:channelDeleted → `card.channelDeleted`(destructive/70);否则渠道名 + channelDisabled 时追加 `card.channelDisabled` 角标(amber/80)。容器 `flex items-center gap-1`。`t` 用现有 line 56 定义

## Step 6 — 前端:utils.ts 保留 buildChannelNameByModelKey

- [ ] 6.1 **不删** `buildChannelNameByModelKey`(log/Item.tsx:20,185 仍用,非死代码),改加注释说明保留原因。保留 `LLMChannel` import(memberKey 与本函数共用)

## Step 7 — i18n 三语

- [ ] 7.1 `web/src/locales/en.json` `group.card`:`activate` 后加逗号 + `channelDeleted: "Channel deleted"` + `channelDisabled: "Disabled"`
- [ ] 7.2 `web/src/locales/zh_hans.json`:同结构,`渠道已删除` / `已禁用`
- [ ] 7.3 `web/src/locales/zh_hant.json`:同结构,`渠道已刪除` / `已停用`

## Step 8 — 前端 gate

- [ ] 8.1 `cd web && pnpm build`(容器 node:22-alpine)
- [ ] 8.2 `pnpm lint` 全量 49 errors 为 baseline 不修;自改文件无新增(ItemList.tsx:157 三元、log/Item.tsx:60 `Date.now` 均为既有 baseline,总数持平)

## Step 9 — 提交 + 归档

- [ ] 9.1 `git add` 自改文件:`internal/model/group.go` `internal/op/group.go` `web/src/api/group.ts` `web/src/components/modules/group/{Card,ItemList,utils}.tsx` `web/src/locales/{en,zh_hans,zh_hant}.json`
- [ ] 9.2 commit,message 沿用 dev 8185f12 写法:`feat: 分组列表渠道名由后端 DTO 下发,禁用/已删除态明确区分` + 正文说明(含与 dev 差异:不搬 GroupListRaw,上游无 auto-group)
- [ ] 9.3 `python3 ./.trellis/scripts/task.py archive 08-19-group-channel-dto`(或父任务归档子任务的既定方式)

## 验证命令汇总

```bash
# 后端(容器)
docker run --rm -v "$PWD":/work -w /work -e GOTOOLCHAIN=auto golang:1.25 go build ./...
docker run --rm -v "$PWD":/work -w /work -e GOTOOLCHAIN=auto golang:1.25 go test -tags=jsoniter ./...

# 前端(容器)
docker run --rm -v "$PWD/web":/work -w /work node:22-alpine sh -c "pnpm build"
# lint 自改文件(49 baseline 不修)
docker run --rm -v "$PWD/web":/work -w /work node:22-alpine sh -c "pnpm lint" 2>&1 | rg "group\.ts|Card\.tsx|ItemList\.tsx|utils\.ts"
```

## CODEX_FALLBACK

本会话无 Codex CLI 连接(工具未注册),全部代码改动走 Write/Edit 直写,符合 fallback 条件 1(CLI 不可用)。无 DB 迁移、无不可逆操作,直写风险可控。
