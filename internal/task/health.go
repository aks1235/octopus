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

// fetchMissThreshold 连续多少轮拉不到模型清单后清空该凭据名下的模型与授权。
//
// "拉不到"指上游 200 但空列表, 或两侧都报错: 两者都说明这个凭据已经拿不到任何模型, 而不是
// 上游偶发下架。取两轮(按默认 30 分钟周期约一小时)是为了挡住单次抖动——清空会级联删掉授权
// 与分组成员, 一次误判的代价远大于多留一小时脏数据。
const fetchMissThreshold = 2

// fetchMissKey 标识"某个渠道的某个凭据"这一路探测。
type fetchMissKey struct {
	channelID int
	keyName   string
}

var (
	fetchMissMu sync.Mutex
	fetchMiss   = make(map[fetchMissKey]int)
)

// noteFetchMiss 记一次"拉不到清单", 返回连续次数。
func noteFetchMiss(key fetchMissKey) int {
	fetchMissMu.Lock()
	defer fetchMissMu.Unlock()
	fetchMiss[key]++
	return fetchMiss[key]
}

// clearFetchMiss 在拉到清单后归零, 连续计数只看紧邻的轮次。
func clearFetchMiss(key fetchMissKey) {
	fetchMissMu.Lock()
	delete(fetchMiss, key)
	fetchMissMu.Unlock()
}

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
		keyName := result.KeyName
		ref := fetchMissKey{channelID: candidate.ID, keyName: keyName}
		fetched, ok := keyFetchModels(result)
		if !ok {
			// 拉不到清单: 上游 200 但空列表, 或两侧都报错。连续够轮数就按空清单清掉,
			// 免得账户失效的渠道把一堆调不动的模型长期留在清单与分组里。
			if noteFetchMiss(ref) < fetchMissThreshold {
				continue
			}
			if err := runWithBusyRetry(func() error {
				ctx, cancel := context.WithTimeout(context.Background(), healthReconcileTimeout)
				defer cancel()
				return op.ChannelModelClear(ctx, candidate.ID, keyName)
			}); err != nil {
				log.Warnf("health check: failed to clear models of channel %d key %q: %v", candidate.ID, keyName, err)
			} else {
				log.Infof("health check: cleared models of channel %d key %q after %d rounds without model list",
					candidate.ID, keyName, fetchMissThreshold)
			}
			continue
		}
		clearFetchMiss(ref)
		if err := runWithBusyRetry(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), healthReconcileTimeout)
			defer cancel()
			return op.ChannelModelReconcile(ctx, candidate.ID, keyName, fetched)
		}); err != nil {
			log.Warnf("health check: failed to reconcile models of channel %d key %q: %v", candidate.ID, keyName, err)
		}
	}
}

// keyFetchModels 把单个凭据的探测结果折成对账清单: 协议位的推导口径在 op.ChannelFetchModels
// (与手动拉取共用同一份), 本函数只负责判定本轮拉没拉到清单。
// ok 为假表示本轮拉不到清单: 两侧都失败, 或任一侧 2xx 却返回空列表。此时不做增量对账(空清单
// 会删掉该凭据名下全部模型), 由调用方累计连续次数, 够轮数后再按空清单清理。
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
	return op.ChannelFetchModels(result.OpenAI, result.Anthropic), true
}
