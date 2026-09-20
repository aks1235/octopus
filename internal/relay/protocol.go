package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// buildPassthroughRequest 构造同协议透传的上游请求: 目标地址, 认证和请求体由渠道决定, 客户端的其余请求头和查询参数
// 经 MergeInboundRequest 透传给上游, 其中认证类, 库自管类和逐跳类请求头会被丢弃以免覆盖渠道凭据。
// 地址与认证取自出站转换器对一个占位请求的转换结果, 使透传与跨协议转换共用同一套地址拼接, 避免两处规则分歧;
// openai 与 anthropic 出站转换器会校验模型名非空, 故占位请求必须带本轮真实的上游模型名。
func buildPassthroughRequest(format llm.APIFormat, raw *httpclient.Request, channel model.Channel, outbound transformer.Outbound, modelName string) (*httpclient.Request, error) {
	probeText := "probe"
	probe, err := outbound.TransformRequest(context.Background(), &llm.Request{
		Model:    modelName,
		Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: &probeText}}},
	})
	if err != nil {
		return nil, fmt.Errorf("resolve upstream endpoint: %w", err)
	}

	// Content-Type 属于库自管头, 不会随客户端请求透传, 需按客户端原值显式重建。
	contentType := raw.Headers.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	request := httpclient.MergeInboundRequest(&httpclient.Request{
		Method:    raw.Method,
		URL:       probe.URL,
		Headers:   http.Header{"Content-Type": []string{contentType}},
		Body:      raw.Body,
		Auth:      probe.Auth,
		APIFormat: format.String(),
	}, raw)
	request, err = httpclient.FinalizeAuthHeaders(request)
	if err != nil {
		return nil, err
	}
	if err := applyChannelConfig(channel, request); err != nil {
		return nil, err
	}
	return request, nil
}

// streamEventParse 是一次流事件解析的全部结论, 供流式循环按事件一次取用:
// 结束判定与错误、输出相位、本次增量正文与思考增量的字符数。
// 其中 last/err/phase 与既有的 inspectStreamEvent/streamEventPhase 语义一一对应。
type streamEventParse struct {
	last        bool   // 该事件是否结束了整个响应流。
	err         error  // 该事件本身导致的失败, 非 nil 时本轮不可提交。
	phase       string // 该事件归属的输出相位, 空串表示不改变既有相位。
	textLen     int    // 该事件携带的正文增量字符数, 思考增量与其它事件为零。
	thinkingLen int    // 该事件携带的思考增量字符数, 正文增量与其它事件为零; 用于思考相位的实时速度 (R1)。
}

