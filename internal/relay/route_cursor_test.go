package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// seedCursorChannel 建一个启用渠道并带 count 枚同构的凭据/模型/授权, 返回授权 ID 列表。
// 实体主键从 firstID 起连号, 分组成员按授权 ID 引用; 启停列带 gorm default 标签, 此处全部保持启用。
func seedCursorChannel(t *testing.T, channelID, firstID, count int) []int {
	t.Helper()
	if err := db.GetDB().Create(&model.Channel{
		ID: channelID,
		ChannelConfig: model.ChannelConfig{
			Name:                     fmt.Sprintf("channel-%d", channelID),
			BaseURL:                  "http://upstream",
			OpenAIChatCompletionPath: "/v1/chat/completions",
		},
	}).Error; err != nil {
		t.Fatalf("create channel %d: %v", channelID, err)
	}
	return seedCursorGrants(t, channelID, firstID, count)
}

// seedCursorGrants 在既有渠道下建 count 枚同构的凭据/模型/授权, 返回授权 ID 列表。
func seedCursorGrants(t *testing.T, channelID, firstID, count int) []int {
	t.Helper()
	grants := make([]int, 0, count)
	for i := firstID; i < firstID+count; i++ {
		if err := db.GetDB().Create(&model.ChannelKey{
			ID: i, ChannelID: channelID,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: fmt.Sprintf("key-%d", i), Key: "sk-" + fmt.Sprint(i)},
		}).Error; err != nil {
			t.Fatalf("create channel key %d: %v", i, err)
		}
		if err := db.GetDB().Create(&model.ChannelModel{ID: i, ChannelID: channelID, Name: fmt.Sprintf("model-%d", i)}).Error; err != nil {
			t.Fatalf("create channel model %d: %v", i, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: i, ChannelModelID: i, ChannelKeyID: i,
			Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", i, err)
		}
		grants = append(grants, i)
	}
	return grants
}

// initCursorTestDB 初始化独立的测试库。
func initCursorTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "route-cursor-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
}

// seedCursorGroup 建分组并按 grants 顺序定成员优先级, 刷新缓存后返回成员主键已定稿的分组,
// 即转发链每轮读到的形状; 分组 ID 由调用方给定以免包内跨测试的路由状态串扰。
func seedCursorGroup(t *testing.T, id int, name string, mode model.GroupMode, grants []int) model.Group {
	t.Helper()
	items := make([]model.GroupItem, 0, len(grants))
	for i, grantID := range grants {
		items = append(items, model.GroupItem{GroupID: id, ChannelGrantID: grantID, Priority: i + 1})
	}
	if err := db.GetDB().Create(&model.Group{
		ID: id, Name: name, Mode: mode,
		RelayConfig: model.DefaultGroupRelayConfig(),
		Items:       items,
	}).Error; err != nil {
		t.Fatalf("create group %d: %v", id, err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	return groupForTest(t, name)
}

// cursorPick 走一步游标选路并写回游标, 与 handler 循环内的一轮选路完全一致。
func cursorPick(group model.Group, walk *routeWalk) model.GroupItem {
	item := pickGroupItem(group, walk)
	if item.ID != 0 {
		walk.lastItemID = item.ID
	}
	return item
}

// itemIndex 按授权 ID 求成员在优先级序列中的下标, 测试断言用。
func itemIndex(t *testing.T, group model.Group, grantID int) int {
	t.Helper()
	for i, item := range group.Items {
		if item.ChannelGrantID == grantID {
			return i
		}
	}
	t.Fatalf("grant %d not in group %d", grantID, group.ID)
	return -1
}

// setCooldowns 直接改写分组路由状态的冷却表: offset 为相对当前的毫秒偏移, 0 表示删除条目。
func setCooldowns(t *testing.T, groupID int, offsets map[int]int64) {
	t.Helper()
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		route = &RouteState{GroupID: groupID, Cooldowns: map[int]int64{}}
		routes[groupID] = route
	}
	now := time.Now().UnixMilli()
	for itemID, offset := range offsets {
		if offset == 0 {
			delete(route.Cooldowns, itemID)
			continue
		}
		route.Cooldowns[itemID] = now + offset
	}
}

// routeSnapshot 取分组路由状态的可比较快照。
func routeSnapshot(t *testing.T, groupID int) RouteState {
	t.Helper()
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		return RouteState{Cooldowns: map[int]int64{}}
	}
	return *route
}

