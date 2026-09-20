# 实时日志详情卡顿:两档模式抽共享组件

## Goal

实时日志详情弹窗在 agent 类请求(消息条数多)下打开卡顿——与历史日志此前的卡顿同源,但当时的「简洁/调试两档」修复只覆盖了历史弹窗(`RequestDetailDialog.tsx`),实时详情(`Item.tsx LogDetail`)仍是全量渲染。把两档内容视图抽成**共享组件**供两处复用,并让已展开的大树不随 SSE 心跳重建。

## Background(2026-09-20 用户反馈,已在代码核实)

- 实时详情:`LogDetail`(web/src/components/modules/log/Item.tsx:157)按需拉 request-body,交给 `JsonContent`(:109)渲染;`JsonContent` 用 `<JsonView collapsed={false}>`(:140)**全量展开**,几千条 messages → 数万 DOM 节点,打开即卡。
- 实时列表由 SSE 驱动,每次心跳 log 对象更新 → 弹窗父组件重渲染;大 JSON 树若未 memo 会反复重建,加剧卡顿(JSON.parse 已 useMemo,但视图树没有)。
- 历史侧已有实现:`RequestDetailDialog.tsx` 的 `lastMessageOf`/`SimpleRequestBody`(简洁档=最后一条用户输入+消息总数+大小摘要,「查看请求体」才渲染全量,收起即卸载)。

## Requirements

- R1 **抽共享组件**:把「两档内容视图」抽为共享模块(如 `web/src/components/modules/log/ContentBody.tsx`),导出:简洁档组件(末条用户消息+总数+大小摘要+「查看请求体」按钮)、全量档(JsonView 折叠视图);历史弹窗改为复用它(行为不变)。
- R2 **实时接入**:实时详情弹窗的请求体区改用共享组件,默认简洁档;响应区同样受用(响应体在流式中可能持续增长——见 R4)。
- R3 **防心跳重建**:全量档的 JSONView 子树用 `React.memo` + 内容字符串比较包裹;SSE 心跳只更新状态字段时不重建已展开的大树。收起即卸载(与历史一致)。
- R4 **流式增长**:实时请求的响应内容会随流增长;简洁档与全量档都不得因每帧追加而反复重建整棵树(例如全量档展开后按需刷新、或对未展开状态零解析)。具体策略取实现中最简可行,PRD 只约束「展开长流不产生每帧全量重建」。
- R5 **零契约变更**:不动后端、不动 API;历史弹窗**请求侧**行为与文案不回归;响应侧有意改为惰性(见下);i18n 三语在既有键基础上新增响应向键(`responseBodySummary`/`viewResponseBody`/`hideResponseBody`)。
- R6 **响应侧惰性(2026-09-20 主会话决定)**:响应体同样默认简洁档(大小摘要+「查看响应体」),点开才渲染全量、收起即卸载——响应聚合后也可能很大,与请求侧策略一致才彻底解决打开卡顿;`mode='response'` 时零 `JSON.parse`。影响面含复用同一弹窗的「渠道调用详情」。

## Non-goals

- 不做虚拟滚动/按消息分页(如确有必要可在实现中评估,但非本任务目标)。
- 不改实时列表卡片本身的渲染。
- 不动响应内容的解析与展示口径(只改渲染时机/惰性)。

## Acceptance Criteria

- [ ] AC1 实时详情打开多消息 agent 请求:默认简洁档,打开**不卡**(与历史侧同观感);点「查看请求体」才渲染全量
- [ ] AC2 历史弹窗请求侧零回归(文案/默认档/JsonView 参数逐项一致);响应侧按 R6 惰性化属有意变更
- [ ] AC3 SSE 心跳期间(请求进行中),已展开的全量视图不重建(可用 React DevTools Profiler 或代码层 memo 断言佐证)
- [ ] AC4 流式响应持续增长时不产生每帧全量重建
- [ ] AC5 tsc/eslint/build 通过;octopus-verify 通过(UI 人审:agent 长请求实时点开流畅、展开全量正常、心跳期间不闪不卡)

## Notes

- 纯前端任务,与进行中的后端任务(`09-20-log-attempts-perf`)无文件重叠,可并行。
- 共享组件落点建议 `log/ContentBody.tsx`;历史侧 `RequestDetailDialog.tsx` 内的 `lastMessageOf`/`SimpleRequestBody`/`formatSizeBytes` 一并迁出复用,避免两份实现漂移。
