package op

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func TestRelayLogList_apiKeyNamesFilter(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// 填充缓存（ID 越大 = 时间越新，返回时按缓存倒序）
	relayLogCacheLock.Lock()
	relayLogCache = []model.RelayLog{
		{ID: 1, Time: 100, RequestAPIKeyName: "key-alpha", RequestModelName: "gpt-4"},
		{ID: 2, Time: 99, RequestAPIKeyName: "key-beta", RequestModelName: "gpt-4"},
		{ID: 3, Time: 98, RequestAPIKeyName: "key-alpha", RequestModelName: "claude-3"},
		{ID: 4, Time: 97, RequestAPIKeyName: "key-gamma", RequestModelName: "gpt-4", Error: "timeout"},
		{ID: 5, Time: 96, RequestAPIKeyName: "", RequestModelName: "gpt-4"},
	}
	relayLogCacheLock.Unlock()

	tests := []struct {
		name        string
		apiKeyNames []string
		modelNames  []string
		hasError    bool
		expectedIDs []int64
	}{
		{
			name:        "filter by single api key name",
			apiKeyNames: []string{"key-alpha"},
			expectedIDs: []int64{3, 1}, // 反转缓存顺序
		},
		{
			name:        "filter by multiple api key names",
			apiKeyNames: []string{"key-alpha", "key-gamma"},
			expectedIDs: []int64{4, 3, 1},
		},
		{
			name:        "filter by single model name",
			modelNames:  []string{"gpt-4"},
			expectedIDs: []int64{5, 4, 2, 1},
		},
		{
			name:        "filter by multiple model names",
			modelNames:  []string{"gpt-4", "claude-3"},
			expectedIDs: []int64{5, 4, 3, 2, 1},
		},
		{
			name:        "filter by api key name and model name",
			apiKeyNames: []string{"key-alpha"},
			modelNames:  []string{"gpt-4"},
			expectedIDs: []int64{1},
		},
		{
			name:        "filter by api key name and hasError",
			apiKeyNames: []string{"key-gamma"},
			hasError:    true,
			expectedIDs: []int64{4},
		},
		{
			name:        "no filter returns all",
			expectedIDs: []int64{5, 4, 3, 2, 1},
		},
		{
			name:        "filter by non-existent api key name",
			apiKeyNames: []string{"nonexistent"},
			expectedIDs: []int64{},
		},
		{
			name:        "empty api key name matches logs with empty key name",
			apiKeyNames: []string{""},
			expectedIDs: []int64{5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs, err := RelayLogList(ctx, nil, nil, 1, 100, tt.hasError, tt.apiKeyNames, tt.modelNames)
			if err != nil {
				t.Fatalf("RelayLogList() error = %v", err)
			}

			gotIDs := idsOf(logs)
			if len(gotIDs) != len(tt.expectedIDs) {
				t.Fatalf("expected IDs %v, got %v", tt.expectedIDs, gotIDs)
			}
			for i, id := range gotIDs {
				if id != tt.expectedIDs[i] {
					t.Errorf("index %d: expected ID %d, got %d", i, tt.expectedIDs[i], id)
				}
			}
		})
	}
}

func TestRelayLogList_modelNamesFilter(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	relayLogCacheLock.Lock()
	relayLogCache = []model.RelayLog{
		{ID: 10, Time: 100, RequestAPIKeyName: "key-a", RequestModelName: "gpt-4o"},
		{ID: 11, Time: 99, RequestAPIKeyName: "key-a", RequestModelName: "gpt-4"},
		{ID: 12, Time: 98, RequestAPIKeyName: "key-b", RequestModelName: "claude-3-opus"},
		{ID: 13, Time: 97, RequestAPIKeyName: "key-b", RequestModelName: "claude-3-sonnet"},
	}
	relayLogCacheLock.Unlock()

	tests := []struct {
		name        string
		modelNames  []string
		expectedIDs []int64
	}{
		{
			name:        "single model",
			modelNames:  []string{"gpt-4o"},
			expectedIDs: []int64{10},
		},
		{
			name:        "multiple models",
			modelNames:  []string{"gpt-4o", "claude-3-opus"},
			expectedIDs: []int64{12, 10}, // 反转缓存顺序
		},
		{
			name:        "non-existent model",
			modelNames:  []string{"nonexistent"},
			expectedIDs: []int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs, err := RelayLogList(ctx, nil, nil, 1, 100, false, nil, tt.modelNames)
			if err != nil {
				t.Fatalf("RelayLogList() error = %v", err)
			}

			gotIDs := idsOf(logs)
			if len(gotIDs) != len(tt.expectedIDs) {
				t.Fatalf("expected IDs %v, got %v", tt.expectedIDs, gotIDs)
			}
			for i, id := range gotIDs {
				if id != tt.expectedIDs[i] {
					t.Errorf("index %d: expected ID %d, got %d", i, tt.expectedIDs[i], id)
				}
			}
		})
	}
}

