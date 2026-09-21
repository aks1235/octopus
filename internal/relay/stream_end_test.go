package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// TestParseStreamEventCompletedDetection 验证 OpenAI Chat 的业务终态识别 (R1):
// 带 finish_reason 的终态分片只标记 completed 而不结束转发 (R4: 其后仍有 usage 分片待收取);
// 空串与 null 不误判; 其余协议的终态仍走既有 last 路径, 不产生 completed。
func TestParseStreamEventCompletedDetection(t *testing.T) {
	cases := []struct {
		name          string
		format        llm.APIFormat
		data          string
		wantCompleted bool
	}{
		{"chat finish reason stop", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, true},
		{"chat finish reason length", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, true},
		// delta 字段整体缺失的终态分片: 判定必须早于 delta 空值检查, 否则这类上游识别不到。
		{"chat finish reason without delta field", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"finish_reason":"stop"}]}`, true},
		// 空串防御: 有上游(如 Sensenova)在每一个流分片里都下发 finish_reason:"", 不防空串会片片误判。
		{"chat empty finish reason", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":""}]}`, false},
		{"chat null finish reason", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`, false},
		{"chat content delta", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"index":0,"delta":{"content":"hi"}}]}`, false},
		{"chat usage only", llm.APIFormatOpenAIChatCompletion, `{"choices":[],"usage":{"prompt_tokens":1}}`, false},
		{"chat no choices", llm.APIFormatOpenAIChatCompletion, `{"usage":{"prompt_tokens":1}}`, false},
		// 两种协议的终态事件本身就是流的最后一个事件, 走的仍是 last 路径。
		{"anthropic message stop", llm.APIFormatAnthropicMessage, `{"type":"message_stop"}`, false},
		{"responses completed", llm.APIFormatOpenAIResponse, `{"type":"response.completed","response":{"status":"completed"}}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseStreamEvent(tc.format, rawStreamEvent(tc.data))
			if parsed.completed != tc.wantCompleted {
				t.Errorf("completed = %v, want %v", parsed.completed, tc.wantCompleted)
			}
			// 业务终态不得同时结束转发: 否则 finish_reason 之后的 usage 分片会被丢弃 (R4)。
			if parsed.completed && parsed.last {
				t.Errorf("completed event also set last = true, want false (would drop the trailing usage chunk)")
			}
		})
	}
}

// serveForwardStream 以假上游跑一次 handler 级流式转发, 返回落库日志与渠道统计快照。
func serveForwardStream(t *testing.T, groupID int, groupName string, upstream http.HandlerFunc) ([]model.RelayLog, model.Channel) {
	t.Helper()

	server := httptest.NewServer(upstream)
	defer server.Close()

	group := seedForwardTestGroup(t, groupID, groupName, server.URL)
	ResetRouteState(group.ID)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/relay", Forward(llm.APIFormatOpenAIChatCompletion))

	done := make(chan struct{})
	go func() {
		defer close(done)
		request := httptest.NewRequest(http.MethodPost, "/relay",
			strings.NewReader(`{"model":"`+groupName+`","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
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

// TestForward_upstreamFinishReasonWithoutDoneStillSucceeds 验证上游以 finish_reason 收尾、
// 不下发 [DONE]、且连接未正确终止时, 请求仍判成功 (AC1/AC2)。
//
// 生产实例: 天翼云在 finish_reason 之后既不发 [DONE] 也不干净关闭连接, 此前即便响应已完整交付
// 也被记为 failed / unexpected EOF。修复后成败依据是业务是否收尾, 而非 TCP 如何关闭。
func TestForward_upstreamFinishReasonWithoutDoneStillSucceeds(t *testing.T) {
	setupRelayLogTest(t)

	// 声明远超实际发送的字节数: handler 写不满即返回, 连接随之关闭, 客户端读到意外 EOF
	// (io.ErrUnexpectedEOF), 复刻"内容已发完但流未正确终止"的上游。
	upstream := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	logs, channel := serveForwardStream(t, 211, "finish-reason-no-done-g", upstream)

	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	if logs[0].Error != "" {
		t.Fatalf("error = %q, want empty (上游已发 finish_reason, 不应判失败)", logs[0].Error)
	}
	// usage 分片位于 finish_reason 之后: 业务终态不得提前结束转发, 否则用量被丢弃 (R4/AC2)。
	if logs[0].OutputTokens == 0 {
		t.Errorf("OutputTokens = 0, want > 0 (finish_reason 之后的 usage 分片被丢弃)")
	}
	if channel.RequestSuccess != 1 {
		t.Errorf("channel RequestSuccess = %d, want 1", channel.RequestSuccess)
	}
	if channel.RequestFailed != 0 {
		t.Errorf("channel RequestFailed = %d, want 0 (不应记为渠道失败)", channel.RequestFailed)
	}
}

// TestForward_upstreamInterruptWithoutFinishReasonStillFails 守住修复的另一侧 (R3/AC4):
// 上游从未宣告业务终态就中断连接, 仍属真中断, 必须保持失败语义, 不能被"尾部读取失败一律放过"误伤。
func TestForward_upstreamInterruptWithoutFinishReasonStillFails(t *testing.T) {
	setupRelayLogTest(t)

	// 与上一用例同为不干净关闭, 唯一差别是只发正文增量、不发 finish_reason。
	upstream := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	logs, channel := serveForwardStream(t, 212, "interrupt-no-finish-reason-g", upstream)

	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	// 不仅要有错, 还要保留原始读错误而非被改写: 这一侧的不干净关闭同样是 io.ErrUnexpectedEOF。
	if !strings.Contains(logs[0].Error, "EOF") {
		t.Fatalf("error = %q, want the original read error containing EOF (未见业务终态即中断应判失败)", logs[0].Error)
	}
	if channel.RequestFailed != 1 {
		t.Errorf("channel RequestFailed = %d, want 1", channel.RequestFailed)
	}
	if channel.RequestSuccess != 0 {
		t.Errorf("channel RequestSuccess = %d, want 0", channel.RequestSuccess)
	}
}
