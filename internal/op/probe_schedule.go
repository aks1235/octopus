package op

import (
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// 成员级探测排期: group_items.probe_next_at 的下次到期时刻。
//
// 排期状态在调度器内存里也有一份(探测是每个请求都要查的热路径, 不能每次读库),
// 库里的列是重启后的来源: 进程重启不会把所有成员重新打成一波。

// ProbeScheduleLoad 读出全部已排期的分组项: 分组项主键 -> 下次探测时刻(unix 秒)。
func ProbeScheduleLoad() (map[int]int64, error) {
	var rows []struct {
		ID          int
		ProbeNextAt int64
	}
	if err := db.GetDB().Model(&model.GroupItem{}).
		Select("id, probe_next_at").
		Where("probe_next_at > 0").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	schedule := make(map[int]int64, len(rows))
	for _, row := range rows {
		schedule[row.ID] = row.ProbeNextAt
	}
	return schedule, nil
}

// ProbeScheduleBackfill 给尚未排期的分组项铺开一个初始到期时刻。
// 按主键取模铺到 spread 秒的跨度里: 顺序稳定(重启后同一成员还是同一时刻)、分布均匀,
// 不会出现"进程一起来, 二百多个成员同时到期"。
func ProbeScheduleBackfill(now int64, spread int64) error {
	if spread <= 0 {
		return nil
	}
	return db.GetDB().Model(&model.GroupItem{}).
		Where("probe_next_at = 0").
		Update("probe_next_at", gorm.Expr("? + (id % ?)", now, spread)).Error
}

// GroupItemSetProbeNextAt 写入单个分组项的下次探测时刻。
func GroupItemSetProbeNextAt(itemID int, next int64) error {
	return db.GetDB().Model(&model.GroupItem{}).
		Where("id = ?", itemID).
		Update("probe_next_at", next).Error
}
