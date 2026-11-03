package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 备份导入的身份口径是天然键而非主键: 两个实例的主键空间各自独立,
// 按主键合并会把 A 的凭据/模型写到 B 的不相关渠道上, 或直接撞唯一键报错。
// 下面每个用例都在「目标实例已有自己的配置」的前提下验证重映射结果。

func initImportTestDB(t *testing.T, name string) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), name), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
}

// dumpA 模拟「实例 A」的导出: 主键从 1 起, 与目标实例的主键空间完全无关。
func dumpA() *model.DBDump {
	return &model.DBDump{
		Version: 5,
		Channels: []model.Channel{{
			ID: 1,
			ChannelConfig: model.ChannelConfig{
				Name:    "腾讯云",
				Dialect: model.DialectGeneric,
				BaseURL: "https://tokenhub.example",
			},
			StatsMetrics: model.StatsMetrics{InputToken: 100, RequestSuccess: 7},
		}},
		ChannelKeys: []model.ChannelKey{{
			ID: 1, ChannelID: 1,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: "测试", Key: "sk-from-A", Enabled: true},
		}},
		ChannelModels: []model.ChannelModel{{ID: 1, ChannelID: 1, Name: "hy3"}},
		ChannelGrants: []model.ChannelGrant{{ID: 1, ChannelModelID: 1, ChannelKeyID: 1, Protocols: 14}},
		Groups:        []model.Group{{ID: 1, Name: "tecent_search", Mode: model.GroupModeManual}},
		GroupItems:    []model.GroupItem{{ID: 1, GroupID: 1, ChannelGrantID: 1, Priority: 1}},
	}
}

func mustImport(t *testing.T, dump *model.DBDump, policy ImportConflictPolicy) *model.DBImportResult {
	t.Helper()
	res, err := DBImportIncremental(context.Background(), dump, policy)
	if err != nil {
		t.Fatalf("import with policy %s: %v", policy, err)
	}
	return res
}

func mustChannel(t *testing.T, name string) model.Channel {
	t.Helper()
	var ch model.Channel
	if err := db.GetDB().Where("name = ?", name).First(&ch).Error; err != nil {
		t.Fatalf("channel %q not found: %v", name, err)
	}
	return ch
}

func keysOf(t *testing.T, channelID int) []model.ChannelKey {
	t.Helper()
	var rows []model.ChannelKey
	if err := db.GetDB().Where("channel_id = ?", channelID).Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("list keys: %v", err)
	}
	return rows
}

func modelsOf(t *testing.T, channelID int) []model.ChannelModel {
	t.Helper()
	var rows []model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channelID).Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("list models: %v", err)
	}
	return rows
}

// 主键相同、名字不同: 这是两个实例各自演进后的典型状态, 过去会静默把目标的凭据/模型覆盖掉。
func TestImportRemapsByNaturalKeyKeepsTargetObjects(t *testing.T) {
	initImportTestDB(t, "import-remap.db")
	g := db.GetDB()
	g.Create(&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "WONG", Dialect: model.DialectGeneric, BaseURL: "https://wong.example"}})
	g.Create(&model.ChannelKey{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "wong-key", Key: "sk-B-own"}})
	g.Create(&model.ChannelModel{ID: 1, ChannelID: 1, Name: "deepseek-v4.1-flash"})

	mustImport(t, dumpA(), ImportConflictSkip)

	// 目标实例自己的渠道与子行必须原封不动。
	wong := mustChannel(t, "WONG")
	if wong.ID != 1 {
		t.Fatalf("WONG id = %d, want 1", wong.ID)
	}
	own := keysOf(t, wong.ID)
	if len(own) != 1 || own[0].Name != "wong-key" || own[0].Key != "sk-B-own" {
		t.Fatalf("target key was modified: %+v", own)
	}
	if ms := modelsOf(t, wong.ID); len(ms) != 1 || ms[0].Name != "deepseek-v4.1-flash" {
		t.Fatalf("target model was modified: %+v", ms)
	}

	// 备份里的渠道另建一行, 凭据/模型挂在它自己下面。
	tc := mustChannel(t, "腾讯云")
	if tc.ID == wong.ID {
		t.Fatalf("imported channel reused target id %d", tc.ID)
	}
	if ks := keysOf(t, tc.ID); len(ks) != 1 || ks[0].Name != "测试" || ks[0].Key != "sk-from-A" {
		t.Fatalf("imported keys = %+v", ks)
	}
	if ms := modelsOf(t, tc.ID); len(ms) != 1 || ms[0].Name != "hy3" {
		t.Fatalf("imported models = %+v", ms)
	}

	// 授权与分组成员必须指向新主键, 不能残留备份里的旧 id。
	var grant model.ChannelGrant
	if err := g.Where("channel_model_id = ? AND channel_key_id = ?", ms(t, tc.ID), ks(t, tc.ID)).First(&grant).Error; err != nil {
		t.Fatalf("grant not remapped: %v", err)
	}
	var item model.GroupItem
	if err := g.Where("channel_grant_id = ?", grant.ID).First(&item).Error; err != nil {
		t.Fatalf("group item not remapped: %v", err)
	}
}