func TestRelayLogList_combinedFilter(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	relayLogCacheLock.Lock()
	relayLogCache = []model.RelayLog{
		{ID: 20, Time: 100, RequestAPIKeyName: "admin", RequestModelName: "gpt-4"},
		{ID: 21, Time: 99, RequestAPIKeyName: "admin", RequestModelName: "gpt-4", Error: "rate limit"},
		{ID: 22, Time: 98, RequestAPIKeyName: "user", RequestModelName: "gpt-4"},
		{ID: 23, Time: 97, RequestAPIKeyName: "admin", RequestModelName: "claude-3"},
		{ID: 24, Time: 96, RequestAPIKeyName: "user", RequestModelName: "claude-3", Error: "timeout"},
	}
	relayLogCacheLock.Unlock()

	// admin key + error only → ID 21
	logs, err := RelayLogList(ctx, nil, nil, 1, 100, true, []string{"admin"}, nil)
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 1 || logs[0].ID != 21 {
		t.Errorf("expected [21], got IDs %v", idsOf(logs))
	}

	// admin key + gpt-4 model → ID 21, 20 (reversed cache order)
	logs, err = RelayLogList(ctx, nil, nil, 1, 100, false, []string{"admin"}, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 logs, got %d", len(logs))
	}

	// user key + claude-3 model + error → ID 24
	logs, err = RelayLogList(ctx, nil, nil, 1, 100, true, []string{"user"}, []string{"claude-3"})
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 1 || logs[0].ID != 24 {
		t.Errorf("expected [24], got IDs %v", idsOf(logs))
	}
}

func TestRelayLogList_pagination(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 5)
	for i := 0; i < 5; i++ {
		relayLogCache[i] = model.RelayLog{
			ID:                int64(i + 1),
			Time:              int64(100 - i),
			RequestAPIKeyName: "key-a",
			RequestModelName:  "gpt-4",
		}
	}
	relayLogCacheLock.Unlock()

	// 不筛选，page=1, pageSize=2
	logs, err := RelayLogList(ctx, nil, nil, 1, 2, false, nil, nil)
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("page 1: expected 2 logs, got %d", len(logs))
	}

	// 筛选 api key，page=1, pageSize=2
	logs, err = RelayLogList(ctx, nil, nil, 1, 2, false, []string{"key-a"}, nil)
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("filtered page 1: expected 2 logs, got %d", len(logs))
	}

	// 筛选不匹配的 api key，应返回 0 条
	logs, err = RelayLogList(ctx, nil, nil, 1, 2, false, []string{"nonexistent"}, nil)
	if err != nil {
		t.Fatalf("RelayLogList() error = %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("non-matching filter: expected 0 logs, got %d", len(logs))
	}
}

func idsOf(logs []model.RelayLog) []int64 {
	ids := make([]int64, len(logs))
	for i, l := range logs {
		ids[i] = l.ID
	}
	return ids
}

func setupLogTestDB(t *testing.T) {
	t.Helper()

	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "log-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		relayLogCacheLock.Lock()
		relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
		relayLogCacheLock.Unlock()
	})

	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// ============================================================================
// RelayLogAttemptsByChannel — 按渠道查规范化 attempts 明细
// ============================================================================

// seedFlushedLogs 走生产落盘路径(relayLogCache → relayLogFlushToDB)写入日志与 attempts 规范化行,
// 让查询类用例的数据形状与线上一致(日志行与其尝试行同源同事务), 而不是只塞 relay_logs 表。
func seedFlushedLogs(t *testing.T, logs []model.RelayLog) {
	t.Helper()
	const chunk = 500
	for start := 0; start < len(logs); start += chunk {
		end := start + chunk
		if end > len(logs) {
			end = len(logs)
		}
		relayLogCacheLock.Lock()
		relayLogCache = append(relayLogCache[:0], logs[start:end]...)
		relayLogCacheLock.Unlock()
		if err := relayLogFlushToDB(context.Background()); err != nil {
			t.Fatalf("flush relay logs: %v", err)
		}
	}
}

// countAttempts 统计某渠道的规范化行数, 供写入/清理/回填用例断言。
func countAttempts(t *testing.T, where string, args ...interface{}) int64 {
	t.Helper()
	var count int64
	query := db.GetDB().Model(&model.RelayLogAttempt{})
	if where != "" {
		query = query.Where(where, args...)
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count relay_log_attempts: %v", err)
	}
	return count
}

