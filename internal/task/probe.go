package task

import (
	"context"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/charmbracelet/log"
)

// 定时探测: 每个分组成员各自有一个到期时刻(group_items.probe_next_at), 到期就单独探它一次,
// 探完按渠道类型排下一次。不再有"一轮把所有成员打一遍"的概念。
//
// 为什么从"轮次"改成"成员排期": 原来一轮 6-12 小时、每轮全量, 分组之间并发 3 个、组内并发 4 个,
// 瞬时最多 12 个请求同时在飞; 而同一个渠道常同时出现在多个分组里(生产实测有渠道横跨 13 个分组、
// 单组 38 个成员)。一个 RPM 只有个位数的公益站几秒内就会被打出 429, 而 429 会被记成失败,
// 三轮之后好成员就被判死 —— 那是探测自己制造的误判。
// 改成成员排期后探测总量不变(每人每 6-12 小时一次), 但每个成员的到期时刻彼此独立,
// 同一渠道的成员不会在同一秒一起开打; 再叠加"同一渠道同时只放一个"与全局 6 个在飞的上限。
const (
	probeIntervalMin = 6 * time.Hour  // 单成员两次探测的最短间隔。
	probeIntervalMax = 12 * time.Hour // 最长间隔; 区间内均匀取随机值, 让成员的到期时刻彼此错开。
	// probeScanInterval 调度扫描间隔: 到期精度是这个量级, 不需要更密。
	probeScanInterval = 30 * time.Second
	// probeBatchSize 单次扫描最多派发的成员数: 某次扫描同时到期的成员很多时分批消化, 不一次打满。
	probeBatchSize = 20
	// probeWorkerConcurrency 同时在飞的探测上限(原实现最坏 3 组 × 4 成员 = 12)。
	probeWorkerConcurrency = 6
	// probeItemTimeout 单个成员的探测上限: 最多三个协议位 × 15 秒, 再留一点余量。
	probeItemTimeout = 90 * time.Second
	// probeDisabledRecheck 关闭状态下重读设置的间隔: 设置项变更没有回调, 只能轮询。
	probeDisabledRecheck = 10 * time.Minute
	// probeFirstRoundDelay 从关闭切到开启后的首次扫描等待: 不在保存设置的瞬间就开打。
	probeFirstRoundDelay = time.Minute

	// 官方渠道(按量计费或消耗免费试用额度)每天只在夜间窗口里探一次。
	// 窗口内随机落点, 所以官方成员也不会在同一分钟一起开打。
	officialProbeDefaultHour = 3
	officialProbeWindow      = 3 * time.Hour
	officialProbeMinGap      = 18 * time.Hour

	// 手动"全量探测一次"仍按分组并发跑, 参数沿用原来的轮次口径。
	probeRoundTimeout     = 10 * time.Minute
	probeGroupConcurrency = 3
)

// probeScheduleSpreadSeconds 未排期成员的初始铺开跨度, 与最长间隔同值:
// 首次启动时把全部成员摊到 12 小时里, 而不是让它们在启动瞬间一起到期。
const probeScheduleSpreadSeconds = int64(probeIntervalMax / time.Second)

// ProbeStatus 是调度器与手动全量轮的对外快照。
// 定时探测不再有"轮", 所以进度只剩手动那一轮用得上; 日常看的是待测个数与上次完成时间。
type ProbeStatus struct {
	Running        bool  `json:"running"`          // 有探测在飞(含手动全量轮)。
	Enabled        bool  `json:"enabled"`          // 定时探测总开关。
	Pending        int   `json:"pending"`          // 当前已到期待测的成员数。
	InFlight       int   `json:"in_flight"`        // 正在探测的成员数。
	NextDueAt      int64 `json:"next_due_at"`      // 最近一个未到期成员的到期时刻; 0 表示没有待测成员。
	LastFinishedAt int64 `json:"last_finished_at"` // 最近一次成员探测完成时间。
	TotalOK        int64 `json:"total_ok"`         // 本进程累计成功次数。
	TotalFailed    int64 `json:"total_failed"`     // 本进程累计失败次数。
	ManualRunning  bool  `json:"manual_running"`   // 手动"全量探测一次"是否在跑。
	ManualTotal    int   `json:"manual_total"`     // 手动轮要测的分组数。
	ManualDone     int   `json:"manual_done"`      // 手动轮已完成的分组数(含跳过)。
}

