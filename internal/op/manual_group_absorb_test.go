package op

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// seedAbsorbTestDB 建两个渠道与四个分组, 覆盖手动分组吸纳的全部判定面:
// 渠道 1 两模型(gpt-4o / claude-3-7-sonnet)共用凭据 key-a, 渠道 2 单模型(glm-4.6-air)用 key-b。
// 分组 gpt 预置一个模型名不含分组名的人工成员(验证既有成员不动); glm 是空的故障转移分组(验证两种模式都吸纳);
// sonnet 是正则分组且分组名命中 claude 模型(验证吸纳不碰正则分组); qwen 无模型命中(验证未命中分组不动)。
func seedAbsorbTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "manual-absorb-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	type seedModel struct {
		id    int    // 渠道模型主键。
		name  string // 上游模型名称。
		grant int    // 该模型对应的授权主键。
	}
	for _, ch := range []struct {
		id     int
		name   string
		keyID  int
		key    string
		models []seedModel
	}{
		{id: 1, name: "chan-a", keyID: 1, key: "key-a", models: []seedModel{
			{id: 1, name: "gpt-4o", grant: 1},
			{id: 2, name: "claude-3-7-sonnet", grant: 2},
		}},
		{id: 2, name: "chan-b", keyID: 2, key: "key-b", models: []seedModel{
			{id: 3, name: "glm-4.6-air", grant: 3},
		}},
	} {
		if err := db.GetDB().Create(&model.Channel{
			ID:            ch.id,
			ChannelConfig: model.ChannelConfig{Name: ch.name, BaseURL: "http://upstream"},
		}).Error; err != nil {
			t.Fatalf("create channel %d: %v", ch.id, err)
		}
		if err := db.GetDB().Create(&model.ChannelKey{
			ID: ch.keyID, ChannelID: ch.id,
			ChannelKeyConfig: model.ChannelKeyConfig{Name: ch.key, Key: "sk-" + ch.key},
		}).Error; err != nil {
			t.Fatalf("create channel key %d: %v", ch.keyID, err)
		}
		for _, m := range ch.models {
			if err := db.GetDB().Create(&model.ChannelModel{ID: m.id, ChannelID: ch.id, Name: m.name}).Error; err != nil {
				t.Fatalf("create channel model %d: %v", m.id, err)
			}
			if err := db.GetDB().Create(&model.ChannelGrant{
				ID: m.grant, ChannelModelID: m.id, ChannelKeyID: ch.keyID,
				Protocols: model.ProtocolOpenAIChatCompletion,
			}).Error; err != nil {
				t.Fatalf("create channel grant %d: %v", m.grant, err)
			}
		}
	}

	groups := []model.Group{
		{
			ID: 1, Name: "gpt", Mode: model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig(),
			// 人工添加的成员: claude 模型名不含分组名, 吸纳既不能删它也不能动它的优先级。
			Items: []model.GroupItem{{GroupID: 1, ChannelGrantID: 2, Priority: 1}},
		},
		{
			ID: 2, Name: "glm", Mode: model.GroupModeFailover,
			RelayConfig: model.DefaultGroupRelayConfig(),
		},
		{
			// 正则分组: 分组名命中 claude 模型, 但成员只由正则(匹配空集)定稿, 吸纳不得介入。
			ID: 3, Name: "sonnet", Mode: model.GroupModeManual, MemberRegex: "^zzz$",
			RelayConfig: model.DefaultGroupRelayConfig(),
		},
		{
			ID: 4, Name: "qwen", Mode: model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig(),
		},
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

// absorbItemRow 是断言用的一行分组成员: 行主键 + 引用的授权 + 优先级。
type absorbItemRow struct {
	itemID   int
	grantID  int
	priority int
}

// loadAbsorbRows 按优先级读回指定分组的成员行, 供断言成员集合, 顺序与优先级; 重复成员直接判失败。
func loadAbsorbRows(t *testing.T, groupID int) []absorbItemRow {
	t.Helper()
	var items []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", groupID).Order("priority").Find(&items).Error; err != nil {
		t.Fatalf("load group %d items: %v", groupID, err)
	}
	rows := make([]absorbItemRow, 0, len(items))
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if _, dup := seen[item.ChannelGrantID]; dup {
			t.Fatalf("group %d has duplicate member for grant %d", groupID, item.ChannelGrantID)
		}
		seen[item.ChannelGrantID] = struct{}{}
		rows = append(rows, absorbItemRow{itemID: item.ID, grantID: item.ChannelGrantID, priority: item.Priority})
	}
	return rows
}

