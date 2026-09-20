package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate              = "price_update"
	TaskStatsSave                = "stats_save"
	TaskCleanLLM                 = "clean_llm"
	TaskGroupRegexSync           = "group_regex_sync"
	TaskRelayLogSave             = "relay_log_save"
	TaskRelayLogAttemptsBackfill = "relay_log_attempts_backfill"
)

// groupRegexSyncInterval 分组成员正则兜底任务的重算间隔。
// 常量间隔不暴露设置: 渠道增删改与分组编辑已有即时重算, 定时只兜漏算, 无需人工调参。
const groupRegexSyncInterval = 5 * time.Minute

// relayLogSaveInterval 转发日志周期落盘间隔。
// 缓冲满 20 条的主动 flush 是主路径, 定时只兜低流量时段的滞留与按保留期清理, 无需人工调参。
const relayLogSaveInterval = time.Minute

// relayLogAttemptsBackfillInterval attempts 规范化回填任务的周期。
// 只在启动时可能真有活(回填上线前的老日志), 之后每周期都是「已完成即跳过」的空转, 故间隔取分钟级即可:
// 首轮时间预算用尽的部分, 下一分钟接着跑。
const relayLogAttemptsBackfillInterval = time.Minute

// busyRetryIntervals 兜底任务遇 SQLite BUSY 时的退避重试间隔序列。
// 兜底大事务与转发路径的持续写入抢写锁时, 等一等通常就能拿到锁, 不值得整轮放弃;
// 间隔递增(2s/5s)避免在锁风暴上火上浇油。变量而非常量是为了测试时可缩短间隔。
var busyRetryIntervals = []time.Duration{2 * time.Second, 5 * time.Second}

// runWithBusyRetry 执行 fn, 遇 SQLite BUSY 类错误按 busyRetryIntervals 退避静默重试,
// 重试耗尽才把最后一次错误交还调用方告警。非 BUSY 错误(如正则编译失败)不重试, 原样返回。
func runWithBusyRetry(fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || !db.IsBusyError(err) || attempt >= len(busyRetryIntervals) {
			return err
		}
		time.Sleep(busyRetryIntervals[attempt])
	}
}

func Init() {
	priceUpdateIntervalHours, err := op.SettingGetInt(model.SettingKeyModelInfoUpdateInterval)
	if err != nil {
		log.Errorf("failed to get model info update interval: %v", err)
		return
	}
	priceUpdateInterval := time.Duration(priceUpdateIntervalHours) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册统计保存任务
	statsSaveIntervalMinutes, err := op.SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		log.Warnf("failed to get stats save interval: %v", err)
		return
	}
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)

	// 注册分组成员兜底任务: 正则分组整体重算 + 手动分组按名吸纳, 启动即跑一轮吸纳存量匹配, 之后定时兜住意外漏算。
	Register(TaskGroupRegexSync, groupRegexSyncInterval, true, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// BUSY 类错误退避静默重试(2s/5s 两轮), 重试耗尽才告警一次;
		// 非 BUSY 错误(如正则坏)不重试, 直接告警。
		if err := runWithBusyRetry(func() error { return op.GroupRegexSync(ctx) }); err != nil {
			log.Warnf("failed to sync regex group members: %v", err)
		}
		// 手动分组吸纳兜底走全量(不限渠道), 串行接在正则重算之后, 与渠道增改的事件触发共用同一入口。
		if err := runWithBusyRetry(func() error { return op.GroupManualAbsorb(ctx) }); err != nil {
			log.Warnf("failed to absorb manual group members: %v", err)
		}
	})

	// 注册渠道健康检查任务; 间隔为 0 时 Register 内部不注册, 任务即关闭。
	// 不在启动时立即执行: 全量探测会拖慢启动观测, 首轮交给第一个周期。
	healthCheckIntervalMinutes, err := op.SettingGetInt(model.SettingKeyHealthCheckInterval)
	if err != nil {
		log.Warnf("failed to get health check interval: %v", err)
		return
	}
	Register(string(model.SettingKeyHealthCheckInterval), time.Duration(healthCheckIntervalMinutes)*time.Minute, false, ChannelHealthCheckTask)

	// 注册转发日志周期落盘任务: flush 滞留缓冲 + 按保留期清理过期行。
	Register(TaskRelayLogSave, relayLogSaveInterval, false, op.RelayLogSaveDBTask)

	// 注册 attempts 规范化回填任务: 把上线前已存在日志的 attempts JSON 展开进 relay_log_attempts,
	// 否则旧日志的「渠道调用详情」查不到。启动即后台跑(不阻塞启动), 一轮没跑完(时间预算用尽)后续周期接着跑;
	// 回填本身幂等, 完成后进程内不再扫表(见 op.RelayLogAttemptsBackfill)。
	Register(TaskRelayLogAttemptsBackfill, relayLogAttemptsBackfillInterval, true, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := op.RelayLogAttemptsBackfill(ctx); err != nil {
			log.Warnf("failed to backfill relay log attempts: %v", err)
		}
	})
}
