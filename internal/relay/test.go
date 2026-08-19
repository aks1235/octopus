package relay

import (
	"context"
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/helper"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestModelResult 保存单个模型的连通性测试结果。
type TestModelResult struct {
	Model  string `json:"model"`            // Model 是被测试的模型名。
	Passed bool   `json:"passed"`           // Passed 表示渠道对该模型可达且凭据有效。
	Error  string `json:"error,omitempty"`  // Error 是失败原因或限流提示。
	Delay  int64  `json:"delay,omitempty"`  // Delay 是本次测试耗时(毫秒)。
}

// TestModels 对渠道中的指定模型逐个进行连通性测试。
// 每个模型发送一个最小 chat 请求("1+1=?"，max_tokens=1，stream=false)，30s 超时，
// 复用 relay 出站转换器(axonhub)与渠道覆盖配置，与真实转发流量一致。
// 2xx 或 429 视为可达(Passed=true)，其余状态或请求失败视为不可达。
func TestModels(ctx context.Context, channel *model.Channel, models []string) []TestModelResult {
	results := make([]TestModelResult, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		results = append(results, testSingleModel(ctx, channel, m))
	}
	return results
}

// testSingleModel 对单个模型执行一次最小上游请求并判定连通性。
func testSingleModel(ctx context.Context, channel *model.Channel, modelName string) TestModelResult {
	start := time.Now()

	if channel.BaseURL == "" || channel.Key == "" {
		return TestModelResult{Model: modelName, Passed: false, Error: "base url or key is empty"}
	}

	client, err := helper.ChannelHttpClient(channel)
	if err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: "failed to create http client: " + err.Error()}
	}

	outbound, err := newOutbound(channel.Type, channel.BaseURL, channel.Key)
	if err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: "unsupported channel type: " + err.Error()}
	}

	falseVal := false
	one := int64(1)
	prompt := "1+1=?"
	llmReq := &llm.Request{
		Model: modelName,
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: &prompt,
				},
			},
		},
		MaxTokens: &one,
		Stream:   &falseVal,
	}

	httpReq, err := outbound.TransformRequest(ctx, llmReq)
	if err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: "build request failed: " + err.Error(), Delay: time.Since(start).Milliseconds()}
	}

	// 应用渠道参数覆盖与自定义 Header，与真实 relay 流量一致。
	if err := applyChannelOptions(channel, httpReq); err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: "apply channel options failed: " + err.Error(), Delay: time.Since(start).Milliseconds()}
	}

	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rawReq, err := httpclient.BuildHttpRequest(testCtx, httpReq)
	if err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: "build http request failed: " + err.Error(), Delay: time.Since(start).Milliseconds()}
	}

	resp, err := client.Do(rawReq)
	delay := time.Since(start).Milliseconds()
	if err != nil {
		return TestModelResult{Model: modelName, Passed: false, Error: err.Error(), Delay: delay}
	}
	defer resp.Body.Close()

	result := TestModelResult{Model: modelName, Delay: delay}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.Passed = true
	case resp.StatusCode == http.StatusTooManyRequests:
		result.Passed = true
		result.Error = "Rate limited (429), but channel is reachable"
	default:
		result.Passed = false
		result.Error = resp.Status
	}
	return result
}
