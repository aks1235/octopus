package task

import (
	"context"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/probe"
	"github.com/charmbracelet/log"
)

// healthCheckTimeout 单轮健康检查的总时限, 覆盖各渠道凭据与两侧端点的串行探测, 死渠道的连接超时也在内。
const healthCheckTimeout = 5 * time.Minute

// healthRecordTimeout 单渠道探测结果落库的时限; 与探测轮分开, 轮时限耗尽后仍要能记下已得出的结论。
const healthRecordTimeout = 10 * time.Second

// ChannelHealthCheckTask 探测候选渠道的健康并自动禁用/解禁。
// 候选为启用中或已被自动禁用的渠道: 前者连续失败达阈值自动禁用, 后者恢复后自动解禁;
// 人工禁用的渠道不在候选之列, 不会被误翻。阈值每轮实时读设置, 修改后对下一轮即生效。
func ChannelHealthCheckTask() {
	threshold, err := op.SettingGetInt(model.SettingKeyHealthFailThreshold)
	if err != nil {
		log.Warnf("health check: failed to get fail threshold: %v", err)
		return
	}
	candidates := op.ChannelHealthCandidates()
	if len(candidates) == 0 {
		return
	}
	roundCtx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()

	// 每渠道一个协程并发探测: 渠道之间互不阻塞, 单渠道内部凭据与端点串行, 无需渠道内部并发。
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		wg.Add(1)
		go func(candidate model.ChannelHealthCandidate) {
			defer wg.Done()
			checkChannelHealth(roundCtx, candidate, threshold)
		}(candidate)
	}
	wg.Wait()
}

// checkChannelHealth 探测单个候选渠道并把结果落库。
// 失败递增连续失败计数, 达阈值时一并置禁用与自动禁用标记; 成功清零计数,
// 此前被自动禁用的渠道随之恢复启用。整轮时限耗尽时探测结论不可信, 不计失败也不清计数, 留给下一轮。
func checkChannelHealth(roundCtx context.Context, candidate model.ChannelHealthCandidate, threshold int) {
	ok, probeErr := probe.FetchModels(roundCtx, candidate.ChannelConfig, candidate.Keys)
	if roundCtx.Err() != nil {
		log.Debugf("health check: round timeout, skip recording channel %d", candidate.ID)
		return
	}
	checkedAt := time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), healthRecordTimeout)
	defer cancel()
	if ok {
		// 自动解禁只对"自动禁用且未启用"的渠道成立; 人工禁用的渠道不在候选之列, 由此不会被误翻。
		if err := op.ChannelHealthSuccess(candidate.ID, checkedAt, candidate.AutoDisabled && !candidate.Enabled, ctx); err != nil {
			log.Warnf("health check: failed to record success of channel %d: %v", candidate.ID, err)
		}
		return
	}
	failCount := candidate.HealthFailCount + 1
	if err := op.ChannelHealthFail(candidate.ID, failCount, probeErr.Error(), checkedAt, failCount >= threshold, ctx); err != nil {
		log.Warnf("health check: failed to record failure of channel %d: %v", candidate.ID, err)
	}
}
