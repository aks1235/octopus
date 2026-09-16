package op

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// seedOrderTestDB 建两个渠道与一个正则分组, 覆盖人工渠道顺序合成的全部判定面:
// 渠道 1(chan-a/key-a)带 glm-4.6-flash 与 glm-4.6-pro, 渠道 2(chan-b/key-b)带 glm-4.6-flash 与 glm-4.6-air,
// 每个渠道再配第二把凭据给其中一个模型, 验证渠道内 (模型, 凭据) 自然序不受块间重排影响。
// 分组 rgx 的正则 ^glm- 命中全部四个模型; 分组 manual 是手动分组, 锁定顺序端点对其不可用。
func seedOrderTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "regex-order-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	type seedModel struct {
		id     int    // 渠道模型主键。
		name   string // 上游模型名称。
		grants []struct {
			id    int // 授权主键。
			keyID int // 凭据主键。
		}
	}
	for _, ch := range []struct {
		id     int
		name   string
		keys   []int
		key    string
		models []seedModel
	}{
		{id: 1, name: "chan-a", keys: []int{1, 2}, key: "key-a", models: []seedModel{
			{id: 1, name: "glm-4.6-flash", grants: []struct {
				id    int
				keyID int
			}{{id: 1, keyID: 1}, {id: 2, keyID: 2}}},
			{id: 2, name: "glm-4.6-pro", grants: []struct {
				id    int
				keyID int
			}{{id: 3, keyID: 1}}},
		}},
		{id: 2, name: "chan-b", keys: []int{3}, key: "key-b", models: []seedModel{
			{id: 3, name: "glm-4.6-flash", grants: []struct {
				id    int
				keyID int
			}{{id: 4, keyID: 3}}},
			{id: 4, name: "glm-4.6-air", grants: []struct {
				id    int
				keyID int
			}{{id: 5, keyID: 3}}},
		}},
	} {
		if err := db.GetDB().Create(&model.Channel{
			ID:            ch.id,
			ChannelConfig: model.ChannelConfig{Name: ch.name, BaseURL: "http://upstream"},
		}).Error; err != nil {
			t.Fatalf("create channel %d: %v", ch.id, err)
		}
		for i, keyID := range ch.keys {
			name := ch.key
			if i > 0 {
				name = ch.key + "-alt"
			}
			if err := db.GetDB().Create(&model.ChannelKey{
				ID: keyID, ChannelID: ch.id,
				ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Key: "sk-" + name},
			}).Error; err != nil {
				t.Fatalf("create channel key %d: %v", keyID, err)
			}
		}
		for _, m := range ch.models {
			if err := db.GetDB().Create(&model.ChannelModel{ID: m.id, ChannelID: ch.id, Name: m.name}).Error; err != nil {
				t.Fatalf("create channel model %d: %v", m.id, err)
			}
			for _, g := range m.grants {
				if err := db.GetDB().Create(&model.ChannelGrant{
					ID: g.id, ChannelModelID: m.id, ChannelKeyID: g.keyID,
					Protocols: model.ProtocolOpenAIChatCompletion,
				}).Error; err != nil {
					t.Fatalf("create channel grant %d: %v", g.id, err)
				}
			}
		}
	}

	groups := []model.Group{
		{ID: 1, Name: "rgx", Mode: model.GroupModeFailover, MemberRegex: "^glm-",
			RelayConfig: model.DefaultGroupRelayConfig()},
		{ID: 2, Name: "manual", Mode: model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig()},
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

// orderRow 是断言用的一行顺序记录: 渠道 + 人工序号。
type orderRow struct {
	channelID int
	position  int
}

// loadOrderRows 按 position 升序读回指定分组的顺序行。
func loadOrderRows(t *testing.T, groupID int) []orderRow {
	t.Helper()
	var rows []model.GroupChannelOrder
	if err := db.GetDB().Where("group_id = ?", groupID).Order("position").Find(&rows).Error; err != nil {
		t.Fatalf("load group %d channel order: %v", groupID, err)
	}
	result := make([]orderRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, orderRow{channelID: row.ChannelID, position: row.Position})
	}
	return result
}

