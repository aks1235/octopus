package model

// 探测来源: 人工在界面上点测试, 还是定时任务跑的一轮。历史里两者的可信度不同
// (人工那次往往是因为"我怀疑它坏了"才点的, 定时那轮是无偏采样), 故分开存。
const (
	ProbeSourceManual    = "manual"
	ProbeSourceScheduled = "scheduled"
)

// GrantProbeRecord 是一次真实探测的审计行: 哪个授权、何时、成没成、错在哪。
//
// 与 ChannelGrant 上的 probe_* 四列分工: 那四列只存"最新结论"用于选路与界面标注, 每次覆盖;
// 本表存时间线, 用于回看"这一周它挂过几次"。两者不可互相替代——结论列不带历史, 历史表不参与选路。
//
// 体量: 一次探测一行。按 268 成员、每天 2-4 轮算, 一年约 20-40 万行, 对 SQLite 无压力;
// 保留期由设置项 grant_probe_keep_days 控制(默认 90 天), 与转发日志的保留期互不影响。
type GrantProbeRecord struct {
	ID       int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	GrantID  int    `json:"grant_id" gorm:"not null;index:idx_grant_probe_record,priority:1"` // 被测的授权(渠道×模型×凭据)。
	At       int64  `json:"at" gorm:"not null;index:idx_grant_probe_record,priority:2"`       // 探测完成时间(unix 秒)。
	Success  bool   `json:"success" gorm:"not null"`                                          // 三层判据(2xx + 可解析 + 有正文/工具调用)全过为真。
	Reason   string `json:"reason" gorm:"not null;default:''"`                                // 失败原因摘要, 已截断; 成功为空。
	UseTime  int64  `json:"use_time" gorm:"not null;default:0"`                               // 该成员本次探测耗时(毫秒, 含多协议累计)。
	Protocol int    `json:"protocol" gorm:"not null;default:0"`                               // 成功时命中的协议位, 失败为 0。
	Source   string `json:"source" gorm:"not null;default:''"`                                // manual / scheduled, 见 ProbeSource* 常量。
}

// GrantProbeTimeline 是单个成员的历史: 汇总 + 明细, 供成员抽屉展示。
type GrantProbeTimeline struct {
	GrantID     int                `json:"grant_id"`     // 授权主键。
	ChannelName string             `json:"channel_name"` // 渠道名称。
	ModelName   string             `json:"model_name"`   // 模型名称。
	KeyName     string             `json:"key_name"`     // 凭据名称。
	OK          int                `json:"ok"`           // 窗口内成功次数。
	Failed      int                `json:"failed"`       // 窗口内失败次数。
	LastAt      int64              `json:"last_at"`      // 窗口内最近一次探测时间。
	LastError   string             `json:"last_error"`   // 窗口内最近一次失败原因; 无失败为空。
	Records     []GrantProbeRecord `json:"records"`      // 明细, 按时间倒序。
}

// GrantProbeMemberSummary 是分组历史上的一行: 该成员的窗口内成败与最近失败原因。
type GrantProbeMemberSummary struct {
	GrantID     int    `json:"grant_id"`
	ChannelName string `json:"channel_name"`
	ModelName   string `json:"model_name"`
	KeyName     string `json:"key_name"`
	OK          int    `json:"ok"`
	Failed      int    `json:"failed"`
	LastAt      int64  `json:"last_at"`
	LastError   string `json:"last_error"`
}
