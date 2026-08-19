# PRD — ③ 模型连通性测试

> 父任务: 08-19-octopus-route-a-migration
> 基底: feat/my-features(8d04257 + ①② 已完成)
> 来源: dev(fork 自 chicring `f9534b5`),`internal/helper/test.go` + `handlers/channel.go` testModels 系列

## Goal

在上游 master 基底(含 ①②)上重新实现「模型连通性测试」。
对指定渠道的一个或多个模型,各发一个最小 chat 请求(max_tokens=1,"1+1=?"),
根据上游响应状态码判定渠道+模型是否可达/凭据是否有效,前端提供按钮触发并展示逐模型结果。

## 来源实现摘要(dev)

- `internal/helper/test.go`:`TestModels(ctx, channel, models)` / `TestModelsWithKey(...)` / `testSingleModel(...)`
  - 用 `outbound.Get(outType)` + `provider.GetOutbound(pid)` 取 dev 的 transformer(自研 transformer 层)
  - 构造 `InternalLLMRequest`(model + 1 条 user message "1+1=?" + MaxTokens=1 + Stream=false;embedding 模型走 EmbeddingInput)
  - `transformer.TransformRequest(ctx, req, baseUrl, key)` → `*http.Request` → `httpClient.Do` → 判状态
  - 2xx → Passed;429 → Passed+"Rate limited, but reachable";其他 → Failed+resp.Status
  - 30s 超时,记录 Delay(毫秒)
  - 多 BaseUrl/多 Key 结构(`resolveFirstBaseUrl`、`ChannelGetKey` 轮询)
- `handlers/channel.go`:`testModels`(`{channel_id, models}`)/ `testModelsByConfig`(未保存配置)/ `testModelsByKey`(`{channel_id, key_id, models}`)
- 路由:`POST /test-models`、`/test-models-by-config`、`/test-models-by-key`

## 上游架构差异(决定重写而非照搬)

- **出站转换层不同**:dev 用自研 `transformer/outbound/provider` 包;上游用 `github.com/looplj/axonhub/llm`,
  由 `internal/relay/transformers.go` 的 `newOutbound(channel.Type, baseURL, key)` 返回 `transformer.Outbound`(axonhub)。
- **channel 结构不同**:上游是单 BaseURL + 单 Key(无多 URL/多 Key),无 keyID 概念 → 不需要 `testModelsByKey`/`resolveFirstBaseUrl`。
- **helper 调上游的方式**:上游 `internal/helper/fetch.go`(`FetchModels`)已用**纯 `net/http`** 直接向渠道上游发请求(GET /models),
  走 `transformer.NormalizeBaseURL` + 每提供商 auth 头 + `applyCustomHeaders`。这是上游既有模式。
- 但连通性测试要发 **chat 请求**而非 GET /models,各提供商 chat 端点/正文不同(Gemini 尤其特殊)。
  若照 fetch.go 的纯 net/http 路线,需为 5 类提供商各手写 chat 端点+正文,易错且与真实流量不一致。
- **决策**:走 axonhub 出站转换器(见 design.md「Design B」),provider 无关、自动正确处理 URL/正文/auth,
  并经 `applyChannelOptions` 应用 ParamOverride+CustomHeader,与真实 relay 流量一致。这契合父 design「③ 后端测试 API 可复用 relay 链路」。

## Requirements

### 功能需求(后端)

- 新增 `internal/relay/test.go`(package relay):
  - `TestModelResult{Model string; Passed bool; Error string; Delay int64}`(与 dev 形状一致,毫秒)
  - `TestModels(ctx, channel *model.Channel, models []string) []TestModelResult`:遍历 models 调 `testSingleModel`
  - `testSingleModel(ctx, channel, modelName)`:
    1. `helper.ChannelHttpClient(channel)` 取 client
    2. `newOutbound(channel.Type, channel.BaseURL, channel.Key)` 取出站转换器;err → `{Passed:false, Error:"unsupported channel type: ..."}`
    3. 构造最小 `*llm.Request`:`{Model: modelName, Messages:[{Role:"user", Content: MessageContent{Content: ptr("1+1=?")}}], MaxTokens: ptr(int64(1)), Stream: ptr(false)}`
    4. `outbound.TransformRequest(ctx, llmReq)` → `*httpclient.Request`
    5. `applyChannelOptions(channel, httpReq)` 应用 ParamOverride+CustomHeader(与真实 relay 一致)
    6. `httpclient.BuildHttpRequest(ctx, httpReq)` → `*http.Request`
    7. 30s 超时 ctx,`client.Do(rawReq)`,记录 Delay
    8. 状态判定:2xx → Passed=true;429 → Passed=true + Error="Rate limited (429), but channel is reachable";其他 → Passed=false + Error=resp.Status;Do 报错 → Passed=false + Error=err.Error()
