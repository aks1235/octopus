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

// healthReconcileTimeout 单个凭据对账落库的时限: 事务替换模型与授权加分组重算,
// 与兜底任务的 30 秒预算同量级; 对账逐凭据串行进行, 不与探测抢时限。
const healthReconcileTimeout = 30 * time.Second

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

// checkChannelHealth 探测单个候选渠道, 把健康结论落库, 并对探测成功的凭据对账模型。
// 失败递增连续失败计数, 达阈值时一并置禁用与自动禁用标记; 成功清零计数,
// 此前被自动禁用的渠道随之恢复启用。整轮时限耗尽时探测结论不可信, 不计失败也不清计数,
// 也不落任何对账结论, 留给下一轮。
func checkChannelHealth(roundCtx context.Context, candidate model.ChannelHealthCandidate, threshold int) {
	healthy, results, probeErr := probe.FetchModelsDetailed(roundCtx, candidate.ChannelConfig, candidate.Keys)
	if roundCtx.Err() != nil {
		log.Debugf("health check: round timeout, skip recording channel %d", candidate.ID)
		return
	}
	checkedAt := time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), healthRecordTimeout)
	defer cancel()
	if healthy {
		// 自动解禁只对"自动禁用且未启用"的渠道成立; 人工禁用的渠道不在候选之列, 由此不会被误翻。
		if err := op.ChannelHealthSuccess(candidate.ID, checkedAt, candidate.AutoDisabled && !candidate.Enabled, ctx); err != nil {
			log.Warnf("health check: failed to record success of channel %d: %v", candidate.ID, err)
		}
	} else {
		failCount := candidate.HealthFailCount + 1
		if err := op.ChannelHealthFail(candidate.ID, failCount, probeErr.Error(), checkedAt, failCount >= threshold, ctx); err != nil {
			log.Warnf("health check: failed to record failure of channel %d: %v", candidate.ID, err)
		}
	}
	reconcileChannelModels(candidate, results)
}

// reconcileChannelModels 按探测结果逐凭据对账模型: 本轮拉到的清单替换该凭据名下的
// 模型与授权(43d6cae 替换语义), 上游已下线的模型随之消失, 分组成员的级联由 op 层收尾。
// 防误删护栏与判活分开: 两侧都失败的凭据不对账(不确定就不动), 任一侧 2xx 却返回空列表的
// 凭据也不对账(空列表更可能是上游故障而非全部下架); 整轮超时不落任何对账, 由上方超时检查保证。
// 单个凭据对账失败只记日志, 不中断其余凭据; BUSY 类错误经 runWithBusyRetry 退避重试。
func reconcileChannelModels(candidate model.ChannelHealthCandidate, results []probe.KeyProbeResult) {
	for _, result := range results {
		fetched, ok := keyFetchModels(result)
		if !ok {
			continue
		}
		keyName := result.KeyName
		if err := runWithBusyRetry(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), healthReconcileTimeout)
			defer cancel()
			return op.ChannelModelReconcile(ctx, candidate.ID, keyName, fetched)
		}); err != nil {
			log.Warnf("health check: failed to reconcile models of channel %d key %q: %v", candidate.ID, keyName, err)
		}
	}
}

// keyFetchModels 把单个凭据的探测结果折成对账清单: 两侧模型名按名称合并, 协议位取并集,
// OpenAI 侧记为 Responses 协议(与手动拉取的标记口径一致, Chat Completions 由用户在界面手动勾选)。
// ok 为假表示本轮不该对账: 两侧都失败, 或任一侧 2xx 却返回空列表。
func keyFetchModels(result probe.KeyProbeResult) ([]model.ChannelFetchModel, bool) {
	if result.OpenAIErr == nil && len(result.OpenAI) == 0 {
		return nil, false
	}
	if result.AnthropicErr == nil && len(result.Anthropic) == 0 {
		return nil, false
	}
	if result.OpenAIErr != nil && result.AnthropicErr != nil {
		return nil, false
	}
	// 先并入 OpenAI 再并入 Anthropic, 顺序与手动拉取的合并一致, 保持清单顺序稳定。
	protocolsByName := make(map[string]model.Protocol, len(result.OpenAI)+len(result.Anthropic))
	order := make([]string, 0, len(result.OpenAI)+len(result.Anthropic))
	for _, name := range result.OpenAI {
		if _, ok := protocolsByName[name]; !ok {
			order = append(order, name)
		}
		protocolsByName[name] |= model.ProtocolOpenAIResponse
	}
	for _, name := range result.Anthropic {
		if _, ok := protocolsByName[name]; !ok {
			order = append(order, name)
		}
		protocolsByName[name] |= model.ProtocolAnthropicMessage
	}
	fetched := make([]model.ChannelFetchModel, 0, len(order))
	for _, name := range order {
		fetched = append(fetched, model.ChannelFetchModel{Name: name, Protocols: protocolsByName[name]})
	}
	return fetched, true
}
