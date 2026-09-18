package migrate

import (
	"fmt"
	"sort"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 13,
		Up:      migrateStatsHourlyDateKey,
	})
}

// migrateStatsHourlyDateKey 把 stats_hourlies 从 hour 单列主键(24 格环形, 每天同 hour 互相覆盖)
// 重建为 (date, hour) 复合主键, 小时行按日期留存。老表的每一行按它行内已有的 date 值并入新表,
// 升级瞬间不丢当天曲线; 历史小时行随后由日志保留期清理接管。
//
// 注意 Up 绝不能是 nil, 哪怕有 HasTable 幂等防御也要给真实函数:
// runMigrationsWithRecord 对 nil Up 直接报 "migration %d has nil Up", InitDB 失败应用起不来(09-15 教训)。
//
// 幂等性: 老表若已是新形状(迁移成功但记录丢失后重跑), 读-重建-回填流程依然安全——
// 新旧形状列集合一致(date, hour + 7 个指标列), 读出的行原样回填不丢数据;
// 表不存在(全新库)时直接跳过, 交给主 AutoMigrate 按新形状建表。
func migrateStatsHourlyDateKey(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("stats_hourlies") {
		return nil
	}

	// 建表与删表在事务内执行: SQLite 的 DDL 是事务性的, 中途失败整体回滚不会把老表删掉。
	// stats_hourlies 没有外键关联, 不受 011 注释里 DROP TABLE 级联清空子表问题的影响。
	return db.Transaction(func(tx *gorm.DB) error {
		var rows []model.StatsHourly
		if err := tx.Find(&rows).Error; err != nil {
			return fmt.Errorf("failed to read stats_hourlies: %w", err)
		}

		// 旧主键 (hour) 不会出现同 hour 两行, 但防御 (date, hour) 撞键:
		// 历史行 date 理论上非空, 极端脏数据多行同键时合并指标而不是丢行或报错。
		merged := make(map[statsHourlyKey]model.StatsHourly, len(rows))
		for _, row := range rows {
			key := statsHourlyKey{date: row.Date, hour: row.Hour}
			existing, ok := merged[key]
			if !ok {
				merged[key] = row
				continue
			}
			existing.StatsMetrics.Add(row.StatsMetrics)
			merged[key] = existing
		}
		hourly := make([]model.StatsHourly, 0, len(merged))
		for _, row := range merged {
			hourly = append(hourly, row)
		}
		// 排序保证回填顺序稳定, 重跑迁移结果一致。
		sort.Slice(hourly, func(i, j int) bool {
			if hourly[i].Date != hourly[j].Date {
				return hourly[i].Date < hourly[j].Date
			}
			return hourly[i].Hour < hourly[j].Hour
		})

		if err := tx.Migrator().DropTable("stats_hourlies"); err != nil {
			return fmt.Errorf("failed to drop old stats_hourlies: %w", err)
		}
		if err := tx.AutoMigrate(&model.StatsHourly{}); err != nil {
			return fmt.Errorf("failed to rebuild stats_hourlies: %w", err)
		}
		if len(hourly) > 0 {
			if err := tx.Create(&hourly).Error; err != nil {
				return fmt.Errorf("failed to restore stats_hourlies rows: %w", err)
			}
		}
		return nil
	})
}

// statsHourlyKey 迁移内并键用的 (date, hour) 键。
type statsHourlyKey struct {
	date string
	hour int
}
