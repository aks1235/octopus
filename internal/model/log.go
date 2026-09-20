package model

// RelayLogDebugContent 日志调试信息，仅在差异/错误/转换失败时记录
type RelayLogDebugContent struct {
	// ClientRequest 原始客户端请求 body（仅在与 request_content 不同时记录）
	ClientRequest string `json:"client_request,omitempty"`
	// UpstreamResponse 上游原始响应 body（仅在 conversion 或出错需要排查时记录）
	UpstreamResponse string `json:"upstream_response,omitempty"`
	// StreamWire 流式失败/转换失败时的截断 SSE 记录
	StreamWire string `json:"stream_wire,omitempty"`
	// Notes 辅助说明，如 truncated, fallback_to_sse, partial 等
	Notes []string `json:"notes,omitempty"`
}

// AttemptStatus 尝试状态
type AttemptStatus string

const (
	AttemptSuccess      AttemptStatus = "success"       // 转发成功
	AttemptFailed       AttemptStatus = "failed"        // 转发失败
	AttemptCircuitBreak AttemptStatus = "circuit_break" // 熔断跳过
	AttemptSkipped      AttemptStatus = "skipped"       // 其他原因跳过（禁用、无Key、类型不兼容等）
)

// ChannelAttempt 记录单次渠道尝试的决策和结果
type ChannelAttempt struct {
	ChannelID        int           `json:"channel_id"`
	ChannelKeyID     int           `json:"channel_key_id,omitempty"`
	ChannelName      string        `json:"channel_name"`
	ChannelKeyRemark string        `json:"channel_key_remark,omitempty"`
	ModelName        string        `json:"model_name"`
	AttemptNum       int           `json:"attempt_num"`
	Status           AttemptStatus `json:"status"`
	Duration         int           `json:"duration"`
	Sticky           bool          `json:"sticky,omitempty"`
	Msg              string        `json:"msg,omitempty"`
}

// RelayLogAttempt 是 RelayLog.Attempts(JSON 数组)的规范化表: 一行 = 一次渠道尝试。
//
// Why: 按渠道查调用明细时, 日志行的 channel_id 只记**最终**渠道, 失败转移途中的每次尝试只存在于
// attempts JSON 列里; 旧实现对这条巨型文本列做前缀通配 LIKE 全表扫描(零索引 + 全表排序),
// 代价随日志量线性恶化。规范化后查询改为 `channel_id` 走索引, 与日志总量无关。
//
// 关键列约定:
//   - Time 冗余自所属日志的 time(unix 秒): 渠道维度查询只碰本表即可按时间过滤/排序, 不回表扫大 JSON 列。
//   - 主键 (log_id, attempt_num): 与 attempts JSON 内的尝试序号一一对应, 重复落盘同一日志天然去重。
//   - 索引 (channel_id, log_id): 渠道维度查询的唯一入口。
//
// 生命周期: 写入与清理都与 relay_logs 同事务(见 op.relayLogFlushToDB / op.relayLogCleanup),
// 日志被删则其尝试行一并删除, 表内不留孤儿行。保留期口径与日志完全一致。
type RelayLogAttempt struct {
	LogID            int64         `json:"log_id" gorm:"primaryKey;autoIncrement:false;index:idx_relay_log_attempts_channel_log,priority:2"` // 所属 relay_log.id(Snowflake)
	AttemptNum       int           `json:"attempt_num" gorm:"primaryKey;autoIncrement:false"`                                                // 该请求内的尝试序号(与 JSON 中 attempt_num 同值)
	ChannelID        int           `json:"channel_id" gorm:"index:idx_relay_log_attempts_channel_log,priority:1"`                            // 本次尝试的渠道(非日志最终渠道)
	Time             int64         `json:"time"`                                                                                             // 冗余自所属日志的 time(unix 秒)
	ChannelName      string        `json:"channel_name"`                                                                                     // 渠道名称
	ChannelKeyID     int           `json:"channel_key_id" gorm:"default:0"`                                                                  // 渠道凭据 ID
	ChannelKeyRemark string        `json:"channel_key_remark"`                                                                               // 渠道凭据备注
	ModelName        string        `json:"model_name"`                                                                                       // 被试模型
	Status           AttemptStatus `json:"status"`                                                                                           // success/failed/circuit_break/skipped
	Duration         int           `json:"duration" gorm:"default:0"`                                                                        // 耗时(ms)
	Sticky           bool          `json:"sticky" gorm:"default:false"`                                                                      // 是否命中渠道亲和
	Msg              string        `json:"msg"`                                                                                              // 失败原因等附加信息
}

