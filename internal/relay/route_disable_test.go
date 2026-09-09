package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// seedDisableTestDB 建两个渠道各带一枚启用凭据, 一个模型与一条授权:
// 渠道 off 启用但凭据被禁用, 渠道 auto 渠道级被禁用, 渠道 on 完全可用。
// 故障转移分组 failover-g 的成员把不可用者排在前面(优先级 1), 可用者殿后(优先级 3)。
func seedDisableTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "route-disable-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	// 三个渠道同构: off 凭据禁用, auto 渠道禁用, on 全启用。
	// 启停列带 gorm default 标签, Create 时零值会被默认值覆盖, 故先全部建成启用, 再显式改库禁用。
	for _, ch := range []struct {
		id        int
		keyName   string
		modelName string
	}{
		{1, "key-off", "model-off"},
		{2, "key-auto", "model-auto"},
		{3, "key-on", "model-on"},
	} {
		if err := db.GetDB().Create(&model.Channel{
			ID:            ch.id,
			ChannelConfig: model.ChannelConfig{Name: "channel-" + ch.keyName[4:], BaseURL: "http://upstream"},
		}).Error; err != nil {
			t.Fatalf("create channel %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelKey{
			ID: ch.id, ChannelID: ch.id,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: ch.keyName, Key: "sk-" + ch.keyName},
		}).Error; err != nil {
			t.Fatalf("create channel key %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelModel{ID: ch.id, ChannelID: ch.id, Name: ch.modelName}).Error; err != nil {
			t.Fatalf("create channel model %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: ch.id, ChannelModelID: ch.id, ChannelKeyID: ch.id,
			Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", ch.id, err)
		}
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", 2).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel 2: %v", err)
	}
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel key 1: %v", err)
	}

	// 故障转移分组: 优先级 1/2 不可用, 优先级 3 可用; 手动分组激活成员指向凭据被禁用者。
	groups := []model.Group{
		{
			ID: 1, Name: "failover-g", Mode: model.GroupModeFailover,
			RelayConfig: model.DefaultGroupRelayConfig(),
			Items: []model.GroupItem{
				{GroupID: 1, ChannelGrantID: 2, Priority: 1}, // 渠道禁用。
				{GroupID: 1, ChannelGrantID: 1, Priority: 2}, // 凭据禁用。
				{GroupID: 1, ChannelGrantID: 3, Priority: 3}, // 可用。
			},
		},
		{
			ID: 2, Name: "manual-g", Mode: model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig(),
			Items:       []model.GroupItem{{GroupID: 2, ChannelGrantID: 1, Priority: 1}},
		},
	}
	for i := range groups {
		if err := db.GetDB().Create(&groups[i]).Error; err != nil {
			t.Fatalf("create group %d: %v", groups[i].ID, err)
		}
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	// 手动分组激活成员指向其唯一成员(创建后拿回分配的主键)。
	var manualItems []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", 2).Find(&manualItems).Error; err != nil || len(manualItems) != 1 {
		t.Fatalf("load manual group items: %v len=%d", err, len(manualItems))
	}
	if err := db.GetDB().Model(&model.Group{}).Where("id = ?", 2).Update("active_item_id", manualItems[0].ID).Error; err != nil {
		t.Fatalf("set active item: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() reload error = %v", err)
	}
}

// groupForTest 取分组缓存副本, 即转发链每轮读取到的形状。
func groupForTest(t *testing.T, name string) model.Group {
	t.Helper()
	group, err := op.GroupGetByName(name)
	if err != nil {
		t.Fatalf("GroupGetByName(%q): %v", name, err)
	}
	return group
}

