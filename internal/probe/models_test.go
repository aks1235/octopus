package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// fakeSide 假上游对"某凭据 x 某侧"的应答编排: models 为 nil 表示该侧一律 500;
// 空切片表示 200 但列表为空, 供空列表护栏的用例区分"失败"与"成功但为空"。
type fakeSide struct {
	models []string
}

// startFakeUpstream 起一个假上游并返回其地址与请求计数。
// 两侧端点地址相同(都落在 /v1/models), 按认证形态分流: 带 X-Api-Key 走 Anthropic 侧,
// 带 Authorization 走 OpenAI 侧, 与真实上游两侧同地址不同认证的形态一致。
// Anthropic 侧多于一个模型时拆成两页返回, 顺带覆盖分页拉取路径。
func startFakeUpstream(t *testing.T, openai, anthropic map[string]fakeSide) (string, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if key := r.Header.Get("X-Api-Key"); key != "" {
			side := anthropic[key]
			if side.models == nil {
				http.Error(w, "anthropic boom", http.StatusInternalServerError)
				return
			}
			if len(side.models) > 1 && r.URL.Query().Get("after_id") == "" {
				writeAnthropicPage(w, side.models[:1], side.models[0], true)
				return
			}
			// 第二页只给剩余部分: after_id 之后的条目, 与真实分页语义一致。
			if afterID := r.URL.Query().Get("after_id"); afterID != "" {
				for i, name := range side.models {
					if name == afterID {
						writeAnthropicPage(w, side.models[i+1:], "", false)
						return
					}
				}
			}
			writeAnthropicPage(w, side.models, "", false)
			return
		}
		side := openai[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if side.models == nil {
			http.Error(w, "openai boom", http.StatusInternalServerError)
			return
		}
		data := make([]model.OpenAIModel, 0, len(side.models))
		for _, name := range side.models {
			data = append(data, model.OpenAIModel{ID: name})
		}
		_ = json.NewEncoder(w).Encode(model.OpenAIModelList{Data: data})
	}))
	t.Cleanup(server.Close)
	return server.URL, &requests
}

func writeAnthropicPage(w http.ResponseWriter, names []string, lastID string, hasMore bool) {
	data := make([]model.AnthropicModel, 0, len(names))
	for _, name := range names {
		data = append(data, model.AnthropicModel{ID: name})
	}
	_ = json.NewEncoder(w).Encode(model.AnthropicModelList{Data: data, LastID: lastID, HasMore: hasMore})
}

// fakeProbeConfig 按假上游地址拼一份两侧路径齐全的探测配置。
func fakeProbeConfig(baseURL string) model.ChannelConfig {
	return model.ChannelConfig{
		BaseURL:              baseURL,
		OpenAIResponsePath:   "/v1/responses",
		AnthropicMessagePath: "/v1/messages",
	}
}

// TestFetchModelsDetailed_resultsShapePerKey 验证结果形状: 逐启用凭据一条结果,
// 两侧列表与错误各自独立, 禁用凭据不出现也不产生请求; Anthropic 分页被完整拉平。
func TestFetchModelsDetailed_resultsShapePerKey(t *testing.T) {
	baseURL, requests := startFakeUpstream(t,
		map[string]fakeSide{"sk-1": {models: []string{"gpt-4o", "gpt-4o-mini"}}, "sk-2": {models: nil}},
		map[string]fakeSide{"sk-1": {models: []string{"claude-3", "claude-4"}}, "sk-2": {models: []string{"claude-3"}}},
	)
	keys := []model.ChannelKeyConfig{
		{Name: "k1", Key: "sk-1", Enabled: true},
		{Name: "k2", Key: "sk-2", Enabled: true},
		{Name: "k3", Key: "sk-3", Enabled: false},
	}

	healthy, results, err := FetchModelsDetailed(context.Background(), fakeProbeConfig(baseURL), keys)
	if err != nil {
		t.Fatalf("FetchModelsDetailed() error = %v", err)
	}
	if !healthy {
		t.Fatalf("FetchModelsDetailed() healthy = false, want true")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for enabled keys, got %d: %+v", len(results), results)
	}
	first := results[0]
	if first.KeyName != "k1" || len(first.OpenAI) != 2 || first.OpenAI[1] != "gpt-4o-mini" {
		t.Fatalf("first result = %+v, want k1 with 2 openai models", first)
	}
	if len(first.Anthropic) != 2 || first.AnthropicErr != nil || first.OpenAIErr != nil {
		t.Fatalf("first result = %+v, want both sides ok with flattened pagination", first)
	}
	second := results[1]
	if second.KeyName != "k2" || second.OpenAIErr == nil || len(second.OpenAI) != 0 {
		t.Fatalf("second result = %+v, want openai side failed", second)
	}
	if len(second.Anthropic) != 1 || second.Anthropic[0] != "claude-3" || second.AnthropicErr != nil {
		t.Fatalf("second result = %+v, want anthropic side ok", second)
	}
	if !strings.Contains(second.OpenAIErr.Error(), "upstream 500") {
		t.Fatalf("openai error = %v, want upstream 500", second.OpenAIErr)
	}
	// k1 两侧 3 次请求(含分页一页) + k2 两侧 2 次 = 5 次; 禁用凭据 k3 不产生请求。
	if got := requests.Load(); got != 5 {
		t.Fatalf("expected 5 upstream requests, got %d", got)
	}
}

