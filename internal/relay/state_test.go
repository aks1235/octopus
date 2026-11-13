package relay

import (
	"context"
	"testing"
	"time"

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

	request := newRequestState(ctx, "grp-x", 1, model.ProtocolAnthropicMessage, true, "{}", created.ID, "ua", "")
	if request.APIKeyName != "prod-key" {
		t.Errorf("APIKeyName = %q, want prod-key", request.APIKeyName)
	}
	if !request.Streaming {
		t.Error("Streaming = false, want true")
	}

	missing := newRequestState(ctx, "grp-x", 1, model.ProtocolAnthropicMessage, false, "{}", created.ID+1000, "ua", "")
	if missing.APIKeyName != "" {
		t.Errorf("APIKeyName = %q, want empty when api key is missing", missing.APIKeyName)
	}
	if missing.Streaming {
		t.Error("Streaming = true, want false")
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

// TestRequestState_appendAttemptPublishes 验证每次尝试结束都写进实时状态并立即推送。
// 这是实时页能看到故障转移过程的前提: 记录若只留在 handler 的局部切片里, 只有等请求结束
// 随日志落库才可见, 过程中切了几次、每次错在哪都看不到。
func TestRequestState_appendAttemptPublishes(t *testing.T) {
	setupRelayLogTest(t)
	request := newRequestState(context.Background(), "grp-x", 1, model.ProtocolOpenAIChatCompletion, false, "{}", 0, "ua", "")

	stream, stop := watchRequestState()
	defer stop()

	request.appendAttempt(model.ChannelAttempt{ChannelName: "渠道A", AttemptNum: 1, Status: model.AttemptFailed, Msg: "boom"})
	request.appendAttempt(model.ChannelAttempt{ChannelName: "渠道B", AttemptNum: 2, Status: model.AttemptSuccess, Duration: 120})

	var latest RequestState
	for i := 0; i < 2; i++ {
		select {
		case latest = <-stream:
		case <-time.After(time.Second):
			t.Fatalf("第 %d 次尝试没有触发推送", i+1)
		}
	}
	if len(latest.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(latest.Attempts))
	}
	if latest.Attempts[0].ChannelName != "渠道A" || latest.Attempts[0].Status != model.AttemptFailed || latest.Attempts[0].Msg != "boom" {
		t.Errorf("first attempt = %+v", latest.Attempts[0])
	}
	if latest.Attempts[1].Status != model.AttemptSuccess || latest.Attempts[1].Duration != 120 {
		t.Errorf("second attempt = %+v", latest.Attempts[1])
	}
}
