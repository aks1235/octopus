package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/planquota"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/probe"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/dlclark/regexp2"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/channel").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/detail/:id", http.MethodGet).
				Handle(getChannelDetail),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(listChannelStats),
		).
		AddRoute(
			router.NewRoute("/key-stats/:id", http.MethodGet).
				Handle(getChannelKeyStats),
		).
		AddRoute(
			router.NewRoute("/plan-quota/:id", http.MethodGet).
				Handle(getChannelPlanQuota),
		).
		AddRoute(
			// 路径不复用 plan-quota/:id 前缀: gin 的路由树不允许同一段既是通配又是字面量,
			// /plan-quota/refresh/:id 与 /plan-quota/:id 会直接冲突并 panic。
			router.NewRoute("/plan-quota-refresh/:id", http.MethodPost).
				Handle(refreshChannelPlanQuota),
		).
		AddRoute(
			router.NewRoute("/grants", http.MethodGet).
				Handle(listChannelGrant),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createChannel),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateChannel),
		).
		AddRoute(
			router.NewRoute("/enable", http.MethodPost).
				Handle(enableChannel),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteChannel),
		).
		AddRoute(
			router.NewRoute("/fetch-model", http.MethodPost).
				Handle(fetchModel),
		).
		AddRoute(
			router.NewRoute("/test-key", http.MethodPost).
				Handle(testKey),
		)
}

// getChannelDetail 返回单个渠道的完整配置, 供编辑表单打开时读取。
// 与列表分开: 整份配置带着路径, 代理与凭据明文, 只有正在编辑的那一个渠道用得上。
func getChannelDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, detail)
}

// listChannelStats 返回全部渠道及其模型的累计统计, 也是渠道列表页的数据来源。
// 不带整份配置: 统计每次转发都在变, 界面按更短的间隔刷新它, 而路径, 代理与凭据明文只在编辑时用得上。
func listChannelStats(c *gin.Context) {
	resp.Success(c, op.ChannelStatsList())
}

// getChannelKeyStats 返回单个渠道各凭据的累计统计, 供统计页按凭据维度查看。
// 与渠道统计同口径: 直接给出转发链路累加的原始七列, 不做聚合与趋势。
func getChannelKeyStats(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	keys, err := op.ChannelKeyStatsList(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, keys)
}

// getChannelPlanQuota 返回渠道订阅套餐的最近一次采集结果。
// 从未采集过时返回空对象而不是 404: 前端据此区分"还没采到"与"渠道不存在"。
func getChannelPlanQuota(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	row, err := planquota.Get(id)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		resp.Success(c, struct{}{})
		return
	}
	resp.Success(c, row)
}

// refreshChannelPlanQuota 立即采集一次并返回采集后的记录, 供面板上的手动刷新。
// 采集失败仍是 200 + 记录里的 error: 额度查不到是这一栏的常规状态, 不该让整个页面弹错误提示。
func refreshChannelPlanQuota(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	channel, err := op.ChannelGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	// 失败原因已落库, 由记录本身交给前端, 这里不重复上报。
	_ = planquota.Refresh(ctx, channel)

	row, err := planquota.Get(id)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, row)
}

// listChannelGrant 返回全部渠道授权候选, 供分组页选取成员。
func listChannelGrant(c *gin.Context) {
	resp.Success(c, op.ChannelGrantCandidates())
}

func createChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	channel, err := op.ChannelCreate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func updateChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.ID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	channel, err := op.ChannelUpdate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func enableChannel(c *gin.Context) {
	var request struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.ChannelEnabled(request.ID, request.Enabled, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func deleteChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.ChannelDel(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

// addChannelModelPrices 为渠道模型匹配校准价格，并批量写入尚不存在的价格记录。
func addChannelModelPrices(modelNames []string, ctx context.Context) error {
	seen := make(map[string]struct{}, len(modelNames))
	llmInfos := make([]model.LLMInfo, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.ToLower(modelName)
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		llmInfo := model.LLMInfo{Name: modelName}
		if modelPrice := price.GetLLMPrice(modelName); modelPrice != nil {
			llmInfo.LLMPrice = *modelPrice
		}
		llmInfos = append(llmInfos, llmInfo)
	}
	return op.LLMBatchCreate(llmInfos, ctx)
}

// fetchModel 按提交的渠道配置与凭据拉取上游模型列表, 并按过滤表达式筛选后返回。
// 同时探测 OpenAI 与 Anthropic 两侧, 谁返回了哪些模型, 就给对应协议位打勾。
// OpenAI 侧拉取到的模型同时标记 Chat Completions 与 Responses, 供渠道授权时选择对应端点。
// 两侧的 /models 地址与认证形态不同, 故必须分别探测:
// 单协议上游只有一侧会成功, "哪侧成功" 本身就是协议支持的证据。
// 只有两侧都失败才算失败; 一侧失败属正常情况, 单协议上游本就只有一侧讲得通, 按成功那侧的结果返回。
func fetchModel(c *gin.Context) {
	var request model.ChannelFetchModelRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	ctx := c.Request.Context()
	// 探测收的是尚未落库的提交配置, 不经 normalizeChannelConfig, 故在此自行去空白;
	// 其中只有地址是硬需求: 渠道尚未命名时也可试拉, 故名称不在此校验。
	target := request.Channel
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	target.MatchRegex = strings.TrimSpace(target.MatchRegex)
	if target.BaseURL == "" {
		resp.Error(c, http.StatusBadRequest, "channel base url is required")
		return
	}

	httpClient, closeClient, err := probe.NewClient(target)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	// 渠道专用代理的客户端不再共享, 探测完关掉空闲连接; 探测收的是未落库的输入, 留着也无从复用。
	defer closeClient()

	var openaiModels, anthropicModels []string
	var openaiErr, anthropicErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		openaiModels, openaiErr = probe.FetchOpenAIModels(httpClient, ctx, target, request.Key, probe.ModelsURL(target.BaseURLFor(model.ProtocolOpenAIResponse), target.OpenAIResponsePath))
	}()
	go func() {
		defer wg.Done()
		anthropicModels, anthropicErr = probe.FetchAnthropicModels(httpClient, ctx, target, request.Key, probe.ModelsURL(target.BaseURLFor(model.ProtocolAnthropicMessage), target.AnthropicMessagePath))
	}()
	wg.Wait()

	if openaiErr != nil && anthropicErr != nil {
		// 两侧都拿不到列表时再探一次 chat 入口, 区分"上游本来就没有 /models"和"地址或 key 配错了"。
		// 套餐类入口(火山方舟 Agent Plan、阿里云 Token Plan)本就不提供模型列表, 直接报上游原文
		// 只会得到一句 404, 用户看不出该手填模型还是该改地址。
		if message, ok := probeChatEndpoint(httpClient, ctx, target, request.Key); ok {
			resp.Error(c, http.StatusBadGateway, message)
			return
		}
		// 上游鉴权失败或地址不通属于调用方配置问题, 按 502 返回并带上上游原文, 便于在界面上直接看到原因。
		resp.Error(c, http.StatusBadGateway, fmt.Sprintf("openai: %v; anthropic: %v", openaiErr, anthropicErr))
		return
	}

	var re, reGlobal *regexp2.Regexp
	if target.MatchRegex != "" {
		if re, err = regexp2.Compile(target.MatchRegex, regexp2.ECMAScript); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	// 全局过滤由设置页维护, 命中语义与渠道级有意相反: 渠道级 match_regex 命中保留(白名单,
	// 常见用途"该渠道只要这几个模型"), 全局 model_filter 命中排除(黑名单, 常见用途拦
	// embedding/rerank 等垃圾模型); 两侧都配置时取 AND, 即「通过渠道白名单 且 未被全局黑名单命中」。
	// 留空的一侧不生效。
	// 设置读取失败按不过滤处理: 启动初始化会补齐默认行, 缺行只可能出现在旧库尚未刷新的瞬间。
	globalFilter, _ := op.SettingGetString(model.SettingKeyModelFilter)
	if globalFilter != "" {
		if reGlobal, err = regexp2.Compile(globalFilter, regexp2.ECMAScript); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	// 匹配错误统一交由调用方按请求错误返回。
	matches := func(name string) (bool, error) {
		return op.ModelNameKept(re, reGlobal, name)
	}

	// 两侧结果按名称合并成一份有序集合, 协议位的推导口径统一走 op.ChannelFetchModels
	// (与自动对账共用同一份, 勿在此另写一份: 口径分叉会把手动勾出的协议位在自动轮次冲掉)。
	// 过滤仍按提交的渠道配置与全局设置收缩, 判定走 op.ModelNameKept, 匹配错误交由调用方按请求错误返回。
	models := make([]model.ChannelFetchModel, 0)
	for _, item := range op.ChannelFetchModels(openaiModels, anthropicModels) {
		matched, err := matches(item.Name)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if matched {
			models = append(models, item)
		}
	}
	resp.Success(c, models)
}

// probeChatEndpoint 在两侧模型列表都失败后补一次 chat 入口探活。
//
// 请求体给空对象: 上游会先鉴权, 再因为缺 model 在生成前拒掉(400), 不消耗额度。
// 返回的报文带固定前缀, 前端据此给出"手动填模型"或"检查 key 与入口"的具体建议。
// 返回 false 表示这次探活也没有结论(网络错误或非预期状态码), 由调用方退回原始错误。
func probeChatEndpoint(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key string) (string, bool) {
	url := strings.TrimRight(target.BaseURLFor(model.ProtocolOpenAIChatCompletion), "/") + target.OpenAIChatCompletionPath
	status, body, err := probe.ChatEndpointStatus(httpClient, ctx, target, key, url)
	if err != nil {
		return "", false
	}
	snippet := strings.TrimSpace(body)
	if len(snippet) > 140 {
		snippet = snippet[:140]
	}
	switch {
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return fmt.Sprintf("model_list_unavailable: upstream chat endpoint is reachable (HTTP %d) but exposes no /models; add model names manually and enable skip health check", status), true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Sprintf("key_endpoint_mismatch: upstream rejected the key at this endpoint (HTTP %d): %s", status, snippet), true
	case status == http.StatusNotFound:
		return fmt.Sprintf("endpoint_not_found: chat endpoint returned 404, check base url and chat path: %s", snippet), true
	}
	return "", false
}

// testKey 按提交的渠道配置与凭据对指定模型逐个发起最小真实请求, 返回逐模型的连通性结果。
// 请求经转发链路同一套出站构造(buildOutbound)发往上游, 测到的即真实转发的可用性;
// 与凭据探测(GET /models 拉列表)互补: 探测确认"列得出模型", 此处确认"调得通模型"。
// 协议位按提交授权里"模型 x 凭据"的实际位精确试测, 逐模型各落一条测试日志(客户端标识"面板测试")。
// 测试请求产生真实计费消耗(生成上限 1 token 量级), 仅由人工在界面上触发, 无定时任务调用。
func testKey(c *gin.Context) {
	var request model.ChannelTestKeyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	ctx := c.Request.Context()
	// 测试收的是尚未落库的提交配置, 不经 normalizeChannelConfig, 故在此自行去空白;
	// 其中只有地址是硬需求: 渠道尚未命名时也可测试, 模型清单由调用方按单模型或全模型给出。
	target := request.Channel
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	if target.BaseURL == "" {
		resp.Error(c, http.StatusBadRequest, "channel base url is required")
		return
	}
	key := strings.TrimSpace(request.Key)
	if key == "" {
		resp.Error(c, http.StatusBadRequest, "key is required")
		return
	}
	models := make([]string, 0, len(request.Models))
	for _, modelName := range request.Models {
		if modelName = strings.TrimSpace(modelName); modelName != "" {
			models = append(models, modelName)
		}
	}
	if len(models) == 0 {
		resp.Error(c, http.StatusBadRequest, "models are required")
		return
	}

	// 表单渠道尚未保存时主键为 0, 名称用表单名: 测试日志照写, 凭据名与授权一并携带供协议位精确化。
	results := relay.TestChannelKey(ctx, model.Channel{ID: request.ChannelID, ChannelConfig: target}, key, request.KeyName, models, request.Grants)
	resp.Success(c, results)
}
