package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
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
// RelayLogAttemptsByChannel — 按渠道展开 attempts 明细
// ============================================================================

func TestRelayLogAttemptsByChannel_filtersAndPaginates(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	// keep_enabled 默认 true, keep_period 默认 7 天, 迁移来的行不会因 cutoff 被排除。
	rows := []model.RelayLog{
		{
			ID: 100, Time: time.Now().Unix() - 10, RequestModelName: "grp-a",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m-x", AttemptNum: 1, Status: model.AttemptFailed, Msg: "timeout"},
				{ChannelID: 8, ChannelName: "ch-8", ModelName: "m-x", AttemptNum: 2, Status: model.AttemptSuccess},
			},
		},
		{
			ID: 101, Time: time.Now().Unix() - 5, RequestModelName: "grp-a", Error: "all failed",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m-x", AttemptNum: 1, Status: model.AttemptFailed, Msg: "500"},
			},
		},
	}
	if err := db.GetDB().CreateInBatches(&rows, 10).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// 渠道 7: 两行各一条, 按时间倒序 → 101 在前
	list, total, truncated, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if truncated {
		t.Errorf("unexpected truncated")
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

// TestRelayLogAttemptsByChannel_likeFalsePositiveFiltered 验证 LIKE 粗筛的
// 前缀误匹配(查 7 误命中 71)由 Go 层按 ChannelID 精确过滤兜底。
func TestRelayLogAttemptsByChannel_likeFalsePositiveFiltered(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	now := time.Now().Unix()
	rows := []model.RelayLog{
		{
			// 同一行含 71(误匹配源)与 7(真目标): 只应返回 7 的 attempt。
			ID: 300, Time: now,
			Attempts: []model.ChannelAttempt{
				{ChannelID: 71, ChannelName: "ch-71", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 2, Status: model.AttemptSuccess},
			},
		},
		{
			// 纯误匹配行: 只含 71, 查 7 时应整行无命中。
			ID: 301, Time: now,
			Attempts: []model.ChannelAttempt{
				{ChannelID: 71, ChannelName: "ch-71", ModelName: "m", AttemptNum: 1, Status: model.AttemptFailed},
			},
		},
	}
	if err := db.GetDB().CreateInBatches(&rows, 10).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

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

// TestRelayLogAttemptsByChannel_truncated 验证粗筛行数触顶扫描上界时 truncated=true。
func TestRelayLogAttemptsByChannel_truncated(t *testing.T) {
	ctx := context.Background()
	setupLogTestDB(t)

	rowsToSeed := relayLogChannelAttemptsScanLimit // 命中上界即视为可能还有未扫到的行
	now := time.Now().Unix()
	batch := make([]model.RelayLog, 0, 500)
	for i := 0; i < rowsToSeed; i++ {
		batch = append(batch, model.RelayLog{
			ID:   int64(1000 + i),
			Time: now - int64(i),
			Attempts: []model.ChannelAttempt{
				{ChannelID: 7, ChannelName: "ch-7", ModelName: "m", AttemptNum: 1, Status: model.AttemptSuccess},
			},
		})
	}
	if err := db.GetDB().CreateInBatches(&batch, 500).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	list, total, truncated, err := RelayLogAttemptsByChannel(ctx, 7, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogAttemptsByChannel() error = %v", err)
	}
	if !truncated {
		t.Errorf("expected truncated=true when scan hits the %d row limit", relayLogChannelAttemptsScanLimit)
	}
	if total != rowsToSeed || len(list) != 50 {
		t.Fatalf("expected total=%d len=50, got total=%d len=%d", rowsToSeed, total, len(list))
	}
}
