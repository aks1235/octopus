package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// seedGroupTestDB 建一个指向假上游的可用渠道与一个渠道级被禁用的渠道, 各带一枚凭据/模型/授权
// (协议位只勾 Chat), 并建一个故障转移分组把两者的授权收为成员; 返回成员主键已定稿的分组。
func seedGroupTestDB(t *testing.T, upstreamURL string) model.Group {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "group-member-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, ch := range []struct {
		id   int
		name string
		base string
	}{
		{1, "group-test-live", upstreamURL},
		{2, "group-test-off", "http://upstream"},
	} {
		if err := db.GetDB().Create(&model.Channel{
			ID: ch.id,
			ChannelConfig: model.ChannelConfig{
				Name:                     ch.name,
				BaseURL:                  ch.base,
				OpenAIChatCompletionPath: "/v1/chat/completions",
			},
		}).Error; err != nil {
			t.Fatalf("create channel %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelKey{
			ID: ch.id, ChannelID: ch.id,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-" + ch.name, Key: "sk-" + ch.name},
		}).Error; err != nil {
			t.Fatalf("create channel key %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelModel{
			ID: ch.id, ChannelID: ch.id, Name: "model-" + ch.name,
		}).Error; err != nil {
			t.Fatalf("create channel model %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: ch.id, ChannelModelID: ch.id, ChannelKeyID: ch.id,
			Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", ch.id, err)
		}
	}
	// 渠道 2 渠道级禁用: 布尔列带 default 标签, Create 的零值会被默认值覆盖, 须建后显式改库。
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", 2).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel 2: %v", err)
	}

	group := model.Group{
		ID: 301, Name: "group-test-g", Mode: model.GroupModeFailover,
		RelayConfig: model.DefaultGroupRelayConfig(),
		Items: []model.GroupItem{
			{GroupID: 301, ChannelGrantID: 2, Priority: 1}, // 不可用成员排在前。
			{GroupID: 301, ChannelGrantID: 1, Priority: 2},
		},
	}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	// 上一个测试滞留在内存缓冲的日志会随本次 flush 落进新库, 先清空保证逐库断言从零开始。
	if err := op.RelayLogClear(context.Background()); err != nil {
		t.Fatalf("clear relay log buffer: %v", err)
	}
	// 走 GroupGet 拿补齐展示字段后的快照: 处理器路径即如此, 不可用成员的结果名称兜底依赖它。
	snapshot, err := op.GroupGet(group.ID)
	if err != nil {
		t.Fatalf("GroupGet(%d): %v", group.ID, err)
	}
	return snapshot
}

// groupTestItem 取分组里引用指定授权的成员, 测试断言用。
func groupTestItem(t *testing.T, group model.Group, grantID int) model.GroupItem {
	t.Helper()
	for _, item := range group.Items {
		if item.ChannelGrantID == grantID {
			return item
		}
	}
	t.Fatalf("grant %d not in group %d", grantID, group.ID)
	return model.GroupItem{}
}

// TestGroupMembers_skipsUnavailableWithReason 验证不可用成员(渠道被禁用)不发上游请求:
// 直接以原因记失败且名称取自成员展示字段, 可用成员照常测通; 结果按成员提交序返回,
// 测试日志带分组测试的客户端标识, 不可用成员不产生日志(无请求可回放)。
func TestGroupMembers_skipsUnavailableWithReason(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionBody()))
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	ResetRouteState(group.ID)

	results := TestGroupMembers(context.Background(), group)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	byItem := make(map[int]model.GroupMemberTestResult, len(results))
	for _, result := range results {
		byItem[result.ItemID] = result
	}
	offItem, liveItem := groupTestItem(t, group, 2), groupTestItem(t, group, 1)

	off := byItem[offItem.ID]
	if off.Success || off.Error == "" {
		t.Fatalf("unavailable result = %+v, want failure with reason", off)
	}
	if !strings.Contains(off.Error, "disabled") {
		t.Fatalf("unavailable error = %q, want channel disabled reason", off.Error)
	}
	// 授权解析失败时名称取自成员读取时补齐的展示字段, 界面仍能对上行列。
	if off.ChannelName != "group-test-off" || off.ModelName != "model-group-test-off" || off.KeyName != "key-group-test-off" {
		t.Fatalf("unavailable names = %q/%q/%q, want group-test-off 三件套",
			off.ChannelName, off.ModelName, off.KeyName)
	}

	live := byItem[liveItem.ID]
	if !live.Success || live.Protocol != model.ProtocolOpenAIChatCompletion || live.Error != "" {
		t.Fatalf("live result = %+v, want success via chat", live)
	}
	if live.ChannelName != "group-test-live" || live.ModelName != "model-group-test-live" {
		t.Fatalf("live names = %q/%q, want group-test-live/model-group-test-live", live.ChannelName, live.ModelName)
	}
	if live.CooldownCleared {
		t.Fatalf("无冷却时 CooldownCleared 应为假")
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1 (不可用成员不发请求)", hits)
	}

	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	entry := logs[0]
	if entry.ClientName != groupTestClientName || entry.UserAgent != groupTestUserAgent {
		t.Errorf("client = %q/%q, want %q/%q", entry.ClientName, entry.UserAgent, groupTestClientName, groupTestUserAgent)
	}
	if entry.ChannelName != "group-test-live" || entry.RequestAPIKeyName != "key-group-test-live" {
		t.Errorf("channel/key = %q/%q, want group-test-live/key-group-test-live", entry.ChannelName, entry.RequestAPIKeyName)
	}
	if entry.Error != "" || entry.TotalAttempts != 1 {
		t.Errorf("error/attempts = %q/%d, want empty/1", entry.Error, entry.TotalAttempts)
	}
}

