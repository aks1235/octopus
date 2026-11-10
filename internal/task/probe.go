package task

import (
	"context"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/charmbracelet/log"
)

// 定时探测: 把"人工点一次分组成员测试"变成定期自动跑, 结论落到授权的 probe_* 列。
//
// 为什么不用 task.Register: 那个框架是固定 time.Ticker + 可 Update 间隔, 而这里要的是
// "每轮结束后随机 6–12 小时再跑", 硬塞等于每次 tick 再掷骰子, 语义绕且难读。
// 为什么按分组串行: relay 的成员并发(4)与每渠道并发(2)都在单次 TestGroupMembers 调用内生效,
// 分组之间并行会让同一渠道(常在多个分组里)被同时打。
const (
	probeIntervalMin = 6 * time.Hour  // 一轮结束到下一轮的最短等待。
	probeIntervalMax = 12 * time.Hour // 最长等待; 区间内均匀取随机值以错峰。
	// probeDisabledRecheck 关闭状态下重读设置的间隔: 设置项变更没有回调, 只能轮询。
	probeDisabledRecheck = 10 * time.Minute
	// probeFirstRoundDelay 从关闭切到开启后的首轮等待: 不在保存设置的瞬间就开打, 也不让人干等几小时。
	probeFirstRoundDelay = time.Minute
	// probeRoundTimeout 单个分组的探测超时上限: 成员多且全失败时避免一轮挂死在这组上。
	probeRoundTimeout = 10 * time.Minute
)

// probeRoundRunning 保证同一时刻只有一轮探测在跑: 定时触发与手动触发可能撞上。
var probeRoundRunning atomic.Bool

// startGroupProbe 启动定时探测协程。开关默认关闭, 由设置项 group_probe_enabled 控制。
func startGroupProbe() {
	go func() {
		// 关闭状态下不退出协程, 只按分钟级间隔重读设置: 用户随时可能打开。
		for {
			enabled, err := op.SettingGetBool(model.SettingKeyGroupProbeEnabled)
			if err != nil || !enabled {
				time.Sleep(probeDisabledRecheck)
				continue
			}
			// 刚开启时只等一分钟, 之后每轮结束再随机 6–12 小时。
			time.Sleep(probeFirstRoundDelay)
			if !isGroupProbeEnabled() {
				continue
			}
			RunGroupProbeRound()
			time.Sleep(randomProbeInterval())
		}
	}()
}

// isGroupProbeEnabled 每轮开跑前再确认一次开关: 用户可能在一分钟的首轮等待里就把它关掉了。
func isGroupProbeEnabled() bool {
	enabled, err := op.SettingGetBool(model.SettingKeyGroupProbeEnabled)
	return err == nil && enabled
}

// randomProbeInterval 在 [min, max) 内均匀取一个等待时长。
func randomProbeInterval() time.Duration {
	spread := int64(probeIntervalMax - probeIntervalMin)
	return probeIntervalMin + time.Duration(rand.Int64N(spread))
}

// RunGroupProbeRound 跑一轮探测(阻塞): 逐个分组测全部成员并落库。返回处理的分组数。
// 定时任务与手动触发共用; 已在跑时直接返回 0, 不排队。
func RunGroupProbeRound() int {
	if !probeRoundRunning.CompareAndSwap(false, true) {
		log.Debugf("group probe round skipped: already running")
		return 0
	}
	defer probeRoundRunning.Store(false)
	return runGroupProbeRoundBody()
}

// RunGroupProbeRoundAsync 后台跑一轮探测, 立即返回是否真的启动(已在跑则为 false)。
// 供 HTTP 手动触发用: 一轮可能持续数分钟, 不能占着请求。
func RunGroupProbeRoundAsync() bool {
	if !probeRoundRunning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer probeRoundRunning.Store(false)
		runGroupProbeRoundBody()
	}()
	return true
}

// runGroupProbeRoundBody 是真正的一轮探测, 调用方必须已持有 probeRoundRunning。
func runGroupProbeRoundBody() int {
	// 保留期清理挂在写入方一侧: 不写就没必要清, 也不必另立定时任务。
	if keepDays, err := op.SettingGetInt(model.SettingKeyGrantProbeKeepDays); err == nil {
		if pruned, err := op.GrantProbePrune(keepDays); err != nil {
			log.Warnf("grant probe history prune failed: %v", err)
		} else if pruned > 0 {
			log.Infof("grant probe history pruned: %d rows", pruned)
		}
	}

	handled := 0
	for _, group := range op.GroupList() {
		if len(group.Items) == 0 {
			continue
		}
		// 抢不到闸说明这个分组正在被手动测试: 跳过它, 继续下一组, 不排队等。
		if !relay.GroupTestAcquire(group.ID) {
			log.Debugf("group %d probe skipped: another test running", group.ID)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), probeRoundTimeout)
		// 来源标识钉成"定时探测": 与人工点"测试全部成员"在转发日志里可区分, 统计排除也覆盖得到。
		ctx = relay.WithProbeClientName(ctx)
		results := relay.TestGroupMembers(ctx, group)
		cancel()
		relay.GroupTestRelease(group.ID)

		// 先释放闸再落库: 落库是写操作, 不该挡住用户手动点测试。
		if err := op.GroupProbeApply(group.ID, results, model.ProbeSourceScheduled); err != nil {
			log.Errorf("group %d probe apply failed: %v", group.ID, err)
		}
		handled++
	}
	log.Infof("group probe round done: %d groups", handled)
	return handled
}
