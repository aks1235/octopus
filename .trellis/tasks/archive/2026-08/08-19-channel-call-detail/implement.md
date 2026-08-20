# Implement — ⑥ 渠道调用详情页

> 执行计划,对应 `design.md`。验证关卡:go build/test -tags=jsoniter + pnpm build + lint(自改文件);容器+UI smoke 留父任务 Step 8。

## 前置确认
- [x] 分支 `feat/my-features`(基底 8d04257 + ①②③④⑤),已在分支。
- [x] ④ 已加 `user_agent`/`client_name` 到 `LogOverview`(commit `49c6e78`/`f3052c4`),⑥ 改 `LogAttempt`/`LogRecord` 不冲突。
- [x] `CODEX_FALLBACK`:本会话无 Codex CLI(用户预授权直写),长文档与代码均直写并记本标记。

## Step 1 — 后端 log.go 存储层
- [ ] 1.1 加 `AttemptStatus` 类型 + `AttemptSuccess/Failed/Skipped` 常量(log.go,LogEventType 常量后)。
- [ ] 1.2 `LogAttempt` 加 `Status AttemptStatus \`json:"status,omitempty"\`` 与 `Duration int64 \`json:"duration,omitempty"\``。
- [ ] 1.3 `LogRecord` 加 `attempts []LogAttempt`(未导出,在 currentAttempt 前)。
- [ ] 1.4 改 `applyLog`:进入时 `if prev,ok := logRecords[record.ID]; ok { record.attempts = prev.attempts }`;AttemptFinished/ResponseCommitted 分支 `record.attempts = append(record.attempts, *attempt)`。其余游标逻辑不动。
- [ ] 1.5 加 `ChannelAttemptDetail` 结构 + `GetLogAttemptsByChannel(channelName string)`(见 design §2.4)。

## Step 2 — 后端 execution.go 设字段
- [ ] 2.1 `recordUnavailableTarget`:`e.emit(LogEventAttemptFinished, attempt)` 前设 `attempt.Status=AttemptSkipped`(Duration 留 0)。
- [ ] 2.2 `handleAttemptFailure`:三处 `e.emit(LogEventAttemptFinished, attempt)`(L207/212/226)前设 `attempt.Status=AttemptFailed; attempt.Duration=duration.Milliseconds()`。
- [ ] 2.3 `commitAttempt`:`e.emit(LogEventResponseCommitted, attempt)`(L233)前设 `attempt.Status=AttemptSuccess; attempt.Duration=metricDuration.Milliseconds()`。
- [ ] 2.4 确认 `import "time"` 已在 execution.go(duration.Milliseconds 无需新 import;metricDuration 已 time.Duration)。

## Step 3 — 后端 HTTP 接口
- [ ] 3.1 `internal/server/handlers/log.go` init() 加 `router.NewRoute("/channel-attempts", http.MethodGet).Handle(getChannelAttempts)`。
- [ ] 3.2 加 `getChannelAttempts` handler:取 `c.Query("channel_name")`,空则 400,否则 `resp.Success(c, relay.GetLogAttemptsByChannel(name))`。补 `"strings"` import。
- [ ] 3.3 路由冲突自检:`go build` 时 gin 不 panic(参照 /clear 先例)。

## Step 4 — 后端验证关卡
```bash
# golang:1.25 + GOTOOLCHAIN=auto(自动拉 1.26.4) + -buildvcs=false
docker run --rm -v "$PWD":/work -w /work -e GOTOOLCHAIN=auto golang:1.25 \
  go build -buildvcs=false ./...
docker run --rm -v "$PWD":/work -w /work -e GOTOOLCHAIN=auto golang:1.25 \
  go test -tags=jsoniter -buildvcs=false ./internal/relay/... ./internal/server/handlers/...
```
- [ ] 4.1 `go build ./...` 通过。
- [ ] 4.2 `go test -tags=jsoniter ./internal/relay/... ./internal/server/handlers/...` 通过(含 client_detect_test.go 等 ④ 既有测试不被破坏)。
- 失败 → 回 Step 1-3 修正,不进前端。

