package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// seedHealthCheckTestDB 建一个启用渠道: 单凭据 k1 名下 m-a / m-b 两模型两授权,
// 供健康检查对账用例验证"探测结果替换该凭据名下的模型与授权"。
func seedHealthCheckTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "health-check-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.GetDB().Create(&model.Channel{
		ID:            1,
		ChannelConfig: model.ChannelConfig{Name: "chan", BaseURL: "http://upstream"},
	}).Error; err != nil {
		t.Fatalf("create channel 1: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelKey{
		ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-test"},
	}).Error; err != nil {
		t.Fatalf("create channel key 1: %v", err)
	}
	for _, grant := range []struct {
		modelID int
		name    string
	}{
		{modelID: 1, name: "m-a"},
		{modelID: 2, name: "m-b"},
	} {
		if err := db.GetDB().Create(&model.ChannelModel{ID: grant.modelID, ChannelID: 1, Name: grant.name}).Error; err != nil {
			t.Fatalf("create channel model %d: %v", grant.modelID, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: grant.modelID, ChannelModelID: grant.modelID, ChannelKeyID: 1,
			Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", grant.modelID, err)
		}
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// startHealthFakeUpstream 起假上游并返回地址: 带 Authorization 走 OpenAI 侧, 带 X-Api-Key 走
// Anthropic 侧(两侧 /models 地址相同, 认证形态不同), 各按传入清单应答; 清单为 nil 时该侧 500。
func startHealthFakeUpstream(t *testing.T, openai, anthropic []string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		isAnthropic := r.Header.Get("X-Api-Key") != ""
		names := openai
		if isAnthropic {
			names = anthropic
		}
		if names == nil {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		data := make([]map[string]string, 0, len(names))
		for _, name := range names {
			data = append(data, map[string]string{"id": name})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// healthCandidate 按假上游地址拼一个候选渠道: 单启用凭据 k1, 两侧路径取协议默认值。
func healthCandidate(baseURL string) model.ChannelHealthCandidate {
	return model.ChannelHealthCandidate{
		ID: 1,
		ChannelConfig: model.ChannelConfig{
			Name:                 "chan",
			BaseURL:              baseURL,
			OpenAIResponsePath:   "/v1/responses",
			AnthropicMessagePath: "/v1/messages",
		},
		Keys: []model.ChannelKeyConfig{{Name: "k1", Key: "sk-test", Enabled: true}},
	}
}

// channelModelsFromCache 读回渠道 1 的模型名集合(有序), 库与缓存经对账收尾后应一致。
func channelModelsFromCache(t *testing.T) []string {
	t.Helper()
	detail, err := op.ChannelDetailGet(1)
	if err != nil {
		t.Fatalf("ChannelDetailGet(1) error = %v", err)
	}
	return detail.Models
}

// loadHealthRow 读回渠道 1 的健康状态列, 供落库断言。
func loadHealthRow(t *testing.T) model.Channel {
	t.Helper()
	var row model.Channel
	if err := db.GetDB().First(&row, 1).Error; err != nil {
		t.Fatalf("load channel 1: %v", err)
	}
	return row
}

// TestCheckChannelHealth_reconcilesModelsAcrossRounds 验证探测清单自动对账:
// 上一轮拉到的模型在下一轮消失后自动删除(授权级联), 新模型自动出现且协议位按两侧探测结果落库;
// 健康成功同时照常清零失败计数。
func TestCheckChannelHealth_reconcilesModelsAcrossRounds(t *testing.T) {
	seedHealthCheckTestDB(t)

	baseURL := startHealthFakeUpstream(t, []string{"m-a", "m-b"}, []string{"m-a", "m-b"})
	checkChannelHealth(context.Background(), healthCandidate(baseURL), 3)
	if got, want := channelModelsFromCache(t), []string{"m-a", "m-b"}; !slices.Equal(got, want) {
		t.Fatalf("models after first round = %v, want %v", got, want)
	}
	if row := loadHealthRow(t); row.HealthFailCount != 0 || row.LastHealthAt == 0 {
		t.Fatalf("health row = %+v, want success recorded", row)
	}
	// 探测成功两侧 → 协议位取并集(Response 标记 OpenAI 侧, Anthropic 同名并入)。
	detail, err := op.ChannelDetailGet(1)
	if err != nil {
		t.Fatalf("ChannelDetailGet(1) error = %v", err)
	}
	for _, grant := range detail.Grants {
		if grant.Protocols != model.ProtocolOpenAIResponse|model.ProtocolAnthropicMessage {
			t.Fatalf("grant %+v protocols = %d, want both sides unioned", grant, grant.Protocols)
		}
	}

	// 第二轮上游下线 m-b、上线 m-c: 对账替换后 m-b 消失、m-c 出现。
	nextURL := startHealthFakeUpstream(t, []string{"m-a", "m-c"}, []string{"m-a", "m-c"})
	checkChannelHealth(context.Background(), healthCandidate(nextURL), 3)
	if got, want := channelModelsFromCache(t), []string{"m-a", "m-c"}; !slices.Equal(got, want) {
		t.Fatalf("models after second round = %v, want %v", got, want)
	}
	var grantCount int64
	if err := db.GetDB().Model(&model.ChannelGrant{}).Count(&grantCount).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grantCount != 2 {
		t.Fatalf("grant count = %d, want 2 (m-b grant cascaded away)", grantCount)
	}
}

// TestCheckChannelHealth_failedProbeSkipsReconcile 验证防误删护栏一: 两侧都探测失败的凭据不对账,
// 名下模型与授权原样保留; 健康判定照旧走失败计数。
func TestCheckChannelHealth_failedProbeSkipsReconcile(t *testing.T) {
	seedHealthCheckTestDB(t)

	baseURL := startHealthFakeUpstream(t, nil, nil)
	checkChannelHealth(context.Background(), healthCandidate(baseURL), 3)

	if row := loadHealthRow(t); row.HealthFailCount != 1 || row.LastHealthError == "" || row.Enabled == false {
		t.Fatalf("health row = %+v, want fail count 1 with reason, still enabled", row)
	}
	if got, want := channelModelsFromCache(t), []string{"m-a", "m-b"}; !slices.Equal(got, want) {
		t.Fatalf("models = %v, want unchanged %v", got, want)
	}
}

// TestCheckChannelHealth_emptyListSkipsReconcile 验证防误删护栏二: 任一侧 2xx 却返回空列表时
// 该凭据不对账(空列表更可能是上游故障而非全部下架), 健康成功照常落库。
func TestCheckChannelHealth_emptyListSkipsReconcile(t *testing.T) {
	seedHealthCheckTestDB(t)

	baseURL := startHealthFakeUpstream(t, []string{}, []string{})
	checkChannelHealth(context.Background(), healthCandidate(baseURL), 3)

	if row := loadHealthRow(t); row.HealthFailCount != 0 || row.LastHealthAt == 0 {
		t.Fatalf("health row = %+v, want success recorded", row)
	}
	if got, want := channelModelsFromCache(t), []string{"m-a", "m-b"}; !slices.Equal(got, want) {
		t.Fatalf("models = %v, want unchanged %v", got, want)
	}
}

// TestCheckChannelHealth_roundTimeoutSkipsAll 验证整轮超时不落任何结论:
// 健康计数与模型集合都保持原样, 留给下一轮。
func TestCheckChannelHealth_roundTimeoutSkipsAll(t *testing.T) {
	seedHealthCheckTestDB(t)

	baseURL := startHealthFakeUpstream(t, nil, nil)
	roundCtx, cancel := context.WithCancel(context.Background())
	cancel()
	checkChannelHealth(roundCtx, healthCandidate(baseURL), 3)

	if row := loadHealthRow(t); row.HealthFailCount != 0 || row.LastHealthAt != 0 {
		t.Fatalf("health row = %+v, want nothing recorded on round timeout", row)
	}
	if got, want := channelModelsFromCache(t), []string{"m-a", "m-b"}; !slices.Equal(got, want) {
		t.Fatalf("models = %v, want unchanged %v", got, want)
	}
}