var (
	probeStatusMu sync.RWMutex
	probeStatus   ProbeStatus

	// probeSchedule 是 group_items.probe_next_at 的内存副本, 库里的列是重启后的来源。
	probeScheduleMu sync.RWMutex
	probeSchedule   = make(map[int]int64)

	// probeSem 限制同时在飞的探测数; 用带缓冲 channel 而不是计数器, 释放语义更直白。
	probeSem = make(chan struct{}, probeWorkerConcurrency)
	// probeChannels 记录正在被探测的渠道: 同一渠道同时只允许一个探测, 撞上就等下一 tick。
	probeChannelsMu sync.Mutex
	probeChannels   = make(map[int]struct{})

	// probeRoundRunning 保证手动全量轮不会被并发触发。
	probeRoundRunning atomic.Bool
)

// ProbeStatusSnapshot 返回当前状态副本, 供 HTTP 接口读取。
func ProbeStatusSnapshot() ProbeStatus {
	probeStatusMu.RLock()
	status := probeStatus
	probeStatusMu.RUnlock()
	status.Enabled = isGroupProbeEnabled()
	status.InFlight = len(probeSem)
	status.Running = status.ManualRunning || status.InFlight > 0
	return status
}

// probeStatusUpdate 在写锁内改状态, 避免调用方各自拼加锁代码。
func probeStatusUpdate(fn func(*ProbeStatus)) {
	probeStatusMu.Lock()
	defer probeStatusMu.Unlock()
	fn(&probeStatus)
}

// startGroupProbe 启动调度协程。开关默认关闭, 由设置项 group_probe_enabled 控制。
func startGroupProbe() {
	go func() {
		initProbeSchedule()
		firstScan := true
		for {
			// 关闭状态下不退出协程, 只按分钟级间隔重读设置: 用户随时可能打开。
			if !isGroupProbeEnabled() {
				time.Sleep(probeDisabledRecheck)
				// 重新打开时再等一分钟: 不在保存设置的瞬间就开打。
				firstScan = true
				continue
			}
			if firstScan {
				time.Sleep(probeFirstRoundDelay)
				firstScan = false
				continue
			}
			runProbeScheduleTick()
			time.Sleep(probeScanInterval)
		}
	}()
}

// isGroupProbeEnabled 每次扫描前再确认一次开关。
func isGroupProbeEnabled() bool {
	enabled, err := op.SettingGetBool(model.SettingKeyGroupProbeEnabled)
	return err == nil && enabled
}

// initProbeSchedule 载入排期: 先把没排过的成员铺开, 再读进内存。
// 读库失败只降级为"内存里没有这个成员", 后续 probeDueAt 会按主键现算并补写, 不影响功能。
func initProbeSchedule() {
	if err := op.ProbeScheduleBackfill(time.Now().Unix(), probeScheduleSpreadSeconds); err != nil {
		log.Warnf("probe schedule backfill failed: %v", err)
	}
	schedule, err := op.ProbeScheduleLoad()
	if err != nil {
		log.Warnf("probe schedule load failed: %v", err)
		return
	}
	probeScheduleMu.Lock()
	probeSchedule = schedule
	probeScheduleMu.Unlock()
}

// randomProbeInterval 在 [min, max) 内均匀取一个间隔。
func randomProbeInterval() time.Duration {
	spread := int64(probeIntervalMax - probeIntervalMin)
	return probeIntervalMin + time.Duration(rand.Int64N(spread))
}