- 新增 `handlers/channel.go` `testModels(c *gin.Context)`:
  - 请求体 `{ChannelID int; Models []string}`
  - 校验 `ChannelID>0 && len(Models)>0`;`op.ChannelGet(ChannelID, ctx)` 取渠道;5 分钟超时;调 `relay.TestModels`;`resp.Success`
- 路由:`POST /api/v1/channel/test-models`(挂在现有 JSON+Auth group,紧邻 `/fetch-model`)

### 功能需求(前端)

- `web/src/api/channel.ts`:
  - `type TestModelResult = { model: string; passed: boolean; error?: string; delay?: number }`
  - `useTestModels()` mutation:`apiRequest<TestModelResult[]>('/api/v1/channel/test-models', {method:'POST', body:{channel_id, models}})`(镜像 `useFetchModel`)
- `web/src/components/modules/channel/CardContent.tsx`(渠道详情弹窗 viewing tab):
  - 加「连通性测试」按钮(测试该渠道全部模型 = auto+custom 去重),调 `useTestModels`
  - 结果区:逐模型展示 名称 + ✓/✗ + 延迟 + 错误信息;测试中显 loading 态
- i18n:`web/src/locales/{en,zh_hans,zh_hant}.json` 的 `channel.detail` 下加 key(testConnectivity / testPending / testPassed / testFailed / delay 等),三语同步

### 技术约束

- **MVP 范围只做「已保存渠道」测试**(`{channel_id, models}`),不做 dev 的 `testModelsByConfig`(未保存配置)与 `testModelsByKey`(按 key)。前者由 `/fetch-model` 覆盖配置校验;后者上游无 keyID 概念,不适用。
- **embedding 模型**:MVP 一律发 chat 请求。embedding-only 模型会返回 4xx → Passed=false(附错误)。可达性已验证(渠道+凭据+端点可达,只是请求类型不匹配)。如需更精准,后续按模型名/类型走 EmbeddingInput,列为可选增强(见范围外)。
- 一个功能一个 commit,message 沿用 dev 原始写法(feat: 模型连通性测试)。
- 不破坏上游已有能力;不碰 ①② 已加的 setting/channel 字段(本任务不涉及 setting.go/channel.go model)。

## Acceptance Criteria

- [ ] `go build -tags=jsoniter ./internal/... ./cmd/...` 通过(BACKEND_BUILD_OK)
- [ ] `go test -tags=jsoniter ./internal/... ./cmd/...` 无 FAIL
- [ ] `internal/relay/test.go` 新增,含 `TestModels`/`testSingleModel`,经 axonhub 出站转换器发请求
- [ ] `internal/server/handlers/channel.go` 含 `testModels` handler + `POST /test-models` 路由
- [ ] `cd web && pnpm lint && pnpm build` 通过
- [ ] `web/src/api/channel.ts` 含 `useTestModels` hook + `TestModelResult` 类型
- [ ] `CardContent.tsx` 含「连通性测试」按钮 + 结果展示
- [ ] `web/src/locales/{en,zh_hans,zh_hant}.json` 三语同步含新 key
- [ ] smoke test:起服务,对一可用渠道+一真实模型发测试,前端显示 ✓+延迟;对一错误凭据/不存在模型显示 ✗+错误

## 范围外

- `testModelsByConfig`(未保存配置测试)/ `testModelsByKey`(按 key 测试)
- embedding 模型走 EmbeddingInput 的精准测试(列为可选增强)
- 测试结果持久化/历史记录(纯即时查询,不入库)
