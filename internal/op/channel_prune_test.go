package op

import (
	"context"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// TestChannelModelClear_onlyTargetKey 验证按凭据清空时, 同渠道其他凭据的模型与授权原样保留。
func TestChannelModelClear_onlyTargetKey(t *testing.T) {
	seedReconcileTestDB(t)

	if err := ChannelModelClear(context.Background(), 1, "key-a"); err != nil {
		t.Fatalf("ChannelModelClear() error = %v", err)
	}

	rows := loadGrantRows(t, 1)
	if len(rows) != 1 || rows[0].modelName != "claude-3" || rows[0].keyName != "key-b" {
		t.Fatalf("清理后剩余授权 = %+v, 期望只剩 (claude-3, key-b)", rows)
	}

	// key-a 独占的 gpt-4o 与无授权的 glm-4.6 都应随清理消失, claude-3 因 key-b 仍授权而保留。
	var modelNames []string
	if err := db.GetDB().Model(&model.ChannelModel{}).
		Where("channel_id = ?", 1).Order("name").Pluck("name", &modelNames).Error; err != nil {
		t.Fatalf("load channel models: %v", err)
	}
	if !slices.Equal(modelNames, []string{"claude-3"}) {
		t.Fatalf("清理后渠道模型 = %v, 期望 [claude-3]", modelNames)
	}
}

// TestChannelModelsPruneByFilter 验证按全局过滤表达式清理存量, 且只删命中项。
func TestChannelModelsPruneByFilter(t *testing.T) {
	seedReconcileTestDB(t)

	// 全局过滤预置为 "embed"(见 seedReconcileTestDB), 造一个命中项挂在 key-a 名下。
	embed := model.ChannelModel{ID: 5, ChannelID: 1, Name: "text-embedding-3"}
	if err := db.GetDB().Create(&embed).Error; err != nil {
		t.Fatalf("create embed model: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelGrant{
		ID: 5, ChannelModelID: 5, ChannelKeyID: 1, Protocols: model.ProtocolOpenAIChatCompletion,
	}).Error; err != nil {
		t.Fatalf("create embed grant: %v", err)
	}

	count, err := ChannelModelsPruneByFilter(context.Background())
	if err != nil {
		t.Fatalf("ChannelModelsPruneByFilter() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("清理数量 = %d, 期望 1", count)
	}

	var remaining []string
	if err := db.GetDB().Model(&model.ChannelModel{}).Order("name").Pluck("name", &remaining).Error; err != nil {
		t.Fatalf("load channel models: %v", err)
	}
	if !slices.Equal(remaining, []string{"claude-3", "glm-4.6", "gpt-4o", "misc-1"}) {
		t.Fatalf("清理后剩余模型 = %v, 期望除了 text-embedding-3 全在", remaining)
	}
}

// TestChannelModelsPruneByFilter_emptyFilter 验证过滤表达式为空时拒绝执行, 避免误删全库。
func TestChannelModelsPruneByFilter_emptyFilter(t *testing.T) {
	seedReconcileTestDB(t)

	if err := SettingSetString(model.SettingKeyModelFilter, ""); err != nil {
		t.Fatalf("clear model filter: %v", err)
	}
	if _, err := ChannelModelsPruneByFilter(context.Background()); err == nil {
		t.Fatal("过滤表达式为空时应当返回错误")
	}
}
