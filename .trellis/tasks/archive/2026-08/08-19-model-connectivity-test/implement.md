# Implement — ③ 模型连通性测试

> 前置:已在 feat/my-features(8d04257 + ①②)。本任务纯新增,无 model 层改动。
> 构建/测试在 docker 容器内(`golang:1.25` + `-e GOTOOLCHAIN=auto`,父 design §2)。
> 本会话无 Codex CLI 工具 → 代码直写(Write/Edit/Bash),记 `CODEX_FALLBACK`(工具不可用)。

## Step 1 — 后端:`internal/relay/test.go`

- [ ] 1.1 新建 `internal/relay/test.go`(package relay),实现:
  - `type TestModelResult struct { Model string `json:"model"`; Passed bool `json:"passed"`; Error string `json:"error,omitempty"`; Delay int64 `json:"delay,omitempty"` }`
  - `func TestModels(ctx context.Context, channel *model.Channel, models []string) []TestModelResult`:去空去重遍历调 testSingleModel;空入参返回空 slice
  - `func testSingleModel(ctx, channel *model.Channel, modelName string) TestModelResult`:按 design §3 流程(newOutbound→llm.Request→TransformRequest→applyChannelOptions→BuildHttpRequest→Do→判状态)
  - ptr 辅助:`ptrString`/`ptrInt64`/`ptrBool`(参考 dev test.go 末尾的 `ptrStr`)
- [ ] 1.2 imports:`context`/`net/http`/`time` + `github.com/bestruirui/octopus/internal/helper` + `github.com/bestruirui/octopus/internal/model` + `github.com/looplj/axonhub/llm` + `github.com/looplj/axonhub/llm/httpclient`
- [ ] 1.3 对照 axonhub 源码(gh api 已 verify)确认 `llm.Request`/`llm.Message`/`llm.MessageContent` 字段名与 design §1 一致;若编译报字段不符,以 axonhub `llm/model.go` 为准修正

**验证门**:`go build -tags=jsoniter ./internal/relay/...` 通过(docker 容器内)。

## Step 2 — 后端:handler + 路由

- [ ] 2.1 `internal/server/handlers/channel.go`:
  - 加 `func testModels(c *gin.Context)`(参考 dev 同名,只取 channel_id 变体;5min ctx;调 `relay.TestModels`)
  - 在 `/fetch-model` 路由后加 `router.NewRoute("/test-models", http.MethodPost).Handle(testModels)`
  - import `relay` 包(handlers 已 import helper/model/op/...;确认加 relay 不循环:relay 不 import handlers,OK)
- [ ] 2.2 handler 请求体 `struct { ChannelID int `json:"channel_id"`; Models []string `json:"models"` }`,校验 `ChannelID>0 && len(Models)>0`,`op.ChannelGet(ChannelID, ctx)` 取渠道

**验证门**:`go build -tags=jsoniter ./internal/... ./cmd/...` 通过 + `go test -tags=jsoniter ./internal/... ./cmd/...` 无 FAIL。

## Step 3 — 前端:API 层

- [ ] 3.1 `web/src/api/channel.ts`:
  - 加 `export type TestModelResult = { model: string; passed: boolean; error?: string; delay?: number }`
  - 加 `export function useTestModels()`(镜像 `useFetchModel`,mutationFn POST `/api/v1/channel/test-models` body `{channel_id, models}`)
- [ ] 3.2 类型校验:`cd web && pnpm exec tsc --noEmit`(或 lint)无类型错

## Step 4 — 前端:UI 按钮 + 结果

- [ ] 4.1 `web/src/components/modules/channel/CardContent.tsx`:
  - import `useTestModels`、`TestModelResult`、`Zap`(或 `Plug`)图标、`CheckCircle2`/`XCircle`(已有)
  - viewing tab 内加「连通性测试」按钮:模型集合 = split(channel.model) ∪ split(channel.custom_model) 去重;`disabled={testModels.isPending || modelCount===0}`
  - `useState<TestModelResult[] | null>(null)` 存结果;`onSuccess` 设结果 + toast 汇总;`onError` toast
  - 结果区:结果非 null 时逐行 `模型 + ✓/✗ + delayms + error`
- [ ] 4.2 复用 Card.tsx 的 `splitModels` 思路(CardContent 内本地实现或抽 util)

**验证门**:`cd web && pnpm lint && pnpm build` 通过。

## Step 5 — i18n 三语同步

- [ ] 5.1 `web/src/locales/en.json` / `zh_hans.json` / `zh_hant.json` 的 `channel.detail` 下加:`testConnectivity`/`testPending`/`testPassed`/`testFailed`/`testDelay`/`testNoModels`/`testSummary`(带计数插值)
- [ ] 5.2 三份 key 完全一致(仅译文不同)

**验证门**:`pnpm build` 再过一次(i18n 缺 key 会报)。

## Step 6 — 集成验证(octopus-verify skill 全流程)

- [ ] 6.1 跑 `/octopus-verify`(容器内编译→后端测试→前端 lint+build→起容器→健康检查→人看 UI)
- [ ] 6.2 smoke:UI 点「连通性测试」:可用渠道+真实模型 → ✓+延迟;错误凭据/不存在模型 → ✗+错误信息
- [ ] 6.3 verify 通过 → 写 `.octopus-verified` marker

## Step 7 — 提交

- [ ] 7.1 `git add` 本任务文件,单 commit,message 沿用 dev 写法:`feat: 模型连通性测试`(正文简述 why:经 axonhub 出站转换器发最小请求,provider 无关)
- [ ] 7.2 不混入其他子功能改动;不碰 ①② 文件

## 回滚点

- 任一验证门失败 → 修到过;无法过 → revert 本 commit(①② 不受影响,基底 8d04257 不动)。