func TestRelayLogAttemptsByChannel_filtersAndPaginates(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// keep_enabled 默认 true, keep_period 默认 7 天, 造的行不会因 cutoff 被排除。
	now := time.Now().Unix()
	rows := []model.RelayLog{
		{
			ID: 100, Time: now - 10, RequestModelName: "grp-a",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m-x", AttemptNum: 1, Status: model.AttemptFailed, Msg: "timeout"},
				{ChannelID: 8, ChannelName: "ch-8", ModelName: "m-x", AttemptNum: 2, Status: model.AttemptSuccess},
			},
		},
		{
			ID: 101, Time: now - 5, RequestModelName: "grp-a", Error: "all failed",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m-x", AttemptNum: 1, Status: model.AttemptFailed, Msg: "500"},
			},
		},
	}
	seedFlushedLogs(t, rows)

	// 渠道 7: 两行各一条, 按时间倒序 → 101 在前
	list, total, truncated, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if truncated {
		t.Errorf("unexpected truncated: 规范化后分页不再截断")
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("expected total=2 len=2, got total=%d len=%d", total, len(list))
	}
	if list[0].RequestID != 101 || list[0].Status != model.AttemptFailed {
		t.Errorf("newest first expected request 101 failed, got %+v", list[0])
	}
	if list[0].RequestError != "all failed" {
		t.Errorf("request-level error not carried: %+v", list[0])
	}
	if list[1].RequestID != 100 || list[1].Status != model.AttemptFailed {
		t.Errorf("second expected request 100 failed, got %+v", list[1])
	}

	// 渠道 8: 只有一条成功明细
	list, total, _, err = RelayLogAttemptsByChannel(ctx, 8, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Status != model.AttemptSuccess {
		t.Fatalf("channel 8 expected 1 success, got total=%d list=%+v", total, list)
	}

	// 分页: 渠道 7 每页 1 条, 第 2 页应剩 1 条
	list, total, _, err = RelayLogAttemptsByChannel(ctx, 7, 2, 1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 2 || len(list) != 1 || list[0].RequestID != 100 {
		t.Fatalf("page 2 expected request 100, got total=%d list=%+v", total, list)
	}

	// 不存在的渠道: 空
	list, total, _, err = RelayLogAttemptsByChannel(ctx, 999, 1, 50)
	if err != nil {
		t.Fatalf("unknown channel: %v", err)
	}
	if total != 0 || list != nil {
		t.Fatalf("unknown channel expected empty, got total=%d list=%+v", total, list)
	}
}

// TestRelayLogAttemptsByChannel_onlyTargetChannel 验证明细只含目标渠道的尝试:
// 同一请求里打过别的渠道(含 7 的前缀大 ID 71/700)时, 只返回 7 的那几次, 不整行返回。
func TestRelayLogAttemptsByChannel_onlyTargetChannel(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{
			// 同一行含 71/700(前缀误匹配源)与 7(真目标): 只应返回 7 的 attempt。
			ID: 300, Time: now,
			Attempts: []model.ChannelAttempt{
				{ChannelID: 71, ChannelName: "ch-71", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 2, Status: model.AttemptSuccess},
			},
		},
		{
			// 不含目标渠道的行: 明细里应零命中。
			ID: 301, Time: now,
			Attempts: []model.ChannelAttempt{
				{ChannelID: 700, ChannelName: "ch-700", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
			},
		},
	}
	seedFlushedLogs(t, rows)

	list, total, truncated, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if truncated {
		t.Errorf("unexpected truncated")
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("expected exactly 1 precise match, got total=%d len=%d", total, len(list))
	}
	if list[0].RequestID != 300 || list[0].ChannelID != 7 || list[0].Status != model.AttemptSuccess {
		t.Errorf("precise match expected request 300 channel 7 success, got %+v", list[0])
	}
}

// TestRelayLogAttemptsByChannel_disabledKeep 验证关闭日志保存后返回空(DB 不再有历史语义)。
func TestRelayLogAttemptsByChannel_disabledKeep(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	if err := SettingSetString(model.SettingKeyRelayLogKeepEnabled, "false"); err != nil {
		t.Fatalf("disable keep: %v", err)
	}

	list, total, _, err := RelayLogAttemptsByChannel(ctx, 1, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if total != 0 || list != nil {
		t.Fatalf("disabled keep expected empty, got total=%d list=%+v", total, list)
	}
}

// ============================================================================
// relayLogCleanup — 保留期清理
// ============================================================================

func TestRelayLogCleanup_deletesExpired(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// keep_period 默认 7 天
	now := time.Now().Unix()
	rows := []model.RelayLog{
		{ID: 1, Time: now - 8*24*3600}, // 过期
		{ID: 2, Time: now - 6*24*3600}, // 保留
		{ID: 3, Time: now - 60},        // 新
	}
	if err := db.GetDB().CreateInBatches(&rows, 10).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup() error = %v", err)
	}

	var count int64
	if err := db.GetDB().Model(&model.RelayLog{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows kept, got %d", count)
	}
	var ids []int64
	if err := db.GetDB().Model(&model.RelayLog{}).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatalf("pluck: %v", err)
	}
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("expected ids [2 3], got %v", ids)
	}
}

// TestRelayLogCleanup_foldsDailyAndKeepsHourly 锁定统计永久化的清理联动:
//  1. 删日志前先折叠被删日期的渠道/模型汇总(账不丢);
//  2. 曲线小时行不再随日志保留期清理(小时行仍在)。
func TestRelayLogCleanup_foldsDailyAndKeepsHourly(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	if err := SettingSetInt(model.SettingKeyRelayLogKeepPeriod, 1); err != nil {
		t.Fatalf("set keep period: %v", err)
	}

	oldDate := at(3, 0).Format("20060102")
	oldTime := at(3, 8)
	// 过期日的日志(整日淘汰)。
	logs := []model.RelayLog{
		{ID: 21, Time: oldTime.Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 40, OutputTokens: 4, Cost: 0.4},
		{ID: 22, Time: oldTime.Add(time.Minute).Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 60, OutputTokens: 6, Cost: 0.6},
	}
	if err := db.GetDB().CreateInBatches(&logs, 10).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	// 过期日的小时行(永久留存)。
	hourly := model.StatsHourly{Date: oldDate, Hour: 8, StatsMetrics: model.StatsMetrics{RequestSuccess: 2, InputToken: 100}}
	if err := db.GetDB().Create(&hourly).Error; err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup() error = %v", err)
	}

	// 1. 过期日志被删。
	var logCount int64
	if err := db.GetDB().Model(&model.RelayLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logCount != 0 {
		t.Fatalf("relay logs = %d, want 0 (过期日志应被清理)", logCount)
	}

	// 2. 曲线小时行仍在(不再跟随日志保留期)。
	var hourlyRows []model.StatsHourly
	if err := db.GetDB().WithContext(ctx).Find(&hourlyRows).Error; err != nil {
		t.Fatalf("load hourly: %v", err)
	}
	if len(hourlyRows) != 1 || hourlyRows[0].Date != oldDate || hourlyRows[0].Hour != 8 || hourlyRows[0].InputToken != 100 {
		t.Fatalf("hourly rows = %+v, want the expired day row kept", hourlyRows)
	}

	// 3. 被删日期已折叠入库: 删日志后仍可从汇总表读出(账不丢)。
	rank, err := StatsRankDaily(ctx, oldDate)
	if err != nil {
		t.Fatalf("StatsRankDaily() error = %v", err)
	}
	if !rank.Available || len(rank.Channels) != 1 ||
		rank.Channels[0].InputToken != 100 || rank.Channels[0].RequestSuccess != 2 {
		t.Fatalf("rank = %+v, want folded alpha 100 input / 2 success", rank)
	}
}

