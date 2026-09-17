# 实施计划:故障转移请求内游标 + 轮询模式回归

> 前置阅读:prd.md(语义定稿)、design.md(游标引擎方案)。

## 执行清单(按序)

### 步骤 1:模型层

- [ ] `internal/model/group.go`:`GroupModeRoundRobin GroupMode = "roundrobin"`;三处 `oneof=manual failover` → `manual failover roundrobin`(Group/CreateRequest/UpdateRequest)。
- [ ] (等待上限旋钮已裁撤——2026-09-16 用户纠正,全员冷却等待维持现状,模型层无新字段)

### 步骤 2:游标引擎(route.go)

- [ ] 新 `routeWalk`(lastItemID)+ per-group 轮询计数器(`sync.Map[int]*uint64`,v1 同款原子递增)。
- [ ] `pickGroupItem(group, walk) model.GroupItem` 重构:粘住→向下→绕圈→冷却/探测门控→零值;按模式定起点(manual 原样 / failover 亲和或队头 / roundrobin 旋转位)。
- [ ] 删除原"扫到 CurrentItemID 即 break"抢占逻辑;`CurrentItemID` 仍在选中时更新(亲和锚+前端展示)。
- [ ] `recordRouteFailure/recordRouteSuccess/releaseRouteProbe/RouteStateOf` 不动。

### 步骤 3:handler 接线

- [ ] 循环外 `walk := &routeWalk{}`;`pickGroupItem` 传游标,选中后 `walk.lastItemID = item.ID`。
- [ ] 零值分支:完全沿用现有 `wait(MemberRetryIntervalSeconds)` 循环,零新增。
- [ ] 中止不计失败:`attempts append` 移到 `ctx.Err()` 检查之后;流式收尾块 `RequestFailed` 统计加 `ctx.Err() == nil` 条件。
- [ ] 其余循环体零改动(grant 校验/超时/统计)。

### 步骤 4:前端

- [ ] `web/src/api/group.ts`:GroupMode 加 `'roundrobin'`(无新旋钮字段)。
- [ ] Editor 模式选择器、Card 模式徽标加 roundrobin;roundrobin 下亲和旋钮置灰+提示。
- [ ] i18n 三语(zh_hans/zh_hant/en):模式名与提示。

### 步骤 5:测试

- [ ] `internal/relay/route_cursor_test.go`:design.md 测试设计十条(粘住/向下/绕圈/全员冷却零新增/探测单飞/亲和起点/轮询旋转与隔离/渠道连续性/中止不计失败×2)。
- [ ] 既有 relay 测试回归:manual 路径、亲和武装、探测解冷全绿。

### 步骤 6:验证关卡(octopus-verify 全流程)

- [ ] 容器内编译 + `go test ./internal/...`;前端 lint + build。
- [ ] 起容器 UI 人审(必做):切 roundrobin 模式连续请求看轮转分布;failover 拖序后走游标(不回头抢);全员冷却时请求等待后恢复;模式切换正常。
- [ ] 写 `.octopus-verified` marker。

## 验证命令

```bash
docker run --rm -v "$PWD":/src -w /src -v "octopus-go-mod:/go/pkg/mod" -v "octopus-go-build-cache:/root/.cache/go-build" \
  golang:1.26 sh -c "CGO_ENABLED=0 go test -tags=jsoniter -buildvcs=false ./internal/relay/... ./internal/..."
cd web && npx tsc --noEmit && npx eslint .
```

## 回滚点

- 无 schema 破坏(Mode 枚举值 + RelayConfig 增量列);revert 单 commit 即回滚。
- 风险最高点是步骤 2 的 pickGroupItem 重构——若行为异常,回滚仅还原 route.go + handler 两文件。

## 审查门

- 步骤 2 完成后:对照 PRD R1-R6 语义逐条自查(尤其"恢复不插队"与"同成员重试粘住")。
- 步骤 5 完成后:既有 relay 测试零改动全绿(改了既有用例=语义回归,停下复查)。
- 提交前:octopus-verify 全过 + spec 更新(relay-routing.md 已知问题节改写为游标契约)。
