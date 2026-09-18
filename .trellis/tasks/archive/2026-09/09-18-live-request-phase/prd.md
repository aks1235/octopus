# 实时请求相位:思考中 / 输出中

## Goal

实时请求列表目前只能看到「已提交、流式传输中」,无法区分模型是在**思考**(reasoning 增量)还是在**输出正文**。流式事件的类型本就区分两者,把相位记进请求状态、只在切换时推送,前端徽标显示「思考中… / 输出中…」。

## Background(2026-09-18 用户提出)

- `RequestState`(`internal/relay/state.go`)已有 Status(running/committed/…),committed 是首帧写出后的状态,覆盖"思考"与"输出"两段。
- 流式循环(handler.go)逐事件转发,事件处于**客户端协议**(同协议透传或经转换后),三种协议都能从事件内容区分:
  - OpenAI Chat:`choices[0].delta.reasoning_content`(思考) vs `.content`(正文)
  - Anthropic:`content_block_delta` 的 `delta.type` = `thinking_delta` vs `text_delta`
  - OpenAI Responses:reasoning 类事件 vs `response.output_text.delta`
- 状态经 `publishRequestLocked` 推 SSE;`markCommitted` 是现成的"状态变更即推送"范式。

## Requirements

- R1 `RequestState` 加 `Phase`(`json:"phase,omitempty"`,`"" / "thinking" / "answering"`);仅流式有值。
- R2 分类器按**客户端协议**识别事件相位;识别不出(解析失败、非增量事件)返回空、不改相位。
- R3 只在相位**变化**时更新并推送(每请求至多两次:进 thinking、进 answering);不进热路径的双重解析——复用既有 inspectStreamEvent 的解析或仅在未定相位前分类。
- R4 首次正文增量即定 answering;此后不再分类(省开销)。
- R5 前端实时卡片:status=committed 时按 phase 显示「思考中… / 输出中…」(无 phase 保持现状文案);i18n 三语。
- R6 非流式、manual、跨协议转换路径行为不变;相位不影响路由/统计/日志。

## Non-goals

- 不显示思考内容本身(只显示相位;内容仍由既有的响应内容展示)。
- 不做相位时序/耗时统计。
- 不改 Status 取值(相位是 Status 之上的正交信息)。

## Acceptance Criteria

- [ ] AC1 三种协议各自的事件序列(思考增量→正文增量)分别使 phase 依次为 thinking→answering
- [ ] AC2 纯正文流(无思考)直接进 answering;无增量事件(如仅 ping/usage)不改相位
- [ ] AC3 推送次数:整条请求至多两次相位推送(断言 publish 次数或等价行为)
- [ ] AC4 解析失败的事件不 panic 且不改相位
- [ ] AC5 前端徽标三语,committed+thinking/answering 显示对应文案;非流式不变
- [ ] AC6 octopus-verify 通过(UI 人审:长思考请求显示「思考中…」,转正文后变「输出中…」)

## Notes

- 轻量任务,PRD-only。改动面:relay/state.go(字段+setter)、relay/protocol.go(分类器)、relay/handler.go(循环内调用)、前端实时卡片徽标、i18n、单测。
- 分类成本:仅在该请求尚未定 answering 时执行,单请求最多两段解析。
