package relay

import (
	"math/bits"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// outcomeWindow 渠道近期表现的记录容量: 故障转移按这张滚动表算失败率, 把不稳定的成员沉到后面。
//
// 为什么用固定窗口而不是累计次数: 累计失败次数是绝对量, 请求量大的成员天然更多,
// 排序会冤枉它; 固定 16 格里只比比率, 与调用总量无关。
const outcomeWindow = 16

// outcomeWarmup 是参与失败率排序所需的最小样本数。
//
// 样本不足的成员先按人工优先级排到前面试跑, 攒够 outcomeWarmup 次才进失败率队列。
// 没有这条会形成死锁: 新成员窗口里只有一两次记录, 平滑后失败率偏高, 排到末尾就拿不到流量,
// 拿不到流量就永远攒不满样本。持久化只保证重启不掉窗口, 挡不住新成员被老成员挤死。
const outcomeWarmup = 3

// outcomeFlushInterval 是窗口快照落库的间隔。
// 选路热路径只改内存并置脏标记, 由后台定时批量 upsert: 崩溃最多丢一个间隔的记录,
// 换来转发路径上零数据库写入。
const outcomeFlushInterval = 5 * time.Second

// outcomeMinElapsed 客户端断开时, 这一轮至少要跑多久才算渠道的责任。
// 低于它的断开更像网络抖动、客户端连接出错或用户手滑, 记到渠道头上会冤枉人。
const outcomeMinElapsed = 10 * time.Second

// grantOutcome 一个渠道最近 16 次调用的成败: 最低位是最新一次, 1 表示失败。
type grantOutcome struct {
	bits  uint16 // 位图, 低位为最新。
	count uint8  // 已记录次数, 上限 outcomeWindow。
	dirty bool   // 自上次落库以来是否有变化。
}

// record 记一次结果。
func (o *grantOutcome) record(failed bool) {
	// 每记一次整体左移一位, 新结果落在最低位; 满窗后最高位自然被挤出去, 形成先进先出。
	o.bits <<= 1
	if o.count < outcomeWindow {
		o.count++
	}
	if failed {
		o.bits |= 1
	}
	o.dirty = true
}

// fails 返回当前窗口内的失败次数。
func (o *grantOutcome) fails() int {
	if o == nil || o.count == 0 {
		return 0
	}
	mask := uint16(0xFFFF)
	if o.count < outcomeWindow {
		mask = uint16(1)<<o.count - 1
	}
	return bits.OnesCount16(o.bits & mask)
}

// rate 返回拉普拉斯平滑后的失败率: (失败数+1)/(记录数+2)。
//
// 无记录时为 0.5, 让没跑过的成员保持中性, 既不抢先也不垫底; 一次失败即 2/3,
// 立刻沉到中性成员后面 —— 这是"坏成员最多坑一次"的依据。样本不足的情形由
// orderByOutcome 的预热规则处理, 不靠调整这里的平滑参数: 平滑影响的是"怎么比",
// 预热影响的是"够不够格比", 两件事混在一起会让数字更难解释。
func (o *grantOutcome) rate() float64 {
	if o == nil || o.count == 0 {
		return 0.5
	}
	return float64(o.fails()+1) / float64(int(o.count)+2)
}

var (
	outcomeMu sync.Mutex
	outcomes  = make(map[int]*grantOutcome)
)

// outcomeView 是一次加锁拷贝出来的排序依据: 放进比较器里反复查 map 会一直持锁。
type outcomeView struct {
	rate  float64
	count uint8
	fails int
}

// outcomeViews 为给定授权批量取当前窗口视图。
func outcomeViews(grantIDs []int) map[int]outcomeView {
	views := make(map[int]outcomeView, len(grantIDs))
	outcomeMu.Lock()
	defer outcomeMu.Unlock()
	for _, grantID := range grantIDs {
		outcome := outcomes[grantID]
		view := outcomeView{rate: outcome.rate(), fails: outcome.fails()}
		if outcome != nil {
			view.count = outcome.count
		}
		views[grantID] = view
	}
	return views
}

// RecordGrantOutcome 记录一次渠道调用的成败, 供故障转移排序使用。
// 是否该记由调用方判定(见 channelOutcomeCountable), 本函数只负责记账。
func RecordGrantOutcome(grantID int, failed bool) {
	if grantID <= 0 {
		return
	}
	outcomeMu.Lock()
	outcome := outcomes[grantID]
	if outcome == nil {
		outcome = &grantOutcome{}
		outcomes[grantID] = outcome
	}
	outcome.record(failed)
	outcomeMu.Unlock()
}

// resetOutcomeWindow 清空全部渠道的近期表现记录。
// 正常运行时窗口应随调用自然滚动, 无需重置; 供测试隔离使用。
func resetOutcomeWindow() {
	outcomeMu.Lock()
	outcomes = make(map[int]*grantOutcome)
	outcomeMu.Unlock()
}

// LoadOutcomes 从数据库回填排序窗口, 进程启动时调用一次。
//
// 失败不致命: 调用方记日志后继续, 窗口按空处理, 相当于退回"重启即失忆"的旧行为,
// 比因为一张辅助表读不出来就拒绝启动要好。
func LoadOutcomes() error {
	rows, err := op.GrantOutcomeLoad()
	if err != nil {
		return err
	}
	outcomeMu.Lock()
	defer outcomeMu.Unlock()
	for _, row := range rows {
		count := row.Count
		if count < 0 {
			count = 0
		}
		if count > outcomeWindow {
			count = outcomeWindow
		}
		// 只保留 count 位: 库里可能有旧版本或人工改坏的脏数据, 多出来的位会虚增失败数。
		bitsValue := uint16(row.Bits & 0xFFFF)
		if count < outcomeWindow {
			bitsValue &= uint16(1)<<count - 1
		}
		outcomes[row.GrantID] = &grantOutcome{bits: bitsValue, count: uint8(count)}
	}
	return nil
}

// FlushOutcomes 把有变化的窗口批量落库, 由后台定时器与优雅关停调用。
//
// 先释放锁再写库: 持锁做 IO 会把所有转发请求卡在选路上。
// 写失败要把脏标记打回去, 否则这一批记录会被静默丢掉, 下次 flush 不会再带上它们。
func FlushOutcomes() error {
	now := time.Now().Unix()
	rows := make([]model.GrantOutcome, 0, len(outcomes))
	outcomeMu.Lock()
	for grantID, outcome := range outcomes {
		if !outcome.dirty {
			continue
		}
		outcome.dirty = false
		rows = append(rows, model.GrantOutcome{
			GrantID:   grantID,
			Bits:      int(outcome.bits),
			Count:     int(outcome.count),
			UpdatedAt: now,
		})
	}
	outcomeMu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	if err := op.GrantOutcomeSave(rows); err != nil {
		outcomeMu.Lock()
		for _, row := range rows {
			if outcome := outcomes[row.GrantID]; outcome != nil {
				outcome.dirty = true
			}
		}
		outcomeMu.Unlock()
		log.Errorf("flush grant outcomes error: %v", err)
		return err
	}
	return nil
}

var outcomePersistenceOnce sync.Once

// StartOutcomePersistence 启动后台快照协程, 进程生命周期内只需调用一次。
// 协程随进程退出结束, 最后一批由 shutdown 注册的 FlushOutcomes 补写。
func StartOutcomePersistence() {
	outcomePersistenceOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(outcomeFlushInterval)
			defer ticker.Stop()
			for range ticker.C {
				FlushOutcomes()
			}
		}()
	})
}

