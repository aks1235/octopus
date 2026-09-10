package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// keyTestChannel 构一个直连测试渠道, 路径取协议默认值, 与保存后的渠道形状一致。
func keyTestChannel(baseURL string) model.Channel {
	return model.Channel{ChannelConfig: model.ChannelConfig{
		Name:                     "test-channel",
		BaseURL:                  baseURL,
		OpenAIChatCompletionPath: "/v1/chat/completions",
		OpenAIResponsePath:       "/v1/responses",
		AnthropicMessagePath:     "/v1/messages",
	}}
}

// anthropicMessageBody 一个可被 anthropic 出站转换器解析的最小非流式响应。
func anthropicMessageBody() string {
	return `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"m-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
}

// chatCompletionBody 一个可被 openai 出站转换器解析的最小非流式响应。
func chatCompletionBody() string {
	return `{"id":"chatcmpl_test","object":"chat.completion","created":1,"model":"m-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
}

// TestChannelKey_successViaAnthropic 验证可用渠道按 key x 模型发最小真实请求返回成功:
// 成功走首个协议(Anthropic), 且上游实际收到渠道凭据与本轮模型名, 生成上限压到 1 token。
func TestChannelKey_successViaAnthropic(t *testing.T) {
	setupRelayLogTest(t)
	const testKey = "sk-test-123"
	var gotAuth, gotModel string
	var gotMaxTokens json.Number
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("X-API-Key")
		var body struct {
			Model     string      `json:"model"`
			MaxTokens json.Number `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode anthropic request body: %v", err)
		}
		gotModel, gotMaxTokens = body.Model, body.MaxTokens
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anthropicMessageBody()))
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), testKey, "k-1", []string{"m-test"}, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	result := results[0]
	if !result.Success || result.Protocol != model.ProtocolAnthropicMessage || result.Error != "" {
		t.Fatalf("result = %+v, want success via anthropic without error", result)
	}
	if gotAuth != testKey {
		t.Fatalf("upstream got key %q, want %q", gotAuth, testKey)
	}
	if gotModel != "m-test" {
		t.Fatalf("upstream got model %q, want m-test", gotModel)
	}
	if gotMaxTokens.String() != "1" {
		t.Fatalf("upstream got max_tokens %s, want 1", gotMaxTokens.String())
	}
}

// TestChannelKey_fallsBackToChatProtocol 验证前序协议端点不通时按协议优先级降级重试:
// Anthropic 与 Responses 均失败后由 Chat Completions 端点测通, 成功协议记为 Chat。
func TestChannelKey_fallsBackToChatProtocol(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(chatCompletionBody()))
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-test"}, nil)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("results = %+v, want success via chat fallback", results)
	}
	if results[0].Protocol != model.ProtocolOpenAIChatCompletion {
		t.Fatalf("protocol = %d, want chat completion bit %d", results[0].Protocol, model.ProtocolOpenAIChatCompletion)
	}
}

// TestChannelKey_failureWithErrorSummary 验证坏凭据场景: 全协议失败时聚合各协议摘要,
// 错误带上上游状态与原文, 供界面直接展示。
func TestChannelKey_failureWithErrorSummary(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-bad", "k-1", []string{"m-test"}, nil)

	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want failure", results)
	}
	message := results[0].Error
	if !strings.Contains(message, "401") || !strings.Contains(message, "invalid api key") {
		t.Fatalf("error %q must contain upstream status and body", message)
	}
	if len(message) > keyTestMaxErrorLength {
		t.Fatalf("error length %d exceeds cap %d", len(message), keyTestMaxErrorLength)
	}
}

// TestChannelKey_unreachableBaseURL 验证坏地址场景: 连接不通按失败返回并带原因摘要。
func TestChannelKey_unreachableBaseURL(t *testing.T) {
	setupRelayLogTest(t)
	// 端口 1 几乎必然无监听, 连接立即被拒, 不触发 30 秒等待。
	results := TestChannelKey(context.Background(), keyTestChannel("http://127.0.0.1:1"), "sk-test", "k-1", []string{"m-test"}, nil)

	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want failure on unreachable upstream", results)
	}
	if results[0].Error == "" {
		t.Fatalf("failure must carry an error summary")
	}
}

// TestChannelKey_truncatesLongErrors 验证上游整页报错被截断到上限, 结果不随上游正文无界膨胀。
func TestChannelKey_truncatesLongErrors(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("x", 4096), http.StatusUnauthorized)
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-test"}, nil)

	if len(results) != 1 || results[0].Success {
		t.Fatalf("results = %+v, want failure", results)
	}
	if len(results[0].Error) > keyTestMaxErrorLength {
		t.Fatalf("error length %d exceeds cap %d", len(results[0].Error), keyTestMaxErrorLength)
	}
}

// TestChannelKey_serialPerModel 验证逐模型串行且结果顺序与提交一致, 多模型整测的返回可直接逐行展示。
func TestChannelKey_serialPerModel(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anthropicMessageBody()))
	}))
	defer server.Close()

	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-a", "m-b", "m-c"}, nil)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %+v", results)
	}
	for i, name := range []string{"m-a", "m-b", "m-c"} {
		if results[i].ModelName != name || !results[i].Success {
			t.Fatalf("results[%d] = %+v, want %q success", i, results[i], name)
		}
	}
}

// TestChannelKey_grantProtocolsAreRespected 验证协议位精确化: 授权里"模型 x 凭据"只勾了 Chat 时,
// 只试 Chat 端点 —— Anthropic 端点即便可用也不被触碰(无授权时会因优先级抢先成功)。
func TestChannelKey_grantProtocolsAreRespected(t *testing.T) {
	setupRelayLogTest(t)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(anthropicMessageBody()))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(chatCompletionBody()))
		default:
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	grants := []model.ChannelGrantConfig{{ModelName: "m-test", KeyName: "k-1", Protocols: model.ProtocolOpenAIChatCompletion}}
	results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-test"}, grants)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("results = %+v, want success via granted chat bit", results)
	}
	if results[0].Protocol != model.ProtocolOpenAIChatCompletion {
		t.Fatalf("protocol = %d, want chat completion bit %d", results[0].Protocol, model.ProtocolOpenAIChatCompletion)
	}
	if len(paths) != 1 || paths[0] != "/v1/chat/completions" {
		t.Fatalf("upstream paths = %v, want only /v1/chat/completions", paths)
	}
}

// TestChannelKey_fallsBackToAllProtocolsWithoutGrant 验证回退路径: 查不到匹配授权或授权位为 0 时
// 按全协议位试测(现行为), 前序端点失败后仍可由后序端点测通。
func TestChannelKey_fallsBackToAllProtocolsWithoutGrant(t *testing.T) {
	cases := map[string][]model.ChannelGrantConfig{
		"no grants":    nil,
		"zero bits":    {{ModelName: "m-test", KeyName: "k-1", Protocols: 0}},
		"unknown bits": {{ModelName: "m-test", KeyName: "k-1", Protocols: 1 << 0}},
		"other key":    {{ModelName: "m-test", KeyName: "k-other", Protocols: model.ProtocolAnthropicMessage}},
	}
	for name, grants := range cases {
		t.Run(name, func(t *testing.T) {
			setupRelayLogTest(t)
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.URL.Path == "/v1/chat/completions" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(chatCompletionBody()))
					return
				}
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			}))
			defer server.Close()

			results := TestChannelKey(context.Background(), keyTestChannel(server.URL), "sk-test", "k-1", []string{"m-test"}, grants)

			if len(results) != 1 || !results[0].Success {
				t.Fatalf("results = %+v, want fallback success via chat", results)
			}
			if results[0].Protocol != model.ProtocolOpenAIChatCompletion {
				t.Fatalf("protocol = %d, want chat completion bit %d", results[0].Protocol, model.ProtocolOpenAIChatCompletion)
			}
			// 全协议位回退应把三个端点都试过: Anthropic 与 Responses 先失败, Chat 收尾成功。
			if len(paths) != 3 || paths[len(paths)-1] != "/v1/chat/completions" {
				t.Fatalf("upstream paths = %v, want all three endpoints tried with chat last", paths)
			}
		})
	}
}

// TestChannelKey_writesRelayLogPerModel 验证逐模型落测试日志: 成功与失败各一条,
// 客户端标识固定"面板测试", 凭据名/渠道/模型齐备, attempts 记录实际试过的协议端点。
func TestChannelKey_writesRelayLogPerModel(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}
		if body.Model == "m-bad" {
			http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anthropicMessageBody()))
	}))
	defer server.Close()

	channel := model.Channel{ID: 7, ChannelConfig: keyTestChannel(server.URL).ChannelConfig}
	channel.Name = "log-channel"
	TestChannelKey(context.Background(), channel, "sk-test", "k-main", []string{"m-ok", "m-bad"}, nil)

	logs := flushAndLoad(t)
	if len(logs) != 2 {
		t.Fatalf("expected 2 persisted logs, got %d", len(logs))
	}
	byModel := make(map[string]model.RelayLog, len(logs))
	for _, entry := range logs {
		byModel[entry.RequestModelName] = entry
	}

	okLog, hasOk := byModel["m-ok"]
	if !hasOk {
		t.Fatalf("missing log for m-ok, got models %v", logs)
	}
	if okLog.ClientName != keyTestClientName || okLog.UserAgent != keyTestUserAgent {
		t.Errorf("client = %q/%q, want %q/%q", okLog.ClientName, okLog.UserAgent, keyTestClientName, keyTestUserAgent)
	}
	if okLog.RequestAPIKeyName != "k-main" {
		t.Errorf("request api key name = %q, want k-main", okLog.RequestAPIKeyName)
	}
	if okLog.ChannelId != 7 || okLog.ChannelName != "log-channel" {
		t.Errorf("channel = %d/%q, want 7/log-channel", okLog.ChannelId, okLog.ChannelName)
	}
	if okLog.ActualModelName != "m-ok" || okLog.Error != "" {
		t.Errorf("actual model/error = %q/%q, want m-ok/empty", okLog.ActualModelName, okLog.Error)
	}
	if okLog.TotalAttempts != 1 || len(okLog.Attempts) != 1 || okLog.Attempts[0].Status != model.AttemptSuccess {
		t.Errorf("attempts = %+v, want single success", okLog.Attempts)
	}
	if okLog.Attempts[0].ChannelKeyRemark != "anthropic message" {
		t.Errorf("attempt remark = %q, want anthropic message", okLog.Attempts[0].ChannelKeyRemark)
	}
	if okLog.InputTokens != 0 || okLog.OutputTokens != 0 || okLog.Cost != 0 {
		t.Errorf("tokens/cost = %d/%d/%v, want zeros", okLog.InputTokens, okLog.OutputTokens, okLog.Cost)
	}
	if okLog.ID == 0 || okLog.Time == 0 {
		t.Errorf("id/time = %d/%d, want assigned snowflake id and unix time", okLog.ID, okLog.Time)
	}

	badLog, hasBad := byModel["m-bad"]
	if !hasBad {
		t.Fatalf("missing log for m-bad, got models %v", logs)
	}
	if badLog.Error == "" || !strings.Contains(badLog.Error, "401") {
		t.Errorf("error = %q, want summary containing 401", badLog.Error)
	}
	// 无匹配授权回退全协议位: 三个端点各记一条失败尝试。
	if badLog.TotalAttempts != 3 || len(badLog.Attempts) != 3 {
		t.Fatalf("attempts = %d/%d, want 3/3", badLog.TotalAttempts, len(badLog.Attempts))
	}
	for _, attempt := range badLog.Attempts {
		if attempt.Status != model.AttemptFailed || attempt.Msg == "" {
			t.Errorf("attempt = %+v, want failed with message", attempt)
		}
	}
	if badLog.ChannelId != 7 || badLog.RequestAPIKeyName != "k-main" || badLog.ActualModelName != "m-bad" {
		t.Errorf("log fields = %d/%q/%q, want 7/k-main/m-bad", badLog.ChannelId, badLog.RequestAPIKeyName, badLog.ActualModelName)
	}
}

// TestChannelKey_logsUnsavedFormChannel 验证未保存的表单渠道照写日志: 主键取 0, 名称用表单名。
func TestChannelKey_logsUnsavedFormChannel(t *testing.T) {
	setupRelayLogTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(anthropicMessageBody()))
	}))
	defer server.Close()

	channel := keyTestChannel(server.URL)
	channel.ID = 0
	channel.Name = ""
	TestChannelKey(context.Background(), channel, "sk-test", "k-form", []string{"m-test"}, nil)

	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	entry := logs[0]
	if entry.ChannelId != 0 || entry.ChannelName != "" {
		t.Errorf("channel = %d/%q, want 0/empty for unsaved form channel", entry.ChannelId, entry.ChannelName)
	}
	if entry.ClientName != keyTestClientName || entry.RequestAPIKeyName != "k-form" {
		t.Errorf("client/key name = %q/%q, want %q/k-form", entry.ClientName, entry.RequestAPIKeyName, keyTestClientName)
	}
}
