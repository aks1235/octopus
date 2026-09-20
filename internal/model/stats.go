package model

type StatsMetrics struct {
	InputToken     int64   `json:"input_token" gorm:"bigint"`
	OutputToken    int64   `json:"output_token" gorm:"bigint"`
	InputCost      float64 `json:"input_cost" gorm:"type:real"`
	OutputCost     float64 `json:"output_cost" gorm:"type:real"`
	WaitTime       int64   `json:"wait_time" gorm:"bigint"`
	RequestSuccess int64   `json:"request_success" gorm:"bigint"`
	RequestFailed  int64   `json:"request_failed" gorm:"bigint"`
}

type StatsTotal struct {
	ID int `gorm:"primaryKey"`
	StatsMetrics
}

type StatsHourly struct {
	// (date, hour) 复合主键: 小时行按日期留存而非 24 格环形覆盖, 保留期内任意一天的小时曲线都可查。
	// 由迁移 013 从旧的 hour 单列主键重建而来, 老表行按行内 date 值并入。
	Date string `json:"date" gorm:"primaryKey"` // 统计日期, 格式 20060102
	Hour int    `json:"hour" gorm:"primaryKey"` // 0-23
	StatsMetrics
}

type StatsDaily struct {
	Date string `json:"date" gorm:"primaryKey"`
	StatsMetrics
}

// StatsChannelDaily 渠道按天汇总(永久账): 由 relay_logs 聚合折叠而来, 不随日志保留期清理。
// (date, channel_id) 复合主键: 同日同渠道恒一行, 折叠时整体替换即幂等;
// 名称取当日字典序最大值(与聚合口径一致), 同日改名不拆行。
type StatsChannelDaily struct {
	Date        string `json:"date" gorm:"primaryKey"`       // 统计日期, 格式 20060102
	ChannelID   int    `json:"channel_id" gorm:"primaryKey"` // 渠道 ID
	ChannelName string `json:"channel_name"`                 // 渠道名(当日 MAX)
	StatsMetrics
}

// StatsModelDaily 模型(请求模型名, 即分组名)按天汇总(永久账), 语义同 StatsChannelDaily。
type StatsModelDaily struct {
	Date      string `json:"date" gorm:"primaryKey"`       // 统计日期, 格式 20060102
	ModelName string `json:"model_name" gorm:"primaryKey"` // 请求模型名(分组名)
	StatsMetrics
}

type StatsAPIKey struct {
	APIKeyID int `json:"api_key_id" gorm:"primaryKey"`
	StatsMetrics
}

// Add aggregates another StatsMetrics into the current one.
func (s *StatsMetrics) Add(delta StatsMetrics) {
	s.InputToken += delta.InputToken
	s.OutputToken += delta.OutputToken
	s.InputCost += delta.InputCost
	s.OutputCost += delta.OutputCost
	s.WaitTime += delta.WaitTime
	s.RequestSuccess += delta.RequestSuccess
	s.RequestFailed += delta.RequestFailed
}
