package relay

import (
	"math/bits"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// outcomeWindow 渠道近期表现的记录容量: 故障转移按这张滚动表算失败率, 把不稳定的成员沉到后面。
//
// 为什么用固定窗口而不是累计次数: 累计失败次数是绝对量, 请求量大的成员天然更多,
// 排序会冤枉它; 固定 16 格里只比比率, 与调用总量无关。
// 为什么放内存: 选路是每个请求都要走的热路径, 不能查库。重启后窗口清空、按中性分处理,
// 坏成员最多被选中一次就会沉下去, 不值得为这一次引入持久化。
const outcomeWindow = 16

// outcomeMinElapsed 客户端断开时, 这一轮至少要跑多久才算渠道的责任。
// 低于它的断开更像网络抖动、客户端连接出错或用户手滑, 记到渠道头上会冤枉人。
const outcomeMinElapsed = 10 * time.Second

// grantOutcome 一个渠道最近 16 次调用的成败: 最低位是最新一次, 1 表示失败。
type grantOutcome struct {
	bits  uint16 // 位图, 低位为最新。
	count uint8  // 已记录次数, 上限 outcomeWindow。
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
}

// rate 返回拉普拉斯平滑后的失败率: (失败数+1)/(记录数+2)。
//
// 无记录时为 0.5, 让没跑过的成员保持中性, 既不抢先也不垫底; 一次失败即 2/3,
// 立刻沉到中性成员后面 —— 这是"重启后坏成员最多坑一次"的依据, 不用等攒满窗口。
func (o *grantOutcome) rate() float64 {
	if o == nil || o.count == 0 {
		return 0.5
	}
	mask := uint16(0xFFFF)
	if o.count < outcomeWindow {
		mask = uint16(1)<<o.count - 1
	}
	fails := bits.OnesCount16(o.bits & mask)
	return float64(fails+1) / float64(int(o.count)+2)
}

var (
	outcomeMu sync.Mutex
	outcomes  = make(map[int]*grantOutcome)
)

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

// recordOutcomeOnce 计入一次结果, 同一请求内同一渠道只记第一格。
// 一次请求对同一成员可能重试多次, 重复记账会把单次请求的失败放大成多次。
func recordOutcomeOnce(recorded map[int]struct{}, grantID int, failed bool) {
	if _, ok := recorded[grantID]; ok {
		return
	}
	recorded[grantID] = struct{}{}
	RecordGrantOutcome(grantID, failed)
}

// orderByOutcome 按失败率重排候选成员: 低的在前, 同率保持原有优先级顺序。
// 用稳定排序是为了不打断用户手排的次序 —— 同率的成员之间, 人工顺序是唯一依据。
func orderByOutcome(items []model.GroupItem) []model.GroupItem {
	if len(items) < 2 {
		return items
	}
	ordered := make([]model.GroupItem, len(items))
	copy(ordered, items)
	rates := make(map[int]float64, len(ordered))
	outcomeMu.Lock()
	for _, item := range ordered {
		// 未记录的授权取中性分, 由 rate 的 nil 分支给出。
		rates[item.ChannelGrantID] = outcomes[item.ChannelGrantID].rate()
	}
	outcomeMu.Unlock()
	sort.SliceStable(ordered, func(i, j int) bool {
		return rates[ordered[i].ChannelGrantID] < rates[ordered[j].ChannelGrantID]
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
