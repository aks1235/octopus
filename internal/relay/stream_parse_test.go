package relay

import (
	"strings"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestParseStreamEventTextAndPhase 验证单次解析同时给出相位与正文增量字符数 (R3 / AC4):
// 三种客户端协议按正文增量定相位并计字符, 思考增量只定相位不计字符, 非增量事件两者皆空。
// 字符按字符而非字节计, 故多字节正文的长度与字面字符数一致。
func TestParseStreamEventTextAndPhase(t *testing.T) {
	cases := []struct {
		name      string
		format    llm.APIFormat
		data      string
		wantPhase string
		wantText  int
	}{
		{"chat content", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"content":"你好ab"}}]}`, phaseAnswering, 4},
		{"chat empty content", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"content":""}}]}`, "", 0},
		{"chat reasoning", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"reasoning_content":"思考中"}}]}`, phaseThinking, 0},
		{"chat usage only", llm.APIFormatOpenAIChatCompletion, `{"choices":[],"usage":{"prompt_tokens":1}}`, "", 0},
		{"chat done", llm.APIFormatOpenAIChatCompletion, `[DONE]`, "", 0},

		{"anthropic text", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好ab"}}`, phaseAnswering, 4},
		{"anthropic thinking", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"思考中"}}`, phaseThinking, 0},
		{"anthropic input json", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{"}}`, "", 0},
		{"anthropic ping", llm.APIFormatAnthropicMessage, `{"type":"ping"}`, "", 0},

		{"responses output text", llm.APIFormatOpenAIResponse, `{"type":"response.output_text.delta","delta":"你好ab"}`, phaseAnswering, 4},
		{"responses reasoning text", llm.APIFormatOpenAIResponse, `{"type":"response.reasoning_text.delta","delta":"思考中"}`, phaseThinking, 0},
		{"responses reasoning summary", llm.APIFormatOpenAIResponse, `{"type":"response.reasoning_summary_text.delta","delta":"思考中"}`, phaseThinking, 0},
		{"responses created", llm.APIFormatOpenAIResponse, `{"type":"response.created"}`, "", 0},

		{"unknown format", llm.APIFormat("something-else"), `{"choices":[{"delta":{"content":"hi"}}]}`, "", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseStreamEvent(tc.format, rawStreamEvent(tc.data))
			if parsed.phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", parsed.phase, tc.wantPhase)
			}
			if parsed.textLen != tc.wantText {
				t.Errorf("textLen = %d, want %d", parsed.textLen, tc.wantText)
			}
			// 相位分类视角的薄封装与合并前一致: 同一份解析的相位即 streamEventPhase 的返回值。
			if got := streamEventPhase(tc.format, rawStreamEvent(tc.data)); got != parsed.phase {
				t.Errorf("streamEventPhase() = %q, want %q (same as parseStreamEvent.phase)", got, parsed.phase)
			}
		})
	}
}

