# Implement — ④ 客户端 UA 识别

> CODEX_FALLBACK:本会话无 Codex CLI,按用户授权用 Write/Edit/Bash 直写。

## 步骤

- [ ] 0. 上下文收集完成,三件套就绪 → 提交用户 review。
- [ ] 1. review gate:用户 review 通过 → `task.py start` → status=in_progress。
- [ ] 2. 后端:移植 `internal/relay/client_detect.go` + `client_detect_test.go`(取 dev `a6c2e4f` 原文,包名 `relay`,可直接搬)。
- [ ] 3. 后端:`internal/relay/log.go` `LogOverview` 加 `UserAgent`/`ClientName` 字段(json: `user_agent`/`client_name`)。
- [ ] 4. 后端:`internal/relay/execution.go` `execute()` line 52 后只读捕获 UA → `DetectClient` → 写 `e.log`。
- [ ] 5. 后端验证:docker `go build -tags=jsoniter ./internal/...` + `go test -tags=jsoniter ./internal/... ./cmd/...`(含 client_detect_test)。
- [ ] 6. 前端:`web/src/api/log.ts` `RelayLogOverview` 加 `user_agent`/`client_name`。
- [ ] 7. 前端:`web/src/lib/client-icons.tsx` 复用 model-icons 机制;列 `@thesvg/react` subpath 导出确定可用客户端品牌图标,缺则 `lucide-react`。
- [ ] 8. 前端:`web/src/components/modules/log/ClientIcon.tsx` 徽标组件(复用 `components/ui/tooltip.tsx`)。
- [ ] 9. 前端:`web/src/components/modules/log/Item.tsx` 模型图标叠加 `<ClientIcon>`(line 203 卡片 + line 266 弹窗)。
- [ ] 10. 前端验证:docker `pnpm build` + 自改文件 `pnpm lint` 干净。
- [ ] 11. commit(`feat: + 中文 why`)。
- [ ] 12. `task.py` 归档子任务。

## 验证命令

```bash
# 后端
docker run --rm -v "$PWD":/src -w /src -e GOTOOLCHAIN=auto golang:1.25 sh -c "go build -tags=jsoniter ./internal/... && go test -tags=jsoniter ./internal/... ./cmd/..."

# 前端
cd web && docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c "corepack enable>/dev/null; pnpm install --frozen-lockfile; pnpm run build"
# 自改文件 lint
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c "corepack enable>/dev/null; pnpm install --frozen-lockfile >/dev/null 2>&1; pnpm exec eslint src/lib/client-icons.tsx src/components/modules/log/ClientIcon.tsx src/components/modules/log/Item.tsx src/api/log.ts"
```

## 回滚点

- 每步未 commit 前:`git checkout -- <file>` 回退单文件。
- commit 后:`git reset --hard HEAD^`。

## 不做

- 不跑 octopus-verify 容器 smoke(deploy.sh 缺失,留父任务 Step 8)。
- 不修 pnpm lint baseline 49 error(上游 UI 基础组件,非 ④ 引入)。
- 不动 dev 的 active_request.go/metrics.go/relay.go(活跃请求监控修复非 ④ 范围)。