func ms(t *testing.T, channelID int) int {
	t.Helper()
	rows := modelsOf(t, channelID)
	if len(rows) == 0 {
		t.Fatalf("channel %d has no model", channelID)
	}
	return rows[0].ID
}

func ks(t *testing.T, channelID int) int {
	t.Helper()
	rows := keysOf(t, channelID)
	if len(rows) == 0 {
		t.Fatalf("channel %d has no key", channelID)
	}
	return rows[0].ID
}

// 渠道同名、主键不同: 过去会因渠道被跳过而子行外键悬空, 整笔导入失败。
func TestImportSameNameDifferentIDDoesNotFail(t *testing.T) {
	initImportTestDB(t, "import-same-name.db")
	db.GetDB().Create(&model.Channel{ID: 7, ChannelConfig: model.ChannelConfig{Name: "腾讯云", Dialect: model.DialectGeneric, BaseURL: "https://tokenhub.example"}})

	res := mustImport(t, dumpA(), ImportConflictSkip)

	tc := mustChannel(t, "腾讯云")
	if tc.ID != 7 {
		t.Fatalf("channel id = %d, want existing 7", tc.ID)
	}
	var channels int64
	db.GetDB().Model(&model.Channel{}).Count(&channels)
	if channels != 1 {
		t.Fatalf("channel count = %d, want 1 (同名对象应复用)", channels)
	}
	// 目标渠道下原本没有凭据, 备份里的凭据应挂到这个渠道上。
	if ks := keysOf(t, 7); len(ks) != 1 || ks[0].Name != "测试" {
		t.Fatalf("keys = %+v", ks)
	}
	if res.Skipped["channels"] != 1 {
		t.Fatalf("skipped channels = %d, want 1", res.Skipped["channels"])
	}
}

// 同渠道同名凭据、凭据主键不同: 过去会撞 channel_keys 的唯一键直接报错。
func TestImportKeyNameConflictRespectsPolicy(t *testing.T) {
	initImportTestDB(t, "import-key-conflict.db")
	g := db.GetDB()
	g.Create(&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "腾讯云", Dialect: model.DialectGeneric, BaseURL: "https://tokenhub.example"}})
	g.Create(&model.ChannelKey{ID: 99, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "测试", Key: "sk-B-own", Enabled: true}})

	mustImport(t, dumpA(), ImportConflictSkip)
	afterSkip := keysOf(t, 1)
	if len(afterSkip) != 1 || afterSkip[0].Key != "sk-B-own" {
		t.Fatalf("skip policy modified target key: %+v", afterSkip)
	}

	mustImport(t, dumpA(), ImportConflictOverwrite)
	afterOverwrite := keysOf(t, 1)
	if len(afterOverwrite) != 1 || afterOverwrite[0].Key != "sk-from-A" {
		t.Fatalf("overwrite policy did not replace key: %+v", afterOverwrite)
	}
}