// loadOrderItemRows 按优先级读回指定分组的成员行, 携带授权所属渠道与模型名供顺序断言。
func loadOrderItemRows(t *testing.T, groupID int) []struct {
	grantID   int
	priority  int
	channelID int
	modelName string
} {
	t.Helper()
	var items []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", groupID).Order("priority").Find(&items).Error; err != nil {
		t.Fatalf("load group %d items: %v", groupID, err)
	}
	result := make([]struct {
		grantID   int
		priority  int
		channelID int
		modelName string
	}, 0, len(items))
	for _, item := range items {
		var grant model.ChannelGrant
		if err := db.GetDB().First(&grant, item.ChannelGrantID).Error; err != nil {
			t.Fatalf("load grant %d: %v", item.ChannelGrantID, err)
		}
		var channelModel model.ChannelModel
		if err := db.GetDB().First(&channelModel, grant.ChannelModelID).Error; err != nil {
			t.Fatalf("load channel model %d: %v", grant.ChannelModelID, err)
		}
		result = append(result, struct {
			grantID   int
			priority  int
			channelID int
			modelName string
		}{grantID: item.ChannelGrantID, priority: item.Priority, channelID: channelModel.ChannelID, modelName: channelModel.Name})
	}
	return result
}

// naturalOrderGrants 是未设人工顺序时 rgx 组的自然序成员(渠道, 模型, 凭据)授权主键。
// 注意渠道 2 内 glm-4.6-air 字典序小于 glm-4.6-flash, 渠道块内 air 在前。
var naturalOrderGrants = []int{1, 2, 3, 5, 4}

// TestGroupChannelOrderApply_composesManualOrder 锁定合成顺序:
// 设 [渠道2, 渠道1] 后渠道 2 的成员整体移到前段, 渠道内 (模型, 凭据) 自然序原样保持;
// 响应与库内一致, 顺序行按提交顺序落库。
func TestGroupChannelOrderApply_composesManualOrder(t *testing.T) {
	seedOrderTestDB(t)

	group, err := GroupChannelOrderApply(1, []int{2, 1}, context.Background())
	if err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	// 响应成员按合成顺序: 渠道 2 的 (air, flash) 在前, 渠道 1 的 (flash/key-a, flash/key-a-alt, pro) 在后,
	// 渠道内部保持 (模型名, 凭据名) 自然序 —— air 字典序小于 flash, 渠道 2 块内 air 在前。
	wantGrants := []int{5, 4, 1, 2, 3}
	got := make([]int, 0, len(group.Items))
	for _, item := range group.Items {
		got = append(got, item.ChannelGrantID)
	}
	if !slices.Equal(got, wantGrants) {
		t.Fatalf("applied items grants = %v, want %v", got, wantGrants)
	}
	for i, item := range group.Items {
		if item.Priority != i+1 {
			t.Fatalf("item grant %d priority = %d, want %d", item.ChannelGrantID, item.Priority, i+1)
		}
	}
	// 库内与缓存一致。
	rows := loadOrderItemRows(t, 1)
	if len(rows) != len(wantGrants) {
		t.Fatalf("group rgx rows = %+v, want %d members", rows, len(wantGrants))
	}
	for i, row := range rows {
		if row.grantID != wantGrants[i] || row.priority != i+1 {
			t.Fatalf("group rgx rows[%d] = %+v, want grant %d at priority %d", i, row, wantGrants[i], i+1)
		}
	}
	// 顺序行按提交顺序落库: 渠道 2 在 position 1。
	if rows := loadOrderRows(t, 1); !slices.Equal(rows, []orderRow{{channelID: 2, position: 1}, {channelID: 1, position: 2}}) {
		t.Fatalf("group rgx order rows = %+v, want channel 2 then 1", rows)
	}
	cached, err := GroupGet(1)
	if err != nil {
		t.Fatalf("GroupGet(1) error = %v", err)
	}
	if cached.Items[0].ChannelID != 2 {
		t.Fatalf("cached first item channel = %d, want 2 (cache must reflect composed order)", cached.Items[0].ChannelID)
	}
}

