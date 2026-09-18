package relay

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// RouteState 是一个分组的进程内路由状态; 跨该分组的全部请求共享。
// 同时作为路由流的消息形状与分组读取响应中的 runtime 字段: 冷却, 探测与亲和都是本包路由算法的概念,
// 故状态形状由本包定义, 分组的持久化配置不含它; 内部标志未导出, 不会随消息出到 JSON。
// 两种模式共用 CurrentItemID: 手动模式下即人工指定的成员, 故障转移模式下由路由决定,
// 前端由此只读这一个字段即可知道当前承载请求的成员, 无需再按模式分支。
type RouteState struct {
	GroupID       int           `json:"group_id"`        // 状态所属的分组 ID, 供状态流按分组定位。
	CurrentItemID int           `json:"current_item_id"` // 当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定。
	ProbeItemID   int           `json:"probe_item_id"`   // 当前占用恢复探测的成员 ID, 同一分组同时只允许一个成员被探测; 手动模式恒为 0。
	AffinityUntil int64         `json:"affinity_until"`  // 当前路由的亲和截止 Unix 毫秒时间, 0 表示无亲和; 手动模式恒为 0。
	Cooldowns     map[int]int64 `json:"cooldowns"`       // 失败成员 ID 对应的冷却截止 Unix 毫秒时间, 已到期的条目由前端按当前时间忽略。

	affinityArmed bool // 当前路由下一次成功后是否开始亲和, 仅故障切换后为真。

	trips map[int]int // 成员 ID 对应的连续冷却次数, 决定指数退避档位; 未导出故不出 JSON, 与 Cooldowns 同生命周期。
}

// cooldownMaxShift 是指数退避的移位上限, 防止连续冷却次数过大时移位溢出; 实际时长仍由封顶值兜底。
const cooldownMaxShift = 20

// cooldownSeconds 返回成员第 trips 次连续冷却的时长: base 起按 2 倍递增, 封顶 max。
// 成功一次后 trips 清零, 下次回到 base。移位上限与封顶双重保护, 不做可能溢出的移位。
func cooldownSeconds(base, max, trips int) int {
	if base < 1 {
		base = 1
	}
	if max < base {
		max = base
	}
	shift := trips - 1
	if shift < 0 {
		shift = 0
	}
	if shift > cooldownMaxShift {
		shift = cooldownMaxShift
	}
	// base<<shift 会超过封顶时直接取上限, 同时避免移位溢出(int 为有符号)。
	if base > max>>uint(shift) {
		return max
	}
	return base << shift
}

const routeStreamBuffer = 16 // 单个路由流连接的非阻塞消息缓冲容量。

var (
	routeMu      sync.Mutex                           // routeMu 保护全部分组路由状态。
	routes       = make(map[int]*RouteState)          // routes 按分组 ID 保存路由状态。
	routeStreams = make(map[chan RouteState]struct{}) // 全部路由 SSE 连接。
)

// routeWalk 是单个转发请求内的选路游标: 记录本轮"走到哪个成员", 决定下一次从哪继续。
// 游标只在本请求内存活; 跨请求的亲和/冷却/探测仍在 RouteState。
// 请求内游标只向下走: 成员失败后从它的下一位继续而非回到队头重扫, 走完一圈才绕回头部,
// 高优先级成员中途恢复也不插队, 等游标绕回时自然轮到; 成员多冷却短时不会反复回头撞刚失败的渠道。
type routeWalk struct {
	lastItemID int // 最近一次选中并尝试的成员; 0 表示尚未开始。
}

// roundRobinCounters 是轮询模式的 per-group 原子计数器, 每个请求起步时递增一次决定旋转起点。
var roundRobinCounters sync.Map // map[int]*uint64, 按分组 ID 隔离。

// RouteStateOf 返回分组当前的实时路由状态, 供读取接口随分组一并返回。
// 手动模式没有进程内路由: 当前成员即人工指定的成员, 冷却与亲和均不适用, 故直接由分组配置得出。
func RouteStateOf(group model.Group) RouteState {
	if group.Mode == model.GroupModeManual {
		return RouteState{
			GroupID:       group.ID,
			CurrentItemID: group.ActiveItemID,
			Cooldowns:     map[int]int64{},
		}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	}
	state := *route
	state.Cooldowns = maps.Clone(route.Cooldowns)
	return state
}

// ResetRouteState 丢弃分组的进程内路由状态, 用于分组切换选择模式或被删除。
// 不丢弃的话冷却与亲和会在 failover 切到 manual 再切回来之后复活并继续影响选路, 分组删除后其状态也会永久残留。
// 轮询计数器一并丢弃: 路由状态是它的语义归属, 分组重建后从队头重新轮起。
func ResetRouteState(groupID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	delete(routes, groupID)
	roundRobinCounters.Delete(groupID)
}

