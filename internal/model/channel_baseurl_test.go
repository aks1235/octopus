package model

import "testing"

// TestBaseURLFor 验证分协议地址的回落规则: 未填专用地址时沿用默认地址, 老渠道行为不变。
func TestBaseURLFor(t *testing.T) {
	shared := ChannelConfig{BaseURL: "https://shared.example"}
	if got := shared.BaseURLFor(ProtocolOpenAIChatCompletion); got != "https://shared.example" {
		t.Fatalf("OpenAI 侧 = %q, 期望沿用默认地址", got)
	}
	if got := shared.BaseURLFor(ProtocolAnthropicMessage); got != "https://shared.example" {
		t.Fatalf("Anthropic 侧 = %q, 期望沿用默认地址", got)
	}

	split := ChannelConfig{
		BaseURL:          "https://shared.example",
		OpenAIBaseURL:    "https://openai.example/v3",
		AnthropicBaseURL: "https://anthropic.example",
	}
	if got := split.BaseURLFor(ProtocolOpenAIResponse); got != "https://openai.example/v3" {
		t.Fatalf("OpenAI 侧 = %q, 期望专用地址", got)
	}
	if got := split.BaseURLFor(ProtocolOpenAIChatCompletion); got != "https://openai.example/v3" {
		t.Fatalf("Chat 侧 = %q, 期望专用地址", got)
	}
	if got := split.BaseURLFor(ProtocolAnthropicMessage); got != "https://anthropic.example" {
		t.Fatalf("Anthropic 侧 = %q, 期望专用地址", got)
	}

	onlyAnthropic := ChannelConfig{BaseURL: "https://shared.example", AnthropicBaseURL: "https://anthropic.example"}
	if got := onlyAnthropic.BaseURLFor(ProtocolOpenAIChatCompletion); got != "https://shared.example" {
		t.Fatalf("只填 Anthropic 时 OpenAI 侧 = %q, 期望默认地址", got)
	}
}
