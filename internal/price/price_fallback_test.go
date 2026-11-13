package price

import (
	"math"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestPickModalPrice_deepseekV41Flash 用真实报价分布验证: 众数取到的是多数转售商在卖的那一档。
// 输入价 0.30 出现 6 次(其余各档零散), 应选中 0.30/1.20; 0 价条目(订阅套餐)必须被排除。
func TestPickModalPrice_deepseekV41Flash(t *testing.T) {
	quotes := []model.LLMPrice{
		{Input: 0.15, Output: 0.6}, {Input: 0.15, Output: 0.6},
		{Input: 0.15, Output: 0.6}, {Input: 0.15, Output: 0.6},
		{Input: 0.3, Output: 1.2}, {Input: 0.3, Output: 1.2}, {Input: 0.3, Output: 1.2},
		{Input: 0.3, Output: 1.2}, {Input: 0.3, Output: 1.2}, {Input: 0.3, Output: 1.2},
		{Input: 0.29754, Output: 1.19015},
		{Input: 0.031},
		{Input: 0, Output: 0}, {Input: 0, Output: 0}, // 订阅套餐档
	}
	got, ok := pickModalPrice(quotes)
	if !ok {
		t.Fatal("pickModalPrice() 应返回价格")
	}
	if math.Abs(got.Input-0.3) > 1e-9 || math.Abs(got.Output-1.2) > 1e-9 {
		t.Fatalf("取到 %v, 期望 0.3/1.2", got)
	}
}

// TestPickModalPrice_kimiK3 验证众数与官方标价一致的情形(3/15 在 20 家出现)。
func TestPickModalPrice_kimiK3(t *testing.T) {
	quotes := make([]model.LLMPrice, 0, 22)
	for i := 0; i < 20; i++ {
		quotes = append(quotes, model.LLMPrice{Input: 3, Output: 15})
	}
	quotes = append(quotes,
		model.LLMPrice{Input: 2.827, Output: 14.133},
		model.LLMPrice{Input: 2.4, Output: 12},
	)
	got, ok := pickModalPrice(quotes)
	if !ok || got.Input != 3 || got.Output != 15 {
		t.Fatalf("取到 %v, 期望 3/15", got)
	}
}

// TestPickModalPrice_noConsensus 验证各档都只出现一次时退化为中位数所在档, 不被最低价带走。
func TestPickModalPrice_noConsensus(t *testing.T) {
	quotes := []model.LLMPrice{
		{Input: 1, Output: 2},
		{Input: 5, Output: 10},
		{Input: 20, Output: 40},
	}
	got, ok := pickModalPrice(quotes)
	if !ok {
		t.Fatal("pickModalPrice() 应返回价格")
	}
	if got.Input != 5 || got.Output != 10 {
		t.Fatalf("取到 %v, 期望中位数档 5/10", got)
	}
}

// TestPickModalPrice_allZero 验证全是 0 价(只有订阅档)时不给价格, 而不是给 0。
func TestPickModalPrice_allZero(t *testing.T) {
	quotes := []model.LLMPrice{{Input: 0, Output: 0}, {Input: 0, Output: 0}}
	if _, ok := pickModalPrice(quotes); ok {
		t.Fatal("全是 0 价时不应返回价格")
	}
}

// TestGetLLMPrice_fallsBackWhenOfficialMissing 验证兜底表只在官方表查不到时生效。
func TestGetLLMPrice_fallsBackWhenOfficialMissing(t *testing.T) {
	llmPriceLock.Lock()
	official := llmPrice
	fallback := llmPriceFallback
	llmPrice = map[string]model.LLMPrice{"official-model": {Input: 1, Output: 2}}
	llmPriceFallback = map[string]model.LLMPrice{"fallback-model": {Input: 3, Output: 4}}
	llmPriceLock.Unlock()
	t.Cleanup(func() {
		llmPriceLock.Lock()
		llmPrice = official
		llmPriceFallback = fallback
		llmPriceLock.Unlock()
	})

	if got := GetLLMPrice("fallback-model"); got == nil || got.Input != 3 {
		t.Fatalf("兜底表命中失败: %v", got)
	}
	if got := GetLLMPrice("official-model"); got == nil || got.Input != 1 {
		t.Fatalf("官方表命中失败: %v", got)
	}
	if got := GetLLMPrice("nothing-here"); got != nil {
		t.Fatalf("不存在的模型不应有价格: %v", got)
	}
}
