package op

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImportConflictPolicy 决定导入时与目标实例同名对象的处理方式。
//
// 身份口径是「天然键」而非主键: 渠道按名称, 凭据与模型按 (渠道, 名称), 分组按名称, API Key 按 key 值。
// 两个实例的主键空间各自独立, 按主键合并会把一个实例的配置写到另一个实例的不相关对象上。
type ImportConflictPolicy string

const (
	// ImportConflictSkip 保留目标实例的同名对象, 只导入目标没有的; 目标已有对象的配置与统计均不改动。
	ImportConflictSkip ImportConflictPolicy = "skip"
	// ImportConflictOverwrite 用备份里的内容覆盖同名对象（含凭据、模型、协议位与统计）。
	ImportConflictOverwrite ImportConflictPolicy = "overwrite"
	// ImportConflictRename 为备份里的同名对象另起名称（追加 -2、-3 …）导入, 两边都保留。
	// 该策略只对有名称的对象生效: 授权、分组成员、顺序行、模型价格无独立名称, 按 Skip 处理;
	// API Key 无唯一名称, 同样按 Skip 处理（重复的 key 值只会让鉴权语义变模糊）。
	ImportConflictRename ImportConflictPolicy = "rename"
)

// ParseImportConflictPolicy 解析策略入参, 空串回落到 Skip。
func ParseImportConflictPolicy(raw string) (ImportConflictPolicy, error) {
	switch ImportConflictPolicy(strings.ToLower(strings.TrimSpace(raw))) {
	case "", ImportConflictSkip:
		return ImportConflictSkip, nil
	case ImportConflictOverwrite:
		return ImportConflictOverwrite, nil
	case ImportConflictRename:
		return ImportConflictRename, nil
	default:
		return "", fmt.Errorf("unknown conflict policy: %q (want skip|overwrite|rename)", raw)
	}
}

// channelImportColumns 覆盖导入需要写入的渠道列（配置 + 统计）; 健康状态由目标实例自行探测, 不随备份迁移。
var channelImportColumns = []string{
	"name", "dialect", "enabled", "base_url",
	"openai_chat_completion_path", "openai_response_path", "anthropic_message_path",
	"proxy", "channel_proxy", "custom_header", "param_override", "match_regex", "health_check_skip",
	"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed",
}

var channelKeyImportColumns = []string{
	"name", "key", "enabled",
	"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed",
}

var channelModelImportColumns = []string{
	"name",
	"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed",
}

var groupImportColumns = []string{"name", "mode", "member_regex", "active_item_id", "relay_config"}

// importRemap 记录备份主键到目标主键的映射; 子表一律用映射后的新主键写入。
type importRemap struct {
	policy ImportConflictPolicy
	res    *model.DBImportResult

	channel map[int]int
	key     map[int]int
	model   map[int]int
	grant   map[int]int
	group   map[int]int
	item    map[int]int
	apiKey  map[int]int
	// pendingActiveItems 记录需要在成员导入完成后回填 active_item_id 的分组: 目标分组主键 -> 备份里的成员主键。
	pendingActiveItems []struct {
		groupID     int
		oldActiveID int
	}
}

func newImportRemap(policy ImportConflictPolicy, res *model.DBImportResult) *importRemap {
	return &importRemap{
		policy:  policy,
		res:     res,
		channel: map[int]int{}, key: map[int]int{}, model: map[int]int{}, grant: map[int]int{},
		group: map[int]int{}, item: map[int]int{}, apiKey: map[int]int{},
	}
}

func (r *importRemap) written(table string, n int) {
	if n > 0 {
		r.res.RowsAffected[table] += int64(n)
	}
}

func (r *importRemap) skipped(table string, n int) {
	if n > 0 {
		r.res.Skipped[table] += int64(n)
	}
}

