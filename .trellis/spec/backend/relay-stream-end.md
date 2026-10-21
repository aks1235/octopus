# 流式结束判定契约

> 转发链如何判定一条流式响应已正常结束, 以及"业务终态"与"协议流结束"为何必须分开。2026-09-21 由缺陷修复
> `09-21-stream-end-tolerance` 沉淀: 此前 OpenAI Chat 只认 `data: [DONE]`, 不发该信号的上游即便响应内容
> 已完整交付也被判 `failed` / `unexpected EOF`(生产实例 `#1789977036413`、`#1789974660477`)。

---

## 契约:业务终态与协议流结束是两个信号

**What**: `streamEventParse`(`internal/relay/protocol.go`)有两个独立字段:

- **`last`** —— 协议流结束, 为 true 时转发循环**立即 break**(`internal/relay/handler.go`)。
- **`completed`** —— 上游业务终态已到达, 但**流仍需继续读**。

**Why**: OpenAI Chat 在 `finish_reason` 之后**还有 usage 分片**(Octopus 主动下发 `stream_options.include_usage=true`, 见 `handler.go` 的请求改写)与 `[DONE]`。若在 `finish_reason` 处直接置 `last`, usage 分片被丢弃, `output_tokens` 恒为 0 —— 等于用"少收一个分片"换掉原本要修的缺陷。

**三协议对照(勿"统一"它们)**:

| 协议 | 终态事件 | 是否流末事件 | 判定 |
|---|---|---|---|
| OpenAI Chat | `choices[0].finish_reason` | **否**(后随 usage 分片与 `[DONE]`) | `completed`, **不设** `last`; 仍以 `[DONE]` 收流 |
| Anthropic | `message_stop` | 是 | `last`(现状正确, 勿改) |
| OpenAI Responses | `response.completed` / `failed` / `incomplete` / `cancelled` | 是 | `last`(现状正确, 勿改) |

接入新协议时先问一句: **终态事件之后还有没有分片?** 有 → `completed`; 没有 → `last`。

---

## 契约:成败判定不依赖 TCP 如何关闭

**What**: 转发循环收尾时的读取错误, 在 `completed` 为真时被清空:

```go
if !result.events.Next() {
    err = result.events.Err()
    // 业务已正常收尾 (R2): 尾部读取失败不再计为请求失败。
    // 未见业务终态即中断仍按真中断报错 (R3)。
    if err != nil && completed {
        err = nil
    }
    break
}
```

**Why**: 部分上游(生产实例: 天翼云)发完 `finish_reason` 后**既不下发 `[DONE]` 也不正确终止流**。此时成败判定退化为"看 TCP 怎么关的":

- 干净关闭(正确终止 chunked / HTTP2 END_STREAM) → `io.EOF` → `err == nil` → 判成功
- 不干净关闭(缺终止帧 / 被 RST / 上游进程崩) → `io.ErrUnexpectedEOF` → 判失败

同一渠道 2183 次成功里, 判定依据完全不受 Octopus 控制。修正后依据是**业务是否收尾**。

**边界**: 未见终态即中断(模型输出中途被掐)必须**保持失败语义**, 且 `error` 保留原始读错误。

---

## finish_reason 判定的两条硬约束

### 约束一:必须早于 `delta` 空值检查

```go
// ✓ 正确: 部分上游的终态分片不带 delta 字段
if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason != "" {
    return streamEventParse{completed: true}
}
if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
    return streamEventParse{}
}
```

`{"choices":[{"index":0,"finish_reason":"stop"}]}` 这类分片没有 `delta` 键, `Delta` 为 nil。判定若放在空值检查之后, 这类上游识别不到。

### 约束二:空串防御不可省

`FinishReason` 是 `*string`。有上游(如 Sensenova)在**每一个**流分片里下发 `finish_reason:""`, 不防空串会把每一片都误判成业务终态。axonhub 自身在 `transformer/openai/outbound.go` 做了同样的空串归一化, 可作旁证。

---

## 契约:空正文(reasoning-only stop)按失败处理

**What**: `emptyAnswerReason`(`internal/relay/protocol.go`)按客户端协议判断一份完整响应是否
"既没有正文也没有工具调用"; 返回空串表示不算空回答。

- **非流式**: 判定注入在 `handler.go` 的 `if err != nil` 之前, 复用既有失败路径换下一个成员重试。
  空正文按"成员给出了完整但不可用的回答"处理, **立即冷却该成员**(传 `MemberMaxAttempts`), 不再消耗
  剩余尝试次数 —— 否则会为同一段不可用响应重复付出整段生成耗时。
- **流式**: 正文已经空着流给了客户端, `committed` 之后无法换成员; 只在聚合完成后判本轮失败并把成员
  打入冷却, 让后续请求自然避开。**正文保持空, 绝不把 `reasoning_content` 提升为 `content`。**

**Why**: 部分推理模型在上游以 `finish_reason=stop` 正常结束, 但只输出 `reasoning_content`、`content`
为空(社区称 reasoning-only stop; 直连 DeepSeek 官方接口同样会出现, 高推理档位下更常见)。此前这类响应
被判成功并原样交给客户端, 客户端拿到空白回复后**不会重试**。

