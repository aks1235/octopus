# 实施计划:分组成员级测试 + 冷却重置

前置:本计划按 design.md 的分层顺序推进,每步有独立验证点;全部完成后走 octopus-verify 全流程。
回滚点:每步各自成 commit 候选(最终可合并为一个 commit),revert 即回滚,无 DB 迁移。

## Step 1 后端 relay 层(核心逻辑 + 单测)

- [x] `internal/relay/route.go`:`ClearGroupCooldowns(groupID int)`(持锁清 cooldowns/trips/probe,publish,保留 current/affinity)。同文件新增私有 `clearMemberCooldown(groupID, itemID)`(单成员版,测试成功后调用,刻意不改 current/affinity)。
- [x] `internal/relay/keytest.go`:
  - `keyTestLogFinalize` 参数化客户端标识(clientName/userAgent),渠道测试调用点传原常量;
  - 新增 `TestGroupMembers(ctx, group)` :授权解析(`op.ChannelGrantGet` → `op.ChannelGet`)→ 复用 `testChannelKeyModel`(协议位取 `grant.Protocols` 经新助手 `protocolsOfMask` 过滤,顺序沿用 `keyTestProtocols`,不新增常量)→ 成功后 `clearMemberCooldown` → 落日志(分组测试标识 "分组测试"/"octopus-group-test");
  - 成员间有界并发(信号量 4),结果按提交序。
- [x] `internal/model/group.go`:`GroupMemberTestResult` 类型。
- [x] 单测:ClearGroupCooldowns 用例落在 `route_cooldown_test.go`(冷却清空/probe 释放/current+affinity 保留并验证亲和期内选路不切换/无状态 no-op);TestGroupMembers 用例落在新文件 `grouptest_test.go`(不可用成员跳过带原因且不写日志不发请求、成功清冷却且不动 current/affinity、失败不动路由状态、空分组返回空切片)。

验证:`go test ./internal/relay/ ./internal/model/` — 通过(容器 golang:1.26.4)。
注意:本地 `go test` 在沙箱内可能遇到网络拉依赖失败——依赖已在 go.sum,正常可离线跑;若环境问题参照 build spec(容器内编译)。本次宿主机无 go,全程走容器,另注意 `go build ./...` 须加 `-buildvcs=false`(spec 已知坑)。

## Step 2 后端 handler 路由

- [x] `internal/server/handlers/group.go`:注册 `POST /test/:id`、`POST /cooldown-reset/:id`(路由组尾部追加);实现 `testGroupMembers` / `resetGroupCooldown`(404 兜底、响应形状见 design §4)。
- [x] 确认 `middleware.RequireJSON` 对空 body POST 的行为,必要时前端发 `{}`(记录结论到本文件)。

**RequireJSON 空 body 结论**:`middleware.RequireJSON` 只对非 GET/DELETE/OPTIONS 请求校验 `Content-Type` 头含 `application/json`,**不读请求体**;空 body POST 只要带 JSON Content-Type 即放行,且两个新 handler 均不做 `ShouldBindJSON`,后端无需任何适配。但前端 `apiRequest` 只在 `body !== undefined` 时才设置 Content-Type,故 mutation 必须显式发 `body: {}`(已在 `useTestGroupMembers`/`useResetGroupCooldown` 落实并注释)。

验证:`go build ./... && go test ./internal/server/...` — 通过(容器,-buildvcs=false)。

## Step 3 前端

- [x] `web/src/api/group.ts`:`GroupMemberTestResult` 接口 + `useTestGroupMembers` + `useResetGroupCooldown`。
- [x] `web/src/components/modules/group/Card.tsx`:卡片头两个 IconButton(测试 FlaskConical / 重置冷却 RotateCcw,后者按 cooldowns 未到期项条件渲染);测试 pending 态(Loader2 spinner + 禁用)、toast 汇总(全成功 success / 部分失败 warning / 全失败 error);结果存本地 state 传入成员列表。
- [x] `web/src/components/modules/group/ItemList.tsx`:`MemberItem`/`MemberList` 接收结果 prop(含 renderClone 分支),渲染结果徽标(成功 CircleCheck 绿 + tooltip 成功协议与"冷却已清除"、失败 CircleX 红 + tooltip 错误摘要、不可用按成员 available 口径置灰 Ban + tooltip 原因)。
- [x] `web/src/locales/{zh_hans,zh_hant,en}.json`:`group.card` 命名空间补文案(14 键:按钮/toast/徽标 tooltip)。

验证:`cd web && pnpm lint && pnpm build` — 通过(容器 node:22-alpine,按 build spec 挂仓库根)。

## Step 4 质量检查(最后一轮全量)

- [ ] `go test ./internal/...` 全绿。
- [ ] 前端 lint + build 通过。
- [ ] 自查清单:与 design 的偏差逐条说明原因;R1.1–R3.2 逐条对照。

## Step 5 octopus-verify 全流程(本地闭环)

- [ ] 走 `.claude/skills/octopus-verify`(容器内编译 → 后端测试 → 前端 lint+build → 起验证容器 → 健康检查 → 人看 UI)。
- [ ] 人工验证 AC1–AC6(重点:AC1 复现场景——冷却成员测试通过后徽标消失、流量可回)。
- [ ] 写 `.octopus-verified` marker。

## Step 6 收尾

- [ ] 更新 spec:`.trellis/spec/backend/relay-routing.md` 补 ClearGroupCooldowns/测试清冷却语义(如有新约定)。
- [ ] 提交 dev-v2(单 commit,message 走 feat(...));**不推送、不打 tag**——发布由用户另行决定后走 octopus-publish。

## 风险与回退

- 全程纯新增,任何一步卡住可直接停,不影响现有功能;
- RequireJSON 空 body 行为是实现期唯一未定项(Step 2 确认),影响面仅前端请求体。