// ChannelAttemptDetail 按渠道查调用明细时返回的单条记录。
// 复用 ChannelAttempt 的渠道尝试字段,并增加请求级溯源字段(指向该 attempt 所属的 relay_log)。
type ChannelAttemptDetail struct {
	RequestID     int64         `json:"request_id"`              // 所属 relay_log.id(Snowflake)
	RequestTime   int64         `json:"request_time"`            // relay_log.time(unix 秒)
	RequestModel  string        `json:"request_model"`           // request_model_name
	RequestError  string        `json:"request_error,omitempty"` // relay_log.error(请求级错误)
	AttemptNum    int           `json:"attempt_num"`             // 本次 attempt 在请求中的序号
	Status        AttemptStatus `json:"status"`                  // success/failed/circuit_break/skipped
	ChannelID     int           `json:"channel_id"`              // 本次尝试渠道(非最终成功渠道)
	ChannelName   string        `json:"channel_name,omitempty"`
	ChannelKeyRem string        `json:"channel_key_remark,omitempty"`
	ModelName     string        `json:"model_name,omitempty"` // 被试模型
	Duration      int           `json:"duration"`             // 耗时(ms)
	Sticky        bool          `json:"sticky,omitempty"`
	Msg           string        `json:"msg,omitempty"`
}

type RelayLog struct {
	ID                  int64            `json:"id" gorm:"primaryKey;autoIncrement:false"` // Snowflake ID
	Time                int64            `json:"time" gorm:"index:idx_relay_log_time"`     // 时间戳（秒）
	RequestModelName    string           `json:"request_model_name"`                       // 请求模型名称
	RequestAPIKeyName   string           `json:"request_api_key_name"`                     // 请求使用的 API Key 名称
	ChannelId           int              `json:"channel"`                                  // 实际使用的渠道ID
	ChannelName         string           `json:"channel_name"`                             // 渠道名称
	ActualModelName     string           `json:"actual_model_name"`                        // 实际使用模型名称
	ReasoningEffort     string           `json:"reasoning_effort"`                         // 实际发送的思考等级: low/medium/high/max, 空字符串表示非推理请求
	InputTokens         int              `json:"input_tokens"`                             // 输入Token
	OutputTokens        int              `json:"output_tokens"`                            // 输出 Token
	CachedTokens        int              `json:"cached_tokens" gorm:"default:0"`           // 缓存命中Token
	CacheCreationTokens int              `json:"cache_creation_tokens" gorm:"default:0"`   // 缓存写入Token
	Ftut                int              `json:"ftut"`                                     // 首字时间(毫秒)
	UseTime             int              `json:"use_time"`                                 // 总用时(毫秒)
	Cost                float64          `json:"cost"`                                     // 消耗费用
	RequestContent      string           `json:"request_content"`                          // 请求内容
	ResponseContent     string           `json:"response_content"`                         // 响应内容
	DebugContent        string           `json:"debug_content" gorm:"type:longtext"`       // 调试信息（差异、错误、转换失败时记录）
	Error               string           `json:"error"`                                    // 错误信息
	Attempts            []ChannelAttempt `json:"attempts" gorm:"serializer:json"`          // 所有尝试记录
	TotalAttempts       int              `json:"total_attempts"`                           // 总尝试次数
	UserAgent           string           `json:"user_agent"`                               // 原始 User-Agent 头
	ClientName          string           `json:"client_name"`                              // 解析后的客户端标识（如 claude-code, cline 等）
}