// TestPickGroupItem_cursorSticksToSameMember 验证同成员重试: 失败未达总尝试次数时游标粘住当前成员,
// 下一轮返回同一成员而非向下切换。
func TestPickGroupItem_cursorSticksToSameMember(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 101, "cursor-stick-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	walk := &routeWalk{}
	first := cursorPick(group, walk)
	if first.ChannelGrantID != grants[0] {
		t.Fatalf("first pick = grant %d, want grant %d (队头)", first.ChannelGrantID, grants[0])
	}
	// 未达 MemberMaxAttempts(2) 的失败不进冷却, 游标粘住。
	if recordRouteFailure(group, first.ID, 1) {
		t.Fatalf("failure below max attempts must not cool the member")
	}
	second := cursorPick(group, walk)
	if second.ID != first.ID {
		t.Fatalf("second pick = item %d, want stuck item %d", second.ID, first.ID)
	}
}

// TestPickGroupItem_cursorMovesDownNoCuttingIn 验证请求内游标向下与恢复不插队:
// 前 5 个成员冷却时从 #6 起步, #6 失败走 #7 而非回头; #1 中途恢复也不插队, 等游标绕回。
func TestPickGroupItem_cursorMovesDownNoCuttingIn(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 10)
	group := seedCursorGroup(t, 102, "cursor-down-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 前 5 个成员进冷却。
	offsets := make(map[int]int64)
	for _, grantID := range grants[:5] {
		offsets[group.Items[itemIndex(t, group, grantID)].ID] = time.Minute.Milliseconds()
	}
	setCooldowns(t, group.ID, offsets)

	walk := &routeWalk{}
	sixth := cursorPick(group, walk)
	if sixth.ChannelGrantID != grants[5] {
		t.Fatalf("pick = grant %d, want grant %d (#6, 冷却只做闸门)", sixth.ChannelGrantID, grants[5])
	}
	if !recordRouteFailure(group, sixth.ID, group.RelayConfig.MemberMaxAttempts) {
		t.Fatalf("failure at max attempts must cool the member")
	}
	seventh := cursorPick(group, walk)
	if seventh.ChannelGrantID != grants[6] {
		t.Fatalf("pick = grant %d, want grant %d (#7, 不回队头重扫)", seventh.ChannelGrantID, grants[6])
	}

	// #1 中途恢复: 高优先级成员不插队, 游标继续向下。
	setCooldowns(t, group.ID, map[int]int64{group.Items[0].ID: 0})
	if !recordRouteFailure(group, seventh.ID, group.RelayConfig.MemberMaxAttempts) {
		t.Fatalf("failure at max attempts must cool the member")
	}
	eighth := cursorPick(group, walk)
	if eighth.ChannelGrantID != grants[7] {
		t.Fatalf("pick = grant %d, want grant %d (#8, 恢复的 #1 不得插队)", eighth.ChannelGrantID, grants[7])
	}
}

// TestPickGroupItem_cursorWrapsAround 验证绕圈回炉: 游标走到末尾后绕回头部,
// 头部已过冷却则服务头部, 仍在冷却则继续向下。
func TestPickGroupItem_cursorWrapsAround(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 4)
	group := seedCursorGroup(t, 103, "cursor-wrap-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 游标置于末尾成员且其刚进冷却(冷却只做闸门): 从它的下一位扫描, 越过尾部绕回头部。
	tail := group.Items[3]
	walk := &routeWalk{lastItemID: tail.ID}
	setCooldowns(t, group.ID, map[int]int64{tail.ID: time.Minute.Milliseconds()})

	first := cursorPick(group, walk)
	if first.ChannelGrantID != grants[0] {
		t.Fatalf("pick = grant %d, want grant %d (绕回头部)", first.ChannelGrantID, grants[0])
	}

	// 头部仍在冷却时继续向下, 不等待。
	setCooldowns(t, group.ID, map[int]int64{group.Items[0].ID: time.Minute.Milliseconds()})
	second := cursorPick(group, walk)
	if second.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (头部冷却中继续向下)", second.ChannelGrantID, grants[1])
	}
}

