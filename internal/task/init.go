package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate    = "price_update"
	TaskStatsSave      = "stats_save"
	TaskCleanLLM       = "clean_llm"
	TaskGroupRegexSync = "group_regex_sync"
)

// groupRegexSyncInterval 分组成员正则兜底任务的重算间隔。
// 常量间隔不暴露设置: 渠道增删改与分组编辑已有即时重算, 定时只兜漏算, 无需人工调参。
const groupRegexSyncInterval = 5 * time.Minute

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

	// 注册分组成员正则兜底任务: 启动即跑一轮吸纳存量匹配, 之后定时兜住意外漏算。
	Register(TaskGroupRegexSync, groupRegexSyncInterval, true, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := op.GroupRegexSync(ctx); err != nil {
			log.Warnf("failed to sync regex group members: %v", err)
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
}
