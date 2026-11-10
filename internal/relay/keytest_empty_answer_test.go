package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
)

// 探测与转发对"空正文"的口径不同, 这里把差异钉死:
//   - 转发链(emptyAnswerReason): 命中输出上限/未完成/内容过滤都不算失败——客户端要长输出被截断是正常语义;
//   - 探测(emptyAnswerReasonWith(..., false)): 只要没吐出正文或工具调用就算没答出来——
//     探测问的是"这个成员能不能给出答案", 思考里空转到上限等于答不出, 标成可用就是假绿。

func TestEmptyAnswerReasonWith_probeStrictness(t *testing.T) {
	cases := []struct {
		name     string
		format   llm.APIFormat
		body     string
		tolerant bool // 转发链是否判定为空正文(容忍截断时为 false)
		strict   bool // 探测是否判定为空正文(不容忍截断)
	}{
		{
			name:     "chat 思考吃满上限, 正文空",
			format:   llm.APIFormatOpenAIChatCompletion,
			body:     `{"choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_content":"thinking..."},"finish_reason":"length"}]}`,
			tolerant: false,
			strict:   true,
		},
		{
			name:     "chat 正文空但正常结束",
			format:   llm.APIFormatOpenAIChatCompletion,
			body:     `{"choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
			tolerant: true,
			strict:   true,
		},
		{
			name:     "chat 有正文",
			format:   llm.APIFormatOpenAIChatCompletion,
			body:     `{"choices":[{"index":0,"message":{"role":"assistant","content":"2"},"finish_reason":"stop"}]}`,
			tolerant: false,
			strict:   false,
		},
		{
			name:     "chat 只有工具调用",
			format:   llm.APIFormatOpenAIChatCompletion,
			body:     `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			tolerant: false,
			strict:   false,
		},
		{
			name:     "anthropic 只有 thinking, 命中 max_tokens",
			format:   llm.APIFormatAnthropicMessage,
			body:     `{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"..."}],"stop_reason":"max_tokens"}`,
			tolerant: false,
			strict:   true,
		},
		{
			name:     "anthropic 有正文",
			format:   llm.APIFormatAnthropicMessage,
			body:     `{"type":"message","role":"assistant","content":[{"type":"text","text":"2"}],"stop_reason":"end_turn"}`,
			tolerant: false,
			strict:   false,
		},
		{
			name:     "responses 未完成且只有 reasoning",
			format:   llm.APIFormatOpenAIResponse,
			body:     `{"status":"incomplete","output":[{"type":"reasoning","summary":[]}]}`,
			tolerant: false,
			strict:   true,
		},
		{
			name:     "responses 有正文",
			format:   llm.APIFormatOpenAIResponse,
			body:     `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"2"}]}]}`,
			tolerant: false,
			strict:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emptyAnswerReasonWith(tc.format, []byte(tc.body), true); (got != "") != tc.tolerant {
				t.Fatalf("tolerant reason = %q, want empty=%v", got, tc.tolerant)
			}
			if got := emptyAnswerReasonWith(tc.format, []byte(tc.body), false); (got != "") != tc.strict {
				t.Fatalf("strict reason = %q, want empty=%v", got, tc.strict)
			}
		})
	}
}

// TestChannelKey_reasoningOnlyTruncatedIsFailure 端到端确认探测不把该形态标成可用(2026-11-10 实测真实存在)。
func TestChannelKey_reasoningOnlyTruncatedIsFailure(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m-test","choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_content":"thinking..."},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":4096,"total_tokens":4097}}`))
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-test"}, nil)
	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want failure for reasoning-only truncated response", results)
	}
	if !strings.Contains(results[0].Error, "no answer") {
		t.Fatalf("error %q must classify empty answer", results[0].Error)
	}
}