反过来"把思考内容冒充正文"更糟: 客户端会以为已经答完, 连重试的机会都没有 —— 这是本条契约最关键的取舍,
**任何情况下都不得把 `reasoning_content` 提升为 `content`**。

**不算空回答的四类情形(勿误伤)**:

| 情形 | 判据 |
|---|---|
| 有工具调用 | chat `message.tool_calls` / anthropic `tool_use` 块 / responses 里非 `message` 且非 `reasoning` 的 `output` 项 |
| 命中长度上限 | chat `finish_reason=length` / anthropic `stop_reason=max_tokens` / responses `status=incomplete` |
| 内容被过滤 | chat `finish_reason=content_filter` |
| 拒答 | chat `message.refusal` 非空 / anthropic `stop_reason=refusal` |

多候选时只要有一个带正文即视为可用(`chat` 的 `content` 为分片数组时逐片取 `text`); 空响应体与错误响应
另行归类 —— `emptyAnswerReason` 对这两类返回空串, 不介入既有错误判定。

**已知边界**: 该判定只解决"空回复被静默当成功", 不改变上游为什么只思考不产出正文。流式一旦提交就无法
重试, 这是 `committed` 语义的直接后果; 若要让流式也能重试, 必须改成"延迟提交到首个正文事件", 代价是
思考内容不再实时显示 —— 属另一条独立取舍, 未采纳。

**Tests Required**:

| 测试 | 层级 | 断言点 |
|---|---|---|
| `TestEmptyAnswerReason` | 单元(表驱动) | 三协议的空正文/有正文/工具调用/长度上限/内容过滤/拒答各一档, 另含分片数组与空响应体 |
| `TestForward_nonStreamingEmptyAnswerRetriesNextMember` | handler 级 | 成员 1 返回空正文 → 记失败并立即冷却, 换成员 2 成功; 响应体来自成员 2 |
| `TestForward_streamingEmptyAnswerRecordedAsFailure` | handler 级 | 流式空正文 → `status=failed`、`error` 含 reasoning-only stop、`RequestFailed=1`、该轮尝试记失败 |

**mutation 验证**: 把 `emptyAnswerReason` 里三处 `return emptyAnswerMessage` 改成 `return ""`
→ 5 个单元用例与 2 个 handler 用例同时变红(实测已验)。

---

## Tests Required

落在 `internal/relay/stream_end_test.go`(独立新文件, 勿并入 `stream_parse_test.go`, 以降低冲突面):

| 测试 | 层级 | 断言点 |
|---|---|---|
| `TestParseStreamEventCompletedDetection` | 单元(表驱动) | `finish_reason` → `completed && !last`; 空串/null → 不误判; 无 `delta` 字段 → 识别; Anthropic/Responses → 不产生 `completed` |
| `TestForward_upstreamFinishReasonWithoutDoneStillSucceeds` | handler 级 | 假上游发 `finish_reason` + usage、**不发 `[DONE]`**、以 `Content-Length` 声明大于实发制造不干净关闭 → `error == ""`、`RequestSuccess == 1`、**`OutputTokens != 0`** |
| `TestForward_upstreamInterruptWithoutFinishReasonStillFails` | handler 级 | 同场景但不发 `finish_reason` → `error` 含 `EOF`、`RequestFailed == 1`(守修复反侧) |

**断言必须能判别, 用 mutation 验证**(本项目要求, 只跑通不代表测到了):

- 还原 handler 收尾修复 → 集成用例应报 `error = "unexpected EOF"`
- 给终态分片加 `last: true` → `OutputTokens` 应归零(证明 aggregator **不会**自行估算 usage, 该断言非空转)
- 判定挪到 `delta` 空值检查之后 → 无 `delta` 字段的用例应失败
- 去掉空串防御 → `finish_reason:""` 用例应失败

---

## Wrong vs Correct

### Wrong:把 `finish_reason` 当作流结束

```go
if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
    return streamEventParse{last: true}  // ✗ 循环立刻 break, usage 分片丢失
}
```

后果: `output_tokens` 恒为 0, 计费与统计失真。

### Correct:只标记业务终态, 让流自然读完

```go
if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason != "" {
    return streamEventParse{completed: true}  // ✓ usage 分片随后被正常收取
}
```

---

## 已失效的排障手段

修复后此类请求不再记 `failed`, 因此 **`unexpected EOF` 不再出现在 `relay_logs.error`**。
据该错误反查"哪些渠道不发 `[DONE]`"已不可行, 需改为直接抓上游流末帧。

另: 若某分片**同时**带 `finish_reason` 与正文增量, 其字符数不计入实时速度(`completed` 判定早于相位提取)。仅影响速度显示, 不影响转发内容与用量, 属已知取舍。

## 构建注意

宿主机无 Go 工具链, 测试须在容器内跑:

```bash
docker run --rm -v /home/yuan/docker/octopus-new:/app -v /home/yuan/go/pkg/mod:/go/pkg/mod \
  -w /app -e CGO_ENABLED=0 golang:1.26.4 go test ./internal/relay/...
```

`go build ./...` 在该挂载下需加 `-buildvcs=false`(容器内以 root 访问宿主 git 目录会报 VCS status 错)。
