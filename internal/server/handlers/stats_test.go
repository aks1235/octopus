package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
)

// statsRankResponse 是 /stats/rank 响应 data 的解码形状, 与 op.StatsDailyRank 对齐。
type statsRankResponse struct {
	Available bool `json:"available"`
	Channels  []struct {
		ChannelID int    `json:"channel_id"`
		Name      string `json:"name"`
		model.StatsMetrics
	} `json:"channels"`
	Models []struct {
		Name string `json:"name"`
		model.StatsMetrics
	} `json:"models"`
}

// seedStatsHandlerDB 建独立测试库并灌两天的转发日志, 供排名端点做按日聚合断言。
func seedStatsHandlerDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "stats-handler-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	now := time.Now()
	dayA := now.AddDate(0, 0, -1).Truncate(24 * time.Hour).Add(10 * time.Hour) // 昨天上午, 保证落在昨天内
	dayB := now.AddDate(0, 0, -2).Truncate(24 * time.Hour).Add(10 * time.Hour) // 前天上午

	logs := []model.RelayLog{
		// 昨天渠道 alpha 两笔: 一成一败, 模型 gpt-4o。
		{ID: 101, Time: dayA.Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 100, OutputTokens: 10, Cost: 1.5},
		{ID: 102, Time: dayA.Unix() + 60, ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 50, OutputTokens: 5, Cost: 0.5, Error: "boom"},
		// 昨天渠道 beta 一笔成功, 模型 claude-3。
		{ID: 103, Time: dayA.Unix() + 120, ChannelId: 2, ChannelName: "beta", RequestModelName: "claude-3",
			InputTokens: 200, OutputTokens: 20, Cost: 2.0},
		// 前天渠道 alpha 一笔成功: 不应混进昨天的聚合。
		{ID: 104, Time: dayB.Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 999, OutputTokens: 99, Cost: 9.9},
		// 今天一笔: date 缺省(=今天)时应命中。
		{ID: 105, Time: now.Unix(), ChannelId: 3, ChannelName: "gamma", RequestModelName: "gpt-4o",
			InputTokens: 7, OutputTokens: 1, Cost: 0.1},
	}
	if err := db.GetDB().CreateInBatches(&logs, 10).Error; err != nil {
		t.Fatalf("seed relay logs: %v", err)
	}

	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatalf("UserInit() error = %v", err)
	}
}

// doStatsRequest 复用共享测试引擎发 GET 请求, 把 data 字段解码到 out。
func doStatsRequest(t *testing.T, engine *gin.Engine, path string, out any) int {
	t.Helper()
	code, body := doOrderRequest(t, engine, http.MethodGet, path, nil)
	if out == nil {
		return code
	}
	data, err := json.Marshal(body["data"])
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal data %q: %v", string(data), err)
	}
	return code
}

