package op

import (
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm/clause"
)

// GrantOutcomeLoad 读取全部凭据的排序窗口快照, 供进程启动时回填内存。
// 凭据数量级很小(几百行), 一次全量查询即可, 不做分页。
func GrantOutcomeLoad() ([]model.GrantOutcome, error) {
	var rows []model.GrantOutcome
	if err := db.GetDB().Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GrantOutcomeSave 批量 upsert 排序窗口快照。
// 用主键冲突更新而非先删后插: 先删会让并发读到的窗口短暂为空, 且多一次写事务。
func GrantOutcomeSave(rows []model.GrantOutcome) error {
	if len(rows) == 0 {
		return nil
	}
	return db.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "grant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"bits", "count", "updated_at"}),
	}).CreateInBatches(rows, 100).Error
}
