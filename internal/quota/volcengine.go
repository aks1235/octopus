package quota

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 火山方舟套餐用量所在的控制面常量。
//
// Host 是 OpenTOP 控制面, 不是推理数据面的 ark.cn-beijing.volces.com: 套餐额度不在推理端点上暴露,
// 用推理 key 调用会被上游拒绝(实测报 "GetAFPUsage requires Volcengine Ark SSO STS"), 只能用 AK/SK 签名。
const (
	volcHost          = "open.volcengineapi.com"
	volcRegion        = "cn-beijing"
	volcService       = "ark"
	volcVersion       = "2024-01-01"
	volcSignedHeaders = "host;x-content-sha256;x-date"
	volcTimeout       = 10 * time.Second
)

// volcAgentPlanActions 是 Agent Plan 的 Action 名, 主名在前。
// 两个名字在现网都可用且返回同一结构: 官方 arkcli 用前者, 早期社区实现只用后者,
// 故主名报"未知 Action"时用备用名再试一次。
var volcAgentPlanActions = []string{"GetAFPUsage", "GetAgentPlanAFPUsage"}

// VolcengineProvider 采集火山方舟订阅套餐的额度。
type VolcengineProvider struct{}

// SignV4 对一次 OpenTOP 的 POST 调用做火山 V4 签名, 返回完整 URL 与需要发送的头。
//
// 与官方 @volcengine/openapi 逐字节一致: 参与签名的头只有 host, x-content-sha256, x-date,
// content-type 不参与; 凭证范围固定为 <日期>/cn-beijing/ark/request。
// 签名只覆盖 host 头而 host 由传输层从 URL 推导, 故 headers 里不含 host。
func SignV4(accessKeyID, secretKey, action string, body []byte, now time.Time) (string, map[string]string) {
	contentSHA := sha256Hex(body)
	xDate := now.UTC().Format("20060102T150405Z")
	dateStamp := xDate[:8]

	query := url.Values{}
	query.Set("Action", action)
	query.Set("Version", volcVersion)
	canonicalQuery := query.Encode()

	// 规范头按名称字典序拼接, 与 SignedHeaders 声明的顺序一致。
	canonicalHeaders := strings.Join([]string{
		"host:" + volcHost,
		"x-content-sha256:" + contentSHA,
		"x-date:" + xDate,
	}, "\n")

	// 规范头的尾部换行 + 空行分隔是 V4 的固定格式, 少一个换行签名必然对不上。
	canonicalRequest := strings.Join([]string{
		http.MethodPost, "/", canonicalQuery, canonicalHeaders + "\n", volcSignedHeaders, contentSHA,
	}, "\n")

	scope := strings.Join([]string{dateStamp, volcRegion, volcService, "request"}, "/")
	stringToSign := strings.Join([]string{"HMAC-SHA256", xDate, scope, sha256Hex([]byte(canonicalRequest))}, "\n")

	signingKey := hmacSHA256([]byte(secretKey), dateStamp)
	signingKey = hmacSHA256(signingKey, volcRegion)
	signingKey = hmacSHA256(signingKey, volcService)
	signingKey = hmacSHA256(signingKey, "request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	authorization := fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID, scope, volcSignedHeaders, signature)

	return "https://" + volcHost + "/?" + canonicalQuery, map[string]string{
		"content-type":     "application/json",
		"x-date":           xDate,
		"x-content-sha256": contentSHA,
		"authorization":    authorization,
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// Fetch 实现 Provider。当前只支持 Agent Plan; Coding Plan 走另一个 Action 与另一套字段, 尚未接入。
func (VolcengineProvider) Fetch(ctx context.Context, client *http.Client, cred Credential) (*Snapshot, error) {
	if cred.AccessKeyID == "" || cred.SecretAccessKey == "" {
		return nil, fmt.Errorf("volcengine plan quota: access key and secret key are required")
	}
	if cred.Kind != "" && cred.Kind != KindAgentPlan {
		return nil, fmt.Errorf("volcengine plan quota: unsupported plan kind %q", cred.Kind)
	}
	if client == nil {
		return nil, fmt.Errorf("volcengine plan quota: http client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, volcTimeout)
	defer cancel()

	body := []byte("{}")
	var lastErr error
	for _, action := range volcAgentPlanActions {
		snapshot, err := volcCallAgentPlan(ctx, client, cred, action, body)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
		// 只有"不认识这个 Action"才值得换名重试, 鉴权失败或限流换名只会再错一次。
		if !isUnknownAction(err) {
			break
		}
	}
	return nil, lastErr
}

func volcCallAgentPlan(ctx context.Context, client *http.Client, cred Credential, action string, body []byte) (*Snapshot, error) {
	endpoint, headers := SignV4(cred.AccessKeyID, cred.SecretAccessKey, action, body, time.Now())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("volcengine %s: build request: %w", action, err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("volcengine %s: %w", action, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("volcengine %s: read response: %w", action, err)
	}

	var payload volcAFPResponse
	// 非 200 也先试着解错误体: 控制面的错误码与说明都在 body 里, 比状态码有用得多。
	if err := json.Unmarshal(raw, &payload); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("volcengine %s: HTTP %d", action, resp.StatusCode)
		}
		return nil, fmt.Errorf("volcengine %s: decode response: %w", action, err)
	}
	if payload.ResponseMetadata.Error != nil {
		return nil, &volcAPIError{
			Action:  action,
			Code:    payload.ResponseMetadata.Error.Code,
			Message: payload.ResponseMetadata.Error.Message,
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("volcengine %s: HTTP %d", action, resp.StatusCode)
	}

	return payload.Result.toSnapshot(), nil
}

// volcAFPResponse 是 GetAFPUsage 的响应外形。
// Quota 与 Used 是字符串数字而不是 JSON number, 直接解成 float64 会失败。
type volcAFPResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	} `json:"ResponseMetadata"`
	Result volcAFPResult `json:"Result"`
}

type volcAFPResult struct {
	PlanType    string        `json:"PlanType"`
	AFPFiveHour volcAFPBucket `json:"AFPFiveHour"`
	AFPWeekly   volcAFPBucket `json:"AFPWeekly"`
	AFPMonthly  volcAFPBucket `json:"AFPMonthly"`
	// AFPDaily 有意不解析: 官方控制台与 arkcli 都只展示 5 小时/周/月三个窗口,
	// 日窗口在 Agent Plan 里只对部分模型生效, 摆出来会让读数多出一个对不上的维度。
}

type volcAFPBucket struct {
	Quota         volcFlexNumber `json:"Quota"`
	Used          volcFlexNumber `json:"Used"`
	ResetTime     int64          `json:"ResetTime"`     // 毫秒。
	SubscribeTime int64          `json:"SubscribeTime"` // 毫秒, 本期窗口的起算点。
}

// volcFlexNumber 兼容控制面把同一字段返回成字符串或数字两种形态。
//
// 文档写的是 string, 但现网 GetAFPUsage 的 Quota/Used 直接返回 JSON number
// (实测报 "cannot unmarshal number into Go struct field ... of type string")。
// 只按其中一种解, 会在真实账号上直接解码失败, 故两种都收。
type volcFlexNumber float64

func (n *volcFlexNumber) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*n = 0
		return nil
	}

	// 先按数字解; 成功即 number 形态。
	var numeric float64
	if err := json.Unmarshal(data, &numeric); err == nil {
		*n = volcFlexNumber(numeric)
		return nil
	}

	// 再按字符串解: 去掉引号后交给同一个解析器, 空串按 0 处理。
	var quoted string
	if err := json.Unmarshal(data, &quoted); err != nil {
		return fmt.Errorf("volcengine plan quota: unexpected number literal %s: %w", trimmed, err)
	}
	value, err := parseVolcNumber(quoted)
	if err != nil {
		return err
	}
	*n = volcFlexNumber(value)
	return nil
}

func (r volcAFPResult) toSnapshot() *Snapshot {
	windows := make([]Window, 0, 3)
	for _, item := range []struct {
		key    WindowKey
		bucket volcAFPBucket
	}{
		{WindowFiveHour, r.AFPFiveHour},
		{WindowWeekly, r.AFPWeekly},
		{WindowMonthly, r.AFPMonthly},
	} {
		window, ok := item.bucket.toWindow(item.key)
		if ok {
			windows = append(windows, window)
		}
	}
	return &Snapshot{PlanName: r.PlanType, Windows: windows}
}

// toWindow 把一个 AFP 窗口归一化。配额非正则视为"套餐不带这个窗口", 跳过而不是显示 0。
func (b volcAFPBucket) toWindow(key WindowKey) (Window, bool) {
	total := float64(b.Quota)
	if total <= 0 {
		return Window{}, false
	}
	used := float64(b.Used)
	if used < 0 {
		used = 0
	}

	percent := used / total * 100
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// ResetTime 为 0 或负数是"本窗口未排定重置"的哨兵值, 不能当 1970 年展示。
	var resetAt int64
	if b.ResetTime > 0 {
		resetAt = b.ResetTime / 1000
	}

	return Window{Key: key, Used: used, Total: total, Percent: percent, ResetAt: resetAt}, true
}

// parseVolcNumber 解析控制面返回的字符串数字, 兼容空串与带小数的形态。
func parseVolcNumber(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("empty number")
	}
	var value float64
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return 0, err
	}
	return value, nil
}

// volcAPIError 是控制面返回的业务错误, 原样保留错误码便于排查 AK/SK 权限问题。
type volcAPIError struct {
	Action  string
	Code    string
	Message string
}

func (e *volcAPIError) Error() string {
	return fmt.Sprintf("volcengine %s: %s: %s", e.Action, e.Code, e.Message)
}

func isUnknownAction(err error) bool {
	apiErr, ok := err.(*volcAPIError)
	if !ok {
		return false
	}
	code := strings.ToLower(apiErr.Code)
	return strings.Contains(code, "invalidaction") || strings.Contains(code, "unknown")
}