// TestRelayLogDistinctDatesBefore_rangeAndEmpty 锁定 R1 的时区无关实现:
// 待封账日期由 MIN(time) 在 Go 侧按本地日逐日展开(不再用 SQL 方言折算), 无日志时为空。
func TestRelayLogDistinctDatesBefore_rangeAndEmpty(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	cutoff := at(1, 0).Unix() // 昨天 0 点, 与 relayLogCleanup 的整日口径一致

	// 空库: 无早于 cutoff 的日志 → 无待封账日期。
	dates, err := relayLogDistinctDatesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("RelayLogDistinctDatesBefore() error = %v", err)
	}
	if len(dates) != 0 {
		t.Fatalf("empty db: want no dates, got %v", dates)
	}

	// 只有今天(>= cutoff)的日志同样不算待封账。
	if err := db.GetDB().Create(&model.RelayLog{ID: 1, Time: at(0, 8).Unix()}).Error; err != nil {
		t.Fatalf("seed today log: %v", err)
	}
	dates, err = relayLogDistinctDatesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("RelayLogDistinctDatesBefore() error = %v", err)
	}
	if len(dates) != 0 {
		t.Fatalf("only-today logs: want no dates, got %v", dates)
	}

	// 补 3 天前与 2 天前的日志 → 从最早日志的本地日逐日覆盖到 cutoff 所在日(含)。
	old := []model.RelayLog{
		{ID: 2, Time: at(3, 8).Unix()},
		{ID: 3, Time: at(3, 20).Unix()},
		{ID: 4, Time: at(2, 1).Unix()},
	}
	if err := db.GetDB().CreateInBatches(&old, 10).Error; err != nil {
		t.Fatalf("seed old logs: %v", err)
	}
	dates, err = relayLogDistinctDatesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("RelayLogDistinctDatesBefore() error = %v", err)
	}
	want := []string{at(3, 0).Format("20060102"), at(2, 0).Format("20060102"), at(1, 0).Format("20060102")}
	if len(dates) != len(want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("dates = %v, want %v", dates, want)
		}
	}
}

// ============================================================================
// 缓存+DB 合并分页 / LIKE 误匹配精确过滤 / truncated 上界
// ============================================================================

// TestRelayLogList_mergesCacheAndDB 验证列表的缓存+DB 合并分页:
// 缓冲中的最新日志倒序在前, 不满一页时由 DB 按 id 倒序补齐, 跨界页的 DB 偏移正确。
func TestRelayLogList_mergesCacheAndDB(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// DB 侧: 5 条已落库的旧日志(id 越大越新)。
	now := time.Now().Unix()
	dbRows := make([]model.RelayLog, 0, 5)
	for i := 0; i < 5; i++ {
		dbRows = append(dbRows, model.RelayLog{
			ID:                int64(100 + i),
			Time:              now - int64(100-i)*60,
			RequestAPIKeyName: "key-db",
			RequestModelName:  "gpt-4",
		})
	}
	if err := db.GetDB().CreateInBatches(&dbRows, 10).Error; err != nil {
		t.Fatalf("seed db rows: %v", err)
	}

	// 缓存侧: 3 条更新的待 flush 日志(append 顺序 = 时间升序)。
	relayLogCacheLock.Lock()
	relayLogCache = []model.RelayLog{
		{ID: 200, Time: now - 3, RequestAPIKeyName: "key-cache", RequestModelName: "gpt-4"},
		{ID: 201, Time: now - 2, RequestAPIKeyName: "key-cache", RequestModelName: "gpt-4"},
		{ID: 202, Time: now - 1, RequestAPIKeyName: "key-cache", RequestModelName: "gpt-4"},
	}
	relayLogCacheLock.Unlock()

	assertIDs := func(page int, want []int64) {
		t.Helper()
		logs, err := RelayLogList(ctx, nil, nil, page, 4, false, nil, nil)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		got := idsOf(logs)
		if len(got) != len(want) {
			t.Fatalf("page %d: expected IDs %v, got %v", page, want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("page %d: expected IDs %v, got %v", page, want, got)
			}
		}
	}

	// page=1: 缓存 3 条倒序(202,201,200) + DB 补 1 条(id 最大=104)。
	assertIDs(1, []int64{202, 201, 200, 104})
	// page=2: 缓存耗尽, DB 偏移 = 4-3 = 1, 依次 103,102,101,100。
	assertIDs(2, []int64{103, 102, 101, 100})
}

// ============================================================================
// attempts 规范化 — 写入一致性 / 幂等 / 清理联动 / 回填 / 与旧实现等价 / 走索引
// ============================================================================