// uniqueName 在「天然键已被占用」的前提下为对象生成一个不冲突的名称。
func uniqueName(base string, exists func(string) (bool, error)) (string, error) {
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		taken, err := exists(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot derive a unique name for %q", base)
}

// DBImportIncremental 按天然键把备份并入当前实例; 主键冲突不再参与判定, 子表主键一律重映射。
func DBImportIncremental(ctx context.Context, dump *model.DBDump, policy ImportConflictPolicy) (*model.DBImportResult, error) {
	if dump == nil {
		return nil, fmt.Errorf("empty dump")
	}
	if dump.Version != 0 && dump.Version != dbDumpVersion {
		return nil, fmt.Errorf("unsupported dump version: %d", dump.Version)
	}

	// 导入也是写入边界, 渠道配置须与接口提交走同一套规范化: 手改过的备份文件同样可能带空白,
	// 空串或缺省字段, 不在此收敛则读侧要为每个字段各自兜底。
	for i := range dump.Channels {
		config, err := normalizeChannelConfig(dump.Channels[i].ChannelConfig)
		if err != nil {
			return nil, fmt.Errorf("import channel %d: %w", dump.Channels[i].ID, err)
		}
		dump.Channels[i].ChannelConfig = config
	}

	res := &model.DBImportResult{
		RowsAffected: map[string]int64{},
		Skipped:      map[string]int64{},
		Conflict:     string(policy),
	}
	remap := newImportRemap(policy, res)

	conn := db.GetDB().WithContext(ctx)
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := importChannels(tx, dump.Channels, remap); err != nil {
			return fmt.Errorf("import channels: %w", err)
		}
		if err := importChannelKeys(tx, dump.ChannelKeys, remap); err != nil {
			return fmt.Errorf("import channel_keys: %w", err)
		}
		if err := importChannelModels(tx, dump.ChannelModels, remap); err != nil {
			return fmt.Errorf("import channel_models: %w", err)
		}
		if err := importChannelGrants(tx, dump.ChannelGrants, remap); err != nil {
			return fmt.Errorf("import channel_grants: %w", err)
		}
		if err := importGroups(tx, dump.Groups, remap); err != nil {
			return fmt.Errorf("import groups: %w", err)
		}
		if err := importGroupItems(tx, dump.GroupItems, remap); err != nil {
			return fmt.Errorf("import group_items: %w", err)
		}
		if err := importGroupChannelOrders(tx, dump.GroupChannelOrders, remap); err != nil {
			return fmt.Errorf("import group_channel_orders: %w", err)
		}
		if err := remap.resolveActiveItems(tx); err != nil {
			return fmt.Errorf("import groups: %w", err)
		}
		if err := importLLMInfos(tx, dump.LLMInfos, remap); err != nil {
			return fmt.Errorf("import llm_infos: %w", err)
		}
		if err := importAPIKeys(tx, dump.APIKeys, remap); err != nil {
			return fmt.Errorf("import api_keys: %w", err)
		}
		if err := importSettings(tx, dump.Settings, remap); err != nil {
			return fmt.Errorf("import settings: %w", err)
		}
		if err := importStats(tx, dump, remap); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func importChannels(tx *gorm.DB, rows []model.Channel, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		src.Keys, src.Models = nil, nil

		var existing model.Channel
		err := tx.Where("name = ?", src.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0 // 备份主键在目标实例没有意义, 交由数据库重新分配。
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.channel[rows[i].ID] = src.ID
			remap.written("channels", 1)
			continue
		}
		if err != nil {
			return err
		}

		switch remap.policy {
		case ImportConflictOverwrite:
			src.ID = existing.ID
			if err := tx.Model(&model.Channel{}).Where("id = ?", existing.ID).
				Select(channelImportColumns).Updates(&src).Error; err != nil {
				return err
			}
			remap.channel[rows[i].ID] = existing.ID
			remap.written("channels", 1)
		case ImportConflictRename:
			name, err := uniqueName(src.Name, func(name string) (bool, error) {
				var n int64
				if err := tx.Model(&model.Channel{}).Where("name = ?", name).Count(&n).Error; err != nil {
					return false, err
				}
				return n > 0, nil
			})
			if err != nil {
				return err
			}
			src.ID = 0
			src.Name = name
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.channel[rows[i].ID] = src.ID
			remap.written("channels", 1)
		default:
			remap.channel[rows[i].ID] = existing.ID
			remap.skipped("channels", 1)
		}
	}
	return nil
}

func importChannelKeys(tx *gorm.DB, rows []model.ChannelKey, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		channelID, ok := remap.channel[src.ChannelID]
		if !ok {
			remap.skipped("channel_keys", 1)
			continue
		}
		src.ChannelID = channelID

		var existing model.ChannelKey
		err := tx.Where("channel_id = ? AND name = ?", channelID, src.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.key[rows[i].ID] = src.ID
			remap.written("channel_keys", 1)
			continue
		}
		if err != nil {
			return err
		}

		switch remap.policy {
		case ImportConflictOverwrite:
			if err := tx.Model(&model.ChannelKey{}).Where("id = ?", existing.ID).
				Select(channelKeyImportColumns).Updates(&src).Error; err != nil {
				return err
			}
			remap.key[rows[i].ID] = existing.ID
			remap.written("channel_keys", 1)
		case ImportConflictRename:
			name, err := uniqueName(src.Name, func(name string) (bool, error) {
				var n int64
				if err := tx.Model(&model.ChannelKey{}).
					Where("channel_id = ? AND name = ?", channelID, name).Count(&n).Error; err != nil {
					return false, err
				}
				return n > 0, nil
			})
			if err != nil {
				return err
			}
			src.ID = 0
			src.Name = name
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.key[rows[i].ID] = src.ID
			remap.written("channel_keys", 1)
		default:
			remap.key[rows[i].ID] = existing.ID
			remap.skipped("channel_keys", 1)
		}
	}
	return nil
}