// pickGroupItem 按分组模式选择本轮目标成员, 没有可用成员时返回零值; group.Items 已按 Priority 升序排列。
// walk 是本请求的游标: 选中后调用方把成员 ID 写回 walk, 下一轮由此继续(粘住重试或向下切换)。
// 选路前先剔除不可选成员: 渠道或凭据被禁用以及授权两侧缺失与冷却同级, 直接不进入选路,
// 不产生失败计数, 不占用恢复探测名额; 禁用不等于删除, 成员仍在分组里, 界面以 Available 标记不可用。
func pickGroupItem(group model.Group, walk *routeWalk) model.GroupItem {
	items := selectableGroupItems(group)
	if group.Mode == model.GroupModeManual {
		for _, item := range items {
			if item.ID == group.ActiveItemID {
				return item
			}
		}
		return model.GroupItem{}
	}
	if len(items) == 0 {
		return model.GroupItem{}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := groupRouteLocked(group)
	now := time.Now().UnixMilli()
	if route.AffinityUntil <= now {
		route.AffinityUntil = 0
	}

	// 粘住当前: 同一请求内上一轮选中的成员仍可选且未进冷却时继续用它, 这就是"同一成员重试";
	// 已进冷却说明该成员的尝试次数耗尽, 游标从它的下一位继续。
	if walk.lastItemID != 0 {
		if last := itemOf(items, walk.lastItemID); last.ID != 0 {
			if deadline, cooling := route.Cooldowns[last.ID]; !cooling || deadline <= now {
				return last
			}
		}
	}

	// 确定扫描起点: 有游标基准时从基准的下一位开始(到尾绕回头部); 基准消失(成员被删或禁用)或尚未开始时按模式定起点。
	// 用 ID 定位而非下标: 分组每轮重读, 成员集合可能增删, ID 稳定而"基准之后的第一个"在集合变化后仍然语义正确。
	start := 0
	if walk.lastItemID != 0 {
		if base := indexOfItem(items, walk.lastItemID); base >= 0 {
			start = base + 1
		} else {
			start = routeStartIndex(group, route, items, now)
		}
	} else {
		start = routeStartIndex(group, route, items, now)
	}
	return scanGroupItems(items, route, now, start)
}

// routeStartIndex 在游标没有基准时按模式给出起步位置。
// 故障转移: 亲和期内从当前成员起步(跨请求亲和), 亲和期内当前成员已不可选(如渠道被禁用)则亲和立即失效回队头。
// 轮询: per-group 计数器旋转起步位, 每个请求轮到下一名成员; 旋转是轮询的存在意义, 不参与亲和。
func routeStartIndex(group model.Group, route *RouteState, items []model.GroupItem, now int64) int {
	if group.Mode == model.GroupModeRoundRobin {
		counter, _ := roundRobinCounters.LoadOrStore(group.ID, new(uint64))
		return int((atomic.AddUint64(counter.(*uint64), 1) - 1) % uint64(len(items)))
	}

	// 亲和期内当前成员已不可选(如渠道被禁用)时亲和立即失效, 重新选路。
	if route.CurrentItemID != 0 && route.AffinityUntil > now {
		if base := indexOfItem(items, route.CurrentItemID); base >= 0 {
			return base
		}
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		publishRouteLocked(route)
	}
	return 0
}

// scanGroupItems 从 start 起按优先级环形扫描一周, 返回第一个通过冷却与探测门控的成员;
// 全员被门控挡下时返回零值, 调用方按既有重试间隔等待后重扫, 不新增上限与报错终态。
// 冷却只做闸门: 冷却中的成员直接跳过继续向下; 冷却已到期的成员只放行一个探测请求, 避免全部请求同时涌向尚未恢复的成员。
func scanGroupItems(items []model.GroupItem, route *RouteState, now int64, start int) model.GroupItem {
	for i := 0; i < len(items); i++ {
		item := items[(start+i)%len(items)]
		deadline, cooling := route.Cooldowns[item.ID]
		if cooling && deadline > now {
			continue
		}
		if cooling {
			if route.ProbeItemID != 0 {
				continue
			}
			route.ProbeItemID = item.ID
			publishRouteLocked(route)
			return item
		}
		route.CurrentItemID = item.ID
		publishRouteLocked(route)
		return item
	}
	return model.GroupItem{}
}

// selectableGroupItems 返回分组内当前可选路的成员, 保持原有优先级顺序。
// 可选与界面 Available 同口径: ChannelGrantGet 能取得授权, 即渠道与凭据均启用且授权两侧均在。
// 转发每轮重读分组后调用, 只查内存缓存, 不引入新的缓存副本。
func selectableGroupItems(group model.Group) []model.GroupItem {
	items := make([]model.GroupItem, 0, len(group.Items))
	for _, item := range group.Items {
		if _, err := op.ChannelGrantGet(item.ChannelGrantID); err == nil {
			items = append(items, item)
		}
	}
	return items
}

// recordRouteSuccess 上报一轮成功: 结束该成员的冷却与探测占用, 并在故障切换后按配置开始亲和。
func recordRouteSuccess(group model.Group, itemID int) {
	if group.Mode == model.GroupModeManual {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := false

	// 成功一次即清零该成员的连续冷却次数, 下次失败回到基础冷却时长; trips 不出 JSON, 无需单独发布。
	delete(route.trips, itemID)

	// 探测成功说明该成员已恢复, 解除冷却; 若当前路由不在亲和期内则立即切回该成员。
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		delete(route.Cooldowns, itemID)
		if route.CurrentItemID == 0 || route.AffinityUntil <= now {
			route.CurrentItemID = itemID
			route.AffinityUntil = 0
		}
		changed = true
	}
	// 亲和只在故障切换后的首次成功时开始, 使请求在一段时间内稳定留在备用成员上。
	if route.CurrentItemID == itemID && route.affinityArmed {
		route.affinityArmed = false
		if group.RelayConfig.MemberAffinitySeconds > 0 {
			route.AffinityUntil = now + int64(group.RelayConfig.MemberAffinitySeconds)*1000
			changed = true
		}
	}
	if changed {
		publishRouteLocked(route)
	}
}

// recordRouteFailure 上报一轮失败: 达到配置的总尝试次数后将该成员打入冷却并让出当前路由, 返回是否已冷却。
// failures 为该成员在本请求内包含首次请求的连续失败次数, 由调用方累计。
func recordRouteFailure(group model.Group, itemID, failures int) bool {
	if group.Mode == model.GroupModeManual {
		return false
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return false
	}
	// 探测请求只有一次机会, 常规成员达到配置的总尝试次数后进入冷却。
	if route.ProbeItemID != itemID && failures < group.RelayConfig.MemberMaxAttempts {
		return false
	}

	now := time.Now().UnixMilli()
	// 连续冷却按指数退避升级: 第 N 次触发时长 = min(base × 2^(N-1), 上限); 成功一次由 recordRouteSuccess 清零。
	if route.trips == nil {
		route.trips = make(map[int]int)
	}
	route.trips[itemID]++
	trips := route.trips[itemID]
	cooldown := cooldownSeconds(group.RelayConfig.MemberCooldownSeconds, group.RelayConfig.MemberMaxCooldownSeconds, trips)
	route.Cooldowns[itemID] = now + int64(cooldown)*1000
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
	}
	// 当前路由失败才需要下一个成员开始亲和; 独立探测失败不影响当前路由。
	// 亲和只在故障转移模式下武装: 轮询按计数器旋转起步, 不参与亲和, 失败切换后不得留下亲和窗口。
	if route.CurrentItemID == itemID {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		if group.Mode == model.GroupModeFailover {
			route.affinityArmed = true
		}
	}
	publishRouteLocked(route)
	return true
}