// TestGroupMembers_successClearsCooldown 验证测通的成员立即清除冷却与连续计数并释放探测占用,
// 当前路由与亲和不动: 测试是旁路观测, 不强制切换流量; 清除后下一轮选路即可命中该成员。
func TestGroupMembers_successClearsCooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionBody()))
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	ResetRouteState(group.ID)

	liveItem := groupTestItem(t, group, 1)
	now := time.Now().UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:       group.ID,
		CurrentItemID: liveItem.ID,
		AffinityUntil: now + 60_000,
		ProbeItemID:   liveItem.ID,
		Cooldowns:     map[int]int64{liveItem.ID: now + 120_000},
		trips:         map[int]int{liveItem.ID: 2},
	}
	routeMu.Unlock()

	results := TestGroupMembers(context.Background(), group)

	var live model.GroupMemberTestResult
	for _, result := range results {
		if result.ItemID == liveItem.ID {
			live = result
		}
	}
	if !live.Success || !live.CooldownCleared {
		t.Fatalf("live result = %+v, want success with cooldown cleared", live)
	}

	routeMu.Lock()
	route := routes[group.ID]
	_, cooling := route.Cooldowns[liveItem.ID]
	trips := route.trips[liveItem.ID]
	probe := route.ProbeItemID
	current, affinity := route.CurrentItemID, route.AffinityUntil
	routeMu.Unlock()
	if cooling || trips != 0 || probe != 0 {
		t.Fatalf("冷却清理后 cooling=%v trips=%d probe=%d, want false/0/0", cooling, trips, probe)
	}
	if current != liveItem.ID || affinity != now+60_000 {
		t.Fatalf("当前路由/亲和被测试改动: current=%d affinity=%d, want 保留", current, affinity)
	}
	// 冷却闸门已开, 下一轮选路即可命中该成员(无需等冷却到期)。
	if item := pickGroupItem(group, &routeWalk{}); item.ID != liveItem.ID {
		t.Fatalf("清冷却后选路 = 成员 %d, want %d", item.ID, liveItem.ID)
	}
}

// TestGroupMembers_failureKeepsRouteState 验证测失败的成员不动路由状态:
// 冷却与连续计数原样保留, 结果带失败摘要且 CooldownCleared 为假。
func TestGroupMembers_failureKeepsRouteState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	ResetRouteState(group.ID)

	liveItem := groupTestItem(t, group, 1)
	now := time.Now().UnixMilli()
	deadline := now + 120_000
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:   group.ID,
		Cooldowns: map[int]int64{liveItem.ID: deadline},
		trips:     map[int]int{liveItem.ID: 2},
	}
	routeMu.Unlock()

	results := TestGroupMembers(context.Background(), group)

	var live model.GroupMemberTestResult
	for _, result := range results {
		if result.ItemID == liveItem.ID {
			live = result
		}
	}
	if live.Success || live.CooldownCleared {
		t.Fatalf("live result = %+v, want failure without cooldown clear", live)
	}
	if !strings.Contains(live.Error, "401") {
		t.Fatalf("error = %q, want summary containing 401", live.Error)
	}

	routeMu.Lock()
	route := routes[group.ID]
	gotDeadline, cooling := route.Cooldowns[liveItem.ID]
	trips := route.trips[liveItem.ID]
	routeMu.Unlock()
	if !cooling || gotDeadline != deadline || trips != 2 {
		t.Fatalf("失败后路由状态被改动: cooling=%v deadline=%d trips=%d, want true/%d/2", cooling, gotDeadline, deadline, trips)
	}
}

// TestGroupMembers_cancelledCtxSendsNothing 验证客户端断开后剩余成员不再发往上游:
// 已取消的 ctx 让每个成员按取消摘要记失败(不发请求、零计费), 结果仍按成员提交序返回。
func TestGroupMembers_cancelledCtxSendsNothing(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(chatCompletionBody()))
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	ResetRouteState(group.ID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := TestGroupMembers(ctx, group)

	offItem, liveItem := groupTestItem(t, group, 2), groupTestItem(t, group, 1)
	// 成员按 Priority 升序提交(不可用成员排在前), 结果下标与提交序一一对应。
	if len(results) != 2 || results[0].ItemID != offItem.ID || results[1].ItemID != liveItem.ID {
		t.Fatalf("results = %+v, want [%d %d] in submit order", results, offItem.ID, liveItem.ID)
	}
	// 不可用成员的授权解析先于取消检查, 报的是禁用原因; 可用成员按取消摘要记失败。
	if results[0].Success || !strings.Contains(results[0].Error, "disabled") {
		t.Fatalf("unavailable result = %+v, want disabled reason", results[0])
	}
	if results[1].Success || results[1].Error != context.Canceled.Error() {
		t.Fatalf("cancelled result = %+v, want failure with %q", results[1], context.Canceled.Error())
	}
	if hits != 0 {
		t.Fatalf("upstream hits = %d, want 0 after cancel", hits)
	}
}

// TestGroupMembers_emptyGroupReturnsEmptySlice 验证空成员分组返回空数组而非 null,
// 前端拿到即可直接遍历。
func TestGroupMembers_emptyGroupReturnsEmptySlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)

	group.Items = nil
	results := TestGroupMembers(context.Background(), group)
	if results == nil || len(results) != 0 {
		t.Fatalf("results = %#v, want empty non-nil slice", results)
	}
}
