package op

import (
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestChannelFetchModels_protocolBitsPerSide 锁定协议位的唯一判定口径:
// OpenAI 侧列出 → Chat Completions | Responses, Anthropic 侧列出 → Messages,
// 同名模型两侧都出现 → 按位或取并集, 顺序为先 OpenAI 后 Anthropic 且同名不重复。
// 手动拉取(handlers.fetchModel)与自动对账(task.keyFetchModels)共用本函数,
// 任何一侧口径被改回"只标 Response"都会在这里立刻变红。
func TestChannelFetchModels_protocolBitsPerSide(t *testing.T) {
	const (
		chatResp = model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse
		message  = model.ProtocolAnthropicMessage
	)
	cases := []struct {
		name      string
		openai    []string
		anthropic []string
		want      []model.ChannelFetchModel
	}{
		{
			name:   "OpenAI 侧同时标 Chat 与 Response",
			openai: []string{"gpt-x"},
			want:   []model.ChannelFetchModel{{Name: "gpt-x", Protocols: chatResp}},
		},
		{
			name:      "Anthropic 侧只标 Message",
			anthropic: []string{"claude-x"},
			want:      []model.ChannelFetchModel{{Name: "claude-x", Protocols: message}},
		},
		{
			name:      "同名模型两侧出现时协议位取并集",
			openai:    []string{"shared"},
			anthropic: []string{"shared"},
			want:      []model.ChannelFetchModel{{Name: "shared", Protocols: chatResp | message}},
		},
		{
			name:      "顺序先 OpenAI 后 Anthropic 且同名只出现一次",
			openai:    []string{"a", "b"},
			anthropic: []string{"b", "c"},
			want: []model.ChannelFetchModel{
				{Name: "a", Protocols: chatResp},
				{Name: "b", Protocols: chatResp | message},
				{Name: "c", Protocols: message},
			},
		},
		{
			name: "两侧皆空返回空清单",
			want: []model.ChannelFetchModel{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ChannelFetchModels(tc.openai, tc.anthropic)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ChannelFetchModels(%v, %v) = %+v, want %+v", tc.openai, tc.anthropic, got, tc.want)
			}
		})
	}
}
