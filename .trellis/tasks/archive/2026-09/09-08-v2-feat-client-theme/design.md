# 设计:客户端识别+主题+思考等级留痕

## 总体结构

三个子功能相互独立,仅共享 relay_logs 落库(relayLogFinalize)与日志行 UI 两个汇合点。全部改动无 DB 迁移、无 API 破坏:relay_logs 三列已存在,只写值;不改任何请求内容(等级透传由客户端与 axonhub 决定)。

实施顺序:R1 后端 → R3 后端(同在 finalize 汇合)→(容器快验)→ 前端 R1+R3 → 前端 R2 → octopus-verify 全闭环。

## R3 思考等级留痕(后端)

`internal/relay/reasoning.go`(新文件),只读不改请求:

```go
func extractReasoningEffort(format llm.APIFormat, body []byte) string
// OpenAIChatCompletion → gjson "reasoning_effort"
// OpenAIResponse     → gjson "reasoning.effort"
// AnthropicMessage   → gjson "output_config.effort", 缺失时 "thinking.budget_tokens" 按阈值反推
//                      (≤5000 low / ≤15000 medium / ≤32768 high / ≤65536 xhigh / 其余 max, 同 fork)
// 三协议均未携带 → 空串(非推理请求)
```

### handler 接线(internal/relay/handler.go Forward)

- 循环前提取一次:`reasoningEffort := extractReasoningEffort(format, raw.Body)`(raw 每轮只改 model/stream_options,等级字段不动,单次提取即全轮有效)。
- `relayLogFinalize` 增加 `reasoningEffort` 参数,写入 `relayLog.ReasoningEffort`。

### 测试

- `internal/relay/reasoning_test.go`:extract 三协议矩阵 + anthropic 预算阈值边界(5000/15000/32768/65536/131073)+ 空串路径。
- `log_finalize_test.go` 扩展:等级字段落库断言。

## R1 客户端识别(后端)

- 移植 fork `client_detect.go`(35+ 客户端规则表、词边界匹配)至 `internal/relay/client_detect.go`,规则表原样保留;移植 `client_detect_test.go`。参考原文:research/fork_client_detect.go。
- `relayLogFinalize`:`ClientName: detectClient(userAgent)`(userAgent 参数已有)。

## 前端(R1+R3)

### api 类型

log 类型补 `user_agent/client_name/reasoning_effort`(若缺)。

### 日志行(web/src/components/modules/log/)

- `ClientIcon.tsx`(新,移植 research/fork_ClientIcon.tsx):client → lucide 图标+品牌色+i18n label。
  - `Item.tsx` 两处模型头像(LogDetail:152、LogCardBody:405)右下角叠加(absolute 定位,同 fork 形态)。
  - `HistoryPanel.tsx` HistoryCard 行内模型名旁小图标(该行无头像锚点)。
- 思考等级 Badge:`Item.tsx` 行 + HistoryCard:Brain 图标+等级原文,配色 low #6b7280 / medium #f59e0b / high #8b5cf6 / xhigh #e11d48 / max #dc2626(同 fork 7a37d98)。历史区 Badge 同时展示 fork 迁移存量值。

### locales

`web/src/locales/{zh_hans,zh_hant,en}.json`:`log.clientNames.*`(35+ 键)、`setting.themeStyle.*`。

## R2 Claude 主题(前端)

- `web/src/stores/setting.ts`:加 `themeStyle: 'default' | 'claude'`(persist,默认 default)。
- `web/src/provider/theme.tsx`:effect 内 `document.documentElement.dataset.theme = themeStyle === 'claude' ? 'claude' : ''`(订阅 zustand store)。
- `web/src/globals.css`:`[data-theme='claude']`(light)与 `html.dark[data-theme='claude']`(dark)两套完整变量集(含 chart/sidebar/shadow),取值以 fork research/fork_globals_css_after_theme.diff.txt 为基础补齐 v2 变量清单;仅 claude 变体改 radius/阴影观感,默认主题零改动。
- 设置页「风格」Select(默认/Claude),紧邻既有「模式」选择器。

## 兼容与回滚

- 无迁移、无列变更、无请求改写;前端 persist 旧值无 themeStyle 字段→zustand 合并默认 default。
- 单提交实现,回滚=revert;`relay-log-persistence.md` 契约(列形状冻结、finalize 挂钩模式、queryKey 纪律)全程遵守。

## 权衡记录

- **不移植分组覆盖**(2026-09-09 用户决策):fork 使用痕迹弱(落地后 4 个月零后续)、单人部署客户端可控等级、服务端覆盖是重复控制点;真实需求出现时纯增量补回(列已冻结、RelayConfig 加字段向后兼容)。ADR-0002 出勘误。
- 跨协议 effort 流转完全交给 axonhub 原生映射,不在 v2 层对齐 fork 数值:跨协议本就有损,库行为即权威。