// legacyAttemptsByChannel 复刻规范化前实现的语义(从 attempts JSON 展开 → 按渠道精确过滤 →
// 按 (request_time DESC, request_id DESC, attempt_num ASC) 排序 → 内存分页),
// 仅用于等价性对拍: 新实现(索引查表 + SQL 分页)必须在同一数据上逐字段一致。
func legacyAttemptsByChannel(logs []model.RelayLog, channelID, page, pageSize int) ([]model.ChannelAttemptDetail, int) {
	matches := make([]model.ChannelAttemptDetail, 0)
	for _, relayLog := range logs {
		for _, a := range relayLog.Attempts {
			if a.ChannelID != channelID {
				continue
			}
			matches = append(matches, model.ChannelAttemptDetail{
				RequestID:     relayLog.ID,
				RequestTime:   relayLog.Time,
				RequestModel:  relayLog.RequestModelName,
				RequestError:  relayLog.Error,
				AttemptNum:    a.AttemptNum,
				Status:        a.Status,
				ChannelID:     a.ChannelID,
				ChannelName:   a.ChannelName,
				ChannelKeyRem: a.ChannelKeyRemark,
				ModelName:     a.ModelName,
				Duration:      a.Duration,
				Sticky:        a.Sticky,
				Msg:           a.Msg,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].RequestTime != matches[j].RequestTime {
			return matches[i].RequestTime > matches[j].RequestTime
		}
		return matches[i].RequestID > matches[j].RequestID
	})

	total := len(matches)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	offset := (page - 1) * pageSize
	if offset >= total {
		return nil, total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	return matches[offset:end], total
}

// TestRelayLogAttemptsByChannel_matchesLegacyExpansion 是 AC2 的对拍:
// 同一批数据下, 新实现(查规范化表)与旧实现(展开 JSON + 内存分页)的 list/total 逐字段一致,
// 覆盖排序(同秒不同请求、同请求多次尝试)、分页越界与 page/page_size 边界收敛。
func TestRelayLogAttemptsByChannel_matchesLegacyExpansion(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	now := time.Now().Unix()
	// 关键构造: 同一时间戳的两个请求(验 tie-break 用 log_id 倒序)、
	// 同一请求内同一渠道被尝试两次(验同请求内 attempt_num 升序)、
	// 7 与 71/700 前缀冲突 ID(验精确匹配)、空 attempts 的日志(两表都零行)。
	logs := []model.RelayLog{
		{
			ID: 9001, Time: now - 300, RequestModelName: "grp-a",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ChannelKeyID: 31, ChannelKeyRemark: "key-a", ModelName: "m-7", AttemptNum: 1, Status: model.AttemptFailed, Duration: 111, Msg: "boom"},
				{ChannelID: 8, ChannelName: "ch-8", ModelName: "m-8", AttemptNum: 2, Status: model.AttemptSuccess, Duration: 22, Sticky: true},
				{ChannelID: 7, ChannelName: "ch-7", ChannelKeyID: 32, ChannelKeyRemark: "key-b", ModelName: "m-7", AttemptNum: 3, Status: model.AttemptSuccess, Duration: 33},
			},
		},
		{ID: 9002, Time: now - 300, RequestModelName: "grp-b", Error: "all failed",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ChannelKeyID: 41, ChannelKeyRemark: "key-c", ModelName: "m-7x", AttemptNum: 1, Status: model.AttemptFailed, Duration: 44, Msg: "500"},
			},
		},
		{ID: 9003, Time: now - 200, RequestModelName: "grp-a"},
		{ID: 9004, Time: now - 100, RequestModelName: "grp-c",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 71, ChannelName: "ch-71", ModelName: "m-71", AttemptNum: 1, Status: model.AttemptFailed},
				{ChannelID: 700, ChannelName: "ch-700", ModelName: "m-700", AttemptNum: 2, Status: model.AttemptFailed},
				{ChannelID: 7, ChannelName: "ch-7", ChannelKeyID: 51, ChannelKeyRemark: "key-d", ModelName: "m-7y", AttemptNum: 3, Status: model.AttemptCircuitBreak, Duration: 55, Sticky: true, Msg: "circuit"},
			},
		},
	}
	seedFlushedLogs(t, logs)

	for _, tc := range []struct {
		channelID int
		page      int
		pageSize  int
	}{
		{7, 1, 50}, {7, 1, 2}, {7, 2, 2}, {7, 3, 2}, {7, 2, 1}, {7, 4, 1},
		{7, 1, 0}, {7, 0, 1}, {7, 1, 500}, // page/page_size 边界: 收敛规则须与旧实现一致
		{8, 1, 50}, {71, 1, 50}, {700, 1, 50}, {999, 1, 50},
	} {
		wantList, wantTotal := legacyAttemptsByChannel(logs, tc.channelID, tc.page, tc.pageSize)
		gotList, gotTotal, truncated, err := RelayLogAttemptsByChannel(ctx, tc.channelID, tc.page, tc.pageSize)
		if err != nil {
			t.Fatalf("channel=%d page=%d size=%d: %v", tc.channelID, tc.page, tc.pageSize, err)
		}
		if truncated {
			t.Errorf("channel=%d page=%d size=%d: 规范化后不应再有 truncated", tc.channelID, tc.page, tc.pageSize)
		}
		if gotTotal != wantTotal {
			t.Fatalf("channel=%d page=%d size=%d: total = %d, want %d", tc.channelID, tc.page, tc.pageSize, gotTotal, wantTotal)
		}
		if !reflect.DeepEqual(gotList, wantList) {
			t.Fatalf("channel=%d page=%d size=%d:\n got %+v\nwant %+v", tc.channelID, tc.page, tc.pageSize, gotList, wantList)
		}
	}
}

