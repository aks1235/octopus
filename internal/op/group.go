package op

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"github.com/dlclark/regexp2"
	"gorm.io/gorm"
)

var (
	groupCache     = cache.New[int, model.Group](16) // 按主键保存完整分组配置。
	groupNameIndex = cache.New[string, int](16)      // 客户端模型名对应的分组主键。
)

// GroupList 返回缓存中的全部分组, 成员已补齐界面展示所需的名称与可用性, 按名称定序。
// 不含实时路由状态: 路由状态由 Relay 持有, 而 Relay 依赖本包, 故由处理器在返回前补齐。
// 定序是为 API Key 面板的模型选择器: 那里没有排序开关, 而缓存遍历顺序随机;
// 分组页自带升降序开关, 会按开关重排, 不依赖此顺序。
func GroupList() []model.Group {
	groups := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		groups = append(groups, groupSnapshot(group))
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups
}

// GroupGet 返回指定分组的读取副本, 成员已补齐界面展示所需的名称与可用性。
// 不含实时路由状态: 与 GroupList 同理, 由处理器在返回前补齐。
func GroupGet(id int) (model.Group, error) {
	group, ok := groupCache.Get(id)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	return groupSnapshot(group), nil
}

// GroupListModel 返回缓存中的全部分组模型名, 按名称定序。
// 两个消费方都不提供排序开关: /v1/models 由第三方客户端直接展示, API Key 面板按返回顺序列出可用模型,
// 而缓存遍历顺序随机, 故顺序须由此处定稿。
func GroupListModel() []string {
	models := make([]string, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		models = append(models, group.Name)
	}
	sort.Strings(models)
	return models
}

// GroupGetByName 返回客户端模型名称对应的分组配置, 供转发选路使用。
// 不补齐成员的展示字段: 转发只需成员主键与顺序, 授权详情由 ChannelGrantGet 按主键单独取,
// 那里会连带校验凭据启用与两侧存在, 使拿到的授权必然可直接转发。
func GroupGetByName(name string) (model.Group, error) {
	groupID, ok := groupNameIndex.Get(name)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	group, ok := groupCache.Get(groupID)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	group.Items = slices.Clone(group.Items)
	return group, nil
}

// GroupCreate 创建分组及其成员并刷新缓存, 返回创建后的分组。
// 成员的提交顺序即优先级顺序; 提交了成员正则时按正则重算成员, 创建响应即带上吸纳后的成员集合。
func GroupCreate(req *model.GroupCreateRequest, ctx context.Context) (*model.Group, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}
	group := model.Group{
		Name:        name,
		Mode:        req.Mode,
		MemberRegex: strings.TrimSpace(req.MemberRegex),
		RelayConfig: req.RelayConfig,
		Items:       make([]model.GroupItem, len(req.Items)),
	}
	if group.Mode == "" {
		group.Mode = model.GroupModeManual
	}
	model.NormalizeGroupRelayConfig(&group.RelayConfig)
	for i, item := range req.Items {
		group.Items[i] = model.GroupItem{ChannelGrantID: item.ChannelGrantID, Priority: i + 1}
	}
	if err := db.GetDB().WithContext(ctx).Create(&group).Error; err != nil {
		return nil, err
	}
	// 成员正则非空时按正则整体替换初始成员: 正则分组的成员由正则决定, 手工提交的初始集合会被覆盖。
	if group.MemberRegex != "" {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return syncRegexGroupItems(tx, group.ID, group.MemberRegex)
		}); err != nil {
			return nil, err
		}
		if err := db.GetDB().WithContext(ctx).Preload("Items").First(&group, group.ID).Error; err != nil {
			return nil, fmt.Errorf("failed to load created group: %w", err)
		}
	}
	// 按正则重载的成员不带排序, 顺序须按优先级定稿: 缓存与响应都承诺成员按 Priority 升序,
	// 故障转移的成员遍历也依赖这一顺序; 手动路径本就按提交顺序落库, 排序对它是空操作。
	sortGroupItems(group.Items)
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(group.Name, group.ID)
	snapshot := groupSnapshot(group)
	return &snapshot, nil
}