// TestPickGroupItem_allCoolingWaitsForEarliestExpiry 验证全员冷却返回零值由调用方按既有间隔等待,
// 冷却到期后(取最早到期者)游标经探测放行继续, 无新增上限与报错终态。
func TestPickGroupItem_allCoolingWaitsForEarliestExpiry(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 104, "cursor-all-cooling-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 全员冷却: 返回零值, handler 沿用既有 wait(MemberRetryIntervalSeconds) 循环(零新增)。
	setCooldowns(t, group.ID, map[int]int64{
		group.Items[0].ID: time.Minute.Milliseconds(),
		group.Items[1].ID: time.Minute.Milliseconds(),
		group.Items[2].ID: time.Minute.Milliseconds(),
	})
	if item := pickGroupItem(group, &routeWalk{}); item.ID != 0 {
		t.Fatalf("pick = item %d, want zero value when all members cooling", item.ID)
	}

	// 等待语义与现状一致: 最早的冷却到期后, 下一次选路经探测放行该成员。
	setCooldowns(t, group.ID, map[int]int64{group.Items[1].ID: 30})
	time.Sleep(80 * time.Millisecond)
	walk := &routeWalk{}
	item := cursorPick(group, walk)
	if item.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (最早到期成员恢复探测)", item.ChannelGrantID, grants[1])
	}
	if snapshot := routeSnapshot(t, group.ID); snapshot.ProbeItemID != item.ID {
		t.Fatalf("probe = %d, want %d (单飞探测名额)", snapshot.ProbeItemID, item.ID)
	}
}

// TestPickGroupItem_probeSingleFlightSkipsAndRetries 验证探测单飞: 冷却已到期但探测名额被占时游标跳过,
// 名额归还后同一成员(含绕回路径)可再次被探测。
func TestPickGroupItem_probeSingleFlightSkipsAndRetries(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 105, "cursor-probe-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// #1 冷却已到期, #2 与 #3 冷却中, 探测名额被 #3 占用: 游标跳过 #1, 全员无候选返回零值。
	setCooldowns(t, group.ID, map[int]int64{
		group.Items[0].ID: -time.Second.Milliseconds(),
		group.Items[1].ID: time.Minute.Milliseconds(),
		group.Items[2].ID: time.Minute.Milliseconds(),
	})
	routeMu.Lock()
	routes[group.ID].ProbeItemID = group.Items[2].ID
	routeMu.Unlock()

	if item := pickGroupItem(group, &routeWalk{}); item.ID != 0 {
		t.Fatalf("pick = item %d, want zero value while probe slot occupied", item.ID)
	}

	// 名额归还后 #1 被探测放行。
	routeMu.Lock()
	routes[group.ID].ProbeItemID = 0
	routeMu.Unlock()
	item := pickGroupItem(group, &routeWalk{})
	if item.ChannelGrantID != grants[0] {
		t.Fatalf("pick = grant %d, want grant %d (冷却到期成员单飞探测)", item.ChannelGrantID, grants[0])
	}

	// 绕回可再试: 游标在尾部成员且全员冷却, 名额被占时跳过, 归还后绕回到 #1 探测。
	setCooldowns(t, group.ID, map[int]int64{
		group.Items[0].ID: -time.Second.Milliseconds(),
		group.Items[1].ID: time.Minute.Milliseconds(),
		group.Items[2].ID: time.Minute.Milliseconds(),
	})
	routeMu.Lock()
	routes[group.ID].ProbeItemID = group.Items[1].ID
	routeMu.Unlock()
	walk := &routeWalk{lastItemID: group.Items[2].ID}
	if item := pickGroupItem(group, walk); item.ID != 0 {
		t.Fatalf("pick = item %d, want zero value while probe slot occupied on wrap", item.ID)
	}
	routeMu.Lock()
	routes[group.ID].ProbeItemID = 0
	routeMu.Unlock()
	if item := pickGroupItem(group, walk); item.ChannelGrantID != grants[0] {
		t.Fatalf("pick = grant %d, want grant %d (绕回后再探测)", item.ChannelGrantID, grants[0])
	}
}