// parseStreamEvent 按客户端协议单次解析一个流事件, 同时得出结束判定, 输出相位与增量字符数。
// 每事件只解析一次 (R4): 相位分类, 实时速度的字符量与结束判定搭车同一份解析, 不为其中任何一项新增解析。
// 思考增量与正文增量分列两个字段: 思考期计思考相位速度、正文期计输出相位速度 (R1), 两者不混。
// 事件此时已按客户端协议编码(同协议透传的原样, 跨协议转换后亦为客户端格式), 故一律按客户端协议分类;
// 本函数不 panic, 供流式循环直接调用。
func parseStreamEvent(format llm.APIFormat, event *httpclient.StreamEvent) streamEventParse {
	if event == nil || len(event.Data) == 0 {
		return streamEventParse{}
	}

	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		if bytes.Equal(event.Data, llm.DoneStreamEvent.Data) {
			return streamEventParse{last: true}
		}
		// 一次解析同时覆盖错误判定(顶层的 error 字段)与相位和正文增量(choices[0].delta)。
		var chunk chatStreamChunk
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			return chatChunkFallback(event)
		}
		if event.Type == "error" || chunk.Error.Message != "" || chunk.Error.Type != "" || chunk.Error.Code != "" {
			detail := chunk.Error
			if detail.Message == "" {
				detail.Message = "openai stream error"
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: detail}}
		}
		if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
			return streamEventParse{}
		}
		delta := chunk.Choices[0].Delta
		if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
			return streamEventParse{phase: phaseThinking, thinkingLen: textCharCount(*delta.ReasoningContent)}
		}
		if delta.Content.Content != nil && *delta.Content.Content != "" {
			return streamEventParse{phase: phaseAnswering, textLen: textCharCount(*delta.Content.Content)}
		}
		return streamEventParse{}

	case llm.APIFormatOpenAIResponse:
		var parsed responses.StreamEvent
		if err := json.Unmarshal(event.Data, &parsed); err != nil {
			return streamEventParse{last: true, err: fmt.Errorf("decode responses stream event: %w", err)}
		}
		switch parsed.Type {
		case responses.StreamEventTypeResponseCompleted:
			// 已完成事件仍可能携带非 completed 的终态, 需按 status 与 error 区分成败。
			if parsed.Response == nil || parsed.Response.Status == nil || *parsed.Response.Status == "" || *parsed.Response.Status == "completed" {
				return streamEventParse{last: true}
			}
			if parsed.Response.Error != nil {
				return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Code: parsed.Response.Error.Code, Message: parsed.Response.Error.Message, Type: parsed.Response.Error.Type}}}
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response " + *parsed.Response.Status, Type: "response_" + *parsed.Response.Status}}}
		case responses.StreamEventTypeResponseFailed:
			if parsed.Response != nil && parsed.Response.Error != nil {
				return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Code: parsed.Response.Error.Code, Message: parsed.Response.Error.Message, Type: parsed.Response.Error.Type}}}
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response failed", Type: "response_failed"}}}
		case responses.StreamEventTypeResponseIncomplete:
			message := "response incomplete"
			if parsed.Response != nil && parsed.Response.IncompleteDetails != nil && parsed.Response.IncompleteDetails.Reason != "" {
				message += ": " + parsed.Response.IncompleteDetails.Reason
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Message: message, Type: "response_incomplete"}}}
		case responses.StreamEventTypeResponseCancelled:
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response cancelled", Type: "response_cancelled"}}}
		case responses.StreamEventTypeError:
			if parsed.Message == "" {
				parsed.Message = "responses stream error"
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Code: parsed.Code, Message: parsed.Message, Type: "stream_error"}}}
		case responses.StreamEventTypeReasoningSummaryTextDelta, responses.StreamEventTypeReasoningTextDelta:
			return streamEventParse{phase: phaseThinking, thinkingLen: textCharCount(parsed.Delta)}
		case responses.StreamEventTypeOutputTextDelta:
			return streamEventParse{phase: phaseAnswering, textLen: textCharCount(parsed.Delta)}
		default:
			return streamEventParse{}
		}

	case llm.APIFormatAnthropicMessage:
		var parsed anthropic.StreamEvent
		if err := json.Unmarshal(event.Data, &parsed); err != nil {
			return streamEventParse{last: true, err: fmt.Errorf("decode anthropic stream event: %w", err)}
		}
		if parsed.Type == "" {
			return streamEventParse{last: true, err: errors.New("anthropic stream event type is empty")}
		}
		// SSE 事件名与正文类型不一致说明流已错乱, 不能继续按协议解析。
		if event.Type != "" && event.Type != parsed.Type {
			return streamEventParse{last: true, err: fmt.Errorf("anthropic stream event type mismatch: %s != %s", event.Type, parsed.Type)}
		}
		if parsed.Type == "message_stop" {
			return streamEventParse{last: true}
		}
		if parsed.Type == "error" {
			var failure anthropic.AnthropicError
			if err := json.Unmarshal(event.Data, &failure); err != nil {
				return streamEventParse{last: true, err: fmt.Errorf("decode anthropic stream error: %w", err)}
			}
			if failure.Error.Message == "" {
				failure.Error.Message = "anthropic stream error"
			}
			return streamEventParse{last: true, err: &llm.ResponseError{Detail: llm.ErrorDetail{Message: failure.Error.Message, Type: failure.Error.Type, RequestID: failure.RequestID}}}
		}
		// 相位与正文增量: 只有 content_block_delta 的 thinking/text 增量参与分类。
		if parsed.Type != "content_block_delta" || parsed.Delta == nil || parsed.Delta.Type == nil {
			return streamEventParse{}
		}
		switch *parsed.Delta.Type {
		case "thinking_delta":
			thinkingLen := 0
			if parsed.Delta.Thinking != nil {
				thinkingLen = textCharCount(*parsed.Delta.Thinking)
			}
			return streamEventParse{phase: phaseThinking, thinkingLen: thinkingLen}
		case "text_delta":
			textLen := 0
			if parsed.Delta.Text != nil {
				textLen = textCharCount(*parsed.Delta.Text)
			}
			return streamEventParse{phase: phaseAnswering, textLen: textLen}
		default:
			return streamEventParse{}
		}

	default:
		return streamEventParse{}
	}
}