// recordOutcomeOnce 计入一次结果, 同一请求内同一渠道只记第一格。
// 一次请求对同一成员可能重试多次, 重复记账会把单次请求的失败放大成多次。
func recordOutcomeOnce(recorded map[int]struct{}, grantID int, failed bool) {
	if _, ok := recorded[grantID]; ok {
		return
	}
	recorded[grantID] = struct{}{}
	RecordGrantOutcome(grantID, failed)
}

// OutcomeStat 是成员在界面上的生效顺序及其依据: 序号必须能回答"为什么排这里"。
type OutcomeStat struct {
	Rank  int // 1 起; 0 表示不参与选路。
	Count int // 窗口内已有记录数。
	Fails int // 窗口内失败次数。
}

// OutcomeStats 给分组里"能参与选路"的成员按生效顺序编号(1 起), 返回 分组项主键 -> 明细。
//
// 过滤与比较器都复用选路本身(selectableGroupItems + orderByOutcome), 不另写一套:
// 序号必须回答"这次请求会先打谁", 所以被禁用、授权缺失、已判死且结论未过期的成员不参与编号 ——
// 它们根本进不了选路, 给它们发一个 1 号只会让人误以为"先打它"。
// 冷却中的成员仍然编号: 它们在候选集合里, 只是暂时被跳过, 位置本身是有意义的。
func OutcomeStats(group model.Group) map[int]OutcomeStat {
	ordered := orderByOutcome(selectableGroupItems(group))
	grantIDs := make([]int, 0, len(ordered))
	for _, item := range ordered {
		grantIDs = append(grantIDs, item.ChannelGrantID)
	}
	views := outcomeViews(grantIDs)
	stats := make(map[int]OutcomeStat, len(ordered))
	for index, item := range ordered {
		view := views[item.ChannelGrantID]
		stats[item.ID] = OutcomeStat{Rank: index + 1, Count: int(view.count), Fails: view.fails}
	}
	return stats
}