func importChannelModels(tx *gorm.DB, rows []model.ChannelModel, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		channelID, ok := remap.channel[src.ChannelID]
		if !ok {
			remap.skipped("channel_models", 1)
			continue
		}
		src.ChannelID = channelID

		var existing model.ChannelModel
		err := tx.Where("channel_id = ? AND name = ?", channelID, src.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.model[rows[i].ID] = src.ID
			remap.written("channel_models", 1)
			continue
		}
		if err != nil {
			return err
		}

		switch remap.policy {
		case ImportConflictOverwrite:
			if err := tx.Model(&model.ChannelModel{}).Where("id = ?", existing.ID).
				Select(channelModelImportColumns).Updates(&src).Error; err != nil {
				return err
			}
			remap.model[rows[i].ID] = existing.ID
			remap.written("channel_models", 1)
		case ImportConflictRename:
			name, err := uniqueName(src.Name, func(name string) (bool, error) {
				var n int64
				if err := tx.Model(&model.ChannelModel{}).
					Where("channel_id = ? AND name = ?", channelID, name).Count(&n).Error; err != nil {
					return false, err
				}
				return n > 0, nil
			})
			if err != nil {
				return err
			}
			src.ID = 0
			src.Name = name
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.model[rows[i].ID] = src.ID
			remap.written("channel_models", 1)
		default:
			remap.model[rows[i].ID] = existing.ID
			remap.skipped("channel_models", 1)
		}
	}
	return nil
}

func importChannelGrants(tx *gorm.DB, rows []model.ChannelGrant, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		modelID, okModel := remap.model[src.ChannelModelID]
		keyID, okKey := remap.key[src.ChannelKeyID]
		if !okModel || !okKey {
			remap.skipped("channel_grants", 1)
			continue
		}
		src.ChannelModelID, src.ChannelKeyID = modelID, keyID
		src.ChannelModel, src.ChannelKey = nil, nil

		var existing model.ChannelGrant
		err := tx.Where("channel_model_id = ? AND channel_key_id = ?", modelID, keyID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.grant[rows[i].ID] = src.ID
			remap.written("channel_grants", 1)
			continue
		}
		if err != nil {
			return err
		}

		// 授权没有独立名称, 重命名无意义: 与 Skip 同义, 只在 Overwrite 下更新协议位。
		if remap.policy == ImportConflictOverwrite {
			if err := tx.Model(&model.ChannelGrant{}).Where("id = ?", existing.ID).
				Select([]string{"protocols"}).Updates(&src).Error; err != nil {
				return err
			}
			remap.written("channel_grants", 1)
		} else {
			remap.skipped("channel_grants", 1)
		}
		remap.grant[rows[i].ID] = existing.ID
	}
	return nil
}

