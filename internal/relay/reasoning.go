package relay

import (
	"github.com/looplj/axonhub/llm"
	"github.com/tidwall/gjson"
)

// extractReasoningEffort 从客户端请求正文提取实际携带的思考等级, 供转发日志留痕。
// 只读不改: 等级如何流转(透传或跨协议映射)完全由客户端与 axonhub 决定, v2 转发链零介入。
// 三种客户端协议的请求形状:
//   - OpenAI Chat Completions: 顶层 reasoning_effort
//   - OpenAI Responses: reasoning.effort
//   - Anthropic Messages: output_config.effort; 客户端(如 claude-code)更多用 thinking.budget_tokens,
//     按阈值反推等级, 阈值与 fork c912f5e/7a37d98 的映射一致
func extractReasoningEffort(format llm.APIFormat, body []byte) string {
	switch format {
	case llm.APIFormatOpenAIResponse:
		if effort := gjson.GetBytes(body, "reasoning.effort"); effort.Exists() {
			return effort.String()
		}
	case llm.APIFormatAnthropicMessage:
		if effort := gjson.GetBytes(body, "output_config.effort"); effort.Exists() {
			return effort.String()
		}
		if budget := gjson.GetBytes(body, "thinking.budget_tokens"); budget.Exists() {
			return budgetToReasoningEffort(budget.Int())
		}
	default:
		if effort := gjson.GetBytes(body, "reasoning_effort"); effort.Exists() {
			return effort.String()
		}
	}
	return ""
}

// budgetToReasoningEffort 按 thinking 预算阈值反推思考等级, 阈值与 fork 保持一致。
func budgetToReasoningEffort(budgetTokens int64) string {
	switch {
	case budgetTokens <= 5000:
		return "low"
	case budgetTokens <= 15000:
		return "medium"
	case budgetTokens <= 32768:
		return "high"
	case budgetTokens <= 65536:
		return "xhigh"
	default:
		return "max"
	}
}
