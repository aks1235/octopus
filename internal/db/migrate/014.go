package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      migrateGroupContextLengthDefault,
	})
}

// migrateGroupContextLengthDefault 把上下文窗口未填写(0)的历史分组补齐为默认值。
//
// 放在 AutoMigrate 之后执行: context_length 是本版本新增的列, 迁移函数依赖它已建好。
// 0 在这里的含义是"这一列出现之前就存在的分组", 不是用户刻意声明 0, 因此补默认值不覆盖任何人工设置。
// 列已存在且有值时不会被改写, 重复执行无副作用。
func migrateGroupContextLengthDefault(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Group{}, "context_length") {
		return nil
	}
	return db.Model(&model.Group{}).
		Where("context_length = ?", 0).
		Update("context_length", model.DefaultGroupContextLength).Error
}
