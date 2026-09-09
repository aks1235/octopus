package relay

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
)

// TestExtractReasoningEffort 覆盖三种客户端协议的等级提取与 anthropic 预算阈值反推。
func TestExtractReasoningEffort(t *testing.T) {
	tests := []struct {
		name   string
		format llm.APIFormat
		body   string
		want   string
	}{
		// OpenAI Chat Completions: 顶层 reasoning_effort
		{"openai chat effort", llm.APIFormatOpenAIChatCompletion, `{"model":"gpt","reasoning_effort":"high"}`, "high"},
		{"openai chat none", llm.APIFormatOpenAIChatCompletion, `{"model":"gpt"}`, ""},

		// OpenAI Responses: reasoning.effort
		{"responses effort", llm.APIFormatOpenAIResponse, `{"model":"gpt","reasoning":{"effort":"medium"}}`, "medium"},
		{"responses reasoning without effort", llm.APIFormatOpenAIResponse, `{"model":"gpt","reasoning":{"summary":"auto"}}`, ""},
		{"responses none", llm.APIFormatOpenAIResponse, `{"model":"gpt"}`, ""},

		// Anthropic Messages: output_config.effort 优先
		{"anthropic output_config", llm.APIFormatAnthropicMessage, `{"model":"claude","output_config":{"effort":"max"}}`, "max"},
		// thinking.budget_tokens 按阈值反推, 边界与 fork 映射一致
		{"anthropic budget 5000", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":5000}}`, "low"},
		{"anthropic budget 15000", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":15000}}`, "medium"},
		{"anthropic budget 32768", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":32768}}`, "high"},
		{"anthropic budget 65536", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":65536}}`, "xhigh"},
		{"anthropic budget 131073", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":131073}}`, "max"},
		{"anthropic budget 0", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"enabled","budget_tokens":0}}`, "low"},
		{"anthropic none", llm.APIFormatAnthropicMessage, `{"model":"claude"}`, ""},
		{"anthropic thinking disabled", llm.APIFormatAnthropicMessage, `{"model":"claude","thinking":{"type":"disabled"}}`, ""},
	}

	for _, tt := range tests {
		if got := extractReasoningEffort(tt.format, json.RawMessage(tt.body)); got != tt.want {
			t.Errorf("%s: extractReasoningEffort() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
