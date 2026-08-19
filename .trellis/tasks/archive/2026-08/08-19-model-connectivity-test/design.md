# Design — ③ 模型连通性测试

> 本设计覆盖后端请求链路、函数归属、前端接线与集成点。prd.md 定义需求与验收。

## 1. 方案选型:Design B(经 axonhub 出站转换器)

### 候选

- **Design A(纯 net/http per provider,镜像 fetch.go)**:helper 包内为每类提供商手写 chat 端点+正文。
  - 弃用理由:5 类提供商(OpenAI/OpenAIResponses/Anthropic/Gemini/Volcengine)的 chat 端点与正文各异,Gemini 原生 `:generateContent` 尤其特殊;手写易错且与真实 relay 流量不一致;fetch.go 的 `applyCustomHeaders` 只覆盖 CustomHeader,不含 ParamOverride。
- **Design B(经 axonhub `transformer.Outbound`)**:用 `relay.newOutbound` 取出站转换器,`TransformRequest(*llm.Request)` 生成 `*httpclient.Request`,经 `applyChannelOptions` 应用渠道覆盖,`httpclient.BuildHttpRequest` 转 `*http.Request`,`client.Do` 判状态。
  - 采用理由:provider 无关(自动处理 URL/正文/auth,含 Gemini/Volcengine);经 `applyChannelOptions` 应用 ParamOverride+CustomHeader,与真实流量一致;契合父 design「复用 relay 链路」;逻辑最小。

### 关键事实(已 verify,见 §6)

- `transformer.Outbound` 接口含 `TransformRequest(ctx, *llm.Request) (*httpclient.Request, error)` + `TransformResponse(...)`。
- `relay.newOutbound(channel.Type, baseURL, key)` 返回 `(transformer.Outbound, error)`(`internal/relay/transformers.go`)。
- `relay.applyChannelOptions(channel *model.Channel, request *httpclient.Request) error`(`internal/relay/forward.go`)应用 ParamOverride + CustomHeader。
- `httpclient.BuildHttpRequest(ctx, *httpclient.Request) (*http.Request, error)`(`executePassthroughStream` 已用)。
- `llm.Request{Model string; Messages []Message; MaxTokens *int64; Stream *bool}`;
  `llm.Message{Role string; Content MessageContent}`;
  `llm.MessageContent{Content *string; MultipleContent []MessageContentPart}`(Content 与 MultipleContent 互斥)。
- `helper.ChannelHttpClient(channel)` 返回 `(*http.Client, error)`(已存在,execution.go 在用)。
- `op.ChannelGet(id, ctx)` 返回 `(*model.Channel, error)`(execution.go resolveTarget 在用)。
- channel 单 BaseURL + 单 Key:`channel.BaseURL`/`channel.Key`/`channel.Type`(ChannelProvider 枚举)。

## 2. 函数归属:放 `internal/relay/test.go`(package relay)

### 为什么放 relay 而非 helper

- `newOutbound`/`applyChannelOptions` 均为 relay 包未导出符号。放 relay 包内可直接复用,无需导出内部函数(避免泄漏 relay 内部 API)。
- analogous 的 `helper.FetchModels` 用纯 net/http 故放 helper;连通性测试依赖 axonhub 出站转换器,放 relay 是架构上最诚实的归属(它本就是 relay 链路的复用)。
- relay 已 import helper(execution.go),`test.go` 调 `helper.ChannelHttpClient` 无循环依赖(helper 不 import relay)。

### 导出面

- `TestModelResult`(struct,导出,字段 json tag)
- `TestModels(ctx, channel *model.Channel, models []string) []TestModelResult`(导出,handler 调)
- `testSingleModel(...)`、ptr 辅助(包内未导出)

## 3. 后端请求流(testSingleModel)

```
client := helper.ChannelHttpClient(channel)               // 代理/系统代理
outbound, err := newOutbound(channel.Type, channel.BaseURL, channel.Key)
  err != nil → {Passed:false, Error:"unsupported channel type: <err>"}
llmReq := &llm.Request{
    Model: modelName,
    Messages: []llm.Message{{Role:"user", Content: llm.MessageContent{Content: ptr("1+1=?")}}},
    MaxTokens: ptr(int64(1)),
    Stream: ptr(false),
}
httpReq, err := outbound.TransformRequest(ctx, llmReq)    // *httpclient.Request(URL/Header/Auth 已含)
applyChannelOptions(channel, httpReq)                      // ParamOverride + CustomHeader
rawReq, err := httpclient.BuildHttpRequest(ctx, httpReq)  // *http.Request
testCtx, cancel := context.WithTimeout(ctx, 30s); defer cancel()
resp, err := client.Do(rawReq.WithContext(testCtx))
delay := time.Since(start).Milliseconds()
  err != nil → {Passed:false, Error: err.Error(), Delay: delay}
  2xx → {Passed:true, Delay: delay}
  429 → {Passed:true, Error:"Rate limited (429), but channel is reachable", Delay: delay}
  其他 → {Passed:false, Error: resp.Status, Delay: delay}
defer resp.Body.Close()  // 不读 body,只判状态(最小成本;避免 draining 大响应)
```

