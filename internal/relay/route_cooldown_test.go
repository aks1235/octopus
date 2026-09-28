package relay

import (
	"fmt"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// cooldownRemaining 读取成员当前的冷却剩余毫秒; 无冷却或分组无路由状态时返回 0。
func cooldownRemaining(t *testing.T, groupID, itemID int) int64 {
	t.Helper()
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		return 0
	}
	deadline, ok := route.Cooldowns[itemID]
	if !ok {
		return 0
	}
	return deadline - time.Now().UnixMilli()
}

// tripsOf 读取成员的连续冷却次数; 分组无路由状态时返回 0。
func tripsOf(t *testing.T, groupID, itemID int) int {
	t.Helper()
	routeMu.Lock()
	defer routeMu.Unlock()
	if route := routes[groupID]; route != nil {
		return route.trips[itemID]
	}
	return 0
}

// assertCooldown 断言冷却剩余时长接近期望值, 允许两秒调度抖动。
func assertCooldown(t *testing.T, groupID, itemID, wantMillis int, step string) {
	t.Helper()
	got := cooldownRemaining(t, groupID, itemID)
	if got < int64(wantMillis)-2000 || got > int64(wantMillis)+2000 {
		t.Fatalf("%s: 冷却剩余 = %dms, 期望约 %dms", step, got, wantMillis)
	}
}

// TestCooldownSeconds_shiftOverflowGuard 验证指数退避的移位上限与封顶双重保护, 不做可能溢出的移位。
func TestCooldownSeconds_shiftOverflowGuard(t *testing.T) {
	cases := []struct {
		name       string
		base, max  int
		trips      int
		wantSecond int
	}{
		{name: "首次冷却为基础值", base: 60, max: 600, trips: 1, wantSecond: 60},
		{name: "第二次翻倍", base: 60, max: 600, trips: 2, wantSecond: 120},
		{name: "第三次再翻倍", base: 60, max: 600, trips: 3, wantSecond: 240},
		{name: "超过封顶取上限", base: 60, max: 100, trips: 2, wantSecond: 100},
		{name: "连续次数极大时移位封顶", base: 1, max: 1 << 62, trips: 100, wantSecond: 1 << cooldownMaxShift},
		{name: "极大基础值时不做溢出移位", base: 1 << 60, max: 1 << 61, trips: 30, wantSecond: 1 << 61},
		{name: "上限低于基础值按基础值兜底", base: 60, max: 10, trips: 1, wantSecond: 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cooldownSeconds(tc.base, tc.max, tc.trips); got != tc.wantSecond {
				t.Fatalf("cooldownSeconds(%d, %d, %d) = %d, want %d", tc.base, tc.max, tc.trips, got, tc.wantSecond)
			}
		})
	}
}

// TestRecordRouteFailure_cooldownBackoffDoubles 验证 AC1: 同一成员连续三次冷却时长依次为基础值的 1/2/4 倍。
func TestRecordRouteFailure_cooldownBackoffDoubles(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 201, "cooldown-backoff-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 选路一次建立路由状态(默认 base=60s, 上限 600s)。
	item := pickGroupItem(group, &routeWalk{})
	if item.ID == 0 {
		t.Fatalf("pickGroupItem() 未选中成员")
	}

	for i, want := range []int{60_000, 120_000, 240_000} {
		if !recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts) {
			t.Fatalf("第 %d 次失败应进入冷却", i+1)
		}
		assertCooldown(t, group.ID, item.ID, want, fmt.Sprintf("第 %d 次冷却", i+1))
		if got := tripsOf(t, group.ID, item.ID); got != i+1 {
			t.Fatalf("连续冷却次数 = %d, want %d", got, i+1)
		}
	}
}

// TestRecordRouteFailure_cooldownBackoffCapsAtMax 验证 AC2: 翻倍后超过上限时按上限取值, 继续失败保持上限。
func TestRecordRouteFailure_cooldownBackoffCapsAtMax(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 202, "cooldown-cap-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	group.RelayConfig.MemberCooldownSeconds = 60
	group.RelayConfig.MemberMaxCooldownSeconds = 100

	item := pickGroupItem(group, &routeWalk{})
	if item.ID == 0 {
		t.Fatalf("pickGroupItem() 未选中成员")
	}
	for i, want := range []int{60_000, 100_000, 100_000} {
		recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
		assertCooldown(t, group.ID, item.ID, want, fmt.Sprintf("第 %d 次冷却(上限 100s)", i+1))
	}
}

// TestRecordRouteSuccess_resetsConsecutiveCooldown 验证 AC3: 成功一次即清零连续次数, 下次冷却回到基础值。
func TestRecordRouteSuccess_resetsConsecutiveCooldown(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 203, "cooldown-reset-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	item := pickGroupItem(group, &routeWalk{})
	if item.ID == 0 {
		t.Fatalf("pickGroupItem() 未选中成员")
	}
	// 连续两次冷却累加到 120s。
	recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
	recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
	if got := tripsOf(t, group.ID, item.ID); got != 2 {
		t.Fatalf("连续冷却次数 = %d, want 2", got)
	}

	recordRouteSuccess(group, item.ID)
	if got := tripsOf(t, group.ID, item.ID); got != 0 {
		t.Fatalf("成功后连续冷却次数 = %d, want 0", got)
	}

	// 再冷却一次应回到基础值 60s 而非 240s。
	// 成员仍在冷却期内, 语义上 recordRouteFailure 会被调用(冷却只做选路闸门, 不拦截失败上报)。
	recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
	assertCooldown(t, group.ID, item.ID, 60_000, "成功清零后的首次冷却")
	if got := tripsOf(t, group.ID, item.ID); got != 1 {
		t.Fatalf("清零后连续冷却次数 = %d, want 1", got)
	}
}