// TestFetchModelsDetailed_aggregationMatchesFetchModels 验证 bool 聚合与失败消息格式和 FetchModels 完全一致:
// 全部失败时两者同判不健康, 聚合错误逐字相同(委托关系不改变既有判活语义)。
func TestFetchModelsDetailed_aggregationMatchesFetchModels(t *testing.T) {
	baseURL, _ := startFakeUpstream(t,
		map[string]fakeSide{"sk-1": {models: nil}, "sk-2": {models: nil}},
		map[string]fakeSide{"sk-1": {models: nil}, "sk-2": {models: nil}},
	)
	keys := []model.ChannelKeyConfig{
		{Name: "k1", Key: "sk-1", Enabled: true},
		{Name: "k2", Key: "sk-2", Enabled: true},
	}

	healthy, results, detailedErr := FetchModelsDetailed(context.Background(), fakeProbeConfig(baseURL), keys)
	if healthy {
		t.Fatalf("FetchModelsDetailed() healthy = true, want false")
	}
	if detailedErr == nil {
		t.Fatalf("FetchModelsDetailed() error = nil, want aggregated failures")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results even when all fail, got %d", len(results))
	}
	// 聚合格式与旧实现一致: 逐凭据先 openai 后 anthropic, 用 "; " 相连;
	// 全量拼出超出 200 字符上限时按上限截断, 排在后面的凭据片段被截掉属预期。
	if !strings.HasPrefix(detailedErr.Error(), `key "k1" openai: upstream 500`) {
		t.Fatalf("aggregated error %q want k1 openai fragment first", detailedErr.Error())
	}
	if !strings.Contains(detailedErr.Error(), `; key "k1" anthropic: upstream 500`) {
		t.Fatalf("aggregated error %q missing k1 anthropic fragment with separator", detailedErr.Error())
	}
	if len(detailedErr.Error()) != 200 {
		t.Fatalf("aggregated error length = %d, want truncated to 200", len(detailedErr.Error()))
	}

	plainHealthy, plainErr := FetchModels(context.Background(), fakeProbeConfig(baseURL), keys)
	if plainHealthy || plainErr == nil || plainErr.Error() != detailedErr.Error() {
		t.Fatalf("FetchModels = (%v, %v), want same as detailed (%v, %v)",
			plainHealthy, plainErr, healthy, detailedErr)
	}
}

// TestFetchModelsDetailed_noEnabledKeys 验证一个启用凭据都没有时判不健康并给出可读原因。
func TestFetchModelsDetailed_noEnabledKeys(t *testing.T) {
	baseURL, requests := startFakeUpstream(t, map[string]fakeSide{}, map[string]fakeSide{})
	keys := []model.ChannelKeyConfig{{Name: "k3", Key: "sk-3", Enabled: false}}

	healthy, results, err := FetchModelsDetailed(context.Background(), fakeProbeConfig(baseURL), keys)
	if healthy || err == nil || err.Error() != "no enabled keys" {
		t.Fatalf("got (%v, %v), want false with no enabled keys", healthy, err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected 0 upstream requests, got %d", got)
	}
}

// TestFetchModelsDetailed_doesNotShortCircuit 验证不短路: 已有凭据成功后, 后续凭据两侧仍被完整探测,
// 对账才能拿到全部凭据的清单(旧 FetchModels 见好就收, 排在后面的凭据永远没有结果)。
func TestFetchModelsDetailed_doesNotShortCircuit(t *testing.T) {
	baseURL, requests := startFakeUpstream(t,
		map[string]fakeSide{"sk-1": {models: []string{"m1"}}, "sk-2": {models: []string{"m2"}}},
		map[string]fakeSide{"sk-1": {models: []string{"m3"}}, "sk-2": {models: []string{"m4"}}},
	)
	keys := []model.ChannelKeyConfig{
		{Name: "k1", Key: "sk-1", Enabled: true},
		{Name: "k2", Key: "sk-2", Enabled: true},
	}

	healthy, results, err := FetchModelsDetailed(context.Background(), fakeProbeConfig(baseURL), keys)
	if err != nil || !healthy {
		t.Fatalf("FetchModelsDetailed() = (%v, %v), want healthy", healthy, err)
	}
	if len(results) != 2 || results[1].KeyName != "k2" || len(results[1].OpenAI) != 1 {
		t.Fatalf("results = %+v, want both keys probed", results)
	}
	// 2 凭据 x 2 侧 = 4 次, 一次不少。
	if got := requests.Load(); got != 4 {
		t.Fatalf("expected 4 upstream requests (no short circuit), got %d", got)
	}
}

// TestFetchModelsDetailed_successWithEmptyList 验证"200 但空列表"的到达形态:
// 错误为 nil 且列表长度为 0 —— 对账的空列表护栏正是靠这对组合与"失败"区分。
func TestFetchModelsDetailed_successWithEmptyList(t *testing.T) {
	baseURL, _ := startFakeUpstream(t,
		map[string]fakeSide{"sk-1": {models: []string{"m1"}}},
		map[string]fakeSide{"sk-1": {models: []string{}}},
	)
	keys := []model.ChannelKeyConfig{{Name: "k1", Key: "sk-1", Enabled: true}}

	healthy, results, err := FetchModelsDetailed(context.Background(), fakeProbeConfig(baseURL), keys)
	if err != nil || !healthy {
		t.Fatalf("FetchModelsDetailed() = (%v, %v), want healthy", healthy, err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if results[0].AnthropicErr != nil || len(results[0].Anthropic) != 0 {
		t.Fatalf("anthropic side = (%v, %v), want nil error with empty list", results[0].AnthropicErr, results[0].Anthropic)
	}
}
