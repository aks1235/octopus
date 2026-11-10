package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 探测熔断在选路层的语义: 已判死且结论未过期的成员不进候选; 结论过期的成员照常可选;
// 全员被判死时退回不过滤(宁可白试一次, 也不能让整组变成零成员卡在等待循环里)。

// seedProbeRoutingDB 建同一渠道下三个模型/授权, 分别对应三种探测结论, 并按优先级 1/2/3 排进一个分组。
func seedProbeRoutingDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "route-probe-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.GetDB().Create(&model.Channel{
		ID:            1,
		ChannelConfig: model.ChannelConfig{Name: "channel-probe", BaseURL: "http://upstream"},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelKey{
		ID: 1, ChannelID: 1,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: "key", Key: "sk-1"},
	}).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	for _, id := range []int{1, 2, 3} {
		if err := db.GetDB().Create(&model.ChannelModel{ID: id, ChannelID: 1, Name: "model-" + string(rune('0'+id))}).Error; err != nil {
			t.Fatalf("create model %d: %v", id, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: id, ChannelModelID: id, ChannelKeyID: 1, Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create grant %d: %v", id, err)
		}
	}

	now := time.Now().Unix()
	// 优先级 1: 刚判死 → 应被跳过。
	// 优先级 2: 判死但结论已超过 24 小时 → 过期作废, 应可选。
	setProbeState(t, 1, op.ProbeStatusDead, now, "upstream responded 410 Gone")
	setProbeState(t, 2, op.ProbeStatusDead, now-op.ProbeDeadTTLSeconds-60, "upstream responded 410 Gone")
	// 优先级 3: 探测通过 → 可选。
	setProbeState(t, 3, op.ProbeStatusOK, now, "")

	group := model.Group{
		ID: 1, Name: "probe-g", Mode: model.GroupModeFailover,
		RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItem{
			{GroupID: 1, ChannelGrantID: 1, Priority: 1},
			{GroupID: 1, ChannelGrantID: 2, Priority: 2},
			{GroupID: 1, ChannelGrantID: 3, Priority: 3},
		},
	}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

func setProbeState(t *testing.T, grantID int, status string, at int64, reason string) {
	t.Helper()
	if err := db.GetDB().Model(&model.ChannelGrant{}).Where("id = ?", grantID).Updates(map[string]any{
		"probe_status": status,
		"probe_at":     at,
		"probe_reason": reason,
	}).Error; err != nil {
		t.Fatalf("set probe state for grant %d: %v", grantID, err)
	}
}

func TestSelectableGroupItems_skipsDeadAndHonorsTTL(t *testing.T) {
	seedProbeRoutingDB(t)
	group := groupForTest(t, "probe-g")

	items := selectableGroupItems(group)
	if len(items) != 2 {
		t.Fatalf("selectable items = %d, want 2 (跳过未过期的判死成员)", len(items))
	}
	if items[0].ChannelGrantID != 2 || items[1].ChannelGrantID != 3 {
		t.Fatalf("selectable grants = %d,%d, want 2,3 (顺序即优先级, 过期结论不跳过)",
			items[0].ChannelGrantID, items[1].ChannelGrantID)
	}
}

func TestSelectableGroupItems_fallsBackWhenAllDead(t *testing.T) {
	seedProbeRoutingDB(t)
	now := time.Now().Unix()
	// 三个成员都判死且都没过期: 过滤后为空, 必须退回"可用但未过滤"的集合。
	for _, grantID := range []int{1, 2, 3} {
		setProbeState(t, grantID, op.ProbeStatusDead, now, "upstream responded 410 Gone")
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	group := groupForTest(t, "probe-g")

	items := selectableGroupItems(group)
	if len(items) != 3 {
		t.Fatalf("selectable items = %d, want 3 (全员判死时不得把分组变成零成员)", len(items))
	}
}

// TestSelectableGroupItems_forceDisabledStillFiltered 确认熔断不会绕过启停:
// 渠道或凭据被禁用时, 即使探测结论是 ok 也仍然不可选(ChannelGrantGet 是唯一裁决点)。
func TestSelectableGroupItems_forceDisabledStillFiltered(t *testing.T) {
	seedProbeRoutingDB(t)
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable key: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	group := groupForTest(t, "probe-g")

	if items := selectableGroupItems(group); len(items) != 0 {
		t.Fatalf("selectable items = %d, want 0 (凭据禁用时任何探测结论都不放行)", len(items))
	}
}
