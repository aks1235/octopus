# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

本文件记录 Octopus 后端(Relay/axonhub + GORM 内存缓存层)的质量铁律,源自路线 A 迁移 7 项功能与上游 rebase 的实操教训。重点不是「干净代码」泛论,而是**会导致编译失败或运行时回归的具体反模式**。

---

## Forbidden Patterns

### 禁止:改函数签名/删源文件而不同步全部调用方

**症状**:rebase 或重构后 `go build` 失败,报「not enough arguments / multiple-value in single-value context / undefined: xxx」。
**根因**:仅改了定义处(删 helper 函数、给函数加 ctx 参数、改返回值个数),漏改分散在多文件的调用方。
**真实教训**:上游 `27a29a3` 删 `internal/helper/price.go` 却留 4 处 `helper.LLMPriceAddToDB` 调用 + 改 `op.LLMList` 去掉 ctx 却留 `task/sync.go:80` 的 `op.LLMList(ctx)`,导致 upstream HEAD 自身不可编译。
**铁律**:
- 改签名的 commit,**同一 commit 内**用 `git grep -n '<旧函数名>'` 找全调用方逐一同步。
- 删函数前先 `git grep -n 'func <名字>'` 确认无外部引用;删完再 `git grep -n '<名字>('` 确认零残留。
- Go 没有跨包「未使用」编译保护(只对局部变量),函数被删后调用方在**别的包**才报错——容易漏。

### 禁止:在 relay 热路径调展示填充函数

详见 [Database Guidelines](./database-guidelines.md)「GroupList 必须按用途分流」。`op.GroupList` 有 O(全量 GroupItem) 填充开销,内部热路径(`ChannelAutoGroup`)必须用 `GroupListRaw`。

### 禁止:就地改缓存对象的 slice 字段

详见 Database Guidelines「填充必须重建 slice」。`groupCache.GetAll()` 返回值的 `Items` slice header 仍指向缓存底层数组,就地改非持久化字段会写回脏值。

---

## Required Patterns

### Go 工具链:GOTOOLCHAIN=auto

上游 `go.mod` 要求 `go 1.26.4`,但 CI/skill 容器配 `golang:1.25`。**所有容器内 go build/test 必须加 `-e GOTOOLCHAIN=auto`**,让 go 自动下载 1.26.4 工具链。不加会报 `go.mod requires go >= 1.26.4 (GOTOOLCHAIN=local)`。
```bash
docker run --rm -v "$PWD":/src -w /src -e GOTOOLCHAIN=auto golang:1.25 \
  sh -c "go build -tags=jsoniter ./internal/... && go test -tags=jsoniter ./internal/..."
```
构建二进制加 `-buildvcs=false`(容器内无干净 git 状态会报 `error obtaining VCS status`)。

### 编译前:满足 go:embed

`internal/static/static.go` 用 `//go:embed all:out` 嵌入前端产物。`go build .` 前 **`static/out/` 必须有内容**(前端 `vite build` 输出,`vite.config.ts` 的 `outDir=../static/out`)。空目录报 `pattern all:out: no matching files found`。纯后端编译用占位:
```bash
mkdir -p static/out && echo '<!--ph-->' > static/out/.placeholder
```

### 新增熔断器接入点:叠加而非替换

熔断器(`internal/relay/balancer/circuit.go`)三处接入 `execution.go` 均**在 ⑥ 已设终态字段之后追加**,不改 ⑥ 字段(见子任务 ⑦ design)。无熔断记录时 `IsTripped` 返回 false,对 ①–⑥ 透明。

---

## Testing Requirements

- **熔断器**:`circuit_test.go` 5 用例(三态/模型隔离/HalfOpen 超时/冷却恢复/指数退避)。手设 `entry` 时间字段验证时间逻辑,**不 sleep**。
- **op 包**:无 _test.go 基建,`GroupAdvanceActiveItem` 等靠**父任务集成 smoke**(起容器 → 模拟失败 → 看日志),非单测。

---

## Code Review Checklist

- [ ] 改了函数签名?`git grep` 全调用方已同步?
- [ ] 删了源文件?全树零残留引用?
- [ ] 容器内 build 用了 `GOTOOLCHAIN=auto`?
- [ ] `go build .` 前 `static/out/` 有内容?
- [ ] 动了缓存对象?slice 字段是否重建?
- [ ] 前端改了 `web/src`?lint error 数 ≤ 上游 baseline(本项目上游 react-hooks 预设已有 12 个既有 error,feat 不应净增)?

---

## 相关

- [Database Guidelines](./database-guidelines.md) — 缓存/slice 重建/渠道-分组一致性契约
- 熔断器设计 → `.trellis/tasks/archive/2026-08/08-19-circuit-breaker-port/design.md`