// TestParseStreamEventKeepsPhaseOnlyTextDeltaSemantics 固定一处易被误改的既有语义:
// Anthropic 的 text_delta 只凭 delta.type 判为输出正文, 即使该增量未带 text 也照常定相位 (仅字符数为 0)。
func TestParseStreamEventKeepsPhaseOnlyTextDeltaSemantics(t *testing.T) {
	parsed := parseStreamEvent(llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"content_block_delta","delta":{"type":"text_delta"}}`))
	if parsed.phase != phaseAnswering {
		t.Errorf("phase = %q, want %q", parsed.phase, phaseAnswering)
	}
	if parsed.textLen != 0 {
		t.Errorf("textLen = %d, want 0", parsed.textLen)
	}
}

// TestParseStreamEventEndDetection 验证合并解析后结束判定与错误识别的既有语义 (R4):
// 三种协议的终态事件结束流, 以事件下发的错误结束流并带出原因, 普通增量与非增量事件都不结束流。
func TestParseStreamEventEndDetection(t *testing.T) {
	cases := []struct {
		name       string
		format     llm.APIFormat
		event      *httpclient.StreamEvent
		wantLast   bool
		wantErr    bool
		wantErrHas string
	}{
		{"chat done", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`[DONE]`), true, false, ""},
		{"chat delta", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":{"content":"hi"}}]}`), false, false, ""},
		{"chat usage only", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[],"usage":{"prompt_tokens":1}}`), false, false, ""},
		{"chat error payload", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"error":{"message":"boom","type":"t"}}`), true, true, "boom"},
		{"chat error event type", llm.APIFormatOpenAIChatCompletion, &httpclient.StreamEvent{Type: "error", Data: []byte(`{"choices":[]}`)}, true, true, "openai stream error"},
		{"chat bad json", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{bad`), true, true, "decode openai stream event"},
		// JSON 合法但字段类型异常的分片不终止流, 与合并前口径一致: 归零相位与字符量, 继续转发。
		{"chat malformed choices", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":"oops"}`), false, false, ""},
		{"chat malformed delta field", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":{"content":123}}]}`), false, false, ""},
		// 顶层不是对象时旧口径即报解码失败, 仍需终止流。
		{"chat non-object json", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`[1,2]`), true, true, "decode openai stream event"},
		{"chat error with malformed choices", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"error":{"message":"boom"},"choices":"oops"}`), true, true, "boom"},

		{"responses completed", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.completed","response":{"status":"completed"}}`), true, false, ""},
		{"responses output text", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.output_text.delta","delta":"hi"}`), false, false, ""},
		{"responses failed", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.failed","response":{"status":"failed","error":{"message":"boom","type":"t"}}}`), true, true, "boom"},
		{"responses incomplete", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`), true, true, "max_output_tokens"},
		{"responses error", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"error","message":"boom"}`), true, true, "boom"},
		{"responses bad json", llm.APIFormatOpenAIResponse, rawStreamEvent(`{`), true, true, "decode responses stream event"},

		{"anthropic message stop", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"message_stop"}`), true, false, ""},
		{"anthropic ping", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"ping"}`), false, false, ""},
		{"anthropic error", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"error","error":{"message":"boom","type":"t"}}`), true, true, "boom"},
		{"anthropic empty type", llm.APIFormatAnthropicMessage, rawStreamEvent(`{}`), true, true, "type is empty"},
		{"anthropic type mismatch", llm.APIFormatAnthropicMessage, &httpclient.StreamEvent{Type: "content_block_delta", Data: []byte(`{"type":"ping"}`)}, true, true, "type mismatch"},
		{"anthropic bad json", llm.APIFormatAnthropicMessage, rawStreamEvent(`not json`), true, true, "decode anthropic stream event"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseStreamEvent(tc.format, tc.event)
			if parsed.last != tc.wantLast {
				t.Errorf("last = %v, want %v", parsed.last, tc.wantLast)
			}
			if (parsed.err != nil) != tc.wantErr {
				t.Errorf("err = %v, want error=%v", parsed.err, tc.wantErr)
			}
			if tc.wantErrHas != "" && (parsed.err == nil || !strings.Contains(parsed.err.Error(), tc.wantErrHas)) {
				t.Errorf("err = %v, want it to contain %q", parsed.err, tc.wantErrHas)
			}
			// 结束判定视角的薄封装与合并前一致: 同一份解析的 last/err 即 inspectStreamEvent 的返回值。
			last, err := inspectStreamEvent(tc.format, tc.event)
			if last != parsed.last || (err != nil) != (parsed.err != nil) {
				t.Errorf("inspectStreamEvent() = (%v, %v), want (%v, %v)", last, err, parsed.last, parsed.err)
			}
			// 错误事件不带相位与字符量, 不会污染实时速度。
			if parsed.err != nil && (parsed.phase != "" || parsed.textLen != 0) {
				t.Errorf("failed event produced phase=%q textLen=%d, want empty and 0", parsed.phase, parsed.textLen)
			}
		})
	}
}

// TestParseStreamEventEmptyEvent 验证空事件既不结束流也不产生相位与字符量, 且不 panic。
func TestParseStreamEventEmptyEvent(t *testing.T) {
	for _, event := range []*httpclient.StreamEvent{nil, rawStreamEvent("")} {
		parsed := parseStreamEvent(llm.APIFormatOpenAIChatCompletion, event)
		if parsed.last || parsed.err != nil || parsed.phase != "" || parsed.textLen != 0 {
			t.Errorf("parseStreamEvent(%v) = %+v, want zero value", event, parsed)
		}
	}
}

// drainSnapshotCount 取空通道并返回本次收到的状态推送条数。
func drainSnapshotCount(stream chan RequestState) int {
	count := 0
	for {
		select {
		case <-stream:
			count++
		default:
			return count
		}
	}
}

// TestRequestStateAddPhaseCharsThrottles 验证字符量累加与节流发布 (R1/R2):
// 非增量事件不累加不推送; 首个增量事件只立节流基线; 节流期内的增量只累加; 到达发布时点才推送一次速度快照。
func TestRequestStateAddPhaseCharsThrottles(t *testing.T) {
	stream, unwatch := watchRequestState()
	defer unwatch()

	// 相位起点固定为 2 秒前, 使速度断言可预期。
	request := &RequestState{Phase: phaseAnswering, phaseStartedAt: time.Now().Add(-2 * time.Second)}

	request.addPhaseChars("", 0) // 非增量事件传 0, 也不带相位。
	if request.PhaseChars != 0 {
		t.Fatalf("PhaseChars = %d, want 0 for zero-count event", request.PhaseChars)
	}
	if got := drainSnapshotCount(stream); got != 0 {
		t.Fatalf("published %d snapshots for zero-count event, want 0", got)
	}

	request.addPhaseChars("", 5) // 首个增量只立节流基线, 不发布。
	if request.PhaseChars != 5 {
		t.Fatalf("PhaseChars = %d, want 5", request.PhaseChars)
	}
	if got := drainSnapshotCount(stream); got != 0 {
		t.Fatalf("published %d snapshots on first increment, want 0", got)
	}
	if request.PhaseSpeed != 0 {
		t.Fatalf("PhaseSpeed = %d, want 0 before first publish", request.PhaseSpeed)
	}

	request.addPhaseChars("", 7) // 节流期内: 累加但不发布。
	if request.PhaseChars != 12 {
		t.Fatalf("PhaseChars = %d, want 12", request.PhaseChars)
	}
	if got := drainSnapshotCount(stream); got != 0 {
		t.Fatalf("published %d snapshots inside throttle window, want 0", got)
	}

	// 把节流基线推到过去, 模拟到达发布时点。
	mu.Lock()
	request.outputPublishAt = time.Now().Add(-time.Millisecond)
	mu.Unlock()

	request.addPhaseChars("", 8)
	if request.PhaseChars != 20 {
		t.Fatalf("PhaseChars = %d, want 20", request.PhaseChars)
	}
	if got := drainSnapshotCount(stream); got != 1 {
		t.Fatalf("published %d snapshots at throttle deadline, want 1", got)
	}
	if request.PhaseSpeed < 9 || request.PhaseSpeed > 10 { // 20 字符 / 约 2 秒
		t.Fatalf("PhaseSpeed = %d, want about 10", request.PhaseSpeed)
	}
}

// TestRequestStateAddPhaseCharsResetsOnPhaseSwitch 验证相位切换(思考→正文)时重新计时 (R1):
// 思考期速度按思考字符与思考窗口算; 切入正文后字符量与计时起点归零, 正文速度只反映正文阶段的字符与时间。
func TestRequestStateAddPhaseCharsResetsOnPhaseSwitch(t *testing.T) {
	_, unwatch := watchRequestState()
	defer unwatch()

	// 思考期: 固定窗口 2 秒, 到达发布时点后速度约 20 c/s。
	request := &RequestState{Phase: phaseThinking, phaseStartedAt: time.Now().Add(-2 * time.Second), PhaseChars: 38}
	mu.Lock()
	request.outputPublishAt = time.Now().Add(-time.Millisecond)
	mu.Unlock()

	request.addPhaseChars(phaseThinking, 2)
	if request.PhaseChars != 40 {
		t.Fatalf("thinking PhaseChars = %d, want 40", request.PhaseChars)
	}
	if request.PhaseSpeed < 19 || request.PhaseSpeed > 21 {
		t.Fatalf("thinking PhaseSpeed = %d, want about 20", request.PhaseSpeed)
	}

	// 切入正文: 字符量与计时起点重置, 相位切换本身推送一次, 首批正文字符只立新的节流基线。
	request.addPhaseChars(phaseAnswering, 10)
	if request.Phase != phaseAnswering {
		t.Fatalf("Phase = %q, want %q", request.Phase, phaseAnswering)
	}
	if request.PhaseChars != 10 || request.PhaseSpeed != 0 {
		t.Fatalf("after switch PhaseChars/PhaseSpeed = %d/%d, want 10/0", request.PhaseChars, request.PhaseSpeed)
	}
	if time.Since(request.phaseStartedAt) > time.Second {
		t.Fatalf("phaseStartedAt not reset on phase switch: %v", request.phaseStartedAt)
	}

	// 正文期到达发布时点: 速度按正文阶段的字符与时间算 (30 字符 / 2 秒 = 15 c/s)。
	mu.Lock()
	request.phaseStartedAt = time.Now().Add(-2 * time.Second)
	request.outputPublishAt = time.Now().Add(-time.Millisecond)
	mu.Unlock()
	request.addPhaseChars(phaseAnswering, 20)
	if request.PhaseChars != 30 {
		t.Fatalf("answering PhaseChars = %d, want 30", request.PhaseChars)
	}
	if request.PhaseSpeed < 14 || request.PhaseSpeed > 16 {
		t.Fatalf("answering PhaseSpeed = %d, want about 15", request.PhaseSpeed)
	}
}

// TestRequestStateAddPhaseCharsWithoutPhase 验证相位未定(未识别出相位)时不累计也不发布 (R1):
// 界面据此在该状态下继续显示「-」, 与既有的相位未定表现一致。
func TestRequestStateAddPhaseCharsWithoutPhase(t *testing.T) {
	stream, unwatch := watchRequestState()
	defer unwatch()

	request := &RequestState{StartedAt: time.Now().Add(-2 * time.Second)}
	request.addPhaseChars("", 7)
	if request.PhaseChars != 0 || request.PhaseSpeed != 0 {
		t.Fatalf("PhaseChars/PhaseSpeed = %d/%d, want 0/0 while phase is unset", request.PhaseChars, request.PhaseSpeed)
	}
	if got := drainSnapshotCount(stream); got != 0 {
		t.Fatalf("published %d snapshots without phase, want 0", got)
	}
}

// TestRequestStateAddPhaseCharsNoBackToThinking 固定既有语义 (AC5):
// 已进入正文相位后, 收尾分片混入的思考增量既不让相位与计时倒退, 也不计入正文相位的字符量
// (否则思考字符会抬高「输出 c/s」), 该阶段字符量保持不变。
func TestRequestStateAddPhaseCharsNoBackToThinking(t *testing.T) {
	stream, unwatch := watchRequestState()
	defer unwatch()

	start := time.Now().Add(-2 * time.Second)
	request := &RequestState{Phase: phaseAnswering, phaseStartedAt: start, PhaseChars: 30}

	request.addPhaseChars(phaseThinking, 5)
	if request.Phase != phaseAnswering {
		t.Fatalf("Phase = %q, want %q (must not fall back to thinking)", request.Phase, phaseAnswering)
	}
	if request.PhaseChars != 30 {
		t.Fatalf("PhaseChars = %d, want 30 (thinking chars must not be counted into the answering phase)", request.PhaseChars)
	}
	if !request.phaseStartedAt.Equal(start) {
		t.Fatalf("phaseStartedAt = %v, want the answering window %v kept unchanged", request.phaseStartedAt, start)
	}
	if got := drainSnapshotCount(stream); got != 0 {
		t.Fatalf("published %d snapshots for blocked thinking delta, want 0", got)
	}
}

// TestParseStreamEventThinkingChars 验证思考增量带出思考字符数 (R1):
// 三种协议均按字符计, 正文增量与其它事件为零, 与正文增量字段互不重叠。
func TestParseStreamEventThinkingChars(t *testing.T) {
	cases := []struct {
		name   string
		format llm.APIFormat
		data   string
		want   int
	}{
		{"chat reasoning", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"reasoning_content":"思考中"}}]}`, 3},
		{"chat content", llm.APIFormatOpenAIChatCompletion, `{"choices":[{"delta":{"content":"abcd"}}]}`, 0},
		{"anthropic thinking", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"思考中"}}`, 3},
		{"anthropic thinking without text", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"thinking_delta"}}`, 0},
		{"anthropic text", llm.APIFormatAnthropicMessage, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"abcd"}}`, 0},
		{"responses reasoning text", llm.APIFormatOpenAIResponse, `{"type":"response.reasoning_text.delta","delta":"思考中"}`, 3},
		{"responses reasoning summary", llm.APIFormatOpenAIResponse, `{"type":"response.reasoning_summary_text.delta","delta":"思考中"}`, 3},
		{"responses output text", llm.APIFormatOpenAIResponse, `{"type":"response.output_text.delta","delta":"abcd"}`, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseStreamEvent(tc.format, rawStreamEvent(tc.data))
			if parsed.thinkingLen != tc.want {
				t.Errorf("thinkingLen = %d, want %d", parsed.thinkingLen, tc.want)
			}
		})
	}
}

// TestRequestStateOutputWindowStart 验证速度耗时窗口起点取本轮上游请求开始时间, 未开始过轮次时回退请求到达时间。
func TestRequestStateOutputWindowStart(t *testing.T) {
	startedAt := time.Now().Add(-10 * time.Second)
	roundStartedAt := time.Now().Add(-3 * time.Second)

	request := &RequestState{StartedAt: startedAt}
	if got := request.outputWindowStart(); !got.Equal(startedAt) {
		t.Errorf("outputWindowStart() = %v, want request start %v", got, startedAt)
	}

	request.RoundStartedAt = roundStartedAt
	if got := request.outputWindowStart(); !got.Equal(roundStartedAt) {
		t.Errorf("outputWindowStart() = %v, want round start %v", got, roundStartedAt)
	}
}

// TestOutputCharSpeed 验证字符速度计算与零窗口保护。
func TestOutputCharSpeed(t *testing.T) {
	start := time.Unix(0, 0)
	if got := outputCharSpeed(500, start, start.Add(2*time.Second)); got != 250 {
		t.Errorf("outputCharSpeed(500, 2s) = %d, want 250", got)
	}
	if got := outputCharSpeed(500, start, start); got != 0 {
		t.Errorf("outputCharSpeed(500, 0s) = %d, want 0 (no division by zero)", got)
	}
	if got := outputCharSpeed(500, start, start.Add(-time.Second)); got != 0 {
		t.Errorf("outputCharSpeed with negative window = %d, want 0", got)
	}
}

// TestFirstTokenMsMatchesLogFtut 验证同一请求的「实时状态首字耗时」与「转发日志 ftut」数值一致 (质检遗留 B):
// 两处取同一时刻、同一算法, 界面因而能用「总耗时 − 首字」推导出与历史面板相同的输出速度。
// 同时确认实时状态定稿后的总耗时与日志 use_time 一致, 即两处的速度分子分母都相同。
func TestFirstTokenMsMatchesLogFtut(t *testing.T) {
	setupRelayLogTest(t)

	startedAt := time.Now().Add(-3 * time.Second)
	firstValidAt := startedAt.Add(1500 * time.Millisecond)
	request := &RequestState{
		ID:        7,
		Status:    StatusSuccess,
		Model:     "grp-x",
		StartedAt: startedAt,
		Duration:  3 * time.Second,
		Usage:     llm.Usage{PromptTokens: 100, CompletionTokens: 50},
	}

	request.markFirstToken(firstValidAt)
	if request.FirstTokenMs != 1500 {
		t.Fatalf("FirstTokenMs = %d, want 1500", request.FirstTokenMs)
	}
	// 多轮重试时首字耗时取首次成功的时刻, 后到的响应不覆盖。
	request.markFirstToken(firstValidAt.Add(time.Second))
	if request.FirstTokenMs != 1500 {
		t.Fatalf("FirstTokenMs = %d after second mark, want 1500", request.FirstTokenMs)
	}

	relayLogFinalize(request, "grp-x", nil, 0, "claude-code/1.0.6", "", firstValidAt)
	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	if logs[0].Ftut != request.FirstTokenMs {
		t.Errorf("log Ftut = %d, request FirstTokenMs = %d, want equal", logs[0].Ftut, request.FirstTokenMs)
	}
	if logs[0].UseTime != int(request.Duration.Milliseconds()) {
		t.Errorf("log UseTime = %d, request Duration = %dms, want equal", logs[0].UseTime, request.Duration.Milliseconds())
	}
}

// TestRequestStateFirstTokenUnsetBeforeResponse 验证未取得任何有效上游响应时不写首字耗时,
// 字段留零因 omitempty 不出现在状态流里, 界面据此不展示速度。
func TestRequestStateFirstTokenUnsetBeforeResponse(t *testing.T) {
	request := &RequestState{StartedAt: time.Now().Add(-time.Second)}
	if request.FirstTokenMs != 0 {
		t.Fatalf("FirstTokenMs = %d before any response, want 0", request.FirstTokenMs)
	}
	if got := firstTokenElapsedMs(request.StartedAt, request.StartedAt); got != 0 {
		t.Fatalf("firstTokenElapsedMs() = %d for identical instants, want 0", got)
	}
}

// TestRequestState_finishClearsLiveSpeed 验证实时速度字段只在流式进行中有值:
// 定稿时归零, 界面改用 usage 与耗时推导的精确速度 (R2), 且不影响既有用量与耗时字段。
// 首字耗时生命周期不同: 定稿后必须保留, 否则界面无法推出与历史面板一致的完成速度。
func TestRequestState_finishClearsLiveSpeed(t *testing.T) {
	setupRelayLogTest(t)

	request := &RequestState{Status: StatusCommitted, StartedAt: time.Now().Add(-time.Second)}
	request.PhaseChars = 120
	request.PhaseSpeed = 60
	request.markFirstToken(time.Now())

	request.markSucceeded("RESP-BODY", nil)

	if request.PhaseChars != 0 || request.PhaseSpeed != 0 {
		t.Errorf("PhaseChars/PhaseSpeed = %d/%d after finish, want 0/0", request.PhaseChars, request.PhaseSpeed)
	}
	if request.FirstTokenMs <= 0 {
		t.Errorf("FirstTokenMs = %d after finish, want it kept for the completed speed", request.FirstTokenMs)
	}
	if request.Status != StatusSuccess {
		t.Errorf("Status = %q, want %q", request.Status, StatusSuccess)
	}
	if request.Duration <= 0 {
		t.Errorf("Duration = %v, want positive", request.Duration)
	}
}