// releaseRouteProbe 归还未产生成败结论的探测占用, 用于请求被人工中止或客户端断开。
func releaseRouteProbe(group model.Group, itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[group.ID]; route != nil && route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		publishRouteLocked(route)
	}
}

// groupRouteLocked 取出分组路由状态并清理已删除成员的残留; 调用方必须持有锁。
func groupRouteLocked(group model.Group) *RouteState {
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64), trips: make(map[int]int)}
		routes[group.ID] = route
	}
	items := make(map[int]bool, len(group.Items))
	for _, item := range group.Items {
		items[item.ID] = true
	}
	for itemID := range route.Cooldowns {
		if !items[itemID] {
			delete(route.Cooldowns, itemID)
		}
	}
	// 已删除成员的连续冷却次数一并清理, 与冷却同生命周期, 不留残留计数。
	for itemID := range route.trips {
		if !items[itemID] {
			delete(route.trips, itemID)
		}
	}
	if route.ProbeItemID != 0 && !items[route.ProbeItemID] {
		route.ProbeItemID = 0
	}
	if route.CurrentItemID != 0 && !items[route.CurrentItemID] {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = false
	}
	return route
}

// itemOf 返回成员列表内指定 ID 的成员, 不存在时返回零值。
func itemOf(items []model.GroupItem, itemID int) model.GroupItem {
	for _, item := range items {
		if item.ID == itemID {
			return item
		}
	}
	return model.GroupItem{}
}

// indexOfItem 返回成员在列表内的位置, 不存在时返回 -1。
func indexOfItem(items []model.GroupItem, itemID int) int {
	for i, item := range items {
		if item.ID == itemID {
			return i
		}
	}
	return -1
}

// publishRouteLocked 非阻塞发布路由状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 冷却表按值复制以免前端读到后续变更; 调用方必须持有锁。
func publishRouteLocked(route *RouteState) {
	message := *route
	message.Cooldowns = maps.Clone(route.Cooldowns)
	for stream := range routeStreams {
		select {
		case stream <- message:
		default:
			delete(routeStreams, stream)
			close(stream)
		}
	}
}

// OpenRouteStream 注册路由流连接, 返回后续增量通道。
// 不再返回快照: 分组读取接口已随分组带回当前路由状态, 前端由此拿到的初始值即全量, 连接只负责增量。
func OpenRouteStream() chan RouteState {
	routeMu.Lock()
	defer routeMu.Unlock()

	stream := make(chan RouteState, routeStreamBuffer)
	routeStreams[stream] = struct{}{}
	return stream
}

// CloseRouteStream 注销并关闭指定路由流连接。
func CloseRouteStream(stream chan RouteState) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if _, exists := routeStreams[stream]; exists {
		delete(routeStreams, stream)
		close(stream)
	}
}