// TestPickGroupItem_affinityStartPoint 验证跨请求亲和保留: 故障切备用并成功后,
// 窗口内新请求的游标从备用成员起步, 而非回到队头。
func TestPickGroupItem_affinityStartPoint(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 106, "cursor-affinity-g", model.GroupModeFailover, grants)
	ResetRouteState(group.ID)

	// 队头失败进冷却, 游标切到备用成员并成功, 亲和武装。
	walk := &routeWalk{}
	first := cursorPick(group, walk)
	if !recordRouteFailure(group, first.ID, group.RelayConfig.MemberMaxAttempts) {
		t.Fatalf("failure at max attempts must cool the member")
	}
	backup := cursorPick(group, walk)
	if backup.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (备用成员)", backup.ChannelGrantID, grants[1])
	}
	recordRouteSuccess(group, backup.ID)
	if snapshot := routeSnapshot(t, group.ID); snapshot.AffinityUntil <= time.Now().UnixMilli() {
		t.Fatalf("affinity = %d, want armed after failover success", snapshot.AffinityUntil)
	}

	// 队头冷却解除后, 窗口内新请求仍从备用成员起步(亲和保留), 不被恢复的队头抢回。
	setCooldowns(t, group.ID, map[int]int64{first.ID: 0})
	next := pickGroupItem(group, &routeWalk{})
	if next.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (亲和窗口内从备用起步)", next.ChannelGrantID, grants[1])
	}
}

// TestPickGroupItem_roundRobinRotationAndIsolation 验证轮询模式: 连续请求按成员旋转起步,
// per-group 计数器隔离, 冷却成员被跳过后旋转不断链, 且不参与亲和。
func TestPickGroupItem_roundRobinRotationAndIsolation(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 107, "cursor-rr-g", model.GroupModeRoundRobin, grants)
	ResetRouteState(group.ID)

	// 连续请求起步位按 0,1,2,0 旋转。
	for i, want := range []int{0, 1, 2, 0} {
		if item := pickGroupItem(group, &routeWalk{}); item.ChannelGrantID != grants[want] {
			t.Fatalf("request %d pick = grant %d, want grant %d (轮询旋转)", i+1, item.ChannelGrantID, grants[want])
		}
	}

	// 另一分组计数器独立, 从它自己的队头轮起。
	other := seedCursorGroup(t, 108, "cursor-rr-other-g", model.GroupModeRoundRobin, grants[:2])
	ResetRouteState(other.ID)
	if item := pickGroupItem(other, &routeWalk{}); item.ChannelGrantID != grants[0] {
		t.Fatalf("other group pick = grant %d, want grant %d (计数器按分组隔离)", item.ChannelGrantID, grants[0])
	}

	// 轮询不参与亲和: 即便状态残留亲和窗口, 起步位仍由计数器决定。
	setCooldowns(t, group.ID, map[int]int64{group.Items[2].ID: time.Minute.Milliseconds()})
	routeMu.Lock()
	routes[group.ID].CurrentItemID = group.Items[2].ID
	routes[group.ID].AffinityUntil = time.Now().Add(time.Minute).UnixMilli()
	routeMu.Unlock()
	// 计数器当前在 4(前四次请求), 下一次起步位为 (5-1)%3=1。
	if item := pickGroupItem(group, &routeWalk{}); item.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (轮询不理会亲和)", item.ChannelGrantID, grants[1])
	}

	// 冷却成员被跳过后旋转不断链: 解除亲和阶段的冷却, 只冷 #1,
	// 起步位仍按请求推进(计数器在 5, 起步为 2,0,1,2), 起步撞上冷却成员就跳过继续向下。
	setCooldowns(t, group.ID, map[int]int64{
		group.Items[0].ID: time.Minute.Milliseconds(),
		group.Items[2].ID: 0,
	})
	for i, want := range []int{2, 1, 1, 2} {
		if item := pickGroupItem(group, &routeWalk{}); item.ChannelGrantID != grants[want] {
			t.Fatalf("request %d pick = grant %d, want grant %d (跳过冷却成员后旋转不断链)", i+1, item.ChannelGrantID, grants[want])
		}
	}
}

// TestPickGroupItem_roundRobinDoesNotArmAffinity 验证轮询不参与亲和的另一半: 当前成员失败切换后,
// 新成员上的成功不得武装亲和窗口, 路由状态不残留 affinity_until(failover 同路径会武装, 见亲和起点用例)。
func TestPickGroupItem_roundRobinDoesNotArmAffinity(t *testing.T) {
	initCursorTestDB(t)
	grants := seedCursorChannel(t, 1, 1, 3)
	group := seedCursorGroup(t, 112, "cursor-rr-affinity-g", model.GroupModeRoundRobin, grants)
	ResetRouteState(group.ID)

	walk := &routeWalk{}
	first := cursorPick(group, walk)
	if first.ChannelGrantID != grants[0] {
		t.Fatalf("pick = grant %d, want grant %d", first.ChannelGrantID, grants[0])
	}
	if !recordRouteFailure(group, first.ID, group.RelayConfig.MemberMaxAttempts) {
		t.Fatalf("failure at max attempts must cool the member")
	}
	second := cursorPick(group, walk)
	if second.ChannelGrantID != grants[1] {
		t.Fatalf("pick = grant %d, want grant %d (失败后向下切换)", second.ChannelGrantID, grants[1])
	}
	recordRouteSuccess(group, second.ID)
	if snapshot := routeSnapshot(t, group.ID); snapshot.AffinityUntil != 0 {
		t.Fatalf("affinity = %d, want 0 (轮询失败切换后的成功不得武装亲和)", snapshot.AffinityUntil)
	}
}

