package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/utils/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 10,
		Up:      defaultChannelAutoGroupToRegex,
	})
}

// 010:
// 将渠道自动分组默认策略从“不自动分组(0)”迁移为“正则匹配(3)”。
// 这样即使分组配置了匹配正则,上游模型更新时,渠道也能按正则把匹配的模型自动写进分组。
// 幂等:由 migration_records 保证只执行一次;SQL 仅影响 auto_group 为 0 或 NULL 的行。
// 防御:channels 表可能不存在(例如纯 stats 的测试库),此时跳过。
func defaultChannelAutoGroupToRegex(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") {
		log.Infof("migration 010: channels table not exists, skip")
		return nil
	}
	res := db.Exec("UPDATE channels SET auto_group = ? WHERE auto_group IS NULL OR auto_group = ?", 3, 0)
	if res.Error != nil {
		return fmt.Errorf("failed to migrate channels.auto_group default to regex: %w", res.Error)
	}
	log.Infof("migration 010: set channels.auto_group to regex(3) for %d channels", res.RowsAffected)
	return nil
}