// TestGroupManualAbsorb_absorbsMatchingGrants 验证全量吸纳的命中与不动面:
// 命中分组补上缺失成员(空组从优先级 1 起), 既有成员行主键与优先级原样保留且新成员追加尾部续排,
// failover 模式的手动分组同样吸纳, 正则分组与未命中分组不被触碰, 库与缓存一致。
func TestGroupManualAbsorb_absorbsMatchingGrants(t *testing.T) {
	seedAbsorbTestDB(t)

	preset := loadAbsorbRows(t, 1)
	if len(preset) != 1 || preset[0].grantID != 2 || preset[0].priority != 1 {
		t.Fatalf("seed group gpt rows = %+v, want preset member grant 2 at priority 1", preset)
	}
	if err := GroupManualAbsorb(context.Background()); err != nil {
		t.Fatalf("GroupManualAbsorb() error = %v", err)
	}

	// gpt 组: 预置成员(claude 模型名不含分组名)保持同一行与优先级 1, 命中的 gpt-4o 授权追加为优先级 2。
	rows := loadAbsorbRows(t, 1)
	if len(rows) != 2 || rows[0].grantID != 2 || rows[0].itemID != preset[0].itemID || rows[0].priority != 1 ||
		rows[1].grantID != 1 || rows[1].priority != 2 {
		t.Fatalf("group gpt rows = %+v, want preset grant 2 (same row, p1) then absorbed grant 1 (p2)", rows)
	}
	// glm 组: failover 模式的手动分组同样吸纳, 空组从优先级 1 起。
	if rows := loadAbsorbRows(t, 2); len(rows) != 1 || rows[0].grantID != 3 || rows[0].priority != 1 {
		t.Fatalf("group glm rows = %+v, want absorbed grant 3 at priority 1", rows)
	}
	// 正则分组: 分组名同样命中 claude 模型, 但成员由正则定稿, 吸纳不得给它添任何成员。
	if rows := loadAbsorbRows(t, 3); len(rows) != 0 {
		t.Fatalf("regex group sonnet rows = %+v, want untouched by absorb", rows)
	}
	// 未命中分组: 无任何模型名包含 qwen, 原样为空。
	if rows := loadAbsorbRows(t, 4); len(rows) != 0 {
		t.Fatalf("group qwen rows = %+v, want no members", rows)
	}
	// 缓存与库一致: 吸纳末尾整体刷新分组缓存, 读取侧立即可见新成员。
	group, err := GroupGet(1)
	if err != nil {
		t.Fatalf("GroupGet(1) error = %v", err)
	}
	if len(group.Items) != 2 || group.Items[1].ModelName != "gpt-4o" {
		t.Fatalf("cached group gpt items = %+v, want gpt-4o member visible in cache", group.Items)
	}
}

// TestGroupManualAbsorb_idempotent 验证重复吸纳幂等: 第二轮不再产生任何行, 成员集合与优先级与第一轮完全一致。
func TestGroupManualAbsorb_idempotent(t *testing.T) {
	seedAbsorbTestDB(t)
	ctx := context.Background()

	if err := GroupManualAbsorb(ctx); err != nil {
		t.Fatalf("first GroupManualAbsorb() error = %v", err)
	}
	firstGpt := loadAbsorbRows(t, 1)
	firstGlm := loadAbsorbRows(t, 2)
	if len(firstGpt) != 2 || len(firstGlm) != 1 {
		t.Fatalf("first absorb rows gpt=%+v glm=%+v, want 2 and 1 members", firstGpt, firstGlm)
	}

	if err := GroupManualAbsorb(ctx); err != nil {
		t.Fatalf("second GroupManualAbsorb() error = %v", err)
	}
	// loadAbsorbRows 对重复成员直接判失败, 这里再比对整行集合: 幂等即一行都不多、优先级都不变。
	if !slices.Equal(firstGpt, loadAbsorbRows(t, 1)) {
		t.Fatalf("group gpt rows changed on second absorb: %+v", loadAbsorbRows(t, 1))
	}
	if !slices.Equal(firstGlm, loadAbsorbRows(t, 2)) {
		t.Fatalf("group glm rows changed on second absorb: %+v", loadAbsorbRows(t, 2))
	}
}

