# 设计:OpenAI Chat 业务终态与协议流结束分离

## 边界

| 文件 | 改动 |
|---|---|
| `internal/relay/protocol.go` | `streamEventParse` 增字段;`parseStreamEvent` 的 OpenAI Chat 分支增终态识别 |
| `internal/relay/handler.go` | 流式转发循环的收尾判定(约 329–366 行) |
| `internal/relay/stream_parse_test.go` 等 | 补分支用例 |

不触碰:Anthropic / Responses 两个分支、非流式路径、`sendConverted`、路由与统计。

## 核心概念:两个"结束"要分开

现状把两个语义压在一个 `last` 上,导致 OpenAI Chat 无法表达"业务已收尾,但流还没读完":

- **`last`(已有)** — 协议层流结束。为 true 时转发循环立即 `break`(`handler.go:358`)。
- **`completed`(新增)** — 业务终态已到达,但**流仍需继续读**。

对 Anthropic(`message_stop`)与 Responses(`response.completed`),终态事件**就是**流的最后一个事件,两者重合,故 `last` 足够。
**OpenAI Chat 是唯一不重合的协议**:`finish_reason` 之后还有 usage 分片(Octopus 主动开了 `include_usage`)和 `[DONE]`。

## 契约

```go
type streamEventParse struct {
    last        bool   // 不变: 该事件是否结束整个响应流
    completed   bool   // 新增: 上游业务终态已到达; 只影响收尾判定, 不结束转发
    err         error
    phase       string
    textLen     int
    thinkingLen int
}
```

`parseStreamEvent` 的 OpenAI Chat 分支(**必须置于 `Delta == nil` 检查之前** —— 部分上游的终态分片不带 `delta` 字段):

```go
if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason != "" {
    return streamEventParse{completed: true}
}
```

空串防御是必要的,且**有 upstream 前车之鉴**:`FinishReason` 是 `*string`,axonhub 自己在 `transformer/openai/outbound.go:346` 就做了专门的空串归一化,注释点名 Sensenova "在**每一个**流分片里下发 `finish_reason:\"\"`"。若不做防御,这类上游会被**每一片**都判成业务终态。

`handler.go` 循环:

```go
var completed bool
// ... parsed := parseStreamEvent(format, event); if parsed.completed { completed = true }
if !result.events.Next() {
    err = result.events.Err()
    if err != nil && completed {
        err = nil // 业务已正常收尾, 尾部读取失败不影响交付结果
    }
    break
}
```

## 权衡

- **选定:completed 与 last 分离。** 改动局部、不丢 usage、语义自解释。
- **否决:finish_reason 直接 `last: true`。** 会在 usage 分片之前 `break`,违反 R4 —— 等于用"少收一个分片"换掉一个错误,引入新缺陷。
- **否决:在 handler 里直接匹配 `event.Data` 里的 `finish_reason` 字符串。** 绕开 `parseStreamEvent` 既有的"每事件只解析一次"设计(见该函数注释 R4),且失去跨协议转换后的格式保证。

## 兼容性

| 场景 | 改动前 | 改动后 |
|---|---|---|
| 有 `[DONE]` | `[DONE]` 处 break,成功 | **不变** |
| 无 `[DONE]` + 干净关闭 | `err==nil`,成功 | **不变** |
| 无 `[DONE]` + 不干净关闭 + 有 `finish_reason` | `failed` / `unexpected EOF` | **`succeeded`** ← 修复目标 |
| 无 `[DONE]` + 不干净关闭 + 无 `finish_reason` | `failed` | **不变**(R3) |
| Anthropic / Responses | — | **不变**(分支未动) |

## 风险

- **误吞真中断**:若上游发了 `finish_reason` 之后连接才被掐,会被判成功。但 `finish_reason` 本身就是上游对"我已正常完成"的声明,Octopus 无从也无需质疑;这正是 R2 的意图。
- **畸形上游**:若上游在正常输出中途误发 `finish_reason`(如 `content_filter` 早停)后续又中断,同样判成功。该场景下上游确实宣告了终态,接受。
- 两者均属"信任上游终态声明"的合理代价,且比现状(信任 TCP 关闭方式)更贴近业务语义。

## 回滚

还原 `protocol.go` 与 `handler.go` 两处改动即可。无数据迁移、无配置项、无对外 API 契约变更、无数据库影响。
