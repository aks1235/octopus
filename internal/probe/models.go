// Package probe 提供按渠道配置探测上游模型列表的共用基建。
// 渠道编辑器的模型拉取与健康检查任务共用同一套客户端构建, 地址拼装与两侧请求逻辑,
// 保证"编辑时能拉到模型"与"健康检查判活"两侧的判定口径完全一致。
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/rhttp"
)

// maxErrorLength 聚合失败原因的截断上限: 部分上游报错是整页 HTML, 全文带进健康状态列无用。
const maxErrorLength = 200

// NewClient 按渠道配置构建探测用的 HTTP 客户端, 并返回随客户端生命周期的清理函数。
// 代理选择与转发同源: 未启用代理直连, 启用且未填渠道专用代理走应用设置的共享代理,
// 填了专用代理按它新建独占客户端。独占客户端不参与共享, 清理函数负责关掉其空闲连接;
// 共享客户端的清理函数为空操作, 调用方无需区分两者。
func NewClient(config model.ChannelConfig) (*http.Client, func(), error) {
	var httpClient *http.Client
	var err error
	switch {
	case !config.Proxy:
		httpClient, err = rhttp.Direct()
	case config.ChannelProxy == "":
		httpClient, err = rhttp.Proxy()
	default:
		httpClient, err = rhttp.New(config.ChannelProxy)
		if httpClient != nil {
			return httpClient, func() { httpClient.CloseIdleConnections() }, nil
		}
	}
	if err != nil {
		return nil, func() {}, err
	}
	return httpClient, func() {}, nil
}

// KeyProbeResult 单个启用凭据两侧模型端点的探测结果。
// 列表与错误按侧独立给出: 判活按"任一侧成功"聚合, 对账按"哪侧成功"合并协议位,
// 两者对同一份结果的消费口径不同, 故不在此预先折算成单一结论。
type KeyProbeResult struct {
	KeyName      string   // 凭据名称, 供对账定位与日志标识。
	OpenAI       []string // OpenAI 侧拉到的模型名列表; 该侧失败时为 nil。
	Anthropic    []string // Anthropic 侧拉到的模型名列表; 该侧失败时为 nil。
	OpenAIErr    error    // OpenAI 侧的探测错误; 该侧成功时为 nil。
	AnthropicErr error    // Anthropic 侧的探测错误; 该侧成功时为 nil。
}

// FetchModels 按渠道配置与其凭据探测两侧模型端点, 任一启用凭据任一侧返回 2xx 即判定健康。
// 宽容判定: 多凭据渠道剩一个可用凭据, 或单协议上游只有一侧讲得通, 都不算失败,
// 健康检查由此不会误杀部分凭据失效或仅支持单侧协议的渠道; 检测部分凭据失效属 KEY 级健康管理, 不在此列。
// 全部探测失败时返回聚合的失败原因, 已截断到 200 字符以内。
// 只需要判活的调用方走本入口; 需要逐凭据模型清单做对账的调用方走 FetchModelsDetailed。
func FetchModels(ctx context.Context, config model.ChannelConfig, keys []model.ChannelKeyConfig) (bool, error) {
	healthy, _, err := FetchModelsDetailed(ctx, config, keys)
	return healthy, err
}