// rename 策略下渠道本身会被另建一份, 其凭据与模型跟随新渠道, 原渠道不受影响。
func TestImportRenameDoesNotTouchOriginalKeys(t *testing.T) {
	initImportTestDB(t, "import-rename-keys.db")
	g := db.GetDB()
	g.Create(&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "腾讯云", Dialect: model.DialectGeneric, BaseURL: "https://tokenhub.example"}})
	g.Create(&model.ChannelKey{ID: 99, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "测试", Key: "sk-B-own", Enabled: true}})

	mustImport(t, dumpA(), ImportConflictRename)

	original := keysOf(t, 1)
	if len(original) != 1 || original[0].Key != "sk-B-own" {
		t.Fatalf("original channel key was modified: %+v", original)
	}
	renamed := mustChannel(t, "腾讯云-2")
	dup := keysOf(t, renamed.ID)
	if len(dup) != 1 || dup[0].Key != "sk-from-A" {
		t.Fatalf("renamed channel keys = %+v", dup)
	}
}

// rename 策略为同名渠道另建一行, 原渠道与新建渠道的凭据各自独立。
func TestImportRenameKeepsBothChannels(t *testing.T) {
	initImportTestDB(t, "import-rename.db")
	g := db.GetDB()
	g.Create(&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "腾讯云", Dialect: model.DialectGeneric, BaseURL: "https://tokenhub.example"}})
	g.Create(&model.ChannelKey{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "本地", Key: "sk-local"}})

	mustImport(t, dumpA(), ImportConflictRename)

	var channels []model.Channel
	g.Order("id").Find(&channels)
	if len(channels) != 2 {
		t.Fatalf("channel count = %d, want 2: %+v", len(channels), channels)
	}
	renamed := mustChannel(t, "腾讯云-2")
	if ks := keysOf(t, renamed.ID); len(ks) != 1 || ks[0].Key != "sk-from-A" {
		t.Fatalf("renamed channel keys = %+v", ks)
	}
	original := mustChannel(t, "腾讯云")
	if ks := keysOf(t, original.ID); len(ks) != 1 || ks[0].Key != "sk-local" {
		t.Fatalf("original channel keys were touched: %+v", ks)
	}
}

// manual 分组的当前成员(active_item_id)指向旧成员主键, 导入后必须重映射到新成员。
func TestImportRemapsActiveItem(t *testing.T) {
	initImportTestDB(t, "import-active-item.db")
	db.GetDB().Create(&model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{Name: "腾讯云", Dialect: model.DialectGeneric, BaseURL: "https://tokenhub.example"}})

	dump := dumpA()
	dump.Groups[0].ActiveItemID = 1
	mustImport(t, dump, ImportConflictSkip)

	var group model.Group
	if err := db.GetDB().Where("name = ?", "tecent_search").First(&group).Error; err != nil {
		t.Fatalf("group not imported: %v", err)
	}
	var item model.GroupItem
	if err := db.GetDB().Where("group_id = ?", group.ID).First(&item).Error; err != nil {
		t.Fatalf("group item not imported: %v", err)
	}
	if group.ActiveItemID != item.ID {
		t.Fatalf("active_item_id = %d, want remapped item id %d", group.ActiveItemID, item.ID)
	}
}

// 空实例导入与旧行为等价: 全量写入。
func TestImportIntoEmptyInstance(t *testing.T) {
	initImportTestDB(t, "import-empty.db")
	res := mustImport(t, dumpA(), ImportConflictSkip)
	for _, table := range []string{"channels", "channel_keys", "channel_models", "channel_grants", "groups", "group_items"} {
		if res.RowsAffected[table] == 0 {
			t.Fatalf("table %s imported 0 rows: %+v", table, res.RowsAffected)
		}
	}
	if res.Conflict != string(ImportConflictSkip) {
		t.Fatalf("conflict = %q", res.Conflict)
	}
}

func TestParseImportConflictPolicy(t *testing.T) {
	cases := map[string]ImportConflictPolicy{
		"":            ImportConflictSkip,
		"skip":        ImportConflictSkip,
		" OVERWRITE ": ImportConflictOverwrite,
		"rename":      ImportConflictRename,
	}
	for raw, want := range cases {
		got, err := ParseImportConflictPolicy(raw)
		if err != nil || got != want {
			t.Fatalf("parse(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := ParseImportConflictPolicy("merge"); err == nil {
		t.Fatal("expected error for unknown policy")
	}
}