// probeDueAt 返回该成员的下次到期时刻; 没见过(新建成员、编辑分组后新增)就现算一个并落库。
func probeDueAt(itemID int, now int64, official bool) int64 {
	probeScheduleMu.RLock()
	next, ok := probeSchedule[itemID]
	probeScheduleMu.RUnlock()
	if ok {
		return next
	}
	if official {
		// 官方成员从第一次起就落在夜间窗口: 否则首次排期可能把它排到白天, 白花额度。
		next = nextOfficialProbeAt(time.Unix(now, 0))
	} else {
		// 按主键取模铺开: 顺序稳定, 重启后同一成员仍是同一时刻, 分布也均匀。
		next = now + int64(itemID)%probeScheduleSpreadSeconds
	}
	setProbeNextAt(itemID, next)
	return next
}

// setProbeNextAt 同时更新内存与库: 内存供调度读, 库供重启后恢复。
func setProbeNextAt(itemID int, next int64) {
	probeScheduleMu.Lock()
	probeSchedule[itemID] = next
	probeScheduleMu.Unlock()
	if err := op.GroupItemSetProbeNextAt(itemID, next); err != nil {
		log.Warnf("group item %d probe schedule write failed: %v", itemID, err)
	}
}

// probeCandidate 是本次扫描要探的一个成员。
type probeCandidate struct {
	group    model.Group
	item     model.GroupItem
	official bool
	dueAt    int64
}

// runProbeScheduleTick 扫一次到期成员并派发; 派不出去的留在队列里等下一 tick。
func runProbeScheduleTick() {
	now := time.Now().Unix()
	candidates, pending, nextDue := collectProbeCandidates(now)
	probeStatusUpdate(func(status *ProbeStatus) {
		status.Pending = pending
		status.NextDueAt = nextDue
	})
	if len(candidates) > probeBatchSize {
		candidates = candidates[:probeBatchSize]
	}
	for _, candidate := range candidates {
		// 并发满了就停: 剩下的下一 tick 再来, 它们的到期时刻不会被改, 只是晚一点。
		select {
		case probeSem <- struct{}{}:
		default:
			return
		}
		// 同一渠道同时只放一个: 撞上就跳过这一轮, 到期时刻不动, 下一 tick 再试。
		if !claimProbeChannel(candidate.item.ChannelID) {
			<-probeSem
			continue
		}
		go func(candidate probeCandidate) {
			defer func() {
				releaseProbeChannel(candidate.item.ChannelID)
				<-probeSem
			}()
			probeOneItem(candidate)
		}(candidate)
	}
}

// collectProbeCandidates 扫出已到期的成员, 同时给出待测总数与最近一个未到期的时刻。
// 顺带跳过不可用成员(渠道或凭据被禁用): 探了也只会得到 unavailable, 白占并发。
func collectProbeCandidates(now int64) (due []probeCandidate, pending int, nextDue int64) {
	for _, group := range op.GroupList() {
		for _, item := range group.Items {
			if !item.Available {
				continue
			}
			// 渠道信息先取: 官方成员的首个到期时刻要直接排进夜间窗口。
			channel, err := op.ChannelGet(item.ChannelID)
			official := err == nil && channel.Official
			dueAt := probeDueAt(item.ID, now, official)
			if dueAt > now {
				if nextDue == 0 || dueAt < nextDue {
					nextDue = dueAt
				}
				continue
			}
			due = append(due, probeCandidate{
				group:    group,
				item:     item,
				official: official,
				dueAt:    dueAt,
			})
		}
	}
	pending = len(due)
	// 到期早的先探: 掉队最久的成员优先补测。
	sort.Slice(due, func(i, j int) bool {
		if due[i].dueAt != due[j].dueAt {
			return due[i].dueAt < due[j].dueAt
		}
		return due[i].item.ID < due[j].item.ID
	})
	return due, pending, nextDue
}