// TestGroupRouteLocked_cleansTripsOfRemovedMember 验证 AC4: 成员被删除或分组路由重置后连续次数一并清理, 不泄漏。
func TestGroupRouteLocked_cleansTripsOfRemovedMember(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 204, "cooldown-clean-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	item := pickGroupItem(group, &routeWalk{})
	if item.ID == 0 {
		t.Fatalf("pickGroupItem() 未选中成员")
	}
	recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
	if got := tripsOf(t, group.ID, item.ID); got != 1 {
		t.Fatalf("成员仍在分组内时连续冷却次数 = %d, want 1", got)
	}

	// 模拟成员被删除: 分组去掉该成员后, 下一次选路触发的清理应移除其计数。
	trimmed := group
	trimmed.Items = group.Items[1:]
	routeMu.Lock()
	route := groupRouteLocked(trimmed)
	_, exists := route.trips[item.ID]
	routeMu.Unlock()
	if exists {
		t.Fatalf("成员删除后连续冷却计数未清理, itemID=%d", item.ID)
	}

	// 分组路由整体重置后状态连同计数一并丢弃。
	ResetRouteState(group.ID)
	routeMu.Lock()
	_, alive := routes[group.ID]
	routeMu.Unlock()
	if alive {
		t.Fatalf("ResetRouteState 未丢弃分组路由状态")
	}
	if got := tripsOf(t, group.ID, item.ID); got != 0 {
		t.Fatalf("重置后连续冷却次数 = %d, want 0", got)
	}
}

// TestRecordRouteFailure_manualUnchanged 验证 AC5: 手动模式不产生冷却, 也不创建路由状态。
func TestRecordRouteFailure_manualUnchanged(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 205, "cooldown-manual-g", model.GroupModeManual, grants)
	ResetRouteState(group.ID)

	if recordRouteFailure(group, group.Items[0].ID, group.RelayConfig.MemberMaxAttempts) {
		t.Fatalf("手动模式不应进入冷却")
	}
	routeMu.Lock()
	route := routes[group.ID]
	routeMu.Unlock()
	if route != nil {
		t.Fatalf("手动模式不应创建路由状态")
	}
}

// TestClearGroupCooldowns_restoresCandidacyKeepsRouting 验证人工重置冷却的语义边界:
// 冷却与连续计数整表清空、探测占用释放, 而当前路由与亲和窗口保留 ——
// 重置只恢复冷却成员的候选资格, 不强制切换流量。
func TestClearGroupCooldowns_restoresCandidacyKeepsRouting(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 206, "clear-cooldown-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 两个成员都在冷却, 第一个占用探测名额, 当前路由与亲和窗口指向第二个成员。
	now := time.Now().UnixMilli()
	affinityUntil := now + 60_000
	routeMu.Lock()
	routes[group.ID] = &RouteState{
		GroupID:       group.ID,
		CurrentItemID: group.Items[1].ID,
		AffinityUntil: affinityUntil,
		ProbeItemID:   group.Items[0].ID,
		Cooldowns:     map[int]int64{group.Items[0].ID: now + 120_000, group.Items[1].ID: now + 240_000},
		trips:         map[int]int{group.Items[0].ID: 2, group.Items[1].ID: 3},
	}
	routeMu.Unlock()

	ClearGroupCooldowns(group.ID)

	routeMu.Lock()
	route := routes[group.ID]
	cooldowns := len(route.Cooldowns)
	trips := len(route.trips)
	probe := route.ProbeItemID
	current, affinity := route.CurrentItemID, route.AffinityUntil
	routeMu.Unlock()
	if cooldowns != 0 || trips != 0 || probe != 0 {
		t.Fatalf("重置后 cooldowns=%d trips=%d probe=%d, want 0/0/0", cooldowns, trips, probe)
	}
	if current != group.Items[1].ID || affinity != affinityUntil {
		t.Fatalf("当前路由/亲和被重置: current=%d affinity=%d, want %d/%d 保留",
			current, affinity, group.Items[1].ID, affinityUntil)
	}

	// 重置不强制切换流量: 亲和窗口保留时下一轮选路仍从当前成员起步 ——
	// 冷却闸门已开只意味着候选资格恢复, 何时轮到由亲和与扫描顺序决定。
	if item := pickGroupItem(group, &routeWalk{}); item.ID != group.Items[1].ID {
		t.Fatalf("重置后选路 = 成员 %d, want 亲和期内保留的当前成员 %d", item.ID, group.Items[1].ID)
	}
}

// TestClearGroupCooldowns_noStateIsNoOp 验证无路由状态的分组(从未转发过)调用重置不创建状态、不 panic。
func TestClearGroupCooldowns_noStateIsNoOp(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 2)
	group := seedCursorGroup(t, 207, "clear-cooldown-empty-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	ClearGroupCooldowns(group.ID)

	routeMu.Lock()
	_, exists := routes[group.ID]
	routeMu.Unlock()
	if exists {
		t.Fatalf("重置不应为无状态分组创建路由状态")
	}
}
