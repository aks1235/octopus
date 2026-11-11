package relay

import (
	"testing"

	"github.com/looplj/axonhub/llm"
)

// TestUsageMetricsCarriesCacheTokens 锁定缓存拆分与价格解耦:
// 命中/写入来自上游 prompt_tokens_details, 模型没配单价时也要保留, 否则总览无法拆分输入构成。
func TestUsageMetricsCarriesCacheTokens(t *testing.T) {
	usage := &llm.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptTokensDetails: &llm.PromptTokensDetails{
			CachedTokens:      600,
			WriteCachedTokens: 100,
		},
	}

	// 模型不在价格缓存里: usageMetrics 会提前返回, 此时缓存拆分仍需落库。
	metrics := usageMetrics("model-without-price-xyz", usage)
	if metrics.InputToken != 1000 || metrics.OutputToken != 200 {
		t.Fatalf("tokens = %d/%d, want 1000/200", metrics.InputToken, metrics.OutputToken)
	}
	if metrics.CacheReadToken != 600 || metrics.CacheWriteToken != 100 {
		t.Fatalf("cache = %d/%d, want 600/100", metrics.CacheReadToken, metrics.CacheWriteToken)
	}
	// 没有价格时费用为零, 但缓存字段不能被一起清零。
	if metrics.InputCost != 0 || metrics.OutputCost != 0 {
		t.Fatalf("cost = %v/%v, want 0/0", metrics.InputCost, metrics.OutputCost)
	}
}

// TestUsageMetricsWithoutCacheDetails 上游没带 prompt_tokens_details 时缓存两项为 0,
// 前端据此把全部输入算作新增输入(而不是显示一半缓存)。
func TestUsageMetricsWithoutCacheDetails(t *testing.T) {
	metrics := usageMetrics("model-without-price-xyz", &llm.Usage{PromptTokens: 300, CompletionTokens: 50})
	if metrics.CacheReadToken != 0 || metrics.CacheWriteToken != 0 {
		t.Fatalf("cache = %d/%d, want 0/0", metrics.CacheReadToken, metrics.CacheWriteToken)
	}
	if metrics.InputToken != 300 {
		t.Fatalf("input = %d, want 300", metrics.InputToken)
	}
}