// TestGroupChannelOrderApply_appendsNewChannelsAtTail 锁定尾段追加:
// 设过顺序后新渠道命中正则吸纳, 其成员追加在人工序渠道之后按自然序排列, 既有人工顺序不被破坏。
func TestGroupChannelOrderApply_appendsNewChannelsAtTail(t *testing.T) {
	seedOrderTestDB(t)

	if _, err := GroupChannelOrderApply(1, []int{2}, context.Background()); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	// 新渠道 3 的模型命中 ^glm-, 由渠道创建事件触发重算吸纳。
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "chan-c", BaseURL: "http://new"},
		Keys:          []model.ChannelKeyConfig{{Name: "key-c", Key: "sk-new"}},
		Models:        []string{"glm-4.6-air"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "glm-4.6-air", KeyName: "key-c", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := ChannelCreate(detail, context.Background())
	if err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}
	rows := loadOrderItemRows(t, 1)
	if len(rows) != 6 {
		t.Fatalf("group rgx rows = %+v, want 6 members after absorb", rows)
	}
	// 人工序渠道 2 仍整体最前(块内 air 在 flash 前), 新渠道 3 落在尾段(其渠道主键大于渠道 1, 自然序排在渠道 1 之后)。
	wantChannels := []int{2, 2, 1, 1, 1, created.ID}
	for i, row := range rows {
		if row.channelID != wantChannels[i] {
			t.Fatalf("group rgx rows[%d].channel = %d, want %d", i, row.channelID, wantChannels[i])
		}
	}
	// 人工顺序行未被动过: 仍只有渠道 2 一条。
	if rows := loadOrderRows(t, 1); len(rows) != 1 || rows[0].channelID != 2 {
		t.Fatalf("group rgx order rows = %+v, want only channel 2", rows)
	}
}

// TestGroupChannelOrderApply_idempotent 验证重复保存同顺序幂等:
// 第二次 Apply 不产生重复成员行, 成员集合与优先级与第一次完全一致。
func TestGroupChannelOrderApply_idempotent(t *testing.T) {
	seedOrderTestDB(t)
	ctx := context.Background()

	if _, err := GroupChannelOrderApply(1, []int{2, 1}, ctx); err != nil {
		t.Fatalf("first GroupChannelOrderApply() error = %v", err)
	}
	first := loadOrderItemRows(t, 1)
	if len(first) != 5 {
		t.Fatalf("group rgx rows = %+v, want 5 members", first)
	}
	if _, err := GroupChannelOrderApply(1, []int{2, 1}, ctx); err != nil {
		t.Fatalf("second GroupChannelOrderApply() error = %v", err)
	}
	second := loadOrderItemRows(t, 1)
	if len(second) != len(first) {
		t.Fatalf("group rgx rows = %+v, want member count stable at %d", second, len(first))
	}
	for i := range first {
		if first[i].grantID != second[i].grantID || first[i].priority != second[i].priority {
			t.Fatalf("group rgx rows changed on second apply: first[%d]=%+v second[%d]=%+v", i, first[i], i, second[i])
		}
	}
}

// TestGroupChannelOrderApply_regexWideningKeepsOrder 锁定正则改宽边界:
// 正则扩大会吸纳新渠道, 既有渠道的人工顺序稳定不变, 新渠道落尾段。
func TestGroupChannelOrderApply_regexWideningKeepsOrder(t *testing.T) {
	seedOrderTestDB(t)

	// 先收紧正则只命中 air 模型并设顺序, 再改宽回 ^glm- 全量吸纳。
	narrow := "^glm-4.6-air$"
	if _, err := GroupUpdate(1, &model.GroupUpdateRequest{MemberRegex: &narrow}, context.Background()); err != nil {
		t.Fatalf("GroupUpdate(narrow) error = %v", err)
	}
	if _, err := GroupChannelOrderApply(1, []int{2}, context.Background()); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	wide := "^glm-"
	if _, err := GroupUpdate(1, &model.GroupUpdateRequest{MemberRegex: &wide}, context.Background()); err != nil {
		t.Fatalf("GroupUpdate(wide) error = %v", err)
	}
	rows := loadOrderItemRows(t, 1)
	if len(rows) != 5 {
		t.Fatalf("group rgx rows = %+v, want 5 members after widening", rows)
	}
	// 渠道 2(人工序)仍最前(块内 air 在 flash 前), 渠道 1 按自然序随后: 改宽重算不得破坏已保存的人工顺序。
	wantChannels := []int{2, 2, 1, 1, 1}
	for i, row := range rows {
		if row.channelID != wantChannels[i] {
			t.Fatalf("group rgx rows[%d].channel = %d, want %d", i, row.channelID, wantChannels[i])
		}
	}
}