func importGroups(tx *gorm.DB, rows []model.Group, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		src.Items = nil

		var existing model.Group
		err := tx.Where("name = ?", src.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			oldActive := src.ActiveItemID
			src.ID = 0
			src.ActiveItemID = 0 // 成员主键尚未重映射, 待成员导入后回填。
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.group[rows[i].ID] = src.ID
			remap.written("groups", 1)
			if oldActive != 0 {
				remap.pendingActiveItems = append(remap.pendingActiveItems, struct {
					groupID     int
					oldActiveID int
				}{src.ID, oldActive})
			}
			continue
		}
		if err != nil {
			return err
		}

		switch remap.policy {
		case ImportConflictOverwrite:
			oldActive := src.ActiveItemID
			src.ActiveItemID = 0
			if err := tx.Model(&model.Group{}).Where("id = ?", existing.ID).
				Select(groupImportColumns).Updates(&src).Error; err != nil {
				return err
			}
			remap.group[rows[i].ID] = existing.ID
			remap.written("groups", 1)
			if oldActive != 0 {
				remap.pendingActiveItems = append(remap.pendingActiveItems, struct {
					groupID     int
					oldActiveID int
				}{existing.ID, oldActive})
			}
		case ImportConflictRename:
			name, err := uniqueName(src.Name, func(name string) (bool, error) {
				var n int64
				if err := tx.Model(&model.Group{}).Where("name = ?", name).Count(&n).Error; err != nil {
					return false, err
				}
				return n > 0, nil
			})
			if err != nil {
				return err
			}
			oldActive := src.ActiveItemID
			src.ID = 0
			src.Name = name
			src.ActiveItemID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.group[rows[i].ID] = src.ID
			remap.written("groups", 1)
			if oldActive != 0 {
				remap.pendingActiveItems = append(remap.pendingActiveItems, struct {
					groupID     int
					oldActiveID int
				}{src.ID, oldActive})
			}
		default:
			remap.group[rows[i].ID] = existing.ID
			remap.skipped("groups", 1)
		}
	}
	return nil
}

func importGroupItems(tx *gorm.DB, rows []model.GroupItem, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		groupID, okGroup := remap.group[src.GroupID]
		grantID, okGrant := remap.grant[src.ChannelGrantID]
		if !okGroup || !okGrant {
			remap.skipped("group_items", 1)
			continue
		}
		src.GroupID, src.ChannelGrantID = groupID, grantID
		src.ChannelGrant = nil

		var existing model.GroupItem
		err := tx.Where("group_id = ? AND channel_grant_id = ?", groupID, grantID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.item[rows[i].ID] = src.ID
			remap.written("group_items", 1)
			continue
		}
		if err != nil {
			return err
		}
		if remap.policy == ImportConflictOverwrite {
			if err := tx.Model(&model.GroupItem{}).Where("id = ?", existing.ID).
				Select([]string{"priority"}).Updates(&src).Error; err != nil {
				return err
			}
			remap.written("group_items", 1)
		} else {
			remap.skipped("group_items", 1)
		}
		remap.item[rows[i].ID] = existing.ID
	}
	return nil
}

func importGroupChannelOrders(tx *gorm.DB, rows []model.GroupChannelOrder, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		groupID, okGroup := remap.group[src.GroupID]
		channelID, okChan := remap.channel[src.ChannelID]
		if !okGroup || !okChan {
			remap.skipped("group_channel_orders", 1)
			continue
		}
		src.GroupID, src.ChannelID = groupID, channelID
		src.Group, src.Channel = nil, nil

		var existing model.GroupChannelOrder
		err := tx.Where("group_id = ? AND channel_id = ?", groupID, channelID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.written("group_channel_orders", 1)
			continue
		}
		if err != nil {
			return err
		}
		if remap.policy == ImportConflictOverwrite {
			if err := tx.Model(&model.GroupChannelOrder{}).Where("id = ?", existing.ID).
				Select([]string{"position"}).Updates(&src).Error; err != nil {
				return err
			}
			remap.written("group_channel_orders", 1)
		} else {
			remap.skipped("group_channel_orders", 1)
		}
	}
	return nil
}

