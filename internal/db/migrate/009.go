package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 9,
		Up:      migrateDropLegacyChannelSchema,
	})
}

// migrateDropLegacyChannelSchema 删除遗留的渠道多地址字段。
// relay_logs 不再删除: dev-v2 恢复日志持久化(ADR-0005), 该表由 AutoMigrate 按完整 fork 列形状
// 重建并承载迁移脚本带入的历史日志; 上游原版此处的 DropTable 已按 ADR-0005 移除。
func migrateDropLegacyChannelSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if db.Migrator().HasTable("channels") {
		if err := dropColumnIfExists(db, &model.Channel{}, "channels", "base_urls"); err != nil {
			return err
		}
	}
	return nil
}
