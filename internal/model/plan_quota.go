package model

// ChannelPlanQuota 是渠道套餐额度的最近一次采集结果, 一渠道一行。
//
// 与 StatsMetrics 是两套账: 这里存的是云厂商控制面上的真实余量(AFP / Credits), 由外部接口采集;
// StatsMetrics 是本网关按 token 累计的估算。两者口径不同, 不得互相扣减。
type ChannelPlanQuota struct {
	ChannelID int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"` // 渠道主键, 一渠道一行。
	Vendor    string `json:"vendor"`                                           // 采集厂商, 与渠道配置对应。
	Kind      string `json:"kind"`                                             // 套餐类型。
	PlanName  string `json:"plan_name"`                                        // 套餐档位, 如 Medium; 厂商不返回时为空。
	// FetchedAt 是最近一次采集完成的时刻, 成功失败都更新, 供界面展示"多久没刷新了"。
	FetchedAt int64 `json:"fetched_at"`
	// SyncedAt 只在采集成功时更新, 代表 Windows 这份数据本身的新鲜度。
	// 采集失败时保留旧值, 界面据此提示"数据可能已过期", 而不是把面板清空。
	SyncedAt int64             `json:"synced_at"`
	Error    string            `json:"error"` // 最近一次采集的失败原因; 成功时清空。
	Windows  []PlanQuotaWindow `json:"windows" gorm:"serializer:json"`
}

// PlanQuotaWindow 是一个计量窗口的采集结果, 字段含义与 quota.Window 一致。
// 在此单独定义一份是为了让 model 不反向依赖 quota 包(quota 的传输层依赖 op, op 依赖 model, 会成环)。
type PlanQuotaWindow struct {
	Key     string  `json:"key"`      // 5h / week / month。
	Used    float64 `json:"used"`     // 已用量。
	Total   float64 `json:"total"`    // 总配额。
	Percent float64 `json:"percent"`  // 已用百分比 0-100。
	ResetAt int64   `json:"reset_at"` // 下次重置的 unix 秒; 0 表示未排定。
}
