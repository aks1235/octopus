package op

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// seedReconcileTestDB 建两个渠道与两个分组, 覆盖单凭据对账的判定面:
// 渠道 1 双凭据(key-a / key-b), 三模型两授权加一个无授权的孤立模型;
// 渠道 2 带渠道级白名单(match_regex ^gpt), 供过滤收缩用例区分两层正则。
// 全局 model_filter 预置为 "embed"(黑名单), 由 InitCache 读进设置缓存。
func seedReconcileTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel-reconcile-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.GetDB().Create(&model.Setting{Key: model.SettingKeyModelFilter, Value: "embed"}).Error; err != nil {
		t.Fatalf("seed model filter setting: %v", err)
	}

	// 渠道 1: key-a 名下 gpt-4o / claude-3 两个授权, key-b 名下 claude-3 一个授权;
	// glm-4.6 是无授权的孤立模型, 对账后应被重导出清除。
	if err := db.GetDB().Create(&model.Channel{
		ID:            1,
		ChannelConfig: model.ChannelConfig{Name: "chan-a", BaseURL: "http://upstream-a"},
	}).Error; err != nil {
		t.Fatalf("create channel 1: %v", err)
	}
	for _, key := range []model.ChannelKey{
		{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-a", Key: "sk-a"}},
		{ID: 2, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-b", Key: "sk-b"}},
	} {
		if err := db.GetDB().Create(&key).Error; err != nil {
			t.Fatalf("create channel key %d: %v", key.ID, err)
		}
	}
	for _, channelModel := range []model.ChannelModel{
		{ID: 1, ChannelID: 1, Name: "gpt-4o", StatsMetrics: model.StatsMetrics{InputToken: 100, RequestSuccess: 3}},
		{ID: 2, ChannelID: 1, Name: "claude-3"},
		{ID: 3, ChannelID: 1, Name: "glm-4.6"},
	} {
		if err := db.GetDB().Create(&channelModel).Error; err != nil {
			t.Fatalf("create channel model %d: %v", channelModel.ID, err)
		}
	}
	for _, grant := range []model.ChannelGrant{
		{ID: 1, ChannelModelID: 1, ChannelKeyID: 1, Protocols: model.ProtocolOpenAIChatCompletion},
		{ID: 2, ChannelModelID: 2, ChannelKeyID: 1, Protocols: model.ProtocolOpenAIChatCompletion},
		{ID: 3, ChannelModelID: 2, ChannelKeyID: 2, Protocols: model.ProtocolOpenAIChatCompletion},
	} {
		if err := db.GetDB().Create(&grant).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", grant.ID, err)
		}
	}

	// 渠道 2: 渠道级白名单 ^gpt, 单凭据 key-c 名下一个 misc-1 授权。
	if err := db.GetDB().Create(&model.Channel{
		ID:            2,
		ChannelConfig: model.ChannelConfig{Name: "chan-b", BaseURL: "http://upstream-b", MatchRegex: "^gpt"},
	}).Error; err != nil {
		t.Fatalf("create channel 2: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelKey{
		ID: 3, ChannelID: 2, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-c", Key: "sk-c"},
	}).Error; err != nil {
		t.Fatalf("create channel key 3: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelModel{ID: 4, ChannelID: 2, Name: "misc-1"}).Error; err != nil {
		t.Fatalf("create channel model 4: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelGrant{
		ID: 4, ChannelModelID: 4, ChannelKeyID: 3, Protocols: model.ProtocolAnthropicMessage,
	}).Error; err != nil {
		t.Fatalf("create channel grant 4: %v", err)
	}

	// 正则分组 auto 匹配 ^gpt(吸纳/移除随对账重算); 手动分组 mini 命中 gpt-4o-mini(验证吸纳兜底)。
	groups := []model.Group{
		{ID: 1, Name: "auto", Mode: model.GroupModeManual, MemberRegex: "^gpt",
			RelayConfig: model.DefaultGroupRelayConfig()},
		{ID: 2, Name: "mini", Mode: model.GroupModeManual, RelayConfig: model.DefaultGroupRelayConfig()},
	}
	for i := range groups {
		if err := db.GetDB().Create(&groups[i]).Error; err != nil {
			t.Fatalf("create group %d: %v", groups[i].ID, err)
		}
	}
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
}

// grantRow 是断言用的一行授权: 业务键(模型名, 凭据名) + 主键 + 协议位。
type grantRow struct {
	modelName string
	keyName   string
	id        int
	protocols model.Protocol
}

// loadGrantRows 读回指定渠道的全部授权行, 按 (模型名, 凭据名) 定序, 供集合比对。
func loadGrantRows(t *testing.T, channelID int) []grantRow {
	t.Helper()
	var joined []struct {
		ID        int
		Protocols model.Protocol
		ModelName string
		KeyName   string
	}
	if err := db.GetDB().Table("channel_grants").
		Joins("JOIN channel_models ON channel_models.id = channel_grants.channel_model_id").
		Joins("JOIN channel_keys ON channel_keys.id = channel_grants.channel_key_id").
		Where("channel_models.channel_id = ?", channelID).
		Select("channel_grants.id AS id, channel_grants.protocols AS protocols, channel_models.name AS model_name, channel_keys.name AS key_name").
		Scan(&joined).Error; err != nil {
		t.Fatalf("load grants of channel %d: %v", channelID, err)
	}
	rows := make([]grantRow, 0, len(joined))
	for _, grant := range joined {
		rows = append(rows, grantRow{
			modelName: grant.ModelName, keyName: grant.KeyName,
			id: grant.ID, protocols: grant.Protocols,
		})
	}
	slices.SortFunc(rows, func(a, b grantRow) int {
		if a.modelName != b.modelName {
			if a.modelName < b.modelName {
				return -1
			}
			return 1
		}
		if a.keyName == b.keyName {
			return 0
		}
		if a.keyName < b.keyName {
			return -1
		}
		return 1
	})
	return rows
}

// loadModelNames 读回指定渠道的模型名集合(有序), 供集合比对。
func loadModelNames(t *testing.T, channelID int) []string {
	t.Helper()
	var names []string
	if err := db.GetDB().Model(&model.ChannelModel{}).
		Where("channel_id = ?", channelID).Order("name").Pluck("name", &names).Error; err != nil {
		t.Fatalf("load models of channel %d: %v", channelID, err)
	}
	return names
}

// loadGroupGrantIDs 读回指定分组的成员引用的授权主键(有序)。
func loadGroupGrantIDs(t *testing.T, groupID int) []int {
	t.Helper()
	var grantIDs []int
	if err := db.GetDB().Model(&model.GroupItem{}).
		Where("group_id = ?", groupID).Order("channel_grant_id").Pluck("channel_grant_id", &grantIDs).Error; err != nil {
		t.Fatalf("load items of group %d: %v", groupID, err)
	}
	return grantIDs
}

// TestChannelModelReconcile_replacesKeyGrantsAndCascades 验证替换语义的全部面向:
// 本凭据授权按探测结果重建(保留行主键, 协议位更新, 新模型新授权加入, 上游没有了的删除),
// 其他凭据的授权原样不动, 无授权的孤立模型被清除, 正则分组与手动分组的成员随之重算。
func TestChannelModelReconcile_replacesKeyGrantsAndCascades(t *testing.T) {
	seedReconcileTestDB(t)

	fetched := []model.ChannelFetchModel{
		{Name: "gpt-4o", Protocols: model.ProtocolOpenAIResponse},
		{Name: "gpt-4o-mini", Protocols: model.ProtocolAnthropicMessage},
		{Name: "claude-3", Protocols: model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage},
	}
	if err := ChannelModelReconcile(context.Background(), 1, "key-a", fetched); err != nil {
		t.Fatalf("ChannelModelReconcile() error = %v", err)
	}

	if got, want := loadModelNames(t, 1), []string{"claude-3", "gpt-4o", "gpt-4o-mini"}; !slices.Equal(got, want) {
		t.Fatalf("channel 1 models = %v, want %v (glm-4.6 orphan cleaned)", got, want)
	}
	// gpt-4o 保留模型主键与统计: 模型行没被删除重建。
	var gptRow model.ChannelModel
	if err := db.GetDB().First(&gptRow, 1).Error; err != nil {
		t.Fatalf("load model 1: %v", err)
	}
	if gptRow.InputToken != 100 || gptRow.RequestSuccess != 3 {
		t.Fatalf("model 1 stats = %+v, want preserved", gptRow.StatsMetrics)
	}

	rows := loadGrantRows(t, 1)
	want := []grantRow{
		{modelName: "claude-3", keyName: "key-a", id: 2, protocols: model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage},
		{modelName: "claude-3", keyName: "key-b", id: 3, protocols: model.ProtocolOpenAIChatCompletion},
		{modelName: "gpt-4o", keyName: "key-a", id: 1, protocols: model.ProtocolOpenAIResponse},
		{modelName: "gpt-4o-mini", keyName: "key-a", id: 5, protocols: model.ProtocolAnthropicMessage},
	}
	if len(rows) != len(want) {
		t.Fatalf("channel 1 grants = %+v, want %+v", rows, want)
	}
	for i, row := range rows {
		if row != want[i] {
			t.Fatalf("channel 1 grants = %+v, want %+v", rows, want)
		}
	}

	// 正则分组 ^gpt 重算后只含 gpt 系模型的授权, 被淘汰与新增的成员都到位。
	if got, want := loadGroupGrantIDs(t, 1), []int{1, 5}; !slices.Equal(got, want) {
		t.Fatalf("regex group items = %v, want %v", got, want)
	}
	// 手动分组 mini 按名吸纳新模型 gpt-4o-mini 的授权。
	if got, want := loadGroupGrantIDs(t, 2), []int{5}; !slices.Equal(got, want) {
		t.Fatalf("manual group items = %v, want %v", got, want)
	}

	// 缓存与库一致: 候选授权同集合可见, 孤立模型不再出现在渠道详情。
	detail, err := ChannelDetailGet(1)
	if err != nil {
		t.Fatalf("ChannelDetailGet(1) error = %v", err)
	}
	if got, want := detail.Models, []string{"claude-3", "gpt-4o", "gpt-4o-mini"}; !slices.Equal(got, want) {
		t.Fatalf("cached detail models = %v, want %v", got, want)
	}
}

// TestChannelModelReconcile_noChangeShortCircuits 验证无实际变更时的短路:
// 两次对账后库内行(主键, 协议位, 模型集合, 分组成员)逐行不变, 第二次调用不产生任何写副作用。
func TestChannelModelReconcile_noChangeShortCircuits(t *testing.T) {
	seedReconcileTestDB(t)

	fetched := []model.ChannelFetchModel{
		{Name: "gpt-4o", Protocols: model.ProtocolOpenAIChatCompletion},
		{Name: "claude-3", Protocols: model.ProtocolOpenAIChatCompletion},
	}
	if err := ChannelModelReconcile(context.Background(), 1, "key-a", fetched); err != nil {
		t.Fatalf("first ChannelModelReconcile() error = %v", err)
	}
	modelsAfterFirst := loadModelNames(t, 1)
	grantsAfterFirst := loadGrantRows(t, 1)
	groupAfterFirst := loadGroupGrantIDs(t, 1)

	if err := ChannelModelReconcile(context.Background(), 1, "key-a", fetched); err != nil {
		t.Fatalf("second ChannelModelReconcile() error = %v", err)
	}
	if got := loadModelNames(t, 1); !slices.Equal(got, modelsAfterFirst) {
		t.Fatalf("models changed on no-op reconcile: %v -> %v", modelsAfterFirst, got)
	}
	if got := loadGrantRows(t, 1); len(got) != len(grantsAfterFirst) {
		t.Fatalf("grants changed on no-op reconcile: %+v -> %+v", grantsAfterFirst, got)
	} else {
		for i := range got {
			if got[i] != grantsAfterFirst[i] {
				t.Fatalf("grants changed on no-op reconcile: %+v -> %+v", grantsAfterFirst, got)
			}
		}
	}
	if got := loadGroupGrantIDs(t, 1); !slices.Equal(got, groupAfterFirst) {
		t.Fatalf("group items changed on no-op reconcile: %v -> %v", groupAfterFirst, got)
	}
}

// TestChannelModelReconcile_emptyFetchedRejected 验证空清单防御: 直接拒绝且库内不动。
func TestChannelModelReconcile_emptyFetchedRejected(t *testing.T) {
	seedReconcileTestDB(t)

	if err := ChannelModelReconcile(context.Background(), 1, "key-a", nil); err == nil {
		t.Fatalf("expected error for empty fetched list")
	}
	if got, want := loadModelNames(t, 1), []string{"claude-3", "glm-4.6", "gpt-4o"}; !slices.Equal(got, want) {
		t.Fatalf("channel 1 models = %v, want unchanged %v", got, want)
	}
	if got := len(loadGrantRows(t, 1)); got != 3 {
		t.Fatalf("channel 1 grants = %d rows, want unchanged 3", got)
	}
}

// TestChannelModelReconcile_appliesFilterShrink 验证两层过滤收缩与探测拉取同口径:
// 全局黑名单拦下 embed 系模型(渠道 1), 渠道级白名单只保留命中模型(渠道 2)。
func TestChannelModelReconcile_appliesFilterShrink(t *testing.T) {
	seedReconcileTestDB(t)

	if err := ChannelModelReconcile(context.Background(), 1, "key-a", []model.ChannelFetchModel{
		{Name: "gpt-4o", Protocols: model.ProtocolOpenAIResponse},
		{Name: "embed-3", Protocols: model.ProtocolOpenAIResponse},
	}); err != nil {
		t.Fatalf("ChannelModelReconcile() channel 1 error = %v", err)
	}
	if got, want := loadModelNames(t, 1), []string{"claude-3", "gpt-4o"}; !slices.Equal(got, want) {
		t.Fatalf("channel 1 models = %v, want %v (embed filtered)", got, want)
	}

	if err := ChannelModelReconcile(context.Background(), 2, "key-c", []model.ChannelFetchModel{
		{Name: "gpt-x", Protocols: model.ProtocolAnthropicMessage},
		{Name: "claude-y", Protocols: model.ProtocolAnthropicMessage},
	}); err != nil {
		t.Fatalf("ChannelModelReconcile() channel 2 error = %v", err)
	}
	if got, want := loadModelNames(t, 2), []string{"gpt-x"}; !slices.Equal(got, want) {
		t.Fatalf("channel 2 models = %v, want %v (whitelist kept gpt only)", got, want)
	}
	rows := loadGrantRows(t, 2)
	if len(rows) != 1 || rows[0].modelName != "gpt-x" || rows[0].keyName != "key-c" ||
		rows[0].protocols != model.ProtocolAnthropicMessage {
		t.Fatalf("channel 2 grants = %+v, want single gpt-x grant under key-c", rows)
	}
}

// TestChannelModelReconcile_unknownTargetRejected 验证探测与落库之间目标消失的防御:
// 渠道或凭据名找不到时返回错误, 不落任何对账结论。
func TestChannelModelReconcile_unknownTargetRejected(t *testing.T) {
	seedReconcileTestDB(t)

	fetched := []model.ChannelFetchModel{{Name: "gpt-4o", Protocols: model.ProtocolOpenAIResponse}}
	if err := ChannelModelReconcile(context.Background(), 99, "key-a", fetched); err == nil {
		t.Fatalf("expected error for missing channel")
	}
	if err := ChannelModelReconcile(context.Background(), 1, "ghost", fetched); err == nil {
		t.Fatalf("expected error for missing key")
	}
	if got := len(loadGrantRows(t, 1)); got != 3 {
		t.Fatalf("channel 1 grants = %d rows, want unchanged 3", got)
	}
}
