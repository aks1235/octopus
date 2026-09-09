# 移植:客户端识别+主题+思考等级留痕

## Goal

把 fork 三个用户可见功能移植到 dev-v2 新架构:日志的客户端识别(UA 解析+图标)、Claude 风格主题+选择器、日志思考等级留痕+展示。relay_logs 三列(`user_agent`/`client_name`/`reasoning_effort`)任务4 已建好(23 列形状冻结),本任务只写值,不改列、不动迁移。

## Background(fork 对照,ADR-0002)

| 功能 | fork commit | fork 形态 |
|---|---|---|
| 客户端 UA 识别 | `a6c2e4f` | `client_detect.go` 35+ 客户端规则表 + 词边界匹配 + 单测;日志行模型头像右下角叠加 ClientIcon |
| Claude 主题 | `e0270ff` | `data-theme="claude"` 赤陶色调色板(羊皮纸底 #f5f4ed + 赤陶 #c96442),设置页「风格」选择器(default/claude),zustand persist |
| 思考等级覆盖 | `c912f5e` `7a37d98` | Group 加 `reasoning_effort_override`(空=不覆盖);relay 层无条件覆盖(同时清 budget/adaptive);日志行思考等级 Badge;anthropic 预算映射 low 5000/medium 15000/high 32768/xhigh 65536/max 131072 |

## 已确认事实(2026-09-09 规划期核实,均带 file:line)

- v2 转发链没有 fork 的 `InternalLLMRequest` 单点:双路径 = 同协议直通(raw 原样)+ 跨协议 axonhub pipeline(`internal/relay/upstream.go:184` sendConverted,inbound→outbound)。
- handler 已有 raw body 改写先例(模型名 `internal/relay/handler.go:141` sjson、stream_options :149)——按客户端协议形状改 raw 一处可同时覆盖两条路径。
- `userAgent` 已留痕(handler.go:89),`client_name`/`reasoning_effort` 暂空(handler.go:398 注释:「客户端识别属本任务」)。
- axonhub(v0.0.0-20260901162339)原生支持 xhigh/max:anthropic inbound 校验 `oneof=xhigh max...`(axonhub `transformer/anthropic/inbound.go:92`,字段 `output_config.effort`),gemini/openai outbound 各自映射。**fork 7a37d98 的「对齐 xhigh/max」由 axonhub 白拿**;等级跨协议流转无需我们介入。
- v2 主题机制:`web/src/provider/theme.tsx` light/dark/system 切 `.dark` class + CSS 变量;默认调色板为暖底+绿色品牌色(`web/src/globals.css` primary oklch 0.62 0.12 145);无命名主题体系。fork 的 data-theme 方案可平移。
- v2 日志行模型头像锚点:`web/src/components/modules/log/Item.tsx:152` `getModelIcon(actualModel)`;历史区 `HistoryPanel.tsx` 同构。v2 无独立活跃请求面板(实时流即日志页,SSE 全生命周期)。
- v2 设置存储:`web/src/stores/setting.ts` zustand persist('octopus-settings',现仅 locale)。

## Key Decisions

- **D1(2026-09-09 用户决策)不移植「分组覆盖思考等级」**:fork 上该功能落地后 4 个月零后续(仅一个 Select 崩溃修复),使用痕迹弱;单人部署下等级在客户端可控(Claude Code MAX_THINKING_TOKENS 等),服务端强制覆盖是重复控制点;真实需求出现时纯增量补回(列已冻结、RelayConfig 加字段向后兼容)。ADR-0002 相应勘误。
- D2 保留「思考等级留痕+展示」(纯可观测性):从请求提取实际等级写 `relay_logs.reasoning_effort` + 日志行 Badge——fork 数据迁移会带历史值(任务4 演练 1285 条),不展示则永远不可见。
- D3 axonhub 已原生支持 xhigh/max 全链映射,跨协议 effort 对齐由库白拿;fork `7a37d98`/`c912f5e` 的 transformer 与覆盖逻辑不移植。

## Requirements

### R1 客户端识别
- 移植 fork `client_detect.go`(规则表+词边界匹配+单测)至 `internal/relay/client_detect.go`。
- `relayLogFinalize` 内解析 UA 填 `client_name`(不改列;`user_agent` 已有)。
- 前端日志行(实时流 Item + 历史区行)模型头像右下角叠加客户端图标组件(移植 ClientIcon)。

### R2 Claude 主题
- 新增 `data-theme="claude"` 调色板变体(含 light/dark 两套),fork 取值为基础、对齐 v2 现有 CSS 变量集。
- 设置页新增「风格」选择器(默认/Claude),zustand persist,默认「默认」(现有观感不变)。

### R3 思考等级留痕+展示
- 转发链从客户端请求 raw 提取实际思考等级(openai chat: `reasoning_effort`;responses: `reasoning.effort`;anthropic: `output_config.effort`,缺失时 `thinking.budget_tokens` 按 fork 阈值反推),`relayLogFinalize` 写 `relay_logs.reasoning_effort`。
- 日志行(实时 Item + 历史行)显示思考等级 Badge(Brain 图标 + 等级配色,含 xhigh),历史区同时覆盖 fork 迁移数据的存量值。
- 不改任何请求内容:等级透传与否完全由客户端与 axonhub 决定。

## Acceptance Criteria

- [ ] claude-code/cline 等客户端请求后,日志行显示对应客户端图标,`relay_logs.client_name` 落值(容器实测)。
- [ ] 设置页切到 Claude 风格立即生效且刷新/重开保持;默认风格无任何观感变化;light/dark 两模式与 Claude 风格正交组合正常。
- [ ] openai 形状请求自带 `reasoning_effort:"high"` → 日志记 high 且行内显示 Badge;anthropic 形状请求 `thinking.budget_tokens=32768` → 日志记 high;不带等级的请求该列为空且无 Badge(容器实测)。
- [ ] fork 迁移历史数据(带 reasoning_effort 值)在历史区正常显示 Badge(可用迁移演练库或手工造行验证)。
- [ ] octopus-verify 闭环通过。

## Out of Scope

- **分组覆盖思考等级**(Group 字段/编辑器 Select/卡片标识/handler 改写)——2026-09-09 用户决策不移植(D1);真实需求出现时纯增量补回。
- fork `a6c2e4f` 里活跃请求监控的 bug 修复(SSE/排序/timer)——v2 实时流为上游原生实现,无此缺陷面。
- 渠道级/凭据级 effort 映射配置(axonhub `ReasoningEffortMapping`/`ReasoningEffortToBudget` 渠道配置面)——仅内部使用,不暴露 UI。
- fork DESIGN.md 设计文档、多 URL/UTLS 等其余 fork 功能。