// TestGroupManualAbsorb_reabsorbsDeletedMember 锁定 append-only 语义:
// 手动删除吸纳来的成员后再触发吸纳, 成员回归并追加到尾部; 既有成员仍保持原行与优先级。
// 规避回归的手段是改分组名或改用正则分组, 与 v1 行为一致。
func TestGroupManualAbsorb_reabsorbsDeletedMember(t *testing.T) {
	seedAbsorbTestDB(t)
	ctx := context.Background()

	if err := GroupManualAbsorb(ctx); err != nil {
		t.Fatalf("GroupManualAbsorb() error = %v", err)
	}
	before := loadAbsorbRows(t, 1)
	if len(before) != 2 {
		t.Fatalf("group gpt rows = %+v, want 2 members before deletion", before)
	}

	// 模拟人工在界面上删掉吸纳来的 gpt-4o 成员。
	if err := db.GetDB().Where("group_id = ? AND channel_grant_id = ?", 1, 1).
		Delete(&model.GroupItem{}).Error; err != nil {
		t.Fatalf("delete absorbed item: %v", err)
	}
	if err := GroupManualAbsorb(ctx); err != nil {
		t.Fatalf("reabsorb GroupManualAbsorb() error = %v", err)
	}

	rows := loadAbsorbRows(t, 1)
	if len(rows) != 2 || rows[0].grantID != 2 || rows[0].itemID != before[0].itemID || rows[0].priority != 1 ||
		rows[1].grantID != 1 || rows[1].priority != 2 {
		t.Fatalf("group gpt rows = %+v, want preset grant 2 (same row, p1) then reabsorbed grant 1 at tail (p2)", rows)
	}
}

// TestGroupManualAbsorb_channelFilter 验证渠道过滤: 指定渠道时只吸纳该渠道的授权,
// 其他渠道的匹配授权留待下一轮全量兜底; 非目标分组不因过滤轮次产生变化。
func TestGroupManualAbsorb_channelFilter(t *testing.T) {
	seedAbsorbTestDB(t)
	ctx := context.Background()

	// 只按渠道 1 触发: gpt 组补上渠道 1 的授权, glm 组(命中的授权属渠道 2)保持为空。
	if err := GroupManualAbsorb(ctx, 1); err != nil {
		t.Fatalf("GroupManualAbsorb(1) error = %v", err)
	}
	if rows := loadAbsorbRows(t, 2); len(rows) != 0 {
		t.Fatalf("group glm rows = %+v, want untouched (grant 3 belongs to channel 2)", rows)
	}
	if rows := loadAbsorbRows(t, 1); len(rows) != 2 || rows[1].grantID != 1 || rows[1].priority != 2 {
		t.Fatalf("group gpt rows = %+v, want channel 1 grant absorbed at tail", rows)
	}

	// 换渠道 2 触发: glm 组补上渠道 2 的授权, gpt 组不再变化。
	if err := GroupManualAbsorb(ctx, 2); err != nil {
		t.Fatalf("GroupManualAbsorb(2) error = %v", err)
	}
	if rows := loadAbsorbRows(t, 2); len(rows) != 1 || rows[0].grantID != 3 || rows[0].priority != 1 {
		t.Fatalf("group glm rows = %+v, want channel 2 grant 3 at priority 1", rows)
	}
	if rows := loadAbsorbRows(t, 1); len(rows) != 2 {
		t.Fatalf("group gpt rows = %+v, want unchanged after channel 2 absorb", rows)
	}
}

