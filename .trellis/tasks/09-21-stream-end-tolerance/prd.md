# 流式结束判定:兼容不发 [DONE] 收尾的上游

## Goal

修复**流式响应在部分上游下被误判为失败**的缺陷。证据来自生产日志 `#1789977036413` 与 `#1789974660477`(2026-09-21):
两条请求的响应内容均已完整送达(其中 `#1789974660477` 正文与思考都完整、`finish_reason=stop`、报告 1323 字符全在),
但请求终态被记为 `failed`,`error=unexpected EOF`,且 `output_tokens=0`。

**结果错误是硬判据**:内容 100% 送达的请求被标失败,不是"偶发网络问题"能解释的。

## Background(已实测确认)

- 生产库只读查询(`data/data.db`):近 2 天 `unexpected EOF` 共 2 条;同渠道(120 天翼云)累计 `success=2183 / failed=341`,故**非必现**。
- **收不到 [DONE] 是逻辑必然**:`handler.go` 流式循环为 `if last { break }`,只要收到 `[DONE]` 就于当轮退出,不可能走到读 EOF 那一步。因此这两条报 `unexpected EOF` 的请求,**必然没收到 `[DONE]`**。
- 上游不发 `[DONE]` 时,Octopus 会持续读取直到连接关闭,此时成败判定**退化为"看 TCP 如何关闭"**:
  - 干净关闭(正确终止 chunked / HTTP2 END_STREAM) → `io.EOF` → `err==nil` → `markSucceeded`(2183 次属此类,歪打正着)
  - 不干净关闭(缺终止帧 / 被 RST / 上游进程崩) → `io.ErrUnexpectedEOF` → `err!=nil` → `markFailed`(本次 2 条)
- **协议对齐缺口**(已逐行核对 `internal/relay/protocol.go`):
  - Anthropic:`message_stop` → `last`,且它是流的**最后一个**事件 → 无缺口
  - OpenAI Responses:`response.completed/failed/incomplete/cancelled` → `last`,同样是最后事件 → 无缺口
  - OpenAI Chat:**只认 `data: [DONE]`**;`choices[0].finish_reason` 从未被检查。而 OpenAI Chat 在 `finish_reason` 之后**还有 usage 分片**(Octopus 主动下发 `stream_options.include_usage=true`,见 `handler.go:156`)与 `[DONE]` → **缺口仅在此协议**
- 客户端为 `Agents/Python 0.18.0`,两条均为长任务(研究/报告类)。

## Requirements

- **R1 OpenAI Chat 增补业务终态识别**:收到携带**非空** `choices[0].finish_reason` 的分片时,标记该流已达业务终态。
  - 必须区分**"业务终态已见"**与**"协议流结束"**两个概念:`finish_reason` 之后仍有 usage 分片需收取,**不得**在该分片处立即结束转发循环,否则丢失用量(R4)。
- **R2 尾部读取失败不再误判**:转发循环收尾时的读取错误,若此刻**已见过业务终态**,则不计为请求失败。
  - 判定依据从"TCP 怎样关闭"改为"业务是否已正常收尾"。
- **R3 真中断仍须报错**:未见过业务终态即中断的流(如模型输出中途被掐),必须保持现有失败语义,不得被 R2 放过。
- **R4 用量不丢**:`finish_reason` 之后的 usage 分片仍须进入聚合,使 `output_tokens` 等统计正确(现状恒为 0)。
- **R5 零回归**:不改 Anthropic 与 OpenAI Responses 的既有结束判定(已正确);不改非流式路径;不改跨协议转换(`sendConverted`)的事件语义;不改路由/冷却/统计口径。

## Non-goals

- 不修"模型只输出思考、不输出正文"—— 那是上游模型行为(见 Notes)。
- 不在本次补发"流中断时给客户端的错误事件",即客户端静默断流的体验问题;它独立于本缺陷,另开任务。
- 不引入首帧后的空闲超时/总时长上限。
- 不改渠道失败统计口径与路由/冷却逻辑。

## Acceptance Criteria

- [ ] **AC1** 上游发 `finish_reason` 但不发 `[DONE]`、且连接不干净关闭时,请求终态为 `succeeded`,`error` 为空
- [ ] **AC2** AC1 场景下 `output_tokens` 与历史正常请求同量级(不再恒为 0)
- [ ] **AC3** 上游发 `finish_reason` 且正常发 `[DONE]` 时,行为与现状**完全一致**(含 usage 收取)
- [ ] **AC4** 未见 `finish_reason` 即中断的流,仍记为 `failed` 且 `error` 保留原始读错误
- [ ] **AC5** Anthropic / OpenAI Responses 两协议结束判定零回归
- [ ] **AC6** 单元测试覆盖:①无 `[DONE]` + 不干净 EOF + 有 `finish_reason` → 成功;②同场景无 `finish_reason` → 失败;③`finish_reason` 后的 usage 分片不丢
- [ ] **AC7** octopus-verify 通过

## Notes

- 请求 `#1789977036413` 的"只看到思考、看不到正文"另有独立原因:该响应 `response_content` 中 `"content"` 出现 **0 次**,仅 `reasoning_content` 84441 字符且呈**退化重复**("我们可以在阶段2设计一个意图搜索…"循环数十遍)。**模型从未产出正文**,属上游模型行为,不在本任务范围。
- 验证手段:单测直接构造事件序列;端到端可用不发 `[DONE]` 的假上游。
- 待确认(不阻塞):本次两条的不干净关闭,究竟是"上游未发 chunked 终止帧"还是"连接被中途掐断",日志无法区分(`debug_content` 为空)。修复对两种成因均有效。
