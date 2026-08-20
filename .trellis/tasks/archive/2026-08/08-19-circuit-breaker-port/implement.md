# Implement — ⑦ 熔断器(X+ 混合方案)

> 粒度方案 X+ 已与用户确认。本会话无 Codex CLI,直写并记 `CODEX_FALLBACK`。
> ⑦ 纯后端(无前端改动),pnpm build 仅作回归关卡。

## Step 1 — 移植 circuit.go(B1 两段 key)

- [ ] 1.1 新建 `internal/relay/balancer/circuit.go`,逐行移植 dev `internal/relay/balancer/circuit.go`,仅改签名(keyID 去掉):
  - `circuitKey(channelID int, modelName string) string` → `fmt.Sprintf("%d:%s", channelID, modelName)`
  - `IsTripped` / `RecordSuccess` / `RecordFailure` 改 `(channelID int, modelName string)`
  - 状态机原样:`CircuitState`/`circuitEntry`/`getOrCreateEntry`/`getThreshold`/`GetCooldown`/三态 switch/`base<<(tripCount-1)` shift≤20/30s HalfOpen 回退
  - import:`fmt`/`sync`/`time`/`internal/model`/`internal/op`/`internal/utils/log`
- [ ] 1.2 编译该包:`go build ./internal/relay/balancer/`(应独立通过)

## Step 2 — setting.go 加回三 key

- [ ] 2.1 `internal/model/setting.go`:
  - const 块加 `SettingKeyCircuitBreakerThreshold`/`Cooldown`/`MaxCooldown`(字符串与注释照 dev)
  - `DefaultSettings()` 加默认值 5/60/600
  - `Validate()` 整数校验 case 并入这 3 个 key

## Step 3 — op/group.go 加 GroupAdvanceActiveItem

- [ ] 3.1 `internal/op/group.go` 加 `GroupAdvanceActiveItem(groupID, fromItemID int, ctx context.Context) bool`(见 design.md §5)
  - priority 升序找当前项,next = `(cur+1)%len`,末尾回绕
  - CAS:`Where("id = ? AND active_item_id = ?", groupID, fromItemID).Update(...)`,`RowsAffected==0` 则不推进
  - 成功后 `groupRefreshCacheByID` + `notifyGroupActiveItemChanged`
  - 新增 import `sort`
- [ ] 3.2 编译:`go build ./internal/op/`

## Step 4 — execution.go 三处接入

- [ ] 4.1 import 加 `fmt` + `github.com/bestruirui/octopus/internal/relay/balancer`
- [ ] 4.2 `executeAttempt` 顶部(design §6.1):IsTripped 查 → 推进 + `recordUnavailableTarget` + return
- [ ] 4.3 `handleAttemptFailure` 真实失败分支末尾(design §6.2):`RecordFailure(channel.ID, item.ModelName)`
- [ ] 4.4 `commitAttempt` 顶部 emit 后(design §6.3):`RecordSuccess(channel.ID, item.ModelName)`
- [ ] 4.5 全量编译:`go build ./...`

## Step 5 — 写 circuit_test.go

- [ ] 5.1 新建 `internal/relay/balancer/circuit_test.go`(package balancer),5 个测试(design §9.1):
  - TripsAfterThreshold / ModelScoped / HalfOpenTimeoutFallback(移植 dev 改 2 参)
  - CooldownToHalfOpen / ExponentialBackoff(新写,手改 entry 时间字段,不 sleep)
  - 每个 test `globalBreaker.Delete(key)` 清理,key=`"1:gpt-4"`

## Step 6 — 验证关卡

- [ ] 6.1 go build
  ```bash
  GOTOOLCHAIN=auto go build -buildvcs=false ./...
  # 或容器:docker run --rm -v "$PWD":/app -w /app -e GOTOOLCHAIN=auto golang:1.25 go build -buildvcs=false ./...
  ```
- [ ] 6.2 go test
  ```bash
  GOTOOLCHAIN=auto go test -tags=jsoniter -buildvcs=false ./...
  ```
  重点看 `internal/relay/balancer/` 全绿。
- [ ] 6.3 lint(49 baseline 不修,只查自改文件新增项):
  ```bash
  # golangci-lint run ./internal/relay/balancer/... ./internal/op/... ./internal/model/... ./internal/relay/...
  # 对比 baseline 49,只处理自改文件 NEW 项;原有 49 不动
  ```
- [ ] 6.4 pnpm build 回归(⑦ 无前端改动,仅确认 web 不破):
  ```bash
  cd web && pnpm build
  # 或容器:docker run --rm -v "$PWD"/web:/app -w /app node:22-alpine sh -c "corepack enable && pnpm install --frozen-lockfile && pnpm build"
  ```

## Step 7 — 提交

- [ ] 7.1 一个 commit,message 沿用 dev 写法(feat + 中文正文说明 why):
  ```
  feat: 移植熔断器并接入上游 execution.go(三态/指数退避/HalfOpen + 活动渠道熔断自动推进)
  ```
- [ ] 7.2 commit 只含 ⑦ 改动,不混其他。

## Step 8 — 交接父任务(不在本任务实现)

- [ ] 8.1 父任务 Step 8 smoke:起容器 → 多渠道分组 → 模拟首渠道连续失败 → 观察自动推进 + 冷却 + HalfOpen 恢复
- [ ] 8.2 ⑦ 完成后,feat/my-features 一次性 rebase 到 upstream/master 最新(⑦ 是最后一项)

## 验证命令速查(容器版,关卡用)

```bash
# 后端编译+测试
docker run --rm -v "$PWD":/app -w /app -e GOTOOLCHAIN=auto golang:1.25 \
  sh -c "go build -buildvcs=false ./... && go test -tags=jsoniter -buildvcs=false ./..."
# 前端回归
docker run --rm -v "$PWD"/web:/app -w /app node:22-alpine \
  sh -c "corepack enable && pnpm install --frozen-lockfile && pnpm build"
```

## 回滚点

- 删 `internal/relay/balancer/` + revert execution.go 三处 + revert setting.go 三 key + 删 `GroupAdvanceActiveItem` = 完全回退,无数据迁移。
