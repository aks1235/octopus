package price

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

const llmPriceUrl = "https://models.dev/api.json"

// developerFamilies 定义研发商及其自研模型系列前缀。
var developerFamilies = map[string][]string{
	"openai":     {"gpt", "o"},
	"anthropic":  {"claude"},
	"google":     {"gemini", "gemma", "lyria", "veo"},
	"deepseek":   {"deepseek"},
	"xai":        {"grok"},
	"alibaba":    {"qwen", "qvq"},
	"zhipuai":    {"glm"},
	"minimax":    {"minimax"},
	"moonshotai": {"kimi"},
	"v0":         {"v0"},
	"xiaomi":     {"mimo"},
}

var lastUpdateTime time.Time // 最近一次成功更新时间。

// llmPriceFallback 是官方条目缺失时的兜底价: 同一模型往往有十几家转售商在卖,
// 取"标价档的众数"作为代表价(见 pickModalPrice)。只在官方表查不到时才用它,
// 保证已有模型的取价口径完全不变, 也不会因为多出的条目让分段匹配出现新的歧义。
var llmPriceFallback = map[string]model.LLMPrice{}

// modalMinVotes 是"有共识"的最小票数: 少于这个票数时众数没有统计意义, 改用中位数档。
const modalMinVotes = 3

// UpdateLLMPrice 从 models.dev 更新自研文本输出模型的价格。
func UpdateLLMPrice(ctx context.Context) error {
	log.Debugf("update LLM price task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("update LLM price task finished, update time: %s", time.Since(startTime))
	}()
	var body []byte
	httpClient, err := rhttp.Direct()
	if err == nil {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if requestErr != nil {
			return requestErr
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, requestErr := httpClient.Do(req)
		if requestErr != nil {
			err = requestErr
		} else {
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
			} else {
				body, err = io.ReadAll(resp.Body)
				if err != nil {
					err = fmt.Errorf("failed to read response body: %w", err)
				}
			}
			resp.Body.Close()
		}
	}
	if err != nil {
		log.Warnf("direct request failed, trying with proxy: %v", err)
		httpClient, err = rhttp.Proxy()
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPriceUrl, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to fetch LLM info: %s", resp.Status)
		}
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}
	}
	var rawPrice map[string]struct {
		Models map[string]struct {
			ID         string `json:"id"`     // 模型标识。
			Family     string `json:"family"` // 模型所属系列。
			Modalities struct {
				Output []string `json:"output"` // 模型支持的输出类型。
			} `json:"modalities"`
			Cost model.LLMPrice `json:"cost"` // 模型价格。
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &rawPrice); err != nil {
		return fmt.Errorf("failed to parse LLM info: %w", err)
	}
	updatedPrices := make(map[string]model.LLMPrice)
	// quotes 收集全部 provider 对同一模型 id 的报价, 供官方缺条目时的兜底取价。
	quotes := make(map[string][]model.LLMPrice)
	for provider, providerModels := range rawPrice {
		familyPrefixes, isDeveloper := developerFamilies[provider]
		for _, priceModel := range providerModels.Models {
			modelID := strings.ToLower(priceModel.ID)
			modelFamily := strings.ToLower(priceModel.Family)
			if !isPricedModel(modelID, modelFamily, priceModel.Modalities.Output) {
				continue
			}

			// 云平台可能同时托管第三方模型, 官方条目只接受该研发商的自研系列。
			if isDeveloper {
				for _, familyPrefix := range familyPrefixes {
					if strings.HasPrefix(modelFamily, familyPrefix) {
						updatedPrices[modelID] = priceModel.Cost
						break
					}
				}
			}
			quotes[modelID] = append(quotes[modelID], priceModel.Cost)
		}
	}

	// 官方没有条目的模型才建兜底价: 例如 deepseek-v4.1-flash 只挂在转售商名下,
	// deepseek 官方 provider 里只有 v4-flash / v4-pro, 缺了官方条目就让整条模型永远 0 价。
	fallbackPrices := make(map[string]model.LLMPrice, len(quotes))
	for modelID, modelQuotes := range quotes {
		if _, ok := updatedPrices[modelID]; ok {
			continue
		}
		if picked, ok := pickModalPrice(modelQuotes); ok {
			fallbackPrices[modelID] = picked
		}
	}

	llmPriceLock.Lock()
	llmPrice = updatedPrices
	llmPriceFallback = fallbackPrices
	lastUpdateTime = time.Now()
	llmPriceLock.Unlock()
	return nil
}

