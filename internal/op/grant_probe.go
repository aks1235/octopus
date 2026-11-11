package op

import (
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
)

// 探测结论的取值, 与 model.ChannelGrant.ProbeStatus 一一对应。
const (
	ProbeStatusUnknown = ""     // 未探测, 或仅出现过未定型的抖动失败。
	ProbeStatusOK      = "ok"   // 最近一次真实探测通过。
	ProbeStatusDead    = "dead" // 已判死, 选路跳过(见 relay 层过滤)。
)

// ProbeDeadAfterFailures 计数类失败(5xx/超时/形状不符)连续多少次判死。
// 与健康检查的 health_fail_threshold 默认值同量级: 确定性错误立即判死, 抖动类要连错三次才认。
const ProbeDeadAfterFailures = 3

// probeReasonMaxLength 落库原因摘要的截断上限, 与 keytest 的 200 字符口径一致:
// 部分上游报错是整页 HTML, 全文落库无用且会把界面撑破。
const probeReasonMaxLength = 200

// probeDeadKeywords 确定性失败的关键词白名单(小写匹配)。
// 命中即判死: 这类错误重试一万次也不会好(生产实测集中在 410 Gone / unknown provider for model /
// model_not_found / insufficient_credits), 按失败率阈值判定只会延迟结论。
// 集中在此一处, 勿在调用方各写一份。
var probeDeadKeywords = []string{
	// 模型不存在或已下线。
	"model_not_found",
	"model not found",
	"unknown provider",
	"no available channel",
	"does not exist",
	"not found",
	// 凭据失效或被拒。
	"invalid api key",
	"invalid_api_key",
	"incorrect api key",
	"invalid token",
	"authentication_error",
	"permission_error",
	"unauthorized",
	"forbidden",
	// 额度耗尽或账户状态异常。
	"insufficient_credits",
	"insufficient_quota",
	"insufficient_user_quota",
	"exceeded your current quota",
	"billing_not_active",
	"account_deactivated",
	"no credit",
	"余额不足",
	"额度已用尽",
	// 需要付费解锁/账号锁定: 上层状态码常是 429(限流的通用码), 但语义是确定性的——
	// 生产实测 429 + "model_requires_purchase: <model> is locked on your account until you
	// make a purchase. Buy credits or a plan" 重试不会自愈, 按限流只计数会一直留在选路里。
	"requires_purchase",
	"locked on your account",
	"make a purchase",
	"buy credits",
}

// probeDeadStatusCodes 确定性失败的 HTTP 状态码。
// 401/402/403 是凭据或额度问题, 404/410 是模型或分组问题, 都不会因重试而自愈。
// 400 刻意不在此列: 它既可能是渠道不认这个模型(确定性), 也可能是请求体本身有问题(全体成员一起失败),
// 后者会把整个分组判死, 故只作为计数类失败, 由关键词命中再定性。
var probeDeadStatusCodes = map[string]struct{}{
	"401": {},
	"402": {},
	"403": {},
	"404": {},
	"410": {},
}

// probeStatusLeaders 出现在状态码之前的引导词: `upstream responded 410 Gone`、`with status 404 Not Found`。
var probeStatusLeaders = map[string]struct{}{"status": {}, "responded": {}}

// probeStatusReasonPhrases 出现在状态码之后的短语: 裸的 `410 Gone` / `401 Unauthorized` 同样要认。
// 这两个名单共同保证"只有像状态行的数字才算状态码", 否则 token 计数里的 `410` 会被误判成 410 Gone
// (单元测试 TestProbeFailureIsFatal 里有对应用例)。
var probeStatusReasonPhrases = map[string]struct{}{
	"gone": {}, "unauthorized": {}, "forbidden": {}, "payment": {}, "not": {}, "bad": {},
}

// probeTrimToken 去掉词元两侧的标点与引号, 让 `429:` 与 `"410"` 都能按原码匹配。
func probeTrimToken(token string) string {
	return strings.Trim(token, `"',.:;()[]{}!`)
}