// TestPickGroupItem_channelBlockContinuity 验证渠道主序: 同渠道的多个成员(多凭据/多模型变体)在
// 游标序内连续, 渠道内轮换完才换下一个渠道; 走完一整圈全员冷却后返回零值。
func TestPickGroupItem_channelBlockContinuity(t *testing.T) {
	initCursorTestDB(t)
	channelA := seedCursorChannel(t, 1, 1, 2)
	channelB := seedCursorChannel(t, 2, 3, 3)
	group := seedCursorGroup(t, 109, "cursor-blocks-g", model.GroupModeFailover, append(append([]int{}, channelA...), channelB...))
	ResetRouteState(group.ID)

	walk := &routeWalk{}
	var visited []int
	for round := 0; round < len(group.Items); round++ {
		item := cursorPick(group, walk)
		if item.ID == 0 {
			t.Fatalf("round %d pick = zero, want a member before the full cycle completes", round)
		}
		visited = append(visited, item.ChannelGrantID)
		if !recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts) {
			t.Fatalf("failure at max attempts must cool the member")
		}
	}
	// 渠道块连续: 渠道 A 的两个成员整体在前, 渠道 B 的三个成员随后。
	want := append(append([]int{}, channelA...), channelB...)
	for i := range want {
		if visited[i] != want[i] {
			t.Fatalf("visit order = %v, want %v (同渠道成员连续)", visited, want)
		}
	}
	// 一整圈走完全员冷却, 返回零值交回调用方等待循环。
	if item := pickGroupItem(group, walk); item.ID != 0 {
		t.Fatalf("pick = item %d, want zero value after a full cooling cycle", item.ID)
	}
}

// seedForwardTestGroup 为 handler 级中止测试播种: 渠道指向上游测试服务, 单成员故障转移分组,
// 刷新缓存后返回分组(成员主键已定稿)。
func seedForwardTestGroup(t *testing.T, groupID int, name, baseURL string) model.Group {
	t.Helper()
	if err := db.GetDB().Create(&model.Channel{
		ID: 1,
		ChannelConfig: model.ChannelConfig{
			Name:                     "forward-channel",
			BaseURL:                  baseURL,
			OpenAIChatCompletionPath: "/v1/chat/completions",
		},
	}).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return seedCursorGroup(t, groupID, name, model.GroupModeFailover, seedCursorGrants(t, 1, 1, 1))
}