// TestChannelCreate_absorbsNewChannelIntoManualGroups 验证接线: 新建渠道后不做任何人工操作,
// 模型名包含手动分组名的授权自动成为成员; 正则分组照旧由正则定稿, 不被吸纳逻辑触碰。
func TestChannelCreate_absorbsNewChannelIntoManualGroups(t *testing.T) {
	seedAbsorbTestDB(t)

	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "chan-new", BaseURL: "http://new"},
		Keys:          []model.ChannelKeyConfig{{Name: "key-new", Key: "sk-new"}},
		Models:        []string{"glm-4.6-flash"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "glm-4.6-flash", KeyName: "key-new", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	if _, err := ChannelCreate(detail, context.Background()); err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}

	// glm 组自动出现新渠道的成员; 事件触发按渠道过滤, 存量渠道的同名模型授权留给全量兜底。
	group, err := GroupGet(2)
	if err != nil {
		t.Fatalf("GroupGet(2) error = %v", err)
	}
	if len(group.Items) != 1 || group.Items[0].ModelName != "glm-4.6-flash" || group.Items[0].Priority != 1 {
		t.Fatalf("group glm items = %+v, want new channel member glm-4.6-flash at priority 1", group.Items)
	}
	// 正则分组照旧不被触碰。
	regexGroup, err := GroupGet(3)
	if err != nil {
		t.Fatalf("GroupGet(3) error = %v", err)
	}
	if len(regexGroup.Items) != 0 {
		t.Fatalf("regex group sonnet items = %+v, want untouched by absorb", regexGroup.Items)
	}
}

// TestGroupCreate_absorbsExistingMatchesIntoManualGroup 验证手动分组创建即吸纳(R1b):
// 先建渠道后建分组时渠道侧触发点不会回补, 创建响应与库内成员直接带上存量匹配授权,
// 吸纳成员追加在提交成员之后续排; failover 模式同样是 member_regex 为空, 一并适用。
func TestGroupCreate_absorbsExistingMatchesIntoManualGroup(t *testing.T) {
	seedAbsorbTestDB(t)

	created, err := GroupCreate(&model.GroupCreateRequest{
		Name: "claude",
		// 初始成员选模型名不含分组名的授权: 提交成员的优先级在前, 吸纳成员只能追加尾部。
		Items: []model.GroupItemInput{{ChannelGrantID: 1}},
	}, context.Background())
	if err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}
	if len(created.Items) != 2 ||
		created.Items[0].ChannelGrantID != 1 || created.Items[0].Priority != 1 ||
		created.Items[1].ChannelGrantID != 2 || created.Items[1].Priority != 2 {
		t.Fatalf("created items = %+v, want submitted grant 1 (p1) then absorbed grant 2 (p2)", created.Items)
	}
	if created.Items[1].ModelName != "claude-3-7-sonnet" {
		t.Fatalf("created items[1] = %+v, want absorbed claude model name filled by snapshot", created.Items[1])
	}
	rows := loadAbsorbRows(t, created.ID)
	if len(rows) != 2 || rows[0].grantID != 1 || rows[0].priority != 1 || rows[1].grantID != 2 || rows[1].priority != 2 {
		t.Fatalf("group claude rows = %+v, want submitted then absorbed members persisted", rows)
	}

	// failover 模式: 空组创建即吸纳命中授权, 从优先级 1 起。
	failover, err := GroupCreate(&model.GroupCreateRequest{
		Name: "air",
		Mode: model.GroupModeFailover,
	}, context.Background())
	if err != nil {
		t.Fatalf("GroupCreate(failover) error = %v", err)
	}
	if len(failover.Items) != 1 || failover.Items[0].ChannelGrantID != 3 || failover.Items[0].Priority != 1 {
		t.Fatalf("failover group items = %+v, want absorbed grant 3 at priority 1", failover.Items)
	}
}