// probeFailureIsFatal 判定一次失败的原文是否属于"确定性死亡"。
// 双条件: 状态码只认"像状态行"的位置(前有 status/responded, 或后接 Gone/Unauthorized 等短语),
// 关键词按子串小写匹配(上游把状态写在正文里也是常态, 例如 `{"error":{"type":"insufficient_quota"}}`)。
func probeFailureIsFatal(reason string) bool {
	lowered := strings.ToLower(reason)
	for _, keyword := range probeDeadKeywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	fields := strings.Fields(lowered)
	for i, field := range fields {
		if _, ok := probeDeadStatusCodes[probeTrimToken(field)]; !ok {
			continue
		}
		if i > 0 {
			if _, ok := probeStatusLeaders[probeTrimToken(fields[i-1])]; ok {
				return true
			}
		}
		if i+1 < len(fields) {
			if _, ok := probeStatusReasonPhrases[probeTrimToken(fields[i+1])]; ok {
				return true
			}
		}
	}
	return false
}

// probeReasonTruncate 截断原因摘要, 按 rune 截以免切碎多字节字符。
func probeReasonTruncate(reason string) string {
	runes := []rune(reason)
	if len(runes) <= probeReasonMaxLength {
		return reason
	}
	return string(runes[:probeReasonMaxLength])
}

// ProbeDeadTTLSeconds 判死结论的有效期(秒)。
// 超过这个时间没有再被探测过, 结论作废、成员回到可选: 探测被关掉或进程崩溃后,
// 成员最多隐身一天就自动回池, 不会因一条陈旧结论被永久跳过。取最大探测间隔(12 小时)的两倍。
const ProbeDeadTTLSeconds int64 = 24 * 60 * 60

// GrantProbeDead 判断授权当前是否处于"已判死且结论未过期"的熔断态。
// 选路层据此跳过成员; 与启停无关——探测从不写渠道/凭据的 enabled, 人工禁用的恢复路径不受影响。
func GrantProbeDead(grant model.ChannelGrant, now int64) bool {
	if grant.ProbeStatus != ProbeStatusDead {
		return false
	}
	if grant.ProbeAt <= 0 {
		return false
	}
	return now-grant.ProbeAt <= ProbeDeadTTLSeconds
}

// grantProbeApplyResult 把单条探测结果落到一个授权上, 返回更新后的授权与待写的历史行。
// 结论推导只此一处: 整组应用与逐条应用必须完全等价, 否则"点测试"与"定时探测"会得出两种结论。
//
// 不作为探测结论的情形(返回 nil, 保持原状态, 也不留历史):
//   - 成员不可用(渠道或凭据被禁用): 根本没发上游请求, 结果里的失败只是本地解析结论;
//   - 请求被取消: 客户端断开或本轮中断, 不代表上游状态。
func grantProbeApplyResult(grantID int, result model.GroupMemberTestResult, source string, now int64) (*model.ChannelGrant, *model.GrantProbeRecord, error) {
	if grantID == 0 || result.Status == "unavailable" || result.Status == "canceled" {
		return nil, nil, nil
	}
	grant, ok := channelGrantCache.Get(grantID)
	if !ok {
		return nil, nil, nil
	}

	probeStatus, probeReason, failCount := grant.ProbeStatus, grant.ProbeReason, grant.ProbeFailCount
	switch {
	case result.Success:
		probeStatus, probeReason, failCount = ProbeStatusOK, "", 0
	case probeFailureIsFatal(result.Error):
		// 确定性死亡: 一次即可定性, 计数一并推高以免下次抖动把它"降级"回未定型。
		probeStatus, probeReason = ProbeStatusDead, probeReasonTruncate(result.Error)
		failCount++
	default:
		probeReason, failCount = probeReasonTruncate(result.Error), failCount+1
		if failCount >= ProbeDeadAfterFailures {
			probeStatus = ProbeStatusDead
		} else {
			// 抖动但未定型: 回到"未探测", 界面上既不标绿也不标死, 由下一轮再判。
			probeStatus = ProbeStatusUnknown
		}
	}

	if err := db.GetDB().Model(&model.ChannelGrant{}).Where("id = ?", grantID).Updates(map[string]any{
		"probe_status":     probeStatus,
		"probe_reason":     probeReason,
		"probe_at":         now,
		"probe_fail_count": failCount,
	}).Error; err != nil {
		return nil, nil, err
	}

	grant.ProbeStatus, grant.ProbeReason, grant.ProbeAt, grant.ProbeFailCount = probeStatus, probeReason, now, failCount
	channelGrantCache.Set(grant.ID, grant)
	return &grant, &model.GrantProbeRecord{
		GrantID:  grantID,
		At:       now,
		Success:  result.Success,
		Reason:   probeReason,
		UseTime:  result.UseTime,
		Protocol: int(result.Protocol),
		Source:   source,
	}, nil
}