// claimProbeChannel 抢占一个渠道的探测名额。
func claimProbeChannel(channelID int) bool {
	probeChannelsMu.Lock()
	defer probeChannelsMu.Unlock()
	if _, busy := probeChannels[channelID]; busy {
		return false
	}
	probeChannels[channelID] = struct{}{}
	return true
}

// releaseProbeChannel 归还渠道名额。
func releaseProbeChannel(channelID int) {
	probeChannelsMu.Lock()
	delete(probeChannels, channelID)
	probeChannelsMu.Unlock()
}

// probeOneItem 探测单个成员并落库, 无论成败都排下一次。
// 排期不看结果: 失败也要按原节奏再试, 否则一次抖动会把该成员永久推远或一直贴着队头重试。
func probeOneItem(candidate probeCandidate) {
	ctx, cancel := context.WithTimeout(context.Background(), probeItemTimeout)
	// 来源标识钉成"定时探测": 与人工测试在转发日志里可区分, 统计排除也覆盖得到。
	ctx = relay.WithProbeClientName(ctx)
	result := relay.TestGroupItem(ctx, candidate.group, candidate.item)
	cancel()

	// 结论落库与会话内缓存由 GroupProbeApply 一并处理, 与人工整组测试走同一条路。
	if err := op.GroupProbeApply(candidate.group.ID, []model.GroupMemberTestResult{result}, model.ProbeSourceScheduled); err != nil {
		log.Errorf("group %d item %d probe apply failed: %v", candidate.group.ID, candidate.item.ID, err)
	}
	setProbeNextAt(candidate.item.ID, nextProbeAt(time.Now(), candidate.official))

	probeStatusUpdate(func(status *ProbeStatus) {
		status.LastFinishedAt = time.Now().Unix()
		if result.Success {
			status.TotalOK++
		} else {
			status.TotalFailed++
		}
	})
}

// nextProbeAt 按渠道类型排下一次: 官方渠道落到下一个夜间窗口, 其余按 6-12 小时随机。
func nextProbeAt(now time.Time, official bool) int64 {
	if official {
		return nextOfficialProbeAt(now)
	}
	return now.Add(randomProbeInterval()).Unix()
}

// nextOfficialProbeAt 把官方成员排到下一个夜间窗口内的随机时刻。
// 至少隔 officialProbeMinGap 才允许进窗口: 手动探测或重启不会让它在同一天被连探两次。
func nextOfficialProbeAt(now time.Time) int64 {
	hour := officialProbeHour()
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	for !windowStart.After(now.Add(officialProbeMinGap)) {
		windowStart = windowStart.AddDate(0, 0, 1)
	}
	return windowStart.Add(time.Duration(rand.Int64N(int64(officialProbeWindow)))).Unix()
}

// officialProbeHour 读设置项, 非法值回落到默认小时(校验在设置写入侧, 这里只兜底读库异常)。
func officialProbeHour() int {
	hour, err := op.SettingGetInt(model.SettingKeyOfficialProbeHour)
	if err != nil || hour < 0 || hour > 23 {
		return officialProbeDefaultHour
	}
	return hour
}

// RunGroupProbeRound 立即把全部分组成员各探一遍(阻塞), 返回处理的分组数。
// 只给人工"立即探测一次"用: 定时探测已改为成员级排期, 不再有轮次。
func RunGroupProbeRound() int {
	if !probeRoundRunning.CompareAndSwap(false, true) {
		log.Debugf("group probe round skipped: already running")
		return 0
	}
	defer probeRoundRunning.Store(false)
	probeStatusUpdate(func(status *ProbeStatus) {
		status.ManualRunning = true
		status.ManualTotal = 0
		status.ManualDone = 0
	})
	return runGroupProbeRoundBody()
}