// isPricedModel 判断一条 models.dev 条目是否参与取价: 必须有 id 且输出含文本。
// 嵌入类模型按向量计费, 与 token 单价不是一个口径, 一律排除。
func isPricedModel(modelID, modelFamily string, outputs []string) bool {
	if modelID == "" || !slices.Contains(outputs, "text") {
		return false
	}
	return !strings.Contains(modelID, "embed") && !strings.Contains(modelFamily, "embed")
}

// pickModalPrice 从同一模型的全部报价里挑一个代表价。
//
// 规则与理由:
//   - 先排除 0 价: models.dev 里 0 价条目是订阅套餐(token plan / coding plan)或免费档,
//     不是按量单价, 混进来会把代表价拉到 0。
//   - 再按 (input, output) 分档投票取众数。实测有官方价的模型里, 非官方报价的众数与官方标价
//     一致的比例约 58%——多数转售商照抄标价, 众数因此最接近"标价"。
//   - 票数不足 modalMinVotes 时没有共识, 退化为输入价中位数所在档, 避免被单家极端报价带走。
//
// 不取最低价: 最低价通常是最便宜 endpoint 或量化档(OpenRouter 卡片价就是这么来的),
// 用它会把成本系统性低估; 也不取 OpenRouter 价, 同理。
func pickModalPrice(quotes []model.LLMPrice) (model.LLMPrice, bool) {
	groups := make(map[[2]float64][]model.LLMPrice, len(quotes))
	inputs := make([]float64, 0, len(quotes))
	for _, quote := range quotes {
		if quote.Input <= 0 || quote.Output <= 0 {
			continue
		}
		key := [2]float64{roundCost(quote.Input), roundCost(quote.Output)}
		groups[key] = append(groups[key], quote)
		inputs = append(inputs, quote.Input)
	}
	if len(groups) == 0 {
		return model.LLMPrice{}, false
	}

	var modalKey [2]float64
	modalVotes := 0
	for key, group := range groups {
		if len(group) > modalVotes || (len(group) == modalVotes && key[0] < modalKey[0]) {
			modalKey, modalVotes = key, len(group)
		}
	}
	if modalVotes >= modalMinVotes {
		return groups[modalKey][0], true
	}

	// 无共识: 取输入价中位数所在档。
	slices.Sort(inputs)
	median := inputs[len(inputs)/2]
	var chosen [2]float64
	bestDiff := math.MaxFloat64
	for key := range groups {
		if diff := math.Abs(key[0] - median); diff < bestDiff {
			bestDiff, chosen = diff, key
		}
	}
	return groups[chosen][0], true
}

// roundCost 把价格规整到 6 位小数, 用于把浮点误差归到同一档。
func roundCost(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}

// GetLastUpdateTime 返回最近一次价格更新时间。
func GetLastUpdateTime() time.Time {
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()
	return lastUpdateTime
}

// GetLLMPrice 根据已规范化的模型名返回 API 提供的校准价格。
func GetLLMPrice(modelName string) *model.LLMPrice {
	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()

	if price := lookupPrice(llmPrice, modelName); price != nil {
		return price
	}
	// 官方条目缺失时才退到兜底表: 已有模型的取价口径保持原样。
	return lookupPrice(llmPriceFallback, modelName)
}

// lookupPrice 在给定价格表里按"精确 → 分段"两级匹配取价。
func lookupPrice(table map[string]model.LLMPrice, modelName string) *model.LLMPrice {
	if price, ok := table[modelName]; ok {
		return &price
	}
	// 将非字母、数字和小数点字符作为边界，使标准模型名可以出现在任意前缀或后缀之间。
	modelNameSegments := strings.FieldsFunc(modelName, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.'
	})

	// 匹配完整且连续的标准名称分段，优先选择分段最多的具体模型。
	matchedModelID := ""
	matchedSegmentCount := 0
	ambiguous := false
	for modelID, price := range table {
		modelIDSegments := strings.Split(modelID, "-")
		for start := 0; start+len(modelIDSegments) <= len(modelNameSegments); start++ {
			matched := true
			for i, segment := range modelIDSegments {
				if modelNameSegments[start+i] != segment {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if len(modelIDSegments) > matchedSegmentCount {
				matchedModelID = modelID
				matchedSegmentCount = len(modelIDSegments)
				ambiguous = false
			} else if len(modelIDSegments) == matchedSegmentCount && table[matchedModelID] != price {
				ambiguous = true
			}
			break
		}
	}
	if matchedModelID == "" || ambiguous {
		return nil
	}
	price := table[matchedModelID]
	return &price
}