// TestGroupChannelOrderApply_channelDeleteCascadesOrder 锁定级联清理:
// 删除渠道后其顺序行随之消失, 其余渠道的人工顺序保持, 成员重算后按剩余顺序合成。
func TestGroupChannelOrderApply_channelDeleteCascadesOrder(t *testing.T) {
	seedOrderTestDB(t)
	ctx := context.Background()

	if _, err := GroupChannelOrderApply(1, []int{2, 1}, ctx); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	if err := ChannelDel(1, ctx); err != nil {
		t.Fatalf("ChannelDel(1) error = %v", err)
	}
	// 顺序行级联清理: 只剩渠道 2, 且 position 不变(级联删除不重排剩余行)。
	if rows := loadOrderRows(t, 1); len(rows) != 1 || rows[0].channelID != 2 || rows[0].position != 1 {
		t.Fatalf("group rgx order rows = %+v, want only channel 2 at position 1", rows)
	}
	// 成员只剩渠道 2 的授权, 重算顺序仍把渠道 2 排最前。
	items := loadOrderItemRows(t, 1)
	if len(items) != 2 || items[0].channelID != 2 || items[1].channelID != 2 {
		t.Fatalf("group rgx rows = %+v, want only channel 2 members", items)
	}
}

// TestGroupChannelOrderApply_reAddChannelDoesNotReviveOrder 锁定不复活边界:
// 删除渠道后以新主键重新添加同名渠道, 旧顺序不复活, 新渠道按自然序参与排序。
func TestGroupChannelOrderApply_reAddChannelDoesNotReviveOrder(t *testing.T) {
	seedOrderTestDB(t)
	ctx := context.Background()

	if _, err := GroupChannelOrderApply(1, []int{1, 2}, ctx); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	if err := ChannelDel(1, ctx); err != nil {
		t.Fatalf("ChannelDel(1) error = %v", err)
	}
	// 渠道 1 的顺序行已被级联清掉, 表里只剩渠道 2。
	if rows := loadOrderRows(t, 1); len(rows) != 1 || rows[0].channelID != 2 {
		t.Fatalf("group rgx order rows = %+v, want only channel 2 after delete", rows)
	}
	// 重新添加同名渠道(新主键): 不产生顺序行, 重算后新渠道落尾段。
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "chan-a", BaseURL: "http://upstream"},
		Keys:          []model.ChannelKeyConfig{{Name: "key-a", Key: "sk-a"}},
		Models:        []string{"glm-4.6-flash"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "glm-4.6-flash", KeyName: "key-a", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := ChannelCreate(detail, ctx)
	if err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}
	if rows := loadOrderRows(t, 1); len(rows) != 1 {
		t.Fatalf("group rgx order rows = %+v, want old order not revived for new channel", rows)
	}
	items := loadOrderItemRows(t, 1)
	last := items[len(items)-1]
	if last.channelID != created.ID {
		t.Fatalf("re-added channel %d not at tail (last channel = %d): %+v", created.ID, last.channelID, items)
	}
}

// TestGroupChannelOrderApply_dropsDanglingChannelID 锁定悬空渠道过滤:
// 提交里含库内不存在的渠道 ID 时该 ID 被静默丢弃(不撞外键、不 500), 其余顺序照常落库并参与合成。
func TestGroupChannelOrderApply_dropsDanglingChannelID(t *testing.T) {
	seedOrderTestDB(t)

	// 9999 库内不存在: 顺序 [9999, 1, 2] 应落库为 [1, 2](保持剩余提交顺序)。
	group, err := GroupChannelOrderApply(1, []int{9999, 1, 2}, context.Background())
	if err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v, want nil (dangling id must be dropped, not fail)", err)
	}
	if rows := loadOrderRows(t, 1); !slices.Equal(rows, []orderRow{{channelID: 1, position: 1}, {channelID: 2, position: 2}}) {
		t.Fatalf("group rgx order rows = %+v, want channel 1 then 2 (dangling dropped)", rows)
	}
	// 合成顺序按保留下来的 [1, 2]: 渠道 1 的成员在前段, 渠道 2 在后段, 渠道内自然序保持。
	got := make([]int, 0, len(group.Items))
	for _, item := range group.Items {
		got = append(got, item.ChannelGrantID)
	}
	if want := []int{1, 2, 3, 5, 4}; !slices.Equal(got, want) {
		t.Fatalf("applied items grants = %v, want %v", got, want)
	}
}