// TestRelayLogFlushToDB_writesAttempts 是 AC3:
// 日志落盘后规范化行齐全(条数与字段与 attempts JSON 一致), 且重复落盘同一日志不产生重复行(幂等)。
func TestRelayLogFlushToDB_writesAttempts(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	now := time.Now().Unix()
	logs := []model.RelayLog{
		{ID: 8801, Time: now - 30, RequestModelName: "grp-a",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelKeyID: 11, ChannelName: "ch-7", ChannelKeyRemark: "key-a", ModelName: "m-a",
					AttemptNum: 1, Status: model.AttemptFailed, Duration: 120, Sticky: false, Msg: "timeout"},
				{ChannelID: 8, ChannelKeyID: 12, ChannelName: "ch-8", ChannelKeyRemark: "key-b", ModelName: "m-b",
					AttemptNum: 2, Status: model.AttemptSuccess, Duration: 30, Sticky: true},
			},
		},
		{ID: 8802, Time: now - 20, RequestModelName: "grp-a"}, // 空 attempts: 明细表零行
	}
	seedFlushedLogs(t, logs)

	var rows []model.RelayLogAttempt
	if err := db.GetDB().WithContext(ctx).Order("log_id, attempt_num").Find(&rows).Error; err != nil {
		t.Fatalf("load attempts: %v", err)
	}
	want := []model.RelayLogAttempt{
		{LogID: 8801, AttemptNum: 1, ChannelID: 7, Time: now - 30, ChannelName: "ch-7", ChannelKeyID: 11,
			ChannelKeyRemark: "key-a", ModelName: "m-a", Status: model.AttemptFailed, Duration: 120, Msg: "timeout"},
		{LogID: 8801, AttemptNum: 2, ChannelID: 8, Time: now - 30, ChannelName: "ch-8", ChannelKeyID: 12,
			ChannelKeyRemark: "key-b", ModelName: "m-b", Status: model.AttemptSuccess, Duration: 30, Sticky: true},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("attempts rows:\n got %+v\nwant %+v", rows, want)
	}

	// 幂等: 同一批日志再展开写一次(模拟重试/回填重跑)不产生重复行, 也不报错。
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return relayLogAttemptsInsert(tx, logs)
	}); err != nil {
		t.Fatalf("re-insert attempts: %v", err)
	}
	if got := countAttempts(t, ""); got != 2 {
		t.Fatalf("after re-insert attempts = %d, want 2", got)
	}
}

// TestRelayLogAttemptRows_dedupesDuplicateAttemptNum 锁定脏数据防御:
// 历史数据同一日志内 attempt_num 重复时按首次出现保留, 不让 (log_id, attempt_num) 撞键结果不确定。
func TestRelayLogAttemptRows_dedupesDuplicateAttemptNum(t *testing.T) {
	rows := relayLogAttemptRows(model.RelayLog{
		ID: 1, Time: 100,
		Attempts: []model.ChannelAttempt{
			{ChannelID: 7, AttemptNum: 1, Status: model.AttemptFailed, Msg: "first"},
			{ChannelID: 8, AttemptNum: 1, Status: model.AttemptSuccess, Msg: "dup"},
			{ChannelID: 9, AttemptNum: 2, Status: model.AttemptFailed},
		},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (重复 attempt_num 去重)", len(rows))
	}
	if rows[0].ChannelID != 7 || rows[0].Msg != "first" {
		t.Fatalf("重复 attempt_num 应保留首次出现, got %+v", rows[0])
	}
	// 空 attempts 的日志零行。
	if got := relayLogAttemptRows(model.RelayLog{ID: 2}); got != nil {
		t.Fatalf("empty attempts should produce no rows, got %+v", got)
	}
}

// TestRelayLogIndexesMigrated 锁定 R5 与新表的索引: 建表/迁移后索引齐备,
// 否则渠道维度查询会退化成全表扫(AC1 的性能前提)。
func TestRelayLogIndexesMigrated(t *testing.T) {
	setupLogTestDB(t)

	migrator := db.GetDB().Migrator()
	for _, tc := range []struct {
		model interface{}
		index string
	}{
		{&model.RelayLog{}, "idx_relay_log_time"},                        // 日志列表按时间倒序分页 / 按天聚合 / MIN(time)
		{&model.RelayLogAttempt{}, "idx_relay_log_attempts_channel_log"}, // (channel_id, log_id): 渠道维度过滤 + 计数
	} {
		if !migrator.HasIndex(tc.model, tc.index) {
			t.Fatalf("missing index %s on %T", tc.index, tc.model)
		}
	}
	// 复合主键 (log_id, attempt_num): 既是明细的唯一键, 也是清理/回填按 log_id 删除与探针的索引。
	// 用行为断言而非 schema 断言: 同一日志的多次尝试必须各自成行(单列主键会被 OnConflict 去重成一行)。
	seedFlushedLogs(t, []model.RelayLog{{
		ID: 7201, Time: 1, RequestModelName: "grp",
		Attempts: []model.ChannelAttempt{
			{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
			{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 2, Status: model.AttemptSuccess},
		},
	}})
	if got := countAttempts(t, "log_id = ?", int64(7201)); got != 2 {
		t.Fatalf("same log must keep both attempt rows (composite PK), got %d", got)
	}
}

// TestRelayLogCleanup_deletesAttempts 是 AC4:
// 过期日志被清理时其尝试行同步消失(同事务), 保留期内的日志尝试行不受影响, 全表无孤儿行。
func TestRelayLogCleanup_deletesAttempts(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// keep_period 默认 7 天; 回填/清理要对齐「整日」口径: 造 9 天前与 6 天前两组日志。
	oldTime := time.Now().AddDate(0, 0, -9)
	newTime := time.Now().AddDate(0, 0, -6)
	logs := []model.RelayLog{
		{ID: 7001, Time: oldTime.Unix(), RequestModelName: "grp-old",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
				{ChannelID: 8, ChannelName: "ch-8", ModelName: "m", AttemptNum: 2, Status: model.AttemptSuccess},
			}},
		{ID: 7002, Time: newTime.Unix(), RequestModelName: "grp-new",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess},
			}},
	}
	seedFlushedLogs(t, logs)
	if got := countAttempts(t, ""); got != 3 {
		t.Fatalf("seeded attempts = %d, want 3", got)
	}

	if err := relayLogCleanup(ctx); err != nil {
		t.Fatalf("relayLogCleanup() error = %v", err)
	}

	// 过期日志与其尝试行一并消失; 保留期内日志的尝试行还在。
	if got := countAttempts(t, "log_id = ?", int64(7001)); got != 0 {
		t.Errorf("expired log attempts = %d, want 0", got)
	}
	if got := countAttempts(t, "log_id = ?", int64(7002)); got != 1 {
		t.Errorf("kept log attempts = %d, want 1", got)
	}
	// 无孤儿行: 尝试行的所属日志必须仍在。
	var orphans int64
	if err := db.GetDB().Model(&model.RelayLogAttempt{}).
		Where("log_id NOT IN (?)", db.GetDB().Model(&model.RelayLog{}).Select("id")).
		Count(&orphans).Error; err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("orphan attempts = %d, want 0", orphans)
	}
}