## Step 5 — 前端 API + 类型
- [ ] 5.1 `web/src/api/log.ts` 加 `ChannelAttemptDetail` interface(镜像后端 json tag)。
- [ ] 5.2 加 `useChannelAttempts(channelName, enabled)` hook(`useQuery` + `apiRequest`,staleTime:0)。

## Step 6 — 前端渠道详情视图
- [ ] 6.1 新建 `web/src/components/modules/channel/CallDetail.tsx`:MorphingDialog + 概览统计 + 明细列表。
- [ ] 6.2 顶部 useMemo 聚合 total/success/failed/skipped 计数。
- [ ] 6.3 逐条明细渲染字段(请求 id/状态/时间/模型/最终渠道/attempt 序号/状态/耗时/错误);request_id 点击 → `useAppStore.getState().setCurrentPage('log')`。
- [ ] 6.4 空态/loading/error 态(复用上游空态样式)。
- [ ] 6.5 stretch 评估:app store 加 `pendingLogRequestId` 让日志页自动开详情是否 <20 行;是则做,否记 TODO。

## Step 7 — 前端 Card 入口
- [ ] 7.1 `channel/Card.tsx` 操作区加「调用详情」按钮,onClick 开 CallDetail MorphingDialog,传 `channel.name`。
- [ ] 7.2 按钮文案走 i18n,不硬编码。

## Step 8 — i18n 三语同步
- [ ] 8.1 `web/src/locales/en.json` / `zh_hans.json` / `zh_hant.json` 加 `channel.callDetail.*`(title/trigger/total/success/failed/skipped/empty/noData)。
- [ ] 8.2 三语 key 一致,缺一不可。

## Step 9 — 前端验证关卡
```bash
# node:22-alpine + corepack
docker run --rm -v "$PWD/web":/work -w /work node:22-alpine \
  sh -lc 'corepack enable && corepack prepare pnpm@latest --activate && pnpm install --frozen-lockfile && pnpm build'
```
- [ ] 9.1 `pnpm build` 通过(tsc + vite,类型错误会失败)。
- [ ] 9.2 lint 自改文件:`pnpm lint` 只查 ⑥ 新增/改动文件,不修 49 baseline(既有告警不动)。
  ```bash
  docker run --rm -v "$PWD/web":/work -w /work node:22-alpine \
    sh -lc 'corepack enable && pnpm install --frozen-lockfile && pnpm lint'
  ```
  只看 CallDetail.tsx/Card.tsx/api/log.ts/locales 是否引入新告警。

## Step 10 — 自检(<self_reflection>)
- [ ] 10.1 Maintainability:applyLog 累积逻辑可读,注释中文。
- [ ] 10.2 Tests:既有 relay/handlers 测试通过;⑥ 查询函数无新单测(内存 map 难 mock,留集成 smoke)。
- [ ] 10.3 Performance:遍历 ≤50 记录 × 每请求少量 attempts,无热路径;查询持锁但 O(n) 小集合,可接受。
- [ ] 10.4 Security:接口 Auth 鉴权,只读,无注入(channel_name 仅做 == 比较)。
- [ ] 10.5 Style:字段命名与上游一致(channel_name/snake json),前端 hook 与 useLogs 同模式。
- [ ] 10.6 Backward compat:LogRecord/LogAttempt 纯增量;SSE 负载多可选字段;④ 字段不受影响。

## Step 11 — 提交
- [ ] 11.1 一个 commit,沿用 dev 原写法(feat + 中文正文 why)。
- [ ] 11.2 不混入其他子功能改动。

## 验证边界(本子任务到此为止)
- go build/test -tags=jsoniter ✓(Step 4)
- pnpm build + lint 自改文件 ✓(Step 9)
- **容器起服务 + UI smoke 留父任务 Step 8**(本子任务不做)。