// TestGroupCreate_withoutMatchesKeepsSubmittedItems 验证无匹配时创建只落提交成员:
// 吸纳不给分组添任何成员, 提交成员的优先级原样保留。
func TestGroupCreate_withoutMatchesKeepsSubmittedItems(t *testing.T) {
	seedAbsorbTestDB(t)

	created, err := GroupCreate(&model.GroupCreateRequest{
		Name:  "kimi", // 没有任何模型名包含 kimi。
		Items: []model.GroupItemInput{{ChannelGrantID: 3}},
	}, context.Background())
	if err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}
	if len(created.Items) != 1 || created.Items[0].ChannelGrantID != 3 || created.Items[0].Priority != 1 {
		t.Fatalf("created items = %+v, want only submitted member grant 3 at priority 1", created.Items)
	}
	if rows := loadAbsorbRows(t, created.ID); len(rows) != 1 || rows[0].grantID != 3 {
		t.Fatalf("group kimi rows = %+v, want only submitted member persisted", rows)
	}
}

// TestGroupUpdate_absorbsOnRename 验证手动分组改名后立即按新名吸纳(R1b):
// 匹配口径是模型名包含分组名, 改名即改变命中集合; 既有成员的行主键与优先级原样保留, 吸纳成员追加尾部。
func TestGroupUpdate_absorbsOnRename(t *testing.T) {
	seedAbsorbTestDB(t)

	preset := loadAbsorbRows(t, 1)
	if len(preset) != 1 || preset[0].grantID != 2 || preset[0].priority != 1 {
		t.Fatalf("seed group gpt rows = %+v, want preset member grant 2 at priority 1", preset)
	}
	// gpt 改名 air: 新名命中渠道 2 的 glm-4.6-air, 旧名不命中的授权随即被吸纳。
	newName := "air"
	updated, err := GroupUpdate(1, &model.GroupUpdateRequest{Name: &newName}, context.Background())
	if err != nil {
		t.Fatalf("GroupUpdate() error = %v", err)
	}
	if len(updated.Items) != 2 ||
		updated.Items[0].ChannelGrantID != 2 || updated.Items[0].ID != preset[0].itemID || updated.Items[0].Priority != 1 ||
		updated.Items[1].ChannelGrantID != 3 || updated.Items[1].Priority != 2 {
		t.Fatalf("updated items = %+v, want preset grant 2 (same row, p1) then absorbed grant 3 (p2)", updated.Items)
	}
	rows := loadAbsorbRows(t, 1)
	if len(rows) != 2 || rows[0].itemID != preset[0].itemID || rows[1].grantID != 3 || rows[1].priority != 2 {
		t.Fatalf("group air rows = %+v, want preset row kept and absorbed grant 3 appended at tail", rows)
	}
}

// TestGroupUpdate_memberEditWithoutRenameDoesNotReabsorb 锁定改名之外不触发的边界:
// 仅删成员保存(未改名)不得回吸 —— 吸纳没有移除记忆, 若成员编辑也触发, 刚删的匹配成员一保存就回来, 成员管理形同虚设。
func TestGroupUpdate_memberEditWithoutRenameDoesNotReabsorb(t *testing.T) {
	seedAbsorbTestDB(t)

	// 先全量吸纳一轮, 让 gpt 组带上模型名含分组名的 gpt-4o 成员。
	if err := GroupManualAbsorb(context.Background()); err != nil {
		t.Fatalf("GroupManualAbsorb() error = %v", err)
	}
	// 模拟人工删掉匹配成员后保存: 只提交剩余成员, 不改名。
	updated, err := GroupUpdate(1, &model.GroupUpdateRequest{
		Items: &[]model.GroupItemInput{{ChannelGrantID: 2}},
	}, context.Background())
	if err != nil {
		t.Fatalf("GroupUpdate() error = %v", err)
	}
	if len(updated.Items) != 1 || updated.Items[0].ChannelGrantID != 2 || updated.Items[0].Priority != 1 {
		t.Fatalf("updated items = %+v, want only remaining member grant 2", updated.Items)
	}
	if rows := loadAbsorbRows(t, 1); len(rows) != 1 || rows[0].grantID != 2 {
		t.Fatalf("group gpt rows = %+v, want deleted matching member NOT reabsorbed without rename", rows)
	}
}