// GroupUpdate 更新分组配置, 成员和当前成员，并返回刷新后的分组。
func GroupUpdate(id int, req *model.GroupUpdateRequest, ctx context.Context) (*model.Group, error) {
	oldGroup, ok := groupCache.Get(id)
	if !ok {
		return nil, fmt.Errorf("group not found")
	}
	oldName := oldGroup.Name

	var selectFields []string
	updates := model.Group{ID: id}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("group name is required")
		}
		selectFields = append(selectFields, "name")
		updates.Name = name
	}
	if req.Mode != nil {
		selectFields = append(selectFields, "mode")
		updates.Mode = *req.Mode
	}
	// 成员正则的最终值: 未提交该字段维持原值; 提交空串表示改回纯手动分组, 已吸纳成员保留为手动成员。
	memberRegex := oldGroup.MemberRegex
	if req.MemberRegex != nil {
		memberRegex = strings.TrimSpace(*req.MemberRegex)
		selectFields = append(selectFields, "member_regex")
		updates.MemberRegex = memberRegex
	}
	if req.RelayConfig != nil {
		config := *req.RelayConfig
		model.NormalizeGroupRelayConfig(&config)
		selectFields = append(selectFields, "relay_config")
		updates.RelayConfig = config
	}

	var group model.Group
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(selectFields) > 0 {
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Select(selectFields).Updates(&updates).Error; err != nil {
				return fmt.Errorf("failed to update group: %w", err)
			}
		}
		if req.Items != nil {
			if err := syncGroupItems(tx, id, *req.Items); err != nil {
				return err
			}
		}
		// 正则分组的成员由正则定稿: 手工成员(若提交)先落地, 再按正则整体替换; 改回手动分组则不重算, 已吸纳成员原地保留。
		if req.MemberRegex != nil && memberRegex != "" {
			if err := syncRegexGroupItems(tx, id, memberRegex); err != nil {
				return err
			}
		}
		if err := tx.Preload("Items").First(&group, id).Error; err != nil {
			return fmt.Errorf("failed to load updated group: %w", err)
		}
		// 当前成员在成员集合定稿后才写入: syncGroupItems 会清空指向已删除成员的当前成员,
		// 先写会被它覆盖; 归属校验同样只对最终集合成立。
		if req.ActiveItemID != nil {
			if *req.ActiveItemID != 0 && !slices.ContainsFunc(group.Items, func(item model.GroupItem) bool { return item.ID == *req.ActiveItemID }) {
				return fmt.Errorf("group item not found")
			}
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Update("active_item_id", *req.ActiveItemID).Error; err != nil {
				return fmt.Errorf("failed to update active item: %w", err)
			}
			group.ActiveItemID = *req.ActiveItemID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortGroupItems(group.Items)
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(group.Name, group.ID)
	if oldName != group.Name {
		groupNameIndex.Del(oldName)
	}
	snapshot := groupSnapshot(group)
	return &snapshot, nil
}

// syncGroupItems 按提交的成员集合新增, 重排与删除分组成员。
// 成员在分组内按渠道授权唯一, 该授权作为匹配依据, 由此已有成员保留其主键:
// 主键被分组的当前成员和 Relay 的路由状态引用, 换主键会让人工选择与冷却记录失效。
// 优先级一律按提交顺序重写, 前端只需提交当前排列, 无需自行算出哪些成员的顺序发生了变化。
func syncGroupItems(tx *gorm.DB, groupID int, requested []model.GroupItemInput) error {
	var existing []model.GroupItem
	if err := tx.Where("group_id = ?", groupID).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to load group items: %w", err)
	}
	existingByGrant := make(map[int]model.GroupItem, len(existing))
	for _, item := range existing {
		existingByGrant[item.ChannelGrantID] = item
	}

	for priority, requestedItem := range requested {
		current, ok := existingByGrant[requestedItem.ChannelGrantID]
		if !ok {
			newItem := model.GroupItem{GroupID: groupID, ChannelGrantID: requestedItem.ChannelGrantID, Priority: priority + 1}
			if err := tx.Create(&newItem).Error; err != nil {
				return fmt.Errorf("failed to create group item: %w", err)
			}
			continue
		}
		if current.Priority != priority+1 {
			if err := tx.Model(&model.GroupItem{}).Where("id = ?", current.ID).
				Update("priority", priority+1).Error; err != nil {
				return fmt.Errorf("failed to update group item: %w", err)
			}
		}
		delete(existingByGrant, requestedItem.ChannelGrantID)
	}

	deletedIDs := make([]int, 0, len(existingByGrant))
	for _, item := range existingByGrant {
		deletedIDs = append(deletedIDs, item.ID)
	}
	if len(deletedIDs) == 0 {
		return nil
	}
	// 被删掉的成员可能正是当前人工指定的成员, 需一并清空, 否则分组会指向一个已不存在的成员。
	if err := tx.Model(&model.Group{}).
		Where("id = ? AND active_item_id IN ?", groupID, deletedIDs).
		Update("active_item_id", 0).Error; err != nil {
		return fmt.Errorf("failed to clear active item: %w", err)
	}
	if err := tx.Delete(&model.GroupItem{}, deletedIDs).Error; err != nil {
		return fmt.Errorf("failed to delete group items: %w", err)
	}
	return nil
}

// GroupRegexSync 重算全部正则分组的成员并刷新分组缓存。
// 渠道增删改后的即时触发与定时兜底共用本入口; 没有正则分组时只做一次查询即返回。
// 单个分组的失败(如导入库带入无法编译的正则)只记日志不中断其余分组: 兜底任务不应被一个坏分组拖垮。
func GroupRegexSync(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).Where("member_regex <> ''").Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load regex groups: %w", err)
	}
	if len(groups) == 0 {
		return nil
	}
	for _, group := range groups {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return syncRegexGroupItems(tx, group.ID, group.MemberRegex)
		}); err != nil {
			log.Warnf("failed to sync members of regex group %q: %v", group.Name, err)
		}
	}
	return groupRefreshCache(ctx)
}

