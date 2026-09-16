package model

import "time"

// DBDump is a full-database JSON export format for Octopus.
// Import uses incremental semantics (insert new rows, and upsert on tables with natural keys).
type DBDump struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`

	Channels      []Channel      `json:"channels,omitempty"`       // 渠道数据。
	ChannelKeys   []ChannelKey   `json:"channel_keys,omitempty"`   // 渠道凭据数据。
	ChannelModels []ChannelModel `json:"channel_models,omitempty"` // 渠道模型数据。
	ChannelGrants []ChannelGrant `json:"channel_grants,omitempty"` // 渠道授权数据, 含统计。
	Groups        []Group        `json:"groups,omitempty"`         // 分组数据。
	GroupItems    []GroupItem    `json:"group_items,omitempty"`    // 分组成员数据。
	// 正则分组的人工渠道顺序。旧备份(v5 早期)无此字段, 导入后为空即回到自然序,
	// 等价于「从未设过人工顺序」, 故 dump 版本不因本字段递增。
	GroupChannelOrders []GroupChannelOrder `json:"group_channel_orders,omitempty"`
	LLMInfos           []LLMInfo          `json:"llm_infos,omitempty"` // 模型价格数据。
	APIKeys            []APIKey           `json:"api_keys,omitempty"`  // API Key 数据。
	Settings           []Setting          `json:"settings,omitempty"`  // 系统设置数据。

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}