// GrantProbeApplyResult 应用单条探测结果并立即写一条历史, 供流式测试逐条落库:
// 成员一测完, 界面拿到的就是新结论, 不必等整组跑完那一跳。
func GrantProbeApplyResult(grantID int, result model.GroupMemberTestResult, source string) (*model.ChannelGrant, error) {
	grant, record, err := grantProbeApplyResult(grantID, result, source, time.Now().Unix())
	if err != nil || record == nil {
		return grant, err
	}
	// 历史写失败只告警: 丢了这一条不该让"判死/恢复"这个更要紧的结论一起失效。
	if err := db.GetDB().Create(record).Error; err != nil {
		log.Warnf("grant %d probe history write failed: %v", grantID, err)
	}
	return grant, nil
}

// GroupProbeApply 把一次分组探测的结果落到各成员引用的授权上, 并写历史记录。
//
// 结论只写授权的 probe_* 四列, 不改渠道与凭据的启停、不改分组成员、不触发授权对账:
// 探测是旁路观测, 唯一的下游影响是选路时跳过已判死的成员(见 relay 层), 24 小时未再探测即自动失效。
//
// 历史与结论分开落库: 结论是选路要用的强一致数据, 历史是审计数据。
func GroupProbeApply(groupID int, results []model.GroupMemberTestResult, source string) error {
	if len(results) == 0 {
		return nil
	}
	var items []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", groupID).Find(&items).Error; err != nil {
		return err
	}
	grantOfItem := make(map[int]int, len(items))
	for _, item := range items {
		grantOfItem[item.ID] = item.ChannelGrantID
	}

	now := time.Now().Unix()
	applied := 0
	records := make([]model.GrantProbeRecord, 0, len(results))
	for _, result := range results {
		_, record, err := grantProbeApplyResult(grantOfItem[result.ItemID], result, source, now)
		if err != nil {
			return err
		}
		if record == nil {
			continue
		}
		records = append(records, *record)
		applied++
	}
	if len(records) > 0 {
		if err := db.GetDB().CreateInBatches(records, 100).Error; err != nil {
			log.Warnf("group %d probe history write failed: %v", groupID, err)
		}
	}
	if applied > 0 {
		log.Debugf("group %d probe applied to %d grants", groupID, applied)
	}
	return nil
}

// GrantProbePrune 按保留期清理历史, 返回删除行数。
// keepDays <= 0 表示不清理(与转发日志 keep_period 的语义一致)。
func GrantProbePrune(keepDays int) (int64, error) {
	if keepDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Unix()
	result := db.GetDB().Where("at < ?", cutoff).Delete(&model.GrantProbeRecord{})
	return result.RowsAffected, result.Error
}

// GrantProbeTimeline 返回单个成员在窗口内的探测历史: 汇总 + 明细(时间倒序)。
// days <= 0 表示不限时间窗。
func GrantProbeTimeline(grantID int, days int) (model.GrantProbeTimeline, error) {
	query := db.GetDB().Where("grant_id = ?", grantID)
	if days > 0 {
		query = query.Where("at >= ?", time.Now().AddDate(0, 0, -days).Unix())
	}
	var records []model.GrantProbeRecord
	if err := query.Order("at DESC, id DESC").Limit(500).Find(&records).Error; err != nil {
		return model.GrantProbeTimeline{}, err
	}
	timeline := model.GrantProbeTimeline{GrantID: grantID, Records: records}
	// 名称取授权当前指向的实体: 历史行只存主键, 渠道/模型/凭据改名后按现状展示。
	if grant, err := ChannelGrantGet(grantID); err == nil && grant.ChannelModel != nil && grant.ChannelKey != nil {
		timeline.ModelName = grant.ChannelModel.Name
		timeline.KeyName = grant.ChannelKey.Name
		if channel, err := ChannelGet(grant.ChannelModel.ChannelID); err == nil {
			timeline.ChannelName = channel.Name
		}
	}
	for _, record := range records {
		if record.Success {
			timeline.OK++
			continue
		}
		timeline.Failed++
		if timeline.LastError == "" {
			timeline.LastError = record.Reason
		}
	}
	if len(records) > 0 {
		timeline.LastAt = records[0].At
	}
	return timeline, nil
}