// TestPickGroupItem_skipsDisabledMembers 验证渠道级与凭据级禁用的成员不进入选路:
// 优先级更高的禁用成员被跳过, 选中其后第一个可用成员; 禁用不产生失败计数与探测占用。
func TestPickGroupItem_skipsDisabledMembers(t *testing.T) {
	seedDisableTestDB(t)
	group := groupForTest(t, "failover-g")
	ResetRouteState(group.ID)

	item := pickGroupItem(group)
	if item.ChannelGrantID != 3 {
		t.Fatalf("pickGroupItem() = grant %d, want grant 3 (唯一可用成员)", item.ChannelGrantID)
	}
	// 禁用成员被剔除而非计入冷却, 路由状态不应残留对它们的任何记录。
	routeMu.Lock()
	route := routes[group.ID]
	cooldowns := len(route.Cooldowns)
	probe := route.ProbeItemID
	routeMu.Unlock()
	if cooldowns != 0 || probe != 0 {
		t.Fatalf("disabled members left state: cooldowns=%d probe=%d, want 0/0", cooldowns, probe)
	}
}

// TestPickGroupItem_affinityInvalidatedByDisable 验证亲和期内当前成员被禁用时立即失效:
// 请求下一轮即改走其他可用成员, 不等亲和期自然结束。
func TestPickGroupItem_affinityInvalidatedByDisable(t *testing.T) {
	seedDisableTestDB(t)
	group := groupForTest(t, "failover-g")
	ResetRouteState(group.ID)

	// 构造亲和: 当前成员为渠道被禁用的优先级 1 成员, 亲和期还有一分钟。
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:       group.ID,
		CurrentItemID: group.Items[0].ID,
		AffinityUntil: time.Now().Add(time.Minute).UnixMilli(),
		Cooldowns:     map[int]int64{},
	}
	routeMu.Unlock()

	item := pickGroupItem(group)
	if item.ChannelGrantID != 3 {
		t.Fatalf("pickGroupItem() = grant %d, want grant 3 (亲和成员被禁用后立即改走可用成员)", item.ChannelGrantID)
	}
	routeMu.Lock()
	current, affinity := routes[group.ID].CurrentItemID, routes[group.ID].AffinityUntil
	routeMu.Unlock()
	if current != item.ID || affinity != 0 {
		t.Fatalf("route after disable: current=%d affinity=%d, want current=%d affinity=0", current, affinity, item.ID)
	}
}

// TestPickGroupItem_allDisabledReturnsZero 验证全部成员不可选时返回零值:
// 调用方由此走等待循环, 不 panic 也不把禁用成员当目标。
func TestPickGroupItem_allDisabledReturnsZero(t *testing.T) {
	seedDisableTestDB(t)
	group := groupForTest(t, "failover-g")
	ResetRouteState(group.ID)

	// 库内禁用最后一个可用渠道后刷新缓存, 模拟人工禁用或健康检查自动禁用的实时效果。
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", 3).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel 3: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() reload error = %v", err)
	}
	group = groupForTest(t, "failover-g")

	if item := pickGroupItem(group); item.ID != 0 {
		t.Fatalf("pickGroupItem() = item %d, want zero value when all members disabled", item.ID)
	}
}

// TestPickGroupItem_manualDisabledActiveWaits 验证手动模式激活成员不可用时同样返回零值走等待,
// 与故障转移模式语义一致。
func TestPickGroupItem_manualDisabledActiveWaits(t *testing.T) {
	seedDisableTestDB(t)
	group := groupForTest(t, "manual-g")

	if item := pickGroupItem(group); item.ID != 0 {
		t.Fatalf("pickGroupItem() = item %d, want zero value when active member's key is disabled", item.ID)
	}
}

// TestPickGroupItem_reenabledMemberRestored 验证渠道重新启用(自动解禁或人工)后成员恢复参与选路,
// 且恢复后按优先级立即回到首选位置。
func TestPickGroupItem_reenabledMemberRestored(t *testing.T) {
	seedDisableTestDB(t)
	group := groupForTest(t, "failover-g")
	ResetRouteState(group.ID)

	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", 2).Update("enabled", true).Error; err != nil {
		t.Fatalf("re-enable channel 2: %v", err)
	}
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 1).Update("enabled", true).Error; err != nil {
		t.Fatalf("re-enable key 1: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() reload error = %v", err)
	}
	group = groupForTest(t, "failover-g")

	item := pickGroupItem(group)
	if item.ChannelGrantID != 2 {
		t.Fatalf("pickGroupItem() = grant %d, want grant 2 (重新启用的优先级 1 成员恢复首选)", item.ChannelGrantID)
	}
}
