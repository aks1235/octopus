package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
)

// setupRelayLogTest 初始化数据库与缓存, relayLogFinalize 经 op.RelayLogAdd 落库, 断言直接查库。
func setupRelayLogTest(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "relay-log-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// flushAndLoad 走生产 flush 路径把缓冲落库后取全部行, 断言基于真实持久化链路。
func flushAndLoad(t *testing.T) []model.RelayLog {
	t.Helper()
	op.RelayLogSaveDBTask()
	var logs []model.RelayLog
	if err := db.GetDB().Order("id").Find(&logs).Error; err != nil {
		t.Fatalf("load relay logs: %v", err)
	}
	return logs
}

// TestRelayLogFinalize_assemblesFromTerminalState 验证终态组装语义:
// 最终渠道取最后一次成功尝试、无成功取最后一次、token/cost 取请求状态定稿值、空 attempts 允许。
func TestRelayLogFinalize_assemblesFromTerminalState(t *testing.T) {
	setupRelayLogTest(t)

	attempts := []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch-a", ModelName: "m-a", AttemptNum: 1, Status: model.AttemptFailed, Msg: "timeout"},
		{ChannelID: 2, ChannelName: "ch-b", ModelName: "m-b", AttemptNum: 2, Status: model.AttemptSuccess},
	}
	request := &RequestState{
		ID:        1,
		Status:    StatusSuccess,
		StartedAt: time.Now().Add(-2 * time.Second),
		Duration:  2 * time.Second,
		Model:     "grp-x",
		Usage: llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			PromptTokensDetails: &llm.PromptTokensDetails{
				CachedTokens:     20,
				WriteCachedTokens: 10,
			},
		},
		Cost: 0.125,
		body:         "RAW-REQUEST",
		responseBody: "RESP-BODY",
	}
	request.Error = ""

	firstValidAt := time.Now().Add(-1 * time.Second)

	relayLogFinalize(request, "grp-x", attempts, 0, "ua-test", firstValidAt)

	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	got := logs[0]

	if got.RequestModelName != "grp-x" {
		t.Errorf("request model = %q, want grp-x", got.RequestModelName)
	}
	// 最终渠道取最后一次成功尝试(ch-b/m-b), 而非最后一条记录。
	if got.ChannelId != 2 || got.ChannelName != "ch-b" || got.ActualModelName != "m-b" {
		t.Errorf("final channel = %d/%q/%q, want 2/ch-b/m-b", got.ChannelId, got.ChannelName, got.ActualModelName)
	}
	if got.TotalAttempts != 2 || len(got.Attempts) != 2 {
		t.Errorf("attempts = %d/%d, want 2/2", got.TotalAttempts, len(got.Attempts))
	}
	if got.InputTokens != 100 || got.OutputTokens != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", got.InputTokens, got.OutputTokens)
	}
	if got.CachedTokens != 20 || got.CacheCreationTokens != 10 {
		t.Errorf("cached tokens = %d/%d, want 20/10", got.CachedTokens, got.CacheCreationTokens)
	}
	if got.Cost != 0.125 {
		t.Errorf("cost = %v, want 0.125", got.Cost)
	}
	if got.RequestContent != "RAW-REQUEST" || got.ResponseContent != "RESP-BODY" {
		t.Errorf("content = %q/%q, want RAW-REQUEST/RESP-BODY", got.RequestContent, got.ResponseContent)
	}
	if got.UserAgent != "ua-test" {
		t.Errorf("user agent = %q, want ua-test", got.UserAgent)
	}
	if got.Ftut <= 0 {
		t.Errorf("ftut = %d, want > 0", got.Ftut)
	}
	if got.UseTime < 1900 || got.UseTime > 2200 {
		t.Errorf("use time = %d ms, want ~2000", got.UseTime)
	}
}

// TestRelayLogFinalize_allFailedFallsBackToLastAttempt 全部失败时最终渠道取最后一次尝试。
func TestRelayLogFinalize_allFailedFallsBackToLastAttempt(t *testing.T) {
	setupRelayLogTest(t)

	attempts := []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch-a", ModelName: "m-a", AttemptNum: 1, Status: model.AttemptFailed},
		{ChannelID: 2, ChannelName: "ch-b", ModelName: "m-b", AttemptNum: 2, Status: model.AttemptFailed},
	}
	request := &RequestState{
		ID:        2,
		Status:    StatusFailed,
		StartedAt: time.Now().Add(-time.Second),
		Duration:  time.Second,
		Model:     "grp-y",
	}
	request.Error = "all members failed"

	relayLogFinalize(request, "grp-y", attempts, 0, "", time.Time{})

	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	l := logs[0]
	if l.ChannelId != 2 || l.ChannelName != "ch-b" || l.ActualModelName != "m-b" {
		t.Errorf("fallback channel = %d/%q/%q, want 2/ch-b/m-b", l.ChannelId, l.ChannelName, l.ActualModelName)
	}
	if l.Error != "all members failed" {
		t.Errorf("error = %q", l.Error)
	}
	if l.Ftut != 0 {
		t.Errorf("ftut = %d, want 0 when no valid response", l.Ftut)
	}
}

// TestRelayLogFinalize_emptyAttemptsModelFallback 空 attempts(未发起任何上游调用)时
// 模型名回退为请求模型名, 渠道为零值。
func TestRelayLogFinalize_emptyAttemptsModelFallback(t *testing.T) {
	setupRelayLogTest(t)

	request := &RequestState{
		ID:        3,
		Status:    StatusCanceled,
		StartedAt: time.Now(),
		Model:     "grp-z",
	}
	request.Error = "context canceled"

	relayLogFinalize(request, "grp-z", nil, 0, "", time.Time{})

	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	l := logs[0]
	if l.ChannelId != 0 || l.ChannelName != "" {
		t.Errorf("channel = %d/%q, want zero", l.ChannelId, l.ChannelName)
	}
	if l.ActualModelName != "grp-z" {
		t.Errorf("actual model = %q, want fallback grp-z", l.ActualModelName)
	}
	if l.TotalAttempts != 0 {
		t.Errorf("total attempts = %d, want 0", l.TotalAttempts)
	}
}