// TestGroupChannelOrderApply_allDanglingClearsOrder 锁定全悬空等价清空:
// 提交的渠道 ID 全部不存在时顺序表被清空(成员回自然序), 不报错也不留垃圾行。
func TestGroupChannelOrderApply_allDanglingClearsOrder(t *testing.T) {
	seedOrderTestDB(t)

	if _, err := GroupChannelOrderApply(1, []int{9998, 9999}, context.Background()); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v, want nil", err)
	}
	if rows := loadOrderRows(t, 1); len(rows) != 0 {
		t.Fatalf("group rgx order rows = %+v, want cleared when all ids are dangling", rows)
	}
	items := loadOrderItemRows(t, 1)
	if len(items) != len(naturalOrderGrants) {
		t.Fatalf("group rgx rows = %+v, want %d members in natural order", items, len(naturalOrderGrants))
	}
	for i, row := range items {
		if row.grantID != naturalOrderGrants[i] {
			t.Fatalf("group rgx rows[%d] = %+v, want grant %d (natural order)", i, row, naturalOrderGrants[i])
		}
	}
}

// TestGroupChannelOrderApply_rejectsManualGroup 锁定路径隔离(回归红线):
// 手动分组调顺序端点返回可识别错误, 其成员仍随 items 提交整体定稿, 不读不写顺序表。
func TestGroupChannelOrderApply_rejectsManualGroup(t *testing.T) {
	seedOrderTestDB(t)
	ctx := context.Background()

	if _, err := GroupChannelOrderApply(2, []int{2, 1}, ctx); err == nil {
		t.Fatalf("GroupChannelOrderApply(manual) want error, got nil")
	}
	// 手动分组成员按提交顺序整体替换, 优先级不受任何顺序表影响。
	submitted := []model.GroupItemInput{{ChannelGrantID: 4}, {ChannelGrantID: 1}}
	updated, err := GroupUpdate(2, &model.GroupUpdateRequest{Items: &submitted}, ctx)
	if err != nil {
		t.Fatalf("GroupUpdate(manual items) error = %v", err)
	}
	if len(updated.Items) != 2 || updated.Items[0].ChannelGrantID != 4 || updated.Items[0].Priority != 1 ||
		updated.Items[1].ChannelGrantID != 1 || updated.Items[1].Priority != 2 {
		t.Fatalf("manual group items = %+v, want submitted order with sequential priorities", updated.Items)
	}
	if rows := loadOrderRows(t, 2); len(rows) != 0 {
		t.Fatalf("manual group order rows = %+v, want none", rows)
	}
}

// TestGroupChannelOrderReset_returnsToNaturalOrder 验证清空顺序:
// Reset 后顺序表为空, 成员回到 (渠道, 模型, 凭据) 自然序, 响应与库内一致。
func TestGroupChannelOrderReset_returnsToNaturalOrder(t *testing.T) {
	seedOrderTestDB(t)
	ctx := context.Background()

	if _, err := GroupChannelOrderApply(1, []int{2, 1}, ctx); err != nil {
		t.Fatalf("GroupChannelOrderApply() error = %v", err)
	}
	group, err := GroupChannelOrderReset(1, ctx)
	if err != nil {
		t.Fatalf("GroupChannelOrderReset() error = %v", err)
	}
	if rows := loadOrderRows(t, 1); len(rows) != 0 {
		t.Fatalf("group rgx order rows = %+v, want cleared", rows)
	}
	got := make([]int, 0, len(group.Items))
	for _, item := range group.Items {
		got = append(got, item.ChannelGrantID)
	}
	if !slices.Equal(got, naturalOrderGrants) {
		t.Fatalf("reset items grants = %v, want natural order %v", got, naturalOrderGrants)
	}
}

// TestGroupRegexSync_withoutOrderIsUnchanged 锁定无人工顺序的回归:
// 未设顺序的正则分组重算结果与引入人工顺序特性前完全一致(自然序)。
func TestGroupRegexSync_withoutOrderIsUnchanged(t *testing.T) {
	seedOrderTestDB(t)

	if err := GroupRegexSync(context.Background()); err != nil {
		t.Fatalf("GroupRegexSync() error = %v", err)
	}
	rows := loadOrderItemRows(t, 1)
	if len(rows) != len(naturalOrderGrants) {
		t.Fatalf("group rgx rows = %+v, want %d members", rows, len(naturalOrderGrants))
	}
	for i, row := range rows {
		if row.grantID != naturalOrderGrants[i] || row.priority != i+1 {
			t.Fatalf("group rgx rows[%d] = %+v, want grant %d at priority %d (natural order)", i, row, naturalOrderGrants[i], i+1)
		}
	}
}
