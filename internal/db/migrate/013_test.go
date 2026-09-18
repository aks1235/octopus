package migrate

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openMigrationTestDB 建一个裸 SQLite 测试库(不经 InitDB, 迁移记录表也不建), 供迁移函数直接调用。
func openMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate-013-test.db")), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// createLegacyStatsHourlies 建旧形状的 stats_hourlies(hour 单列主键, date 只是普通列)。
func createLegacyStatsHourlies(t *testing.T, db *gorm.DB) {
	t.Helper()
	// 列集合与旧模型一致: 复合主键重建后这些行原样并入。
	err := db.Exec(`CREATE TABLE stats_hourlies (
		hour integer PRIMARY KEY,
		date text NOT NULL,
		input_token bigint,
		output_token bigint,
		input_cost real,
		output_cost real,
		wait_time bigint,
		request_success bigint,
		request_failed bigint
	)`).Error
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
}

// TestMigrateStatsHourlyDateKey_preservesRows 锁定迁移语义: 老表行按行内 date 并入新表且获得复合主键。
func TestMigrateStatsHourlyDateKey_preservesRows(t *testing.T) {
	db := openMigrationTestDB(t)
	createLegacyStatsHourlies(t, db)

	legacy := []model.StatsHourly{
		{Hour: 5, Date: "20260916", StatsMetrics: model.StatsMetrics{RequestSuccess: 2, InputToken: 20}},
		{Hour: 6, Date: "20260917", StatsMetrics: model.StatsMetrics{RequestFailed: 1}},
		{Hour: 23, Date: "20260917", StatsMetrics: model.StatsMetrics{RequestSuccess: 7, OutputToken: 70}},
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	if err := migrateStatsHourlyDateKey(db); err != nil {
		t.Fatalf("migrateStatsHourlyDateKey() error = %v", err)
	}

	var rows []model.StatsHourly
	if err := db.Order("date, hour").Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (老数据必须全量保留)", len(rows))
	}
	if rows[0].Date != "20260916" || rows[0].Hour != 5 || rows[0].RequestSuccess != 2 {
		t.Fatalf("row0 = %+v, want 20260916/5 preserved", rows[0])
	}
	if rows[2].Hour != 23 || rows[2].RequestSuccess != 7 {
		t.Fatalf("row2 = %+v, want 20260917/23 preserved", rows[2])
	}

	// 复合主键生效: 同一 hour 不同 date 可并存。
	inserted := model.StatsHourly{Hour: 5, Date: "20260918", StatsMetrics: model.StatsMetrics{RequestSuccess: 1}}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatalf("insert same hour new date: %v (复合主键未生效?)", err)
	}
	// 同 (date, hour) 撞主键必须被拒绝, 证明主键确实是复合的。
	dup := model.StatsHourly{Hour: 5, Date: "20260918"}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatalf("insert duplicate (date,hour) succeeded, composite PK missing")
	}
}

// TestMigrateStatsHourlyDateKey_idempotentAndSkipsFresh 锁定幂等防御:
// 重跑迁移不丢数据; 全新库无表时直接跳过交给 AutoMigrate。
func TestMigrateStatsHourlyDateKey_idempotentAndSkipsFresh(t *testing.T) {
	db := openMigrationTestDB(t)
	createLegacyStatsHourlies(t, db)
	seed := model.StatsHourly{Hour: 9, Date: "20260917", StatsMetrics: model.StatsMetrics{RequestSuccess: 4}}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := migrateStatsHourlyDateKey(db); err != nil {
			t.Fatalf("run %d: migrateStatsHourlyDateKey() error = %v", i+1, err)
		}
	}

	var rows []model.StatsHourly
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestSuccess != 4 || rows[0].Date != "20260917" {
		t.Fatalf("rows = %+v, want the seeded row preserved across reruns", rows)
	}

	// 全新库: 无 stats_hourlies 表时迁移是安全空操作。
	fresh := openMigrationTestDB(t)
	if err := migrateStatsHourlyDateKey(fresh); err != nil {
		t.Fatalf("fresh db: migrateStatsHourlyDateKey() error = %v", err)
	}
	if fresh.Migrator().HasTable("stats_hourlies") {
		t.Fatalf("fresh db should not create the table, AutoMigrate owns it")
	}
}