> `TransformRequest` 已通过 outbound 的 `auth.NewStaticKeyProvider(key)`(见 `newOutbound` 实现)把 auth 烘进 httpReq,无需再手动设 Authorization。CustomHeader 经 `applyChannelOptions` 叠加(与真实 relay 一致)。

### TestModels 外层

- 入参 models 去空/去重后遍历;空 models → 返回空 slice。
- 每个 model 独立 30s 超时;handler 层给 5 分钟总超时兜底(N 个模型串行,最多 N×30s 上界,5min 足够常见场景)。如需并发可后续用 errgroup,列为可选(MVP 串行)。

## 4. 后端 handler 与路由

- `handlers/channel.go` 加 `testModels(c *gin.Context)`,挂 `POST /api/v1/channel/test-models`(JSON+Auth group,紧邻 `/fetch-model`)。
- 请求体 `struct{ChannelID int; Models []string}`;校验 + `op.ChannelGet` + 5min ctx + `relay.TestModels` + `resp.Success`。
- 参考 dev `testModels`(只取 channel_id 变体,去掉 dev 的多 key 逻辑)。

## 5. 前端

### API 层(`web/src/api/channel.ts`)

```ts
export type TestModelResult = { model: string; passed: boolean; error?: string; delay?: number };
export function useTestModels() {
    return useMutation({
        mutationFn: (data: { channel_id: number; models: string[] }) =>
            apiRequest<TestModelResult[]>('/api/v1/channel/test-models', { method: 'POST', body: data }),
    });
}
```
镜像 `useFetchModel`(同文件,无 invalidate,测试不改变渠道列表数据)。

### UI(`web/src/components/modules/channel/CardContent.tsx`,viewing tab)

- 模型集合:`split(channel.model) ∪ split(channel.custom_model)` 去重(复用 Card.tsx 的 splitModels 思路)。
- 「连通性测试」按钮(lucide 图标如 `Zap`/`Plug`),`disabled={测试中 || 无模型}`。
- 结果区:state 存 `TestModelResult[] | null`;测试中按钮 loading;完成后逐行 `模型名 + ✓/✗ + delayms + 错误`。用 ✓(CheckCircle2,emerald)/✗(XCircle,destructive)与现有图标体系一致。
- toast 兜底:`onSuccess` 汇总「N/M 通过」,详情看结果区;`onError` toast 报错。

### i18n

`web/src/locales/{en,zh_hans,zh_hant}.json` 的 `channel.detail` 下新增三语同步:
`testConnectivity`、`testPending`、`testPassed`、`testFailed`、`testDelay`(单位 ms)、`testNoModels`、`testSummary`(带计数插值)。

## 6. 集成点与跨子任务影响

- **不碰** setting.go/channel.go(group.go)/log.go:本任务纯新增(relay/test.go + handler route + 前端 + i18n)。与父 design §5 集成点表一致(③ 无 model 层字段改动)。
- **relay 包内新增文件**:与 ④(UA)/⑦(熔断)将来改 execution.go 不冲突(本任务不改 execution.go/forward.go/converted.go/passthrough.go,只新增 test.go + handler route)。
- **locales 三语同步**:按父 design §5 约定,三份一起加。
- **构建标签**:上游用 `-tags=jsoniter`(见 ② prd 的 build 命令);本地无 go,编译在 docker `golang:1.25` + `GOTOOLCHAIN=auto` 容器内跑(octopus-verify skill)。

## 7. 验证计划

- 后端:`go build -tags=jsoniter ./internal/... ./cmd/...` + `go test -tags=jsoniter ./internal/... ./cmd/...`(docker 容器内,见父 design §2 工具链)。
- 前端:`cd web && pnpm lint && pnpm build`。
- smoke:octopus-verify 全流程(容器内编译+测试+前端 build+起容器+健康检查+人看 UI),对可用/不可用渠道各测一次,前端结果正确。
- 验证 marker:octopus-verify 通过后写 `.octopus-verified`(父 memory [[octopus-deploy-skill-workflow]] 的硬关卡)。

## 8. 回滚

- 本任务纯新增文件 + 一条路由 + 前端 hook/按钮 + i18n key,无破坏性改动。
- 失败回滚 = revert 单个 commit(①② 不受影响)。

## 9. 风险

| 风险 | 触发 | 应对 |
|---|---|---| 
| `llm.Request`/`MessageContent` 字段在实现期与预期不符 | 编译失败 | 以 axonhub 源码(gh api 已 verify,见 §1)为准修正;dev test.go 是参考形状 |
| 某提供商 429 频繁导致测试误判 | 测免费层渠道 | 429 仍判 Passed(可达),与 dev 语义一致;Delay 供参考 |
| 测试串行 N 模型超 5min | 单渠道模型极多 | MVP 可接受;后续改 errgroup 并发(可选) |
| embedding-only 模型测出 ✗ | 发 chat 请求被拒 | 可达性已验证(✗ 附 4xx 错误);精准测试列范围外 |