// waitForRequestStatus 轮询进程内请求状态直至指定分组模型的目标状态出现, 超时 fatal。
func waitForRequestStatus(t *testing.T, requestModel string, status Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := OpenRequestStream()
		for i := range snapshot {
			if snapshot[i].Model == requestModel && snapshot[i].Status == status {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no request for %s reached status %s in time", requestModel, status)
}

// TestForward_clientAbortBeforeCommitRecordsNoFailure 验证客户端中止不计失败(R9):
// 等待上游响应期间客户端取消, 该轮不追加 AttemptFailed, 不触发冷却, 渠道统计无失败,
// 转发日志以空 attempts 落库(请求未产生真实渠道故障)。
func TestForward_clientAbortBeforeCommitRecordsNoFailure(t *testing.T) {
	setupRelayLogTest(t)

	// 上游收到请求后挂住等客户端断开, 保证取消发生在本轮已在途(pre-commit)时。
	// 必须先读完请求体再挂住: server 端对未消费完 body 的请求不感知客户端断开, 挂住的连接会让 Close 无法返回。
	received := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer func() {
		close(release)
		upstream.Close()
	}()

	group := seedForwardTestGroup(t, 110, "cursor-abort-g", upstream.URL)
	ResetRouteState(group.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/relay", Forward(llm.APIFormatOpenAIChatCompletion))

	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		request := httptest.NewRequest(http.MethodPost, "/relay",
			strings.NewReader(`{"model":"cursor-abort-g","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request.WithContext(ctx))
	}()

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatalf("upstream never received the forwarded request")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("forward handler did not finish in time")
	}

	// 取消轮次不追加失败尝试: 日志空 attempts, 最终渠道为零值, 错误为取消原因。
	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	if logs[0].TotalAttempts != 0 || len(logs[0].Attempts) != 0 {
		t.Fatalf("attempts = %d/%d, want 0/0 (客户端中止不计失败)", logs[0].TotalAttempts, len(logs[0].Attempts))
	}
	if logs[0].ChannelId != 0 {
		t.Fatalf("final channel = %d, want 0", logs[0].ChannelId)
	}
	if !strings.Contains(logs[0].Error, "canceled") {
		t.Fatalf("error = %q, want context canceled", logs[0].Error)
	}

	// 中止不进冷却、不记渠道失败: 选路状态与渠道统计保持干净。
	if snapshot := routeSnapshot(t, group.ID); len(snapshot.Cooldowns) != 0 {
		t.Fatalf("cooldowns = %v, want empty after client abort", snapshot.Cooldowns)
	}
	channel, err := op.ChannelGet(1)
	if err != nil {
		t.Fatalf("ChannelGet(1): %v", err)
	}
	if channel.RequestFailed != 0 || channel.RequestSuccess != 0 {
		t.Fatalf("channel stats failed/success = %d/%d, want 0/0 after client abort", channel.RequestFailed, channel.RequestSuccess)
	}
}

// TestForward_clientAbortMidStreamRecordsNoChannelFailure 验证流式中途客户端断开不计渠道失败(R9):
// 首帧已提交后客户端取消, 渠道统计既无成功也无失败(只保留等待耗时), attempts 只有提交前的一条成功。
func TestForward_clientAbortMidStreamRecordsNoChannelFailure(t *testing.T) {
	setupRelayLogTest(t)

	// 上游发出首个流事件后挂住, 客户端取消时连接随之断开, 模拟流式中途断开。
	// 与非流式用例同理: 先读完请求体再挂住, server 端才能感知客户端断开。
	firstFlushed := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m-1\"," +
			"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case firstFlushed <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		upstream.Close()
	}()

	group := seedForwardTestGroup(t, 111, "cursor-abort-stream-g", upstream.URL)
	ResetRouteState(group.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/relay", Forward(llm.APIFormatOpenAIChatCompletion))

	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		request := httptest.NewRequest(http.MethodPost, "/relay",
			strings.NewReader(`{"model":"cursor-abort-stream-g","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request.WithContext(ctx))
	}()

	select {
	case <-firstFlushed:
	case <-time.After(5 * time.Second):
		t.Fatalf("upstream never flushed the first stream event")
	}
	// 等首帧提交后再取消, 保证取消发生在流式转发中途。
	waitForRequestStatus(t, "cursor-abort-stream-g", StatusCommitted)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("forward handler did not finish in time")
	}

	// 渠道统计无 RequestFailed: 客户端断开不属于渠道故障。
	channel, err := op.ChannelGet(1)
	if err != nil {
		t.Fatalf("ChannelGet(1): %v", err)
	}
	if channel.RequestFailed != 0 {
		t.Fatalf("channel RequestFailed = %d, want 0 (流式中途断开不计渠道失败)", channel.RequestFailed)
	}
	if channel.RequestSuccess != 0 {
		t.Fatalf("channel RequestSuccess = %d, want 0 (流未完整交付)", channel.RequestSuccess)
	}

	// attempts 只含提交前的一条成功尝试, 不追加失败。
	logs := flushAndLoad(t)
	if len(logs) != 1 {
		t.Fatalf("expected 1 persisted log, got %d", len(logs))
	}
	if logs[0].TotalAttempts != 1 || len(logs[0].Attempts) != 1 || logs[0].Attempts[0].Status != model.AttemptSuccess {
		t.Fatalf("attempts = %+v, want single success attempt", logs[0].Attempts)
	}
	if !strings.Contains(logs[0].Error, "canceled") {
		t.Fatalf("error = %q, want context canceled", logs[0].Error)
	}

	// 成功轮次已解除冷却, 中止路径不再写入新的冷却。
	if snapshot := routeSnapshot(t, group.ID); len(snapshot.Cooldowns) != 0 {
		t.Fatalf("cooldowns = %v, want empty after mid-stream abort", snapshot.Cooldowns)
	}
}