// TestRelayLogClear_clearsAttempts 清库端点同样要清尝试行(否则日志清空后明细仍能查到孤儿数据)。
func TestRelayLogClear_clearsAttempts(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	seedFlushedLogs(t, []model.RelayLog{
		{ID: 7101, Time: time.Now().Unix(), RequestModelName: "grp",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess},
			}},
	})

	if err := RelayLogClear(ctx); err != nil {
		t.Fatalf("RelayLogClear() error = %v", err)
	}
	if got := countAttempts(t, ""); got != 0 {
		t.Fatalf("attempts after clear = %d, want 0", got)
	}
	var logs int64
	if err := db.GetDB().Model(&model.RelayLog{}).Count(&logs).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if logs != 0 {
		t.Fatalf("logs after clear = %d, want 0", logs)
	}
}

// TestRelayLogAttemptsBackfill_isIdempotentAndQueryable 是 AC5:
// 上线前已存在(只有 attempts JSON、无规范化行)的日志, 回填后调用详情可查, 且重复执行不产生重复行。
func TestRelayLogAttemptsBackfill_isIdempotentAndQueryable(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	now := time.Now().Unix()
	// 直插 relay_logs 模拟「规范化表上线前就已存在的日志」: 没有尝试行。
	logs := []model.RelayLog{
		{ID: 6001, Time: now - 60, RequestModelName: "grp-a",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ChannelKeyRemark: "key-a", ModelName: "m-a",
					AttemptNum: 1, Status: model.AttemptFailed, Duration: 11, Msg: "old-1"},
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m-a",
					AttemptNum: 2, Status: model.AttemptSuccess, Duration: 22},
			}},
		{ID: 6002, Time: now - 30, RequestModelName: "grp-b", Error: "all failed",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 8, ChannelName: "ch-8", ModelName: "m-b", AttemptNum: 1, Status: model.AttemptFailed, Msg: "old-2"},
			}},
		{ID: 6003, Time: now - 10, RequestModelName: "grp-c"}, // 空 attempts
	}
	if err := db.GetDB().CreateInBatches(&logs, 10).Error; err != nil {
		t.Fatalf("seed legacy logs: %v", err)
	}
	if got := countAttempts(t, ""); got != 0 {
		t.Fatalf("precondition failed: attempts = %d, want 0", got)
	}

	// 空库/未回填时明细为空(端点在回填前查不到旧日志, 这正是回填要解决的问题)。
	if list, total, _, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50); err != nil || total != 0 || list != nil {
		t.Fatalf("before backfill: total=%d list=%+v err=%v, want empty", total, list, err)
	}

	relayLogAttemptsBackfillDone.Store(false)
	if err := RelayLogAttemptsBackfill(ctx); err != nil {
		t.Fatalf("RelayLogAttemptsBackfill() error = %v", err)
	}
	if got := countAttempts(t, ""); got != 3 {
		t.Fatalf("attempts after backfill = %d, want 3", got)
	}

	// 回填后旧日志的调用详情可查, 内容与 JSON 一致(含请求级错误字段的 join)。
	list, total, _, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	if err != nil {
		t.Fatalf("after backfill: %v", err)
	}
	wantList, wantTotal := legacyAttemptsByChannel(logs, 7, 1, 50)
	if total != wantTotal || !reflect.DeepEqual(list, wantList) {
		t.Fatalf("after backfill:\n got total=%d %+v\nwant total=%d %+v", total, list, wantTotal, wantList)
	}

	// 幂等: 重跑(清掉进程内 done 标记)不产生重复行。
	relayLogAttemptsBackfillDone.Store(false)
	if err := RelayLogAttemptsBackfill(ctx); err != nil {
		t.Fatalf("RelayLogAttemptsBackfill() second run error = %v", err)
	}
	if got := countAttempts(t, ""); got != 3 {
		t.Fatalf("attempts after second backfill = %d, want 3 (幂等)", got)
	}
}