// RunGroupProbeRoundAsync 后台跑一次全量探测, 立即返回是否真的启动(已在跑则为 false)。
func RunGroupProbeRoundAsync() bool {
	if !probeRoundRunning.CompareAndSwap(false, true) {
		return false
	}
	// 先置 Running 再起协程: 否则 HTTP 响应里带的是启动前的旧快照, 前端刚点完按钮会显示"没在跑"。
	probeStatusUpdate(func(status *ProbeStatus) {
		status.ManualRunning = true
		status.ManualTotal = 0
		status.ManualDone = 0
	})
	go func() {
		defer probeRoundRunning.Store(false)
		runGroupProbeRoundBody()
	}()
	return true
}

// runGroupProbeRoundBody 是人工全量轮的实体, 调用方必须已持有 probeRoundRunning。
// 与调度器的关系: 调度器按分组项逐个探, 这里按分组整体探, 两边都没有分组级互斥,
// 所以人工轮进行中时某个成员可能被同时探两次 —— 代价是多一个请求, 不影响结论正确性。
func runGroupProbeRoundBody() int {
	// 保留期清理挂在写入方一侧: 不写就没必要清, 也不必另立定时任务。
	if keepDays, err := op.SettingGetInt(model.SettingKeyGrantProbeKeepDays); err == nil {
		if pruned, err := op.GrantProbePrune(keepDays); err != nil {
			log.Warnf("grant probe history prune failed: %v", err)
		} else if pruned > 0 {
			log.Infof("grant probe history pruned: %d rows", pruned)
		}
	}

	groups := make([]model.Group, 0, 16)
	for _, group := range op.GroupList() {
		if len(group.Items) == 0 {
			continue
		}
		groups = append(groups, group)
	}
	startedAt := time.Now()
	probeStatusUpdate(func(status *ProbeStatus) {
		status.ManualRunning = true
		status.ManualTotal = len(groups)
		status.ManualDone = 0
	})

	var handled atomic.Int64
	semaphore := make(chan struct{}, probeGroupConcurrency)
	var waitGroup sync.WaitGroup
	for _, group := range groups {
		waitGroup.Add(1)
		go func(group model.Group) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			// 抢不到闸说明这个分组正在被人工单组测试: 跳过它, 不排队等。
			if !relay.GroupTestAcquire(group.ID) {
				log.Debugf("group %d probe skipped: another test running", group.ID)
				probeManualGroupFinished()
				return
			}
			officialByItem := make(map[int]bool, len(group.Items))
			for _, item := range group.Items {
				channel, err := op.ChannelGet(item.ChannelID)
				officialByItem[item.ID] = err == nil && channel.Official
			}

			ctx, cancel := context.WithTimeout(context.Background(), probeRoundTimeout)
			ctx = relay.WithProbeClientName(ctx)
			results := relay.TestGroupMembers(ctx, group)
			cancel()
			relay.GroupTestRelease(group.ID)

			// 先释放闸再落库: 落库是写操作, 不该挡住用户手动点测试。
			if err := op.GroupProbeApply(group.ID, results, model.ProbeSourceScheduled); err != nil {
				log.Errorf("group %d probe apply failed: %v", group.ID, err)
			}
			// 人工轮也要排下一次: 否则调度器会立刻把这些成员当成到期, 刚测完又测一遍。
			now := time.Now()
			for itemID, official := range officialByItem {
				setProbeNextAt(itemID, nextProbeAt(now, official))
			}
			handled.Add(1)
			probeManualGroupFinished()
		}(group)
	}
	waitGroup.Wait()

	duration := time.Since(startedAt)
	probeStatusUpdate(func(status *ProbeStatus) {
		status.ManualRunning = false
		status.ManualDone = status.ManualTotal
		status.LastFinishedAt = time.Now().Unix()
	})
	log.Infof("manual probe round done: %d groups in %s", handled.Load(), duration.Round(time.Second))
	return int(handled.Load())
}

// probeManualGroupFinished 推进手动轮的进度计数(含被人工测试占用的分组)。
func probeManualGroupFinished() {
	probeStatusUpdate(func(status *ProbeStatus) {
		status.ManualDone++
	})
}
