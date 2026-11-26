// Package quota 采集上游订阅套餐的额度用量。
//
// 套餐额度和网关自身的统计是两套口径: 前者是云厂商控制面上的真实余量(火山按 AFP 计,
// 阿里云按 Credits 计), 后者是本网关按 token 累计的估算。两者不能互相扣减, 展示时也必须分开。
package quota

import (
	"context"
	"net/http"
)

// WindowKey 是窗口的稳定标识。展示用的文案由前端按语言给出, 后端只给 key。
type WindowKey string

const (
	WindowFiveHour WindowKey = "5h"    // 5 小时滚动窗口。
	WindowWeekly   WindowKey = "week"  // 周窗口。
	WindowMonthly  WindowKey = "month" // 订阅月窗口。
)

// 套餐类型。决定调用哪个 Action 以及如何解析响应。
const (
	KindAgentPlan  = "agent_plan"  // 火山方舟 Agent Plan, 走 GetAFPUsage。
	KindCodingPlan = "coding_plan" // 火山方舟 Coding Plan, 走 GetCodingPlanUsage。
)

// Window 是一个计量窗口的归一化结果。
type Window struct {
	Key     WindowKey `json:"key"`
	Used    float64   `json:"used"`     // 窗口内已用量, 单位随厂商(AFP/Credits)。
	Total   float64   `json:"total"`    // 窗口总配额, 同上。
	Percent float64   `json:"percent"`  // 已用百分比, 0-100。
	ResetAt int64     `json:"reset_at"` // 下次重置的 unix 秒; 0 表示该窗口未排定重置。
}

// Snapshot 是一次采集的完整结果。
type Snapshot struct {
	PlanName string   `json:"plan_name"` // 套餐档位, 如 Medium; 厂商不返回时为空。
	Windows  []Window `json:"windows"`
}

// Credential 是采集套餐额度所需的凭证。
//
// 与渠道的推理凭据是两回事: 套餐用量在云厂商的控制面上, 推理 key 调不通。
// 火山实测报 "GetAFPUsage requires Volcengine Ark SSO STS", 必须用账号级 AK/SK 签名。
type Credential struct {
	AccessKeyID     string
	SecretAccessKey string
	Kind            string // KindAgentPlan 或 KindCodingPlan。
}

// Provider 采集某家厂商某个套餐的额度。
// 返回空 Windows 且无错误表示账号下没有生效中的套餐, 不是失败。
//
// HTTP 客户端由调用方注入: 代理选择依赖应用设置, 而设置读取在 op 包, 本包若自行取客户端会与 op 成环。
type Provider interface {
	Fetch(ctx context.Context, client *http.Client, cred Credential) (*Snapshot, error)
}
