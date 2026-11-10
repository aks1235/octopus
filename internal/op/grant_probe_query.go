package op

import (
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// GrantProbeGroupSummary 返回分组内各成员在窗口内的探测汇总, 按失败次数倒序(问题成员排前面)。
// 只统计该分组当前持有的成员; 成员被移出分组后, 它的历史仍留在表里, 但不在此视图出现。
func GrantProbeGroupSummary(groupID int, days int) ([]model.GrantProbeMemberSummary, error) {
	engine := db.GetDB()
	since := time.Now().AddDate(0, 0, -days).Unix()
	if days <= 0 {
		since = 0
	}

	var rows []model.GrantProbeMemberSummary
	if err := engine.Table("grant_probe_records AS r").
		Select(`r.grant_id AS grant_id,
			ch.name AS channel_name,
			cm.name AS model_name,
			ck.name AS key_name,
			SUM(CASE WHEN r.success THEN 1 ELSE 0 END) AS ok,
			SUM(CASE WHEN r.success THEN 0 ELSE 1 END) AS failed,
			MAX(r.at) AS last_at`).
		Joins("JOIN group_items gi ON gi.channel_grant_id = r.grant_id AND gi.group_id = ?", groupID).
		Joins("JOIN channel_grants g ON g.id = r.grant_id").
		Joins("JOIN channel_models cm ON cm.id = g.channel_model_id").
		Joins("JOIN channel_keys ck ON ck.id = g.channel_key_id").
		Joins("JOIN channels ch ON ch.id = cm.channel_id").
		Where("r.at >= ?", since).
		Group("r.grant_id").
		Order("failed DESC, last_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return rows, nil
	}

	// 最近一次失败原因单独取: 聚合里拿不到"最近那条的 reason", 而它恰恰是界面上最有用的信息。
	grantIDs := make([]int, 0, len(rows))
	for _, row := range rows {
		if row.Failed > 0 {
			grantIDs = append(grantIDs, row.GrantID)
		}
	}
	if len(grantIDs) > 0 {
		var failures []model.GrantProbeRecord
		if err := engine.Select("grant_id, reason, at").
			Where("success = ? AND at >= ? AND grant_id IN ?", false, since, grantIDs).
			Order("at DESC").
			Find(&failures).Error; err != nil {
			return nil, err
		}
		latest := make(map[int]string, len(grantIDs))
		for _, failure := range failures {
			if _, ok := latest[failure.GrantID]; !ok {
				latest[failure.GrantID] = failure.Reason
			}
		}
		for index := range rows {
			rows[index].LastError = latest[rows[index].GrantID]
		}
	}
	return rows, nil
}
