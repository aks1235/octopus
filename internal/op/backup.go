package op

import (
	"context"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 渠道拆分为渠道, 凭据, 模型与渠道授权后导出结构变化, 版本随之递增;
// 版本 5 起 api_keys.supported_models 由逗号分隔字符串改为 JSON 数组。
const dbDumpVersion = 5

// DBExportAll 导出完整数据库内容，包括所有统计数据。
func DBExportAll(ctx context.Context) (*model.DBDump, error) {
	conn := db.GetDB().WithContext(ctx)

	d := &model.DBDump{
		Version:    dbDumpVersion,
		ExportedAt: time.Now().UTC(),
	}

	if err := conn.Find(&d.Channels).Error; err != nil {
		return nil, fmt.Errorf("export channels: %w", err)
	}
	if err := conn.Find(&d.Groups).Error; err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}
	if err := conn.Find(&d.ChannelKeys).Error; err != nil {
		return nil, fmt.Errorf("export channel_keys: %w", err)
	}
	if err := conn.Find(&d.ChannelModels).Error; err != nil {
		return nil, fmt.Errorf("export channel_models: %w", err)
	}
	if err := conn.Find(&d.ChannelGrants).Error; err != nil {
		return nil, fmt.Errorf("export channel_grants: %w", err)
	}
	if err := conn.Find(&d.GroupItems).Error; err != nil {
		return nil, fmt.Errorf("export group_items: %w", err)
	}
	if err := conn.Find(&d.GroupChannelOrders).Error; err != nil {
		return nil, fmt.Errorf("export group_channel_orders: %w", err)
	}
	if err := conn.Find(&d.LLMInfos).Error; err != nil {
		return nil, fmt.Errorf("export llm_infos: %w", err)
	}
	if err := conn.Find(&d.APIKeys).Error; err != nil {
		return nil, fmt.Errorf("export api_keys: %w", err)
	}
	if err := conn.Find(&d.Settings).Error; err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}

	if err := conn.Find(&d.StatsTotal).Error; err != nil {
		return nil, fmt.Errorf("export stats_total: %w", err)
	}
	if err := conn.Find(&d.StatsDaily).Error; err != nil {
		return nil, fmt.Errorf("export stats_daily: %w", err)
	}
	if err := conn.Find(&d.StatsHourly).Error; err != nil {
		return nil, fmt.Errorf("export stats_hourly: %w", err)
	}
	if err := conn.Find(&d.StatsAPIKey).Error; err != nil {
		return nil, fmt.Errorf("export stats_api_key: %w", err)
	}

	return d, nil
}

// batchSize 控制每次 INSERT 的最大行数。
// 单行字段数较多（如 stats_hourly 含 9 个字段），若一次插入过多行会超过数据库绑定参数上限（SQLite/PostgreSQL 为 65535），按行数分批写入可规避该限制。
const batchSize = 2000

func createUpsertAll[T any](tx *gorm.DB, rows []T, columns []clause.Column) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   columns,
		UpdateAll: true,
	}).CreateInBatches(&rows, batchSize)
	return result.RowsAffected, result.Error
}
