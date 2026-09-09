# 执行计划:客户端识别+主题+思考等级留痕

## 前置

- 契约必读:`.trellis/spec/backend/relay-log-persistence.md`(列冻结/finalize 挂钩/queryKey)。
- fork 参考:`research/fork_client_detect.go`、`fork_client_detect_test.go`、`fork_ClientIcon.tsx`、`fork_globals_css_after_theme.diff.txt`。
- 本地无 go/pnpm 工具链:一律容器执行(`golang:1.26` / `node:22-alpine`,复用 octopus-go-mod / octopus-pnpm-store 卷);容器验证用 data-verify 隔离目录 + /tmp/octopus-verify-override.yml(勿动 ./data,见 memory)。

## 步骤(按序)

### 1. R1 后端:客户端识别
- [ ] `internal/relay/client_detect.go` + `client_detect_test.go` 移植(规则表原样)。
- [ ] `relayLogFinalize` 填 `ClientName: detectClient(userAgent)`。
- [ ] 容器快验:`go build ./internal/... && go test ./internal/relay/ ./internal/op/`。

### 2. R3 后端:思考等级留痕
- [ ] `internal/relay/reasoning.go` 新建:extractReasoningEffort(三协议,anthropic budget 阈值反推;不改请求)。
- [ ] `internal/relay/handler.go` Forward:循环前提取一次;`relayLogFinalize` 加参写 `relay_logs.reasoning_effort`。
- [ ] `internal/relay/reasoning_test.go`:三协议矩阵 + 阈值边界(5000/15000/32768/65536/131073)+ 空串;`log_finalize_test.go` 等级断言扩展。
- [ ] 容器快验同上。

### 3. 容器功能实测(后端部分)
- [ ] 重建镜像起容器(data-verify);mock 上游照常回显(等级透传不需断言 mock 侧,客户端发什么到什么)。
- [ ] 带 UA `claude-code/1.x` 的请求:`relay_logs.client_name=claude-code`。
- [ ] openai 形状请求自带 `reasoning_effort:"high"` → 日志记 high;anthropic 形状请求 `thinking.budget_tokens=32768` → 日志记 high;无等级请求列为空。
- [ ] 手工造一条带 reasoning_effort 的历史行(模拟 fork 迁移数据),确认 API 返回该值。

### 4. 前端 R1+R3
- [ ] log 类型补 `user_agent/client_name/reasoning_effort`。
- [ ] ClientIcon.tsx 移植 + Item.tsx 两处头像叠加 + HistoryPanel 行内图标。
- [ ] 思考等级 Badge(Item + HistoryCard,配色含 xhigh)。
- [ ] locales 三语言补键(log.clientNames.* / setting.themeStyle.*)。
- [ ] 容器快验:pnpm lint + build。

### 5. 前端 R2:Claude 主题
- [ ] setting.ts 加 themeStyle;theme.tsx 应用 data-theme;globals.css 两套 claude 变量(light+dark,补齐 v2 变量清单);设置页「风格」Select。
- [ ] 容器快验同上。

### 6. ADR 勘误 + octopus-verify 全闭环
- [ ] `docs/adr/0002-feature-migration-scope.md` 勘误:识别+主题+思考等级包调整为「客户端识别+主题+思考等级留痕」,分组覆盖不移植及理由。
- [ ] 后端 build+test(硬关卡)→ 前端 lint+build → deploy(手工步骤,跳过 pull)→ 起容器健康检查。
- [ ] UI 确认点告知用户:日志行客户端图标+等级 Badge(含历史区)、设置页风格切换(含 dark 组合)、默认观感无变化。
- [ ] 用户确认后写 `.octopus-verified`。

## 验证命令速查

```bash
# 后端
docker run --rm -v "$PWD":/src -w /src -v octopus-go-mod:/go/pkg/mod -v octopus-go-build-cache:/root/.cache/go-build \
  golang:1.26 sh -c "go build ./internal/... && go test ./internal/relay/ ./internal/op/"
# 前端(仓库根挂载,vite outDir 绝对路径解析)
docker run --rm -v "$PWD":/src -w /src/web -v octopus-pnpm-store:/root/.local/share/pnpm/store -v octopus-node-modules:/src/web/node_modules \
  node:22-alpine sh -c "corepack enable >/dev/null 2>&1; pnpm install --frozen-lockfile >/dev/null 2>&1; pnpm run lint && pnpm run build"
```

## 风险与回滚点

- handler.go 是任务4 冻结契约所在(finalize 挂钩):只加一个提取调用与一个 finalize 参数,不动既有结构;违和即停对照 spec。
- relayLogFinalize 签名变更波及 log_finalize_test:同步更新,勿复制旧断言。
- 回滚:单 revert,无 schema/迁移残留;前端 persist 旧值安全合并。
