# ④ 客户端 UA 识别

## Goal

在上游 master 基底的 relay 入口解析入站请求的 `User-Agent`,识别 AI 编码客户端(claude-code / cline / cursor 等 35+),在日志概览(SSE 流)携带原始 UA + 客户端标识,前端在日志卡片的模型图标上叠加客户端图标徽标。

## 背景

- 来源:dev(fork 自 chicring `a6c2e4f`),原提交捆绑「UA 识别 + 活跃请求监控多个 bug 修复」。④ 只搬 UA 识别核心,跳过活跃请求监控修复(上游 active request 机制与 dev 不同)。
- **上游日志为纯内存 SSE**:`internal/relay/log.go` 的 `LogOverview`/`LogRecord`(map + trim 50 条),**无持久化 gorm log 模型**——dev 的 `internal/model/log.go` 在上游不存在,`grep RelayLog internal/` 全空。

## Requirements

- **后端**:移植 `client_detect.go`(UA→客户端名解析器,35+ 规则,`wordBoundaryContains` 避免短词误匹配)+ 单元测试。
- **后端**:`LogOverview` 加 `UserAgent` / `ClientName` 两字段(JSON tag),SSE 概览流自动下发。
- **后端**:`execution.go` 入口只读捕获 UA(不改请求流):`e.ctx.GetHeader("User-Agent")` → `DetectClient` → 写 `e.log`。
- **前端**:`RelayLogOverview` TS 类型加 `user_agent` / `client_name`。
- **前端**:新建 `client-icons.tsx`(复用 `model-icons.tsx` 机制:getter + 品牌色 + 图标组件,优先 `@thesvg/react` 品牌图标,缺则 `lucide-react`)+ `ClientIcon.tsx` 徽标组件(复用 `components/ui/tooltip.tsx`)。
- **前端**:`log/Item.tsx` 在模型图标右下角叠加客户端图标徽标。
- **i18n**:客户端显示名用静态 label(产品名多为专有名词),**不新增 locale key**(沿用 `model-icons.tsx` 不走 i18n 的先例)——见 design §5 决策 D2。

## Constraints

- UA 捕获只读不改请求流(⑦ 后续也接 execution.go,留干净接入点)。
- ④⑥ 共改 `internal/relay/log.go`(**非** dev 的 `model/log.go`):④ 加 UA 字段,⑥ 后续加 channel-attempts 查询。字段名规划不冲突(见 design §4)。
- **不涉及 DB migrate**:上游无 log 表。原「加列走 migrate」约束在上游结构下不适用,改为内存结构字段(见 design §5 决策 D1)。
- 一个功能一个 commit,message 沿用 dev 写法(`feat: + 中文正文 why`)。
- 本会话无 Codex CLI → 代码直写 Write/Edit/Bash,记 `CODEX_FALLBACK`。
- 构建/测试在 docker 容器:`docker run --rm -v "$PWD":/src -w /src -e GOTOOLCHAIN=auto golang:1.25 sh -c "go build -tags=jsoniter ./internal/... && go test -tags=jsoniter ./internal/... ./cmd/..."`
- 前端:`cd web && docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c "corepack enable>/dev/null; pnpm install --frozen-lockfile; pnpm run build"`
- 验证只到 build/test/前端 build(技术关卡),不跑 octopus-verify 容器 smoke(deploy.sh 在本分支缺失,容器+UI smoke 留父任务 Step 8)。
- `pnpm lint` baseline 49 error(上游 UI 基础组件)不当失败,只查自改文件 lint 干净。

## Acceptance Criteria

- [ ] `client_detect.go` + `_test.go` 移植,`go test` 通过。
- [ ] `LogOverview` 含 `UserAgent`/`ClientName` 字段,SSE 概览流下发含两字段。
- [ ] `execution.go` 入口捕获 UA,只读不改请求流。
- [ ] 前端 `RelayLogOverview` 类型含两字段。
- [ ] 前端日志卡片模型图标右下角叠加客户端图标(有 client_name 时),tooltip 显示客户端名。
- [ ] `go build -tags=jsoniter ./internal/...` 通过。
- [ ] `go test -tags=jsoniter ./internal/... ./cmd/...` 通过。
- [ ] `pnpm build`(web/)通过。
- [ ] 自改前端文件 `pnpm lint` 干净。
- [ ] 一个 commit,归档子任务。

## Notes

- 跳过 dev 的活跃请求监控 bug 修复(`active_request.go`/`metrics.go`/`relay.go` 在上游结构不同,非 ④ 范围)。
- passthrough(图片生成)走 `forwarder`,在 `executeAttempt` 内共享 `e.log`,execution 入口捕获 UA 即覆盖全路径。
- 待用户 review 确认:D1(不涉及 migrate);D2(i18n 用静态 label)。
