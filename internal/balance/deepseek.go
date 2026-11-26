// Package balance 查询上游账户的真实余额。
//
// 与套餐额度是两套东西: 余额是账户里剩的钱, 没有重置窗口; 套餐额度是订阅周期内还能用多少。
// DeepSeek 官方用推理 key 就能查余额, 不需要像火山那样另配账号级 AK/SK。
package balance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// deepSeekBalanceURL 是包内变量而非 const: 单测把它换成 httptest 地址, 不必真的打官方接口。
var deepSeekBalanceURL = "https://api.deepseek.com/user/balance"

const balanceTimeout = 10 * time.Second

// Entry 是一种货币的余额明细。
// 金额保留官方返回的字符串而不转 float: 金额不该参与运算, 转成浮点只会引入精度误差与科学计数法。
type Entry struct {
	Currency string `json:"currency"`
	Total    string `json:"total_balance"`
	Granted  string `json:"granted_balance"`
	ToppedUp string `json:"topped_up_balance"`
}

// Result 是余额查询的归一化结果。
type Result struct {
	Available bool    `json:"available"` // 账户当前能否调用 API。
	Entries   []Entry `json:"entries"`   // 按币种列; DeepSeek 可能同时返回 CNY 与 USD。
}

// IsOfficialDeepSeekBaseURL 判断是否 DeepSeek 官方地址。
//
// 只看主机名, 忽略路径与 /v1 后缀 —— 官方余额接口固定在 /user/balance, 与转发的 base_url 路径无关。
// 中转站即使名字里带 deepseek 也必须排除: 它们的 key 查不到官方账户的余额。
func IsOfficialDeepSeekBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "api.deepseek.com")
}

// Fetch 用渠道的推理 key 查 DeepSeek 账户余额。
func Fetch(ctx context.Context, client *http.Client, apiKey string) (*Result, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("deepseek balance: api key is required")
	}
	if client == nil {
		return nil, fmt.Errorf("deepseek balance: http client is required")
	}

	ctx, cancel := context.WithTimeout(ctx, balanceTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deepSeekBalanceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("deepseek balance: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek balance: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("deepseek balance: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek balance: HTTP %d: %s", resp.StatusCode, clip(string(body), 200))
	}
	return parseResponse(body)
}

// parseResponse 解析官方响应。金额字段是字符串, 但个别网关可能回数字, 两种形态都收,
// 免得又踩一次火山 Quota/Used 文档写 string 实际回 number 的坑。
func parseResponse(body []byte) (*Result, error) {
	var raw struct {
		IsAvailable  bool              `json:"is_available"`
		BalanceInfos []json.RawMessage `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("deepseek balance: decode response: %w", err)
	}

	result := &Result{Available: raw.IsAvailable, Entries: make([]Entry, 0, len(raw.BalanceInfos))}
	for _, item := range raw.BalanceInfos {
		var entry struct {
			Currency string     `json:"currency"`
			Total    flexString `json:"total_balance"`
			Granted  flexString `json:"granted_balance"`
			ToppedUp flexString `json:"topped_up_balance"`
		}
		if err := json.Unmarshal(item, &entry); err != nil {
			return nil, fmt.Errorf("deepseek balance: decode entry: %w", err)
		}
		result.Entries = append(result.Entries, Entry{
			Currency: entry.Currency,
			Total:    entry.Total.Value,
			Granted:  entry.Granted.Value,
			ToppedUp: entry.ToppedUp.Value,
		})
	}
	return result, nil
}

// flexString 同时接受 JSON 字符串与数字形态的金额。
type flexString struct{ Value string }

func (f *flexString) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &f.Value)
	}
	f.Value = string(data)
	return nil
}

func clip(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
