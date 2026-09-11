package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// seedHealthTestDB 建五个渠道, 覆盖跳过开关与候选过滤的全部状态组合:
// 渠道 1 普通启用; 渠道 2 启用且勾选跳过; 渠道 3 被自动禁用; 渠道 4 被自动禁用且勾选跳过; 渠道 5 人工禁用且勾选跳过。
// 启停列带 gorm default 标签, Create 时零值会被默认值覆盖, 故禁用, 自动禁用与跳过状态一律先建成再显式改库。
func seedHealthTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel-health-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	channels := []model.Channel{
		{ID: 1, ChannelConfig: model.ChannelConfig{Name: "plain", BaseURL: "http://plain"}},
		{ID: 2, ChannelConfig: model.ChannelConfig{Name: "skipped", BaseURL: "http://skipped"}},
		{ID: 3, ChannelConfig: model.ChannelConfig{Name: "auto-disabled", BaseURL: "http://auto"}},
		{ID: 4, ChannelConfig: model.ChannelConfig{Name: "skipped-auto-disabled", BaseURL: "http://skipped-auto"}},
		{ID: 5, ChannelConfig: model.ChannelConfig{Name: "skipped-manual-disabled", BaseURL: "http://skipped-manual"}},
	}
	for i := range channels {
		if err := db.GetDB().Create(&channels[i]).Error; err != nil {
			t.Fatalf("create channel %d: %v", channels[i].ID, err)
		}
	}
	// 渠道 3/4 打上自动禁用态: 禁用 + 自动禁用标记 + 连续失败计数; 渠道 2/4/5 勾选跳过; 渠道 5 仅人工禁用。
	states := map[int]map[string]any{
		2: {"health_check_skip": true},
		3: {"enabled": false, "auto_disabled": true, "health_fail_count": 2, "last_health_error": "boom", "last_health_at": 111},
		4: {"enabled": false, "auto_disabled": true, "health_fail_count": 2, "last_health_error": "boom", "last_health_at": 111, "health_check_skip": true},
		5: {"enabled": false, "health_check_skip": true},
	}
	for id, updates := range states {
		if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			t.Fatalf("seed channel %d state: %v", id, err)
		}
	}

	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// TestChannelHealthCandidates_excludesSkippedChannels 验证勾选跳过的渠道不成为探测候选:
// 启用中的跳过渠道(2)与自动禁用的跳过渠道(4)都不出现, 未勾选的启用(1)与自动禁用(3)渠道照常入列。
func TestChannelHealthCandidates_excludesSkippedChannels(t *testing.T) {
	seedHealthTestDB(t)

	candidates := ChannelHealthCandidates()
	got := make(map[int]model.ChannelHealthCandidate, len(candidates))
	for _, candidate := range candidates {
		got[candidate.ID] = candidate
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates (channels 1 and 3), got %d: %+v", len(got), candidates)
	}
	if _, ok := got[1]; !ok {
		t.Fatalf("plain enabled channel 1 missing from candidates: %+v", candidates)
	}
	c3, ok := got[3]
	if !ok {
		t.Fatalf("auto-disabled channel 3 missing from candidates: %+v", candidates)
	}
	if !c3.AutoDisabled || c3.HealthFailCount != 2 {
		t.Fatalf("channel 3 candidate = %+v, want auto_disabled with fail count 2", c3)
	}
}

// TestChannelUpdate_skipRestoresAutoDisabledChannel 验证被自动禁用的渠道勾选跳过后随保存恢复:
// 表单读出的是禁用态(提交 enabled=false), 保存仍强制恢复启用并清掉自动禁用标记与失败计数, 库与缓存一致。
func TestChannelUpdate_skipRestoresAutoDisabledChannel(t *testing.T) {
	seedHealthTestDB(t)

	detail := &model.ChannelDetail{
		ID:            3,
		ChannelConfig: model.ChannelConfig{Name: "auto-disabled", BaseURL: "http://auto", HealthCheckSkip: true},
	}
	updated, err := ChannelUpdate(detail, context.Background())
	if err != nil {
		t.Fatalf("ChannelUpdate() error = %v", err)
	}
	if !updated.Enabled || !updated.HealthCheckSkip {
		t.Fatalf("updated detail = %+v, want enabled with health_check_skip", updated.ChannelConfig)
	}

	var row model.Channel
	if err := db.GetDB().First(&row, 3).Error; err != nil {
		t.Fatalf("load channel 3: %v", err)
	}
	if !row.Enabled || row.AutoDisabled || row.HealthFailCount != 0 || !row.HealthCheckSkip {
		t.Fatalf("db row = %+v, want enabled, auto_disabled cleared, fail count 0, skip on", row)
	}
	cached, err := ChannelGet(3)
	if err != nil {
		t.Fatalf("ChannelGet(3) error = %v", err)
	}
	if !cached.Enabled || cached.AutoDisabled || cached.HealthFailCount != 0 {
		t.Fatalf("cached channel = %+v, want restored in cache too", cached)
	}
}

// TestChannelUpdate_withoutSkipKeepsAutoDisabled 验证未勾选跳过时自动禁用态原样保留:
// 提交禁用的渠道不被恢复, 失败计数与自动禁用标记照旧, 健康检查仍会对它探测以便恢复。
func TestChannelUpdate_withoutSkipKeepsAutoDisabled(t *testing.T) {
	seedHealthTestDB(t)

	detail := &model.ChannelDetail{
		ID:            3,
		ChannelConfig: model.ChannelConfig{Name: "auto-disabled", BaseURL: "http://auto"},
	}
	if _, err := ChannelUpdate(detail, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate() error = %v", err)
	}

	var row model.Channel
	if err := db.GetDB().First(&row, 3).Error; err != nil {
		t.Fatalf("load channel 3: %v", err)
	}
	if row.Enabled || !row.AutoDisabled || row.HealthFailCount != 2 {
		t.Fatalf("db row = %+v, want still auto-disabled with fail count 2", row)
	}
}

// TestChannelUpdate_skipOnManuallyDisabledStaysDisabled 验证人工禁用的渠道勾选跳过不会被翻回启用:
// 跳过的恢复只针对自动禁用态, 人工禁用是用户意愿, 保存后保持禁用。
func TestChannelUpdate_skipOnManuallyDisabledStaysDisabled(t *testing.T) {
	seedHealthTestDB(t)

	detail := &model.ChannelDetail{
		ID:            5,
		ChannelConfig: model.ChannelConfig{Name: "skipped-manual-disabled", BaseURL: "http://skipped-manual", HealthCheckSkip: true},
	}
	if _, err := ChannelUpdate(detail, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate() error = %v", err)
	}

	var row model.Channel
	if err := db.GetDB().First(&row, 5).Error; err != nil {
		t.Fatalf("load channel 5: %v", err)
	}
	if row.Enabled || row.AutoDisabled {
		t.Fatalf("db row = %+v, want manually disabled channel to stay disabled", row)
	}
}