// OutcomeRanks 给分组里"能参与选路"的成员按生效顺序编号(1 起), 返回 分组项主键 -> 序号。
// 保留给只需要序号的调用方; 界面要展示依据时用 OutcomeStats。
func OutcomeRanks(group model.Group) map[int]int {
	stats := OutcomeStats(group)
	ranks := make(map[int]int, len(stats))
	for itemID, stat := range stats {
		ranks[itemID] = stat.Rank
	}
	return ranks
}

// orderByOutcome 按失败率重排候选成员: 低的在前, 同率保持原有优先级顺序。
//
// 样本不足(outcomeWarmup 次以内)的成员排在最前, 组内仍按人工优先级 —— 先试跑攒样本,
// 尝到失败自然会涨到 2/3 以上沉下去, 不会长期占据队头。
// 用稳定排序是为了不打断用户手排的次序 —— 同率的成员之间, 人工顺序是唯一依据。
func orderByOutcome(items []model.GroupItem) []model.GroupItem {
	if len(items) < 2 {
		return items
	}
	ordered := make([]model.GroupItem, len(items))
	copy(ordered, items)
	grantIDs := make([]int, len(ordered))
	for index, item := range ordered {
		grantIDs[index] = item.ChannelGrantID
	}
	views := outcomeViews(grantIDs)
	sort.SliceStable(ordered, func(i, j int) bool {
		first, second := views[ordered[i].ChannelGrantID], views[ordered[j].ChannelGrantID]
		if (first.count < outcomeWarmup) != (second.count < outcomeWarmup) {
			return first.count < outcomeWarmup
		}
		return first.rate < second.rate
	})
	return ordered
}

// channelOutcomeCountable 判断一次上游失败是否该算到渠道头上。
//
// 只认能指向渠道的信号: 5xx / 429 / 401 / 403 / 404 / 408 / 超时 / 连接失败 / 空正文。
//
// 404 计入: 模型不存在是渠道之间的差异, 换个有这个模型的渠道就能用, 该沉的是它。
// 400、422 与本地转换错误不计: 它们多半是请求本身写错了, 换哪个渠道都一样; 更要紧的是
// "老实报 400"的渠道不该被惩罚, 否则等于奖励那些静默吞掉非法参数的渠道。
func channelOutcomeCountable(err error, emptyAnswer bool) bool {
	if emptyAnswer {
		return true
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, signal := range []string{
		"timeout", "deadline", "connection refused", "connection reset", "no such host",
	} {
		if strings.Contains(message, signal) {
			return true
		}
	}
	for _, code := range []string{"upstream 5", "upstream 429", "upstream 401", "upstream 403", "upstream 404", "upstream 408"} {
		if strings.Contains(message, code) {
			return true
		}
	}
	return false
}
