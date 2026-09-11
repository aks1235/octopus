package relay

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// TestNewRequestState_apiKeyNameSnapshot 验证登记时快照 API Key 名称:
// Key 存在时名称随首条状态一并写入; Key 查询失败时名称留空, 不影响登记。
func TestNewRequestState_apiKeyNameSnapshot(t *testing.T) {
	setupRelayLogTest(t)
	ctx := context.Background()

	created := model.APIKey{Name: "prod-key", APIKey: "sk-snapshot-test"}
	if err := op.APIKeyCreate(&created, ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	request := newRequestState(ctx, "grp-x", 1, model.ProtocolAnthropicMessage, "{}", created.ID, "ua", "")
	if request.APIKeyName != "prod-key" {
		t.Errorf("APIKeyName = %q, want prod-key", request.APIKeyName)
	}

	missing := newRequestState(ctx, "grp-x", 1, model.ProtocolAnthropicMessage, "{}", created.ID+1000, "ua", "")
	if missing.APIKeyName != "" {
		t.Errorf("APIKeyName = %q, want empty when api key is missing", missing.APIKeyName)
	}
}

// TestRequestState_startRoundRecordsStartTime 验证每轮上游请求开始时记录轮次开始时间。
func TestRequestState_startRoundRecordsStartTime(t *testing.T) {
	request := &RequestState{}
	round := request.startRound(nil, "ch-a", "m-a", model.ProtocolAnthropicMessage)
	if round != 1 {
		t.Errorf("round = %d, want 1", round)
	}
	if request.RoundStartedAt.IsZero() {
		t.Error("RoundStartedAt is zero, want the round start time")
	}
}