// TestStatsRank_aggregatesByDay 锁定按日聚合正确性: 两天日志互不串台, 各指标求和与成败计数正确。
func TestStatsRank_aggregatesByDay(t *testing.T) {
	seedStatsHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	var rank statsRankResponse
	code := doStatsRequest(t, engine, fmt.Sprintf("/api/v1/stats/rank?date=%s", yesterday), &rank)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !rank.Available {
		t.Fatalf("available = false, want true for a retained date")
	}

	if len(rank.Channels) != 2 {
		t.Fatalf("channels = %d, want 2 (前天那笔不得混入)", len(rank.Channels))
	}
	// 顺序按请求数倒序: alpha 2 笔在前。
	byName := map[string]int{}
	for i, ch := range rank.Channels {
		byName[ch.Name] = i
	}
	alpha := rank.Channels[byName["alpha"]]
	beta := rank.Channels[byName["beta"]]
	if alpha.ChannelID != 1 || alpha.RequestSuccess != 1 || alpha.RequestFailed != 1 ||
		alpha.InputToken != 150 || alpha.OutputToken != 15 || alpha.InputCost != 2.0 {
		t.Fatalf("alpha = %+v, want 1成功1失败/150/15/2.0", alpha)
	}
	if beta.ChannelID != 2 || beta.RequestSuccess != 1 || beta.RequestFailed != 0 ||
		beta.InputToken != 200 || beta.OutputToken != 20 {
		t.Fatalf("beta = %+v, want 1成功/200/20", beta)
	}

	if len(rank.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(rank.Models))
	}
	byModel := map[string]int{}
	for i, m := range rank.Models {
		byModel[m.Name] = i
	}
	gpt := rank.Models[byModel["gpt-4o"]]
	if gpt.RequestSuccess != 1 || gpt.RequestFailed != 1 || gpt.InputToken != 150 {
		t.Fatalf("gpt-4o = %+v, want 前天那笔不计入", gpt)
	}

	// 前天: 只有 alpha 的一笔。
	dayBefore := time.Now().AddDate(0, 0, -2).Format("20060102")
	var rankB statsRankResponse
	if code := doStatsRequest(t, engine, fmt.Sprintf("/api/v1/stats/rank?date=%s", dayBefore), &rankB); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(rankB.Channels) != 1 || rankB.Channels[0].InputToken != 999 {
		t.Fatalf("dayB channels = %+v, want only alpha's 999-input row", rankB.Channels)
	}
}

// TestStatsRank_defaultsToToday 锁定 date 缺省语义: 不带参数即按今天聚合。
func TestStatsRank_defaultsToToday(t *testing.T) {
	seedStatsHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	var rank statsRankResponse
	if code := doStatsRequest(t, engine, "/api/v1/stats/rank", &rank); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !rank.Available || len(rank.Channels) != 1 || rank.Channels[0].Name != "gamma" {
		t.Fatalf("rank = %+v, want today's gamma only", rank)
	}
}

// TestStatsRank_beyondRetention 锁定保留期边界: 整日落入保留期之外时 available=false 且数组为空。
func TestStatsRank_beyondRetention(t *testing.T) {
	seedStatsHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	// 收紧保留期为 1 天: 前天整天(次日 0 点 <= cutoff)不可用。
	if err := op.SettingSetInt(model.SettingKeyRelayLogKeepPeriod, 1); err != nil {
		t.Fatalf("set keep period: %v", err)
	}

	dayBefore := time.Now().AddDate(0, 0, -2).Format("20060102")
	var expired statsRankResponse
	if code := doStatsRequest(t, engine, fmt.Sprintf("/api/v1/stats/rank?date=%s", dayBefore), &expired); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if expired.Available {
		t.Fatalf("available = true, want false beyond retention")
	}
	if expired.Channels == nil || expired.Models == nil {
		t.Fatalf("arrays must be non-nil, got channels=%v models=%v", expired.Channels == nil, expired.Models == nil)
	}
	if len(expired.Channels) != 0 || len(expired.Models) != 0 {
		t.Fatalf("expired date must return empty arrays, got %d/%d", len(expired.Channels), len(expired.Models))
	}

	// 保留期内(昨天部分落入 1 天窗口)仍可用。
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	var retained statsRankResponse
	if code := doStatsRequest(t, engine, fmt.Sprintf("/api/v1/stats/rank?date=%s", yesterday), &retained); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !retained.Available {
		t.Fatalf("available = false, want true for partially retained day")
	}
}

// TestStatsHourly_invalidDate 锁定 date 参数校验: 非法格式 400。
func TestStatsHourly_invalidDate(t *testing.T) {
	seedStatsHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	if code := doStatsRequest(t, engine, "/api/v1/stats/hourly?date=2026-09-18", nil); code != http.StatusBadRequest {
		t.Fatalf("hourly status = %d, want 400", code)
	}
	if code := doStatsRequest(t, engine, "/api/v1/stats/rank?date=notadate", nil); code != http.StatusBadRequest {
		t.Fatalf("rank status = %d, want 400", code)
	}
}