// TestGroupUpdate_regexToManualWithRenameAbsorbsByNewName 锁定触发条件按 memberRegex 最终值判定的边界:
// 正则分组在改回手动(提交空串)的同时改名, 更新后已是换了新名的手动分组, 按新名吸纳成立;
// 仅改回手动而未改名则不触发, 与「未改名的成员编辑不回吸」同一边界, 留待渠道事件或兜底轮补齐。
func TestGroupUpdate_regexToManualWithRenameAbsorbsByNewName(t *testing.T) {
	seedAbsorbTestDB(t)

	// 分组 3(sonnet, 正则 ^zzz$ 匹配空集)改回手动并改名 air: 新名命中渠道 2 的 glm-4.6-air, 应立即吸纳。
	emptyRegex := ""
	newName := "air"
	updated, err := GroupUpdate(3, &model.GroupUpdateRequest{Name: &newName, MemberRegex: &emptyRegex}, context.Background())
	if err != nil {
		t.Fatalf("GroupUpdate(3) error = %v", err)
	}
	if updated.MemberRegex != "" {
		t.Fatalf("updated member_regex = %q, want cleared to manual", updated.MemberRegex)
	}
	if len(updated.Items) != 1 || updated.Items[0].ChannelGrantID != 3 || updated.Items[0].Priority != 1 {
		t.Fatalf("updated items = %+v, want absorbed grant 3 at priority 1 by new name", updated.Items)
	}
	if rows := loadAbsorbRows(t, 3); len(rows) != 1 || rows[0].grantID != 3 {
		t.Fatalf("group air rows = %+v, want absorbed grant 3 persisted", rows)
	}

	// 对照: 正则分组仅改回手动而未改名, 分组名 claude 命中 claude 模型也不吸纳, 成员保持正则定稿的空集。
	regexClaude, err := GroupCreate(&model.GroupCreateRequest{Name: "claude", MemberRegex: "^zzz$"}, context.Background())
	if err != nil {
		t.Fatalf("GroupCreate(claude regex) error = %v", err)
	}
	converted, err := GroupUpdate(regexClaude.ID, &model.GroupUpdateRequest{MemberRegex: &emptyRegex}, context.Background())
	if err != nil {
		t.Fatalf("GroupUpdate(%d) error = %v", regexClaude.ID, err)
	}
	if converted.MemberRegex != "" {
		t.Fatalf("converted member_regex = %q, want cleared to manual", converted.MemberRegex)
	}
	if len(converted.Items) != 0 {
		t.Fatalf("converted items = %+v, want no absorb without rename", converted.Items)
	}
	if rows := loadAbsorbRows(t, regexClaude.ID); len(rows) != 0 {
		t.Fatalf("regex group rows = %+v, want no members without rename", rows)
	}
}

// TestGroupCreate_regexGroupSkipsManualAbsorb 验证正则分组创建走正则吸纳而非手动吸纳:
// 分组名命中既有模型而正则匹配空集, 成员必须为空 —— 若误走手动吸纳路径, 分组名命中的授权会被错误纳入。
func TestGroupCreate_regexGroupSkipsManualAbsorb(t *testing.T) {
	seedAbsorbTestDB(t)

	created, err := GroupCreate(&model.GroupCreateRequest{
		Name:        "claude", // 分组名命中 claude 模型: 误走手动吸纳会纳入 grant 2。
		MemberRegex: "^zzz$",  // 正则匹配空集: 正确路径的成员必须为空。
	}, context.Background())
	if err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}
	if len(created.Items) != 0 {
		t.Fatalf("created items = %+v, want empty: regex group members are defined by regex, not manual absorb", created.Items)
	}
	if rows := loadAbsorbRows(t, created.ID); len(rows) != 0 {
		t.Fatalf("regex group rows = %+v, want no members", rows)
	}
}