// resolveActiveItems 在成员重映射完成后回填 manual 分组的当前成员。
func (r *importRemap) resolveActiveItems(tx *gorm.DB) error {
	for _, pending := range r.pendingActiveItems {
		newItemID, ok := r.item[pending.oldActiveID]
		if !ok || newItemID == 0 {
			continue
		}
		if err := tx.Model(&model.Group{}).Where("id = ?", pending.groupID).
			Update("active_item_id", newItemID).Error; err != nil {
			return err
		}
	}
	return nil
}

func importLLMInfos(tx *gorm.DB, rows []model.LLMInfo, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		var existing model.LLMInfo
		err := tx.Where("name = ?", src.Name).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.written("llm_infos", 1)
			continue
		}
		if err != nil {
			return err
		}
		if remap.policy != ImportConflictOverwrite {
			remap.skipped("llm_infos", 1)
			continue
		}
		if err := tx.Model(&model.LLMInfo{}).Where("name = ?", src.Name).
			Select("*").Omit("name").Updates(&src).Error; err != nil {
			return err
		}
		remap.written("llm_infos", 1)
	}
	return nil
}

// importAPIKeys 以 key 值作为身份: 表上没有唯一名称, 同名不同 key 属于两个不同的凭据。
func importAPIKeys(tx *gorm.DB, rows []model.APIKey, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		var existing model.APIKey
		err := tx.Where("api_key = ?", src.APIKey).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			src.ID = 0
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.apiKey[rows[i].ID] = src.ID
			remap.written("api_keys", 1)
			continue
		}
		if err != nil {
			return err
		}
		if remap.policy == ImportConflictOverwrite {
			if err := tx.Model(&model.APIKey{}).Where("id = ?", existing.ID).
				Select([]string{"name", "enabled", "expire_at", "max_cost", "supported_models"}).Updates(&src).Error; err != nil {
				return err
			}
			remap.written("api_keys", 1)
		} else {
			// rename 对 API Key 无意义: 同 key 值复制一份只会让鉴权语义变模糊。
			remap.skipped("api_keys", 1)
		}
		remap.apiKey[rows[i].ID] = existing.ID
	}
	return nil
}

func importSettings(tx *gorm.DB, rows []model.Setting, remap *importRemap) error {
	for i := range rows {
		src := rows[i]
		var existing model.Setting
		err := tx.Where("key = ?", src.Key).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&src).Error; err != nil {
				return err
			}
			remap.written("settings", 1)
			continue
		}
		if err != nil {
			return err
		}
		if remap.policy != ImportConflictOverwrite {
			remap.skipped("settings", 1)
			continue
		}
		if err := tx.Model(&model.Setting{}).Where("key = ?", src.Key).
			Update("value", src.Value).Error; err != nil {
			return err
		}
		remap.written("settings", 1)
	}
	return nil
}

// importStats 统计是聚合账, 始终按既有主键/自然键覆盖写入; API Key 统计需跟随主键重映射。
func importStats(tx *gorm.DB, dump *model.DBDump, remap *importRemap) error {
	if n, err := createUpsertAll(tx, dump.StatsTotal, []clause.Column{{Name: "id"}}); err != nil {
		return fmt.Errorf("import stats_total: %w", err)
	} else {
		remap.written("stats_total", int(n))
	}
	if n, err := createUpsertAll(tx, dump.StatsDaily, []clause.Column{{Name: "date"}}); err != nil {
		return fmt.Errorf("import stats_daily: %w", err)
	} else {
		remap.written("stats_daily", int(n))
	}
	if n, err := createUpsertAll(tx, dump.StatsHourly, []clause.Column{{Name: "date"}, {Name: "hour"}}); err != nil {
		return fmt.Errorf("import stats_hourly: %w", err)
	} else {
		remap.written("stats_hourly", int(n))
	}

	rows := make([]model.StatsAPIKey, 0, len(dump.StatsAPIKey))
	for _, row := range dump.StatsAPIKey {
		newID, ok := remap.apiKey[row.APIKeyID]
		if !ok {
			continue
		}
		row.APIKeyID = newID
		rows = append(rows, row)
	}
	if n, err := createUpsertAll(tx, rows, []clause.Column{{Name: "api_key_id"}}); err != nil {
		return fmt.Errorf("import stats_api_key: %w", err)
	} else {
		remap.written("stats_api_key", int(n))
	}
	return nil
}
