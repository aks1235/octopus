package model

// GrantOutcome 是故障转移排序窗口的持久化快照。
//
// 窗口本体仍在内存: 选路是每个请求都要走的热路径, 不能查库; 这张表只解决"重启即失忆"——
// 没有它, 一次重启把全部成员打回中性分, 排序退回人工优先级, 样本不足的新成员会被永久挤在末尾。
// 写入由 relay 侧定时批量 upsert(默认 5 秒), 崩溃最多丢一个间隔的记录, 不影响选路本身。
type GrantOutcome struct {
	GrantID int `json:"grant_id" gorm:"primaryKey;autoIncrement:false"`
	// Bits 是 16 位失败位图, 低位为最新一次; Count 是已记录次数(0-16), 满窗后按先进先出滚动。
	Bits  int `json:"bits" gorm:"not null;default:0"`
	Count int `json:"count" gorm:"not null;default:0"`
	// UpdatedAt 记录最后一次快照时间, 便于排查与后续清理长期不用的凭据。
	UpdatedAt int64 `json:"updated_at" gorm:"not null;default:0;index"`
}
