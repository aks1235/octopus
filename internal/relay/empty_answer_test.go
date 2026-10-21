package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// TestEmptyAnswerReason 守住 reasoning-only stop 的判定口径: 只有"既无正文又无工具调用,
// 且不是长度上限/内容过滤/拒答"才算空回答; 三个客户端协议的正文与工具调用位置各不相同, 逐个覆盖。
func TestEmptyAnswerReason(t *testing.T) {
	cases := []struct {
		name   string
		format llm.APIFormat
		body   string
		want   string
	}{
		{
			name:   "chat 只有 reasoning 没有 content",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"thinking"},"finish_reason":"stop"}]}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "chat content 为空串",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_content":"t"},"finish_reason":"stop"}]}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "chat 有正文",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`,
			want:   "",
		},
		{
			name:   "chat 只有空白正文仍算空",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":"  \n "},"finish_reason":"stop"}]}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "chat content 为分片数组且有文本",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"finish_reason":"stop"}]}`,
			want:   "",
		},
		{
			name:   "chat content 为分片数组但无文本",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":[]},"finish_reason":"stop"}]}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "chat 有工具调用",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			want:   "",
		},
		{
			name:   "chat 命中长度上限",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"t"},"finish_reason":"length"}]}`,
			want:   "",
		},
		{
			name:   "chat 命中内容过滤",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"content_filter"}]}`,
			want:   "",
		},
		{
			name:   "chat 拒答",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"cannot help"},"finish_reason":"stop"}]}`,
			want:   "",
		},
		{
			name:   "chat 多候选中有一个有正文",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"choices":[{"index":0,"message":{"role":"assistant"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`,
			want:   "",
		},
		{
			name:   "chat 错误响应不在这里归类",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   `{"error":{"message":"boom"}}`,
			want:   "",
		},
		{
			name:   "anthropic 只有 thinking 块",
			format: llm.APIFormatAnthropicMessage,
			body:   `{"id":"m","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"t"}],"stop_reason":"end_turn"}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "anthropic 有 text 块",
			format: llm.APIFormatAnthropicMessage,
			body:   `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`,
			want:   "",
		},
		{
			name:   "anthropic 有 tool_use",
			format: llm.APIFormatAnthropicMessage,
			body:   `{"id":"m","type":"message","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}],"stop_reason":"tool_use"}`,
			want:   "",
		},
		{
			name:   "anthropic stop_reason 为 max_tokens",
			format: llm.APIFormatAnthropicMessage,
			body:   `{"id":"m","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"t"}],"stop_reason":"max_tokens"}`,
			want:   "",
		},
		{
			name:   "responses 只有 reasoning 项",
			format: llm.APIFormatOpenAIResponse,
			body:   `{"id":"r","object":"response","status":"completed","output":[{"type":"reasoning","summary":[]}]}`,
			want:   emptyAnswerMessage,
		},
		{
			name:   "responses 有 output_text",
			format: llm.APIFormatOpenAIResponse,
			body:   `{"id":"r","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`,
			want:   "",
		},
		{
			name:   "responses 有函数调用项",
			format: llm.APIFormatOpenAIResponse,
			body:   `{"id":"r","object":"response","status":"completed","output":[{"type":"function_call","name":"f","arguments":"{}"}]}`,
			want:   "",
		},
		{
			name:   "responses 未完成",
			format: llm.APIFormatOpenAIResponse,
			body:   `{"id":"r","object":"response","status":"incomplete","output":[{"type":"reasoning"}]}`,
			want:   "",
		},
		{
			name:   "空响应体",
			format: llm.APIFormatOpenAIChatCompletion,
			body:   "",
			want:   "upstream returned empty response body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emptyAnswerReason(tc.format, []byte(tc.body)); got != tc.want {
				t.Errorf("emptyAnswerReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

// seedEmptyAnswerGroup 建「1 个渠道 + 2 个成员」的分组: 优先级 1 的成员按凭据返回空正文,
// 优先级 2 的成员返回正常正文, 用于验证空正文是否会让出并换下一个成员。
func seedEmptyAnswerGroup(t *testing.T, groupID int, name, baseURL string) model.Group {
	t.Helper()
	if err := db.GetDB().Create(&model.Channel{
		ID: 1,
		ChannelConfig: model.ChannelConfig{
			Name:                     "empty-answer-channel",
			BaseURL:                  baseURL,
			OpenAIChatCompletionPath: "/v1/chat/completions",
		},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return seedCursorGroup(t, groupID, name, model.GroupModeFailover, seedCursorGrants(t, 1, 1, 2))
}

// serveForward 以假上游跑一次 handler 级转发, stream 决定客户端是否走流式。
func serveForward(t *testing.T, groupID int, groupName string, stream bool, upstream http.HandlerFunc) ([]model.RelayLog, model.Channel) {
	t.Helper()

	server := httptest.NewServer(upstream)
	defer server.Close()

	group := seedEmptyAnswerGroup(t, groupID, groupName, server.URL)
	ResetRouteState(group.ID)

	body := `{"model":"` + groupName + `","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	if stream {
		body = `{"model":"` + groupName + `","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/relay", Forward(llm.APIFormatOpenAIChatCompletion))

	done := make(chan struct{})
	go func() {
		defer close(done)
		request := httptest.NewRequest(http.MethodPost, "/relay", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), request)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("forward handler did not finish in time")
	}

	channel, err := op.ChannelGet(1)
	if err != nil {
		t.Fatalf("ChannelGet(1): %v", err)
	}
	return flushAndLoad(t), channel
}

// TestForward_nonStreamingEmptyAnswerRetriesNextMember 守住非流式的兜底:
// 优先级 1 的成员返回「只有 reasoning、没有 content」的完整响应时, 本轮按失败处理并换优先级 2 的成员重试,
// 最终请求成功、正文来自第二个成员 —— 空回复不再被当成功交给客户端。
func TestForward_nonStreamingEmptyAnswerRetriesNextMember(t *testing.T) {
	setupRelayLogTest(t)

	upstream := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "Bearer sk-1" {
			_, _ = w.Write([]byte(`{"id":"a","object":"chat.completion","model":"model-1","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"thinking a lot"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"b","object":"chat.completion","model":"model-2","choices":[{"index":0,"message":{"role":"assistant","content":"real answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}

	logs, channel := serveForward(t, 311, "empty-answer-nonstream-g", false, upstream)

	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	log := logs[0]
	if log.Status != string(StatusSuccess) {
		t.Fatalf("status = %q, want %q (应换成员后成功)", log.Status, StatusSuccess)
	}
	// 第一个成员那一轮必须被记成失败并带上兜底原因, 否则冷却与统计都不会生效。
	if len(log.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (空正文一轮 + 成功一轮)", len(log.Attempts))
	}
	if log.Attempts[0].Status != model.AttemptFailed || !strings.Contains(log.Attempts[0].Msg, "reasoning-only stop") {
		t.Errorf("attempt[0] = %+v, want failed with reasoning-only stop", log.Attempts[0])
	}
	if log.Attempts[1].Status != model.AttemptSuccess {
		t.Errorf("attempt[1] = %+v, want success", log.Attempts[1])
	}
	if channel.RequestFailed != 1 || channel.RequestSuccess != 1 {
		t.Errorf("channel success/failed = %d/%d, want 1/1", channel.RequestSuccess, channel.RequestFailed)
	}
	if !strings.Contains(log.ResponseContent, "real answer") {
		t.Errorf("response body = %q, want the second member's answer", log.ResponseContent)
	}
}

// TestForward_streamingEmptyAnswerRecordedAsFailure 守住流式的兜底:
// 正文已经空着流给客户端, 无法再换成员, 但整轮必须记成失败并把成员打入冷却, 而不是当成功留在日志里。
func TestForward_streamingEmptyAnswerRecordedAsFailure(t *testing.T) {
	setupRelayLogTest(t)

	upstream := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"thinking a lot\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\n" +
				"data: [DONE]\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	logs, channel := serveForward(t, 312, "empty-answer-stream-g", true, upstream)

	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	log := logs[0]
	if log.Status != string(StatusFailed) {
		t.Fatalf("status = %q, want %q", log.Status, StatusFailed)
	}
	if !strings.Contains(log.Error, "reasoning-only stop") {
		t.Errorf("error = %q, want reasoning-only stop", log.Error)
	}
	if len(log.Attempts) != 1 || log.Attempts[0].Status != model.AttemptFailed {
		t.Errorf("attempts = %+v, want a single failed attempt", log.Attempts)
	}
	if channel.RequestFailed != 1 {
		t.Errorf("channel RequestFailed = %d, want 1", channel.RequestFailed)
	}
}
