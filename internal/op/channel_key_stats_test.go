package op

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// seedKeyStatsTestDB 建两个渠道: 渠道 1 带两枚凭据(key-b 被禁用但统计保留), 渠道 2 带一枚凭据。
// 启停列带 gorm default 标签, Create 时零值会被默认值覆盖, 故先建成启用再显式改库禁用。
func seedKeyStatsTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "key-stats-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.GetDB().Create(&model.Channel{
		ID:            1,
		ChannelConfig: model.ChannelConfig{Name: "channel-a", BaseURL: "http://upstream-a"},
	}).Error; err != nil {
		t.Fatalf("create channel 1: %v", err)
	}
	keys := []model.ChannelKey{
		{
			ID: 11, ChannelID: 1,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-a", Key: "sk-a"},
			StatsMetrics:     model.StatsMetrics{InputToken: 100, OutputToken: 40, InputCost: 0.5, OutputCost: 0.2, WaitTime: 1200, RequestSuccess: 7, RequestFailed: 3},
		},
		{
			ID: 12, ChannelID: 1,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-b", Key: "sk-b"},
			StatsMetrics:     model.StatsMetrics{InputToken: 10, OutputToken: 4, InputCost: 0.05, OutputCost: 0.02, WaitTime: 300, RequestSuccess: 1, RequestFailed: 2},
		},
	}
	for i := range keys {
		if err := db.GetDB().Create(&keys[i]).Error; err != nil {
			t.Fatalf("create channel key %d: %v", keys[i].ID, err)
		}
	}
	// key-b 禁用: 禁用只影响选路, 凭据行与统计都保留。
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 12).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable key 12: %v", err)
	}
	if err := db.GetDB().Create(&model.Channel{
		ID:            2,
		ChannelConfig: model.ChannelConfig{Name: "channel-b", BaseURL: "http://upstream-b"},
	}).Error; err != nil {
		t.Fatalf("create channel 2: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelKey{
		ID: 21, ChannelID: 2,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-other", Key: "sk-other"},
	}).Error; err != nil {
		t.Fatalf("create channel key 21: %v", err)
	}

	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// TestChannelKeyStatsList_returnsAllKeysWithStats 验证多凭据渠道返回全量凭据统计,
// 数值与库内一致且按名称定序, 被禁用凭据照常返回并带上禁用状态。
func TestChannelKeyStatsList_returnsAllKeysWithStats(t *testing.T) {
	seedKeyStatsTestDB(t)

	stats, err := ChannelKeyStatsList(1)
	if err != nil {
		t.Fatalf("ChannelKeyStatsList(1) error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 keys, got %d: %+v", len(stats), stats)
	}
	// 按名称定序: key-a 在前。
	if stats[0].KeyID != 11 || stats[0].KeyName != "key-a" || !stats[0].Enabled {
		t.Fatalf("first key = %+v, want key 11 key-a enabled", stats[0])
	}
	if stats[1].KeyID != 12 || stats[1].KeyName != "key-b" || stats[1].Enabled {
		t.Fatalf("second key = %+v, want key 12 key-b disabled", stats[1])
	}
	got := stats[0].StatsMetrics
	want := model.StatsMetrics{InputToken: 100, OutputToken: 40, InputCost: 0.5, OutputCost: 0.2, WaitTime: 1200, RequestSuccess: 7, RequestFailed: 3}
	if got != want {
		t.Fatalf("key 11 stats = %+v, want %+v", got, want)
	}
	// 禁用凭据的统计保留, 不因禁用被清零。
	if stats[1].RequestSuccess != 1 || stats[1].RequestFailed != 2 {
		t.Fatalf("disabled key 12 stats = %+v, want success=1 failed=2", stats[1].StatsMetrics)
	}
}

// TestChannelKeyStatsList_isolatedByChannel 验证只返回指定渠道的凭据, 不混入其他渠道。
func TestChannelKeyStatsList_isolatedByChannel(t *testing.T) {
	seedKeyStatsTestDB(t)

	stats, err := ChannelKeyStatsList(2)
	if err != nil {
		t.Fatalf("ChannelKeyStatsList(2) error = %v", err)
	}
	if len(stats) != 1 || stats[0].KeyID != 21 || stats[0].KeyName != "key-other" {
		t.Fatalf("channel 2 keys = %+v, want only key 21 key-other", stats)
	}
}

// TestChannelKeyStatsList_unknownChannel 验证渠道不存在时返回错误, 由调用方转 404。
func TestChannelKeyStatsList_unknownChannel(t *testing.T) {
	seedKeyStatsTestDB(t)

	if _, err := ChannelKeyStatsList(999); err == nil {
		t.Fatalf("ChannelKeyStatsList(999) expected error for unknown channel")
	}
}

// TestChannelKeyStatsList_emptyKeys 验证没有凭据的渠道返回空数组而非 null, 与既有集合字段承诺一致。
func TestChannelKeyStatsList_emptyKeys(t *testing.T) {
	seedKeyStatsTestDB(t)

	if err := db.GetDB().Create(&model.Channel{
		ID:            3,
		ChannelConfig: model.ChannelConfig{Name: "channel-empty", BaseURL: "http://upstream-c"},
	}).Error; err != nil {
		t.Fatalf("create channel 3: %v", err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() reload error = %v", err)
	}

	stats, err := ChannelKeyStatsList(3)
	if err != nil {
		t.Fatalf("ChannelKeyStatsList(3) error = %v", err)
	}
	if stats == nil || len(stats) != 0 {
		t.Fatalf("expected empty non-nil slice, got %+v", stats)
	}
}
