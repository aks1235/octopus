package migrate

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestMigrateGroupContextLengthDefault_fillsLegacyRows 验证新增列后老分组被补齐为默认值,
// 已经有值的行不被覆盖。
func TestMigrateGroupContextLengthDefault_fillsLegacyRows(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.AutoMigrate(&model.Group{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	// 模拟"这一列出现之前就存在的分组": 插入后把列置 0(GORM 的 default 会让零值走库默认值)。
	if err := db.Create(&model.Group{Name: "legacy"}).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	if err := db.Exec("UPDATE groups SET context_length = 0 WHERE name = ?", "legacy").Error; err != nil {
		t.Fatalf("reset legacy: %v", err)
	}
	if err := db.Create(&model.Group{Name: "custom", ContextLength: 262144}).Error; err != nil {
		t.Fatalf("create custom: %v", err)
	}

	if err := migrateGroupContextLengthDefault(db); err != nil {
		t.Fatalf("migrateGroupContextLengthDefault() error = %v", err)
	}

	var legacy, custom model.Group
	if err := db.Where("name = ?", "legacy").First(&legacy).Error; err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	if err := db.Where("name = ?", "custom").First(&custom).Error; err != nil {
		t.Fatalf("load custom: %v", err)
	}
	if legacy.ContextLength != model.DefaultGroupContextLength {
		t.Fatalf("老分组补齐后 = %d, want %d", legacy.ContextLength, model.DefaultGroupContextLength)
	}
	if custom.ContextLength != 262144 {
		t.Fatalf("已有值被覆盖 = %d, want 262144", custom.ContextLength)
	}
}