// TestRelayLogAttemptsBackfill_resumesInBatches 验证分批推进: 日志数超过单批上界时分多批完成(批间水位推进),
// 且不会漏回填(总行数 = 各日志 attempts 之和)。
func TestRelayLogAttemptsBackfill_resumesInBatches(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	total := relayLogAttemptsBackfillBatch + 7 // 跨两个批次
	logs := make([]model.RelayLog, 0, total)
	for i := 0; i < total; i++ {
		logs = append(logs, model.RelayLog{
			ID:   int64(5000 + i),
			Time: time.Now().Unix() - int64(i),
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess, Duration: i},
			},
		})
	}
	if err := db.GetDB().CreateInBatches(&logs, 200).Error; err != nil {
		t.Fatalf("seed legacy logs: %v", err)
	}

	relayLogAttemptsBackfillDone.Store(false)
	if err := RelayLogAttemptsBackfill(ctx); err != nil {
		t.Fatalf("RelayLogAttemptsBackfill() error = %v", err)
	}
	if got := countAttempts(t, ""); got != int64(total) {
		t.Fatalf("backfilled attempts = %d, want %d", got, total)
	}
	if !relayLogAttemptsBackfillDone.Load() {
		t.Errorf("done flag should be set after a complete pass")
	}
}

// TestRelayLogAttemptsBackfill_stopsOnContextBudget 验证时间预算用尽时安静退出且不置 done:
// 下个周期续跑(不把未完成当完成, 否则剩下的旧日志永远查不到)。
func TestRelayLogAttemptsBackfill_stopsOnContextBudget(t *testing.T) {
	setupLogTestDB(t)

	if err := db.GetDB().Create(&model.RelayLog{
		ID: 5901, Time: time.Now().Unix(),
		Attempts: []model.ChannelAttempt{
			{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess},
		},
	}).Error; err != nil {
		t.Fatalf("seed legacy log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预算立即用尽

	relayLogAttemptsBackfillDone.Store(false)
	if err := RelayLogAttemptsBackfill(ctx); err != nil {
		t.Fatalf("budget-exhausted backfill should return nil, got %v", err)
	}
	if got := countAttempts(t, ""); got != 0 {
		t.Fatalf("attempts = %d, want 0 (预算用尽不应写入)", got)
	}
	if relayLogAttemptsBackfillDone.Load() {
		t.Fatalf("done flag must stay false when the pass was cut short")
	}
}

// TestRelayLogAttemptsByChannel_usesIndexAndScalesWithChannelRows 是 AC1:
//  1. 计划断言(确定性): 渠道维度查询走 idx_relay_log_attempts_channel_log 索引, 不做 relay_log_attempts 全表扫描;
//  2. 耗时断言(放宽阈值防抖): 5000 行日志 + 上万条尝试中, 查只有几十条尝试的渠道不随时间总量膨胀。
func TestRelayLogAttemptsByChannel_usesIndexAndScalesWithChannelRows(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// 计划断言用与生产同形的 SQL(过滤 + 排序 + 分页)。
	planRows, err := db.GetDB().Raw(
		"EXPLAIN QUERY PLAN SELECT * FROM relay_log_attempts WHERE channel_id = ? AND time >= ? ORDER BY time DESC, log_id DESC, attempt_num ASC LIMIT 50",
		7, time.Now().Add(-7*24*time.Hour).Unix(),
	).Rows()
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer planRows.Close()
	plan := ""
	for planRows.Next() {
		var id, parent, notused int
		var detail string
		if err := planRows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan += detail + "\n"
	}
	t.Logf("query plan:\n%s", plan)
	if !strings.Contains(plan, "idx_relay_log_attempts_channel_log") {
		t.Fatalf("channel-dimension query must use idx_relay_log_attempts_channel_log, plan:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN relay_log_attempts") {
		t.Fatalf("channel-dimension query must not full-scan relay_log_attempts, plan:\n%s", plan)
	}

	// 造量: 5000 行日志, 其中 4990 行各带 3 次尝试打在其他渠道(与目标渠道同表, 构成背景体量),
	// 目标渠道 7 只有 50 次尝试(50 行日志各 1 次)。
	const logRows = 5000
	logs := make([]model.RelayLog, 0, logRows)
	now := time.Now().Unix()
	for i := 0; i < logRows; i++ {
		channelID := 100000 + i%50 // 背景渠道, 与 7 无关
		var attempts []model.ChannelAttempt
		if i < 50 {
			channelID = 7
			attempts = []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess, Duration: i},
			}
		} else {
			attempts = []model.ChannelAttempt{
				{ChannelID: channelID, ChannelName: "ch-bg", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed, Msg: "bg"},
				{ChannelID: channelID + 1, ChannelName: "ch-bg", ModelName: "m", AttemptNum: 2, Status: model.AttemptFailed, Msg: "bg"},
				{ChannelID: channelID + 2, ChannelName: "ch-bg", ModelName: "m", AttemptNum: 3, Status: model.AttemptSuccess, Msg: "bg"},
			}
		}
		logs = append(logs, model.RelayLog{
			ID: int64(100000 + i), Time: now - int64(i), RequestModelName: "grp-bg",
			Attempts: attempts,
		})
	}
	seedFlushedLogs(t, logs)

	if got := countAttempts(t, "channel_id = ?", 7); got != 50 {
		t.Fatalf("target channel attempts = %d, want 50", got)
	}

	start := time.Now()
	list, total, truncated, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if total != 50 || len(list) != 50 || truncated {
		t.Fatalf("target channel expected total=50 len=50 truncated=false, got total=%d len=%d truncated=%v", total, len(list), truncated)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("channel-attempts query took %v with %d background attempts, want < 500ms", elapsed, logRows*3)
	} else {
		t.Logf("channel-attempts query: %v (背景 %d 日志 / %d 条尝试)", elapsed, logRows, logRows*3)
	}
}
