# 实施计划:正则分组渠道人工排序

> 前置阅读:prd.md(需求与 AC)、design.md(方案与现状盘点)。
> 工作区已有部分实现(见 design.md「现状盘点 · 已有实现」),本计划从缺口起算。

## 执行清单(按序)

### 步骤 1:后端 op 层收口

- [ ] `internal/op/group.go`:新增 `GroupChannelOrderApply(groupID int, channelIDs []int, ctx) (*model.Group, error)`——事务内:校验分组存在且 `member_regex <> ''`(否则报错给 handler 转 400/404)→ 清删旧顺序行 → 批量插入(去重校验放 handler)→ `syncRegexGroupItems(tx, groupID, group.MemberRegex)` 重算 → 事务外 `groupRefreshCache` → 返回重读后的分组。
- [ ] `GroupChannelOrderSave` 改为包内私有或并入 Apply;`GroupChannelOrderReset` 保留(DELETE 端点用,重算+刷缓存同样要走)。
- [ ] 校验既有 diff 的合成排序代码与注释口径(收编,补足中文注释与 doc comment,匹配仓库风格)。

### 步骤 2:API 层

- [ ] `internal/model/group.go`:`GroupChannelOrderRequest{ ChannelIDs []int }`(binding required)。
- [ ] `internal/server/handlers/group.go`:
  - 路由 `POST /channel-order/:id` → handler:绑 JSON → channel_ids 去重校验(重复 400)→ `op.GroupChannelOrderApply` → `resp.Success(groupResponse)` + `publishGroupEvent`。
  - 路由 `DELETE /channel-order/:id` → handler:`op.GroupChannelOrderReset` + 重算该组 + 刷缓存 + 发事件。
- [ ] 错误映射:分组不存在 404;手动分组调此端点 400。

### 步骤 3:备份覆盖

- [ ] `internal/model/backup.go`:DBDump 加 `GroupChannelOrders` 字段(omitempty,列在 GroupItems 后)。
- [ ] `internal/op/backup.go`:导出 Find 一行 + 导入 createDoNothing 一行(对齐 GroupItems 模式)。

### 步骤 4:迁移清理

- [ ] 删除空的 `internal/db/migrate/013.go`(建表走 AutoMigrate,设计已定);`go build ./...` 确认无引用残留。

### 步骤 5:前端

- [ ] `web/src/api/group.ts`:`useGroupChannelOrder`(POST)与 `useGroupChannelOrderReset`(DELETE)mutation,onSuccess `writeGroupCache`(响应即更新后的分组)。
- [ ] `web/src/components/modules/group/Card.tsx`:`submitMembers` 按 `group.member_regex` 分流——非空时折算去重 channel_ids 提交顺序端点;空时维持 items 提交。折算 = 按成员列表首次出现序取 `channel_id` 去重。
- [ ] `web/src/components/modules/group/Editor.tsx`:RegexMemberSection 只读列表换 `MemberList`(可拖拽,**不传 onRemove**);编辑态暂存渠道顺序,`handleSubmit` 在顺序有变时先调顺序端点成功再走原更新;无变化不调。
- [ ] i18n:`web/src/locales/{zh_hans,zh_hant,en}.json` 补 key(正则分组拖拽提示,置于 group.form 命名空间,参考 `regexMembersHint` 位置 zh_hans.json:389)。

### 步骤 6:测试

- [ ] `internal/op/regex_group_channel_order_test.go`:按 design.md §6 七条用例。
- [ ] 既有测试回归:`go test ./internal/op/... ./internal/...`(manual_group_absorb_test 与 group 相关用例必须全绿)。

### 步骤 7:验证关卡(octopus-verify 全流程)

- [ ] 容器内编译 + 后端全量测试 + 前端 lint + build。
- [ ] 起本地容器,UI 人审:
  - 正则分组卡片拖拽天翼云到最前 → 顺序保持;手动触发渠道更新(保存任一渠道)→ 顺序不回退;等 >5 分钟兜底 → 顺序仍保持(AC1)。
  - 新渠道命中吸纳 → 落尾(AC2);渠道内成员块整体移动(AC3)。
  - 删渠道再重加 → 顺序不复活(AC4)。
  - 手动分组拖拽行为不变(AC6);正则组编辑器成员仍不可增删(AC7)。
- [ ] 写 `.octopus-verified` 标记。

## 验证命令

```bash
# 后端编译与测试(容器内,octopus-verify 流程)
go build ./... && go test ./internal/...
# 前端
cd web && npm run lint && npm run build
```

## 回滚点

- 全部为增量变更:revert 单 commit 即回滚;残留 `group_channel_orders` 表无代码引用,惰性无害。

## 审查门

- 步骤 1-2 完成后:自查 handler 错误映射与事件发布是否与 updateGroup 同构。
- 步骤 5 完成后:自查正则组编辑器「不可增删」语义未被破坏(MemberList 无 onRemove 路径)。
- 提交前:octopus-verify 全过 + 本清单全勾。