// FetchModelsDetailed 逐启用凭据探测两侧模型端点, 返回逐凭据结果与整体判活。
// 与 FetchModels 的差别在不短路: 每个启用凭据两侧都测完才收口, 因为对账需要全部凭据的完整清单,
// 短路会让排在已成功凭据之后的凭据永远没有对账机会; 判活仍按"任一凭据任一侧成功"聚合, 语义不变。
// 全部探测失败时返回聚合的失败原因(拼接格式与 FetchModels 一致), 已截断到 200 字符以内。
func FetchModelsDetailed(ctx context.Context, config model.ChannelConfig, keys []model.ChannelKeyConfig) (bool, []KeyProbeResult, error) {
	httpClient, closeClient, err := NewClient(config)
	if err != nil {
		return false, nil, err
	}
	defer closeClient()

	// 凭据之间与两侧端点之间串行探测: 探测是轻量的 GET /models, 无需渠道内部并发。
	openaiURL := ModelsURL(config.BaseURL, config.OpenAIResponsePath)
	anthropicURL := ModelsURL(config.BaseURL, config.AnthropicMessagePath)
	results := make([]KeyProbeResult, 0, len(keys))
	failures := make([]string, 0, len(keys)*2)
	healthy := false
	for _, key := range keys {
		if !key.Enabled {
			continue
		}
		result := KeyProbeResult{KeyName: key.Name}
		result.OpenAI, result.OpenAIErr = FetchOpenAIModels(httpClient, ctx, config, key.Key, openaiURL)
		result.Anthropic, result.AnthropicErr = FetchAnthropicModels(httpClient, ctx, config, key.Key, anthropicURL)
		if result.OpenAIErr != nil {
			failures = append(failures, fmt.Sprintf("key %q openai: %v", key.Name, result.OpenAIErr))
		} else {
			healthy = true
		}
		if result.AnthropicErr != nil {
			failures = append(failures, fmt.Sprintf("key %q anthropic: %v", key.Name, result.AnthropicErr))
		} else {
			healthy = true
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		// 一个启用凭据都没有的渠道无从转发, 判为不健康并给出可读原因。
		return false, results, fmt.Errorf("no enabled keys")
	}
	if healthy {
		return true, results, nil
	}
	return false, results, errors.New(truncateError(strings.Join(failures, "; ")))
}

// truncateError 把失败原因截断到上限以内。
func truncateError(message string) string {
	if len(message) <= maxErrorLength {
		return message
	}
	return message[:maxErrorLength]
}

// ModelsURL 取协议请求路径的父级目录, 与地址拼成同级的 /models 地址。
// 例如 /v1/chat/completions 与 /v1/messages 都得到 /v1/models, /chat/completions 得到 /models。
func ModelsURL(baseURL, protocolPath string) string {
	parent := path.Dir(strings.TrimRight(protocolPath, "/"))
	// Anthropic 的 /v1/messages 只有一层, 父级即 /v1; Chat 的 /v1/chat/completions 需要再上一层。
	if strings.HasSuffix(parent, "/chat") {
		parent = path.Dir(parent)
	}
	if parent == "." || parent == "/" {
		parent = ""
	}
	return strings.TrimRight(baseURL, "/") + parent + "/models"
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func FetchOpenAIModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	result, err := decodeModelList[model.OpenAIModelList](response)
	if err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://platform.claude.com/docs
func FetchAnthropicModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	var allModels []string
	var afterID string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		for _, header := range target.CustomHeader {
			if header.HeaderKey != "" {
				req.Header.Set(header.HeaderKey, header.HeaderValue)
			}
		}
		if afterID != "" {
			q := req.URL.Query()
			q.Set("after_id", afterID)
			req.URL.RawQuery = q.Encode()
		}

		response, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		// 分页时每轮都会新建响应, 必须当轮读完即关; 用 defer 会攒到整个函数返回才释放。
		result, err := decodeModelList[model.AnthropicModelList](response)
		if err != nil {
			return nil, err
		}
		for _, m := range result.Data {
			allModels = append(allModels, m.ID)
		}
		if !result.HasMore {
			break
		}
		afterID = result.LastID
	}
	return allModels, nil
}

// decodeModelList 关闭响应并把响应体解成模型列表; 非 2xx 时按上游错误返回。
// 两侧解析流程一致, 只有目标结构不同, 故用类型参数收敛; 分页调用要求当轮读完即关, 关闭点放在此处最稳。
func decodeModelList[T any](response *http.Response) (T, error) {
	defer response.Body.Close()
	var result T
	// 上游报错时响应体常是能被正常解码的 JSON, 若不先拦下, 模型列表会解成空列表并当作成功;
	// 响应体截断到 512 字节: 部分上游在鉴权失败时返回整页 HTML, 全文带到界面上无用。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := io.ReadAll(io.LimitReader(response.Body, 512))
		if err != nil {
			return result, fmt.Errorf("upstream %s", response.Status)
		}
		return result, fmt.Errorf("upstream %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}
