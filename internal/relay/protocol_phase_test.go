package relay

import (
	"reflect"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// rawStreamEvent 构造一个只携带原始数据的事件, 供相位分类测试使用。
func rawStreamEvent(data string) *httpclient.StreamEvent {
	return &httpclient.StreamEvent{Data: []byte(data)}
}

// watchRequestState 注册一个仅用于测试的临时 watcher, 返回接收推送的通道与注销函数。
func watchRequestState() (chan RequestState, func()) {
	stream := make(chan RequestState, streamBuffer)
	mu.Lock()
	watchers[stream] = struct{}{}
	mu.Unlock()
	return stream, func() {
		mu.Lock()
		delete(watchers, stream)
		mu.Unlock()
	}
}

// drainPhases 取出通道中已发布状态的全部非空相位并清空通道, 用于断言推送内容与次数。
func drainPhases(stream chan RequestState) []string {
	phases := make([]string, 0)
	for {
		select {
		case state := <-stream:
			if state.Phase != "" {
				phases = append(phases, state.Phase)
			}
		default:
			return phases
		}
	}
}

// TestStreamEventPhase 逐事件验证分类器: 三种客户端协议能识别出思考/正文档位,
// 非增量事件、解析失败与未知协议一律返回空串 (AC2 / AC4), 且不 panic。
func TestStreamEventPhase(t *testing.T) {
	cases := []struct {
		name   string
		format llm.APIFormat
		event  *httpclient.StreamEvent
		want   string
	}{
		{"nil event", llm.APIFormatOpenAIChatCompletion, nil, ""},
		{"empty data", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(""), ""},

		{"chat reasoning", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`), phaseThinking},
		{"chat content", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":{"content":"hi"}}]}`), phaseAnswering},
		{"chat empty delta", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":{}}]}`), ""},
		{"chat null delta", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[{"delta":null}]}`), ""},
		{"chat usage only", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{"choices":[],"usage":{"prompt_tokens":1}}`), ""},
		{"chat bad json", llm.APIFormatOpenAIChatCompletion, rawStreamEvent(`{bad`), ""},

		{"anthropic thinking", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`), phaseThinking},
		{"anthropic text", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`), phaseAnswering},
		{"anthropic input json delta", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`), ""},
		{"anthropic ping", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"ping"}`), ""},
		{"anthropic message delta", llm.APIFormatAnthropicMessage, rawStreamEvent(`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`), ""},
		{"anthropic bad json", llm.APIFormatAnthropicMessage, rawStreamEvent(`not json`), ""},

		{"responses reasoning summary", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.reasoning_summary_text.delta","delta":"hmm"}`), phaseThinking},
		{"responses reasoning text", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.reasoning_text.delta","delta":"hmm"}`), phaseThinking},
		{"responses output text", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.output_text.delta","delta":"hi"}`), phaseAnswering},
		{"responses created", llm.APIFormatOpenAIResponse, rawStreamEvent(`{"type":"response.created"}`), ""},
		{"responses bad json", llm.APIFormatOpenAIResponse, rawStreamEvent(`{`), ""},

		{"unknown format", llm.APIFormat("something-else"), rawStreamEvent(`{"choices":[{"delta":{"content":"hi"}}]}`), ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamEventPhase(tc.format, tc.event); got != tc.want {
				t.Errorf("streamEventPhase() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStreamEventPhaseSequence 按 handler 流式循环的调用方式驱动三种协议的"思考增量→正文增量"序列,
// 断言相位发布序列 (AC1) 与推送次数上限 (AC3): 纯正文流只有一次 answering, 含思考则为 thinking→answering。
func TestStreamEventPhaseSequence(t *testing.T) {
	cases := []struct {
		name       string
		format     llm.APIFormat
		events     []string
		wantPhases []string
	}{
		{
			name:   "openai chat thinking then content",
			format: llm.APIFormatOpenAIChatCompletion,
			events: []string{
				`{"choices":[{"delta":{"reasoning_content":"a"}}]}`,
				`{"choices":[{"delta":{"reasoning_content":"b"}}]}`,
				`{"choices":[{"delta":{"content":"c"}}]}`,
				`{"choices":[{"delta":{"content":"d"}}]}`,
			},
			wantPhases: []string{phaseThinking, phaseAnswering},
		},
		{
			name:   "openai chat content only",
			format: llm.APIFormatOpenAIChatCompletion,
			events: []string{
				`{"choices":[{"delta":{"content":"c"}}]}`,
				`{"choices":[{"delta":{"content":"d"}}]}`,
			},
			wantPhases: []string{phaseAnswering},
		},
		{
			name:   "anthropic thinking then text",
			format: llm.APIFormatAnthropicMessage,
			events: []string{
				`{"type":"message_start"}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"a"}}`,
				`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"b"}}`,
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"c"}}`,
			},
			wantPhases: []string{phaseThinking, phaseAnswering},
		},
		{
			name:   "anthropic text only",
			format: llm.APIFormatAnthropicMessage,
			events: []string{
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"c"}}`,
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"d"}}`,
			},
			wantPhases: []string{phaseAnswering},
		},
		{
			name:   "responses reasoning then output",
			format: llm.APIFormatOpenAIResponse,
			events: []string{
				`{"type":"response.created"}`,
				`{"type":"response.reasoning_summary_text.delta","delta":"a"}`,
				`{"type":"response.output_text.delta","delta":"b"}`,
				`{"type":"response.output_text.delta","delta":"c"}`,
			},
			wantPhases: []string{phaseThinking, phaseAnswering},
		},
		{
			name:   "responses output only",
			format: llm.APIFormatOpenAIResponse,
			events: []string{
				`{"type":"response.output_text.delta","delta":"b"}`,
				`{"type":"response.output_text.delta","delta":"c"}`,
			},
			wantPhases: []string{phaseAnswering},
		},
		{
			name:   "ping and usage never change phase",
			format: llm.APIFormatAnthropicMessage,
			events: []string{
				`{"type":"ping"}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
				`bad json`,
			},
			wantPhases: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream, unwatch := watchRequestState()

			// 复刻 handler 流式循环: 进入 answering 前分类, 非空且变化时更新相位, 之后不再分类。
			request := &RequestState{}
			phaseSettled := false
			for _, data := range tc.events {
				if phaseSettled {
					break
				}
				phase := streamEventPhase(tc.format, rawStreamEvent(data))
				if phase == "" {
					continue
				}
				request.markPhase(phase)
				phaseSettled = phase == phaseAnswering
			}

			got := drainPhases(stream)
			unwatch()
			if !reflect.DeepEqual(got, tc.wantPhases) {
				t.Errorf("published phases = %v, want %v", got, tc.wantPhases)
			}
		})
	}
}

// TestRequestState_markPhasePublishesOnlyOnChange 验证相位只在变化时推送 (AC3):
// 相同值重复调用不产生新推送, 变化时推送一次并写入字段。
func TestRequestState_markPhasePublishesOnlyOnChange(t *testing.T) {
	stream, unwatch := watchRequestState()
	defer unwatch()

	request := &RequestState{}

	request.markPhase(phaseThinking)
	request.markPhase(phaseThinking)
	if got := drainPhases(stream); !reflect.DeepEqual(got, []string{phaseThinking}) {
		t.Fatalf("published phases = %v, want [thinking] exactly once", got)
	}

	request.markPhase(phaseAnswering)
	request.markPhase(phaseAnswering)
	if got := drainPhases(stream); !reflect.DeepEqual(got, []string{phaseAnswering}) {
		t.Fatalf("published phases = %v, want [answering] exactly once", got)
	}

	if request.Phase != phaseAnswering {
		t.Errorf("Phase = %q, want %q", request.Phase, phaseAnswering)
	}
}