// textCharCount 返回一段增量正文的字符数; 按字符(而非字节)计, 使跨语言文本的实时速度同量级可比。
func textCharCount(text string) int {
	return utf8.RuneCountInString(text)
}

// chatStreamChunk 是聊天流事件的单次解析落点: choices 提供相位与正文增量, 顶层 error 提供结束判定。
// 顶层 error 直接落在 llm.ErrorDetail 上, 与合并前 inspectStreamEvent 所用的 openai.OpenAIError 同形(该结构的
// Detail 亦以 "error" 为键), 故以事件下发的错误的识别口径保持不变; 不能改用 openai.Response.Error, 它的
// Detail 还多一层 "error" 嵌套, 与协议实际下发的 {"error":{...}} 不合。
type chatStreamChunk struct {
	Choices []openai.Choice `json:"choices"`
	Error   llm.ErrorDetail `json:"error"`
}

// chatChunkFallback 处理无法按分片结构解析的聊天事件(JSON 本身合法但某字段类型异常):
// 相位与字符量无从取得, 该分片按不改变相位、不累计字符量处理, 与合并前相位分类失败的表现一致;
// 结束判定沿用合并前的口径 —— 只有 JSON 语法错误或顶层 error 才终止流, 避免把畸形分片误判为流终止。
func chatChunkFallback(event *httpclient.StreamEvent) streamEventParse {
	var failure openai.OpenAIError
	if err := json.Unmarshal(event.Data, &failure); err != nil {
		return streamEventParse{last: true, err: fmt.Errorf("decode openai stream event: %w", err)}
	}
	if event.Type != "error" && failure.Detail.Message == "" && failure.Detail.Type == "" && failure.Detail.Code == "" {
		return streamEventParse{}
	}
	if failure.Detail.Message == "" {
		failure.Detail.Message = "openai stream error"
	}
	return streamEventParse{last: true, err: &llm.ResponseError{Detail: failure.Detail}}
}

// inspectStreamEvent 判断一个客户端协议流事件是否结束了整个响应流, 并识别以事件形式下发的上游错误。
// 返回 true 表示流已结束; 返回的 error 非空表示该事件本身即失败, 本轮不可提交。
// 它是 parseStreamEvent 结束判定视角的薄封装, 供上游预读使用, 语义与合并前完全一致。
func inspectStreamEvent(format llm.APIFormat, event *httpclient.StreamEvent) (bool, error) {
	parsed := parseStreamEvent(format, event)
	return parsed.last, parsed.err
}

// streamEventPhase 判断一个流事件所处相位: thinking 表示模型正在思考(reasoning 增量), answering 表示模型正在输出正文。
// 解析失败、非增量事件或未知协议返回空串, 调用方据此保持相位不变。
// 它是 parseStreamEvent 相位视角的薄封装, 常规事件与合并前的相位分类逐项一致; 仅在下列两类自相矛盾的分片上不同,
// 且都不改变结束判定与写给客户端的内容: ① 分片既带顶层 error 或事件名 error、又带增量 —— 合并前仍会分类相位,
// 现在按失败事件处理不再分类(该轮随即失败, 相位已无意义); ② 分片带类型异常的无关字段(如 usage/created 非对象)
// —— 合并前的相位分类会因整体解码失败而丢相位, 现在只看 choices/delta 反而能正常分类。
func streamEventPhase(format llm.APIFormat, event *httpclient.StreamEvent) string {
	return parseStreamEvent(format, event).phase
}

// validateResponse 检查统一响应中需要在提交前判定为失败的终止原因; 仅 Responses 协议会以正常响应下发这类终态。
func validateResponse(format llm.APIFormat, response *llm.Response) error {
	if response == nil {
		return errors.New("upstream response is empty")
	}
	if format != llm.APIFormatOpenAIResponse || len(response.Choices) == 0 || response.Choices[0].FinishReason == nil {
		return nil
	}
	switch *response.Choices[0].FinishReason {
	case "error":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response failed", Type: "response_failed"}}
	case "length":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response incomplete", Type: "response_incomplete"}}
	case "cancelled":
		return &llm.ResponseError{Detail: llm.ErrorDetail{Message: "response cancelled", Type: "response_cancelled"}}
	default:
		return nil
	}
}