// syncRegexGroupItems 按分组成员正则重算目标成员集合, 并在事务内整体替换现有成员。
// 匹配对象是缓存中全部渠道的全部模型名, 不看渠道与凭据的启停: 禁用不等于删除, 可用性由成员展示层的 Available 表达;
// 命中模型的全部授权纳入成员, 同渠道同模型的多个凭据一并纳入。目标成员按 (渠道, 模型, 凭据) 稳定排序,
// 提交顺序即优先级顺序。整体替换复用 syncGroupItems: 按授权主键匹配, 既有成员的主键与统计得以保留,
// 被正则淘汰的成员随之删除, 指向它的当前成员选择也被清空。
func syncRegexGroupItems(tx *gorm.DB, groupID int, memberRegex string) error {
	re, err := regexp2.Compile(memberRegex, regexp2.ECMAScript)
	if err != nil {
		return fmt.Errorf("failed to compile member regex: %w", err)
	}

	type memberRef struct {
		channelID int    // 排序键: 授权所属渠道主键。
		modelName string // 排序键: 授权引用的模型名称。
		keyName   string // 排序键: 授权引用的凭据名称。
		grantID   int    // 分组成员按它引用授权。
	}
	members := make([]memberRef, 0, channelGrantCache.Len())
	for _, grant := range channelGrantCache.GetAll() {
		channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
		if !ok {
			continue
		}
		matched, err := re.MatchString(channelModel.Name)
		if err != nil {
			return fmt.Errorf("failed to match model name %q: %w", channelModel.Name, err)
		}
		if !matched {
			continue
		}
		channelKey, ok := channelKeyCache.Get(grant.ChannelKeyID)
		if !ok {
			continue
		}
		members = append(members, memberRef{
			channelID: channelModel.ChannelID,
			modelName: channelModel.Name,
			keyName:   channelKey.Name,
			grantID:   grant.ID,
		})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].channelID != members[j].channelID {
			return members[i].channelID < members[j].channelID
		}
		if members[i].modelName != members[j].modelName {
			return members[i].modelName < members[j].modelName
		}
		return members[i].keyName < members[j].keyName
	})

	inputs := make([]model.GroupItemInput, len(members))
	for i, member := range members {
		inputs[i] = model.GroupItemInput{ChannelGrantID: member.grantID}
	}
	return syncGroupItems(tx, groupID, inputs)
}

// GroupDel 删除分组及其成员，成员删除不会影响被其他分组引用的渠道授权。
func GroupDel(id int, ctx context.Context) error {
	group, ok := groupCache.Get(id)
	if !ok {
		return fmt.Errorf("group not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.Group{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}
	groupCache.Del(id)
	groupNameIndex.Del(group.Name)
	return nil
}

// groupRefreshCache 从数据库刷新完整分组缓存和名称索引。
// 缓存只存库内行, 成员的名称与可用性在读取时由 groupSnapshot 现算: 它们随渠道与凭据变化,
// 存进缓存就得在每次渠道改动后跟着刷新一遍。
func groupRefreshCache(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Find(&groups).Error; err != nil {
		return err
	}
	groupCache.Clear()
	groupNameIndex.Clear()
	for _, group := range groups {
		sortGroupItems(group.Items)
		groupCache.Set(group.ID, group)
		groupNameIndex.Set(group.Name, group.ID)
	}
	return nil
}

// sortGroupItems 按优先级和主键生成稳定的成员顺序。
func sortGroupItems(items []model.GroupItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}

// groupSnapshot 为成员补齐授权两侧的名称, 所属渠道与可用性。
// 可用性在此一次定稿: 渠道与凭据均启用且模型, 凭据均存在时可转发, 否则仍列出该成员但标记不可用,
// 由此界面无需再按渠道列表回查, 也不会出现前后端各判一套的分歧。
func groupSnapshot(group model.Group) model.Group {
	// 成员恒为数组: 读取侧承诺该字段不为 null, 空分组也要给出空数组。
	group.Items = append(make([]model.GroupItem, 0, len(group.Items)), group.Items...)
	for i := range group.Items {
		grant, ok := channelGrantCache.Get(group.Items[i].ChannelGrantID)
		if !ok {
			continue
		}
		group.Items[i].Protocols = grant.Protocols
		channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
		channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
		if !modelOK || !keyOK {
			continue
		}
		group.Items[i].ChannelID = channelModel.ChannelID
		group.Items[i].ModelName = channelModel.Name
		group.Items[i].KeyName = channelKey.Name
		channel, channelOK := channelCache.Get(channelModel.ChannelID)
		if !channelOK {
			continue
		}
		group.Items[i].ChannelName = channel.Name
		group.Items[i].Available = channel.Enabled && channelKey.Enabled
	}
	return group
}
