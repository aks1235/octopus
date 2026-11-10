package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// TestPickGroupItem_manualIgnoresProbeBlocking 手动模式必须无视探测熔断。
//
// 熔断省的是"先撞一次再换下一个"的浪费, 而手动模式只有人工钉住的那一个成员, 没有"下一个"可换:
// 跳过它只会让请求在等待循环里挂到客户端超时, 把"某个成员坏了"升级成"整个分组不可用"。
// 反过来, 探测打的是 1+1, 结论可能是假阴性, 人工钉住就该按人工的意思试一次。
func TestPickGroupItem_manualIgnoresProbeBlocking(t *testing.T) {
	seedProbeRoutingDB(t)

	var item model.GroupItem
	if err := db.GetDB().Where("channel_grant_id = ?", 1).First(&item).Error; err != nil {
		t.Fatalf("load group item: %v", err)
	}
	// 授权 1 在种子数据里是"刚判死", 手动模式钉住它。
	if err := db.GetDB().Model(&model.Group{}).Where("id = ?", 1).Updates(map[string]any{
		"mode":           model.GroupModeManual,
		"active_item_id": item.ID,
	}).Error; err != nil {
		t.Fatalf("switch group to manual: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}

	group := groupForTest(t, "probe-g")
	picked := pickGroupItem(group, &routeWalk{})
	if picked.ID != item.ID {
		t.Fatalf("picked item = %d, want %d (手动模式钉住的判死成员仍要尝试, 否则请求挂到超时)", picked.ID, item.ID)
	}
}
