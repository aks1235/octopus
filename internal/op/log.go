package op

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/snowflake"
	"github.com/charmbracelet/log"
)

// relayLogMaxSize 启用日志保存时内存缓冲的条数上限, 达到即批量落库。
const relayLogMaxSize = 20

// relayLogMaxSizeNoDB 未启用日志保存时允许的更大缓存上限, 仅用于实时查询最近日志。
const relayLogMaxSizeNoDB = 100

var relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
var relayLogCacheLock sync.Mutex

var relayLogFlushLock sync.Mutex

// relayLogChannelAttemptsScanLimit 粗筛阶段从 DB 拉回的最大行数上界。
// attempts 是 JSON serializer 字段无法 SQL 展开,需粗筛缩小行集后再 Go 层精确展开。
// 粗筛后超过此上界的部分被截断,前端以 truncated 标志提示"仅展示最近 N 条"。
const relayLogChannelAttemptsScanLimit = 5000

func relayLogFlushToDB(ctx context.Context) error {
	relayLogFlushLock.Lock()
	defer relayLogFlushLock.Unlock()

	relayLogCacheLock.Lock()
	if len(relayLogCache) == 0 {
		relayLogCacheLock.Unlock()
		return nil
	}
	batch := make([]model.RelayLog, len(relayLogCache))
	copy(batch, relayLogCache)
	flushedUpto := len(batch)
	relayLogCacheLock.Unlock()

	result := db.GetDB().WithContext(ctx).CreateInBatches(&batch, 100)
	if result.Error != nil {
		return result.Error
	}

	relayLogCacheLock.Lock()
	if len(relayLogCache) >= flushedUpto {
		relayLogCache = relayLogCache[flushedUpto:]
	} else {
		relayLogCache = relayLogCache[:0]
	}
	if len(relayLogCache) == 0 {
		relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	}
	relayLogCacheLock.Unlock()

	return nil
}

// RelayLogAdd 写入一条转发日志: 启用保存时先进缓冲, 满 relayLogMaxSize 条批量落库;
// 未启用保存时仅保留内存中最近 relayLogMaxSizeNoDB 条供列表查询。
func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) error {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}
	maxSize := relayLogMaxSize
	if !enabled {
		maxSize = relayLogMaxSizeNoDB
	}
	relayLog.ID = snowflake.GenerateID()

	relayLogCacheLock.Lock()
	relayLogCache = append(relayLogCache, relayLog)
	if len(relayLogCache) >= maxSize {
		if enabled {
			relayLogCacheLock.Unlock()
			return relayLogFlushToDB(ctx)
		}
		// 如果未启用日志保存，移除最旧的日志，保留最新的日志用于实时查询
		keepSize := maxSize / 2
		if len(relayLogCache) > keepSize {
			relayLogCache = relayLogCache[len(relayLogCache)-keepSize:]
		}
	}
	relayLogCacheLock.Unlock()
	return nil
}

// RelayLogSaveDBTask 周期落盘任务: 满页缓冲兜底 flush + 按保留期清理过期行。
// 缓冲满 20 条的主动 flush 是主路径, 本任务只兜低流量时段的滞留与过期清理。
func RelayLogSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := relayLogSaveDB(ctx); err != nil {
		log.Warnf("failed to save relay logs: %v", err)
	}
}

func relayLogSaveDB(ctx context.Context) error {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}

	if enabled {
		if err := relayLogFlushToDB(ctx); err != nil {
			return err
		}
		return relayLogCleanup(ctx)
	}

	// 如果未启用日志保存，检查缓存大小，如果超过限制则清理旧日志
	relayLogCacheLock.Lock()
	if len(relayLogCache) > relayLogMaxSizeNoDB {
		keepSize := relayLogMaxSizeNoDB / 2
		relayLogCache = relayLogCache[len(relayLogCache)-keepSize:]
	}
	relayLogCacheLock.Unlock()

	return nil
}

func relayLogCleanup(ctx context.Context) error {
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return err
	}

	if keepPeriod <= 0 {
		return nil
	}

	cutoffTime := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	return db.GetDB().WithContext(ctx).Where("time < ?", cutoffTime).Delete(&model.RelayLog{}).Error
}

// RelayLogList 查询日志列表，支持可选的时间范围过滤和错误筛选
// startTime 和 endTime 为 nil 时表示不限制时间范围
// hasError 为 true 时只返回有错误信息的日志
// 不返回 request_content 和 response_content 以提升性能
func RelayLogList(ctx context.Context, startTime, endTime *int, page, pageSize int, hasError bool, apiKeyNames []string, modelNames []string) ([]model.RelayLog, error) {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	hasTimeFilter := startTime != nil && endTime != nil

	// 获取缓存中符合条件的日志（排除大字段）
	relayLogCacheLock.Lock()
	var cachedLogs []model.RelayLog
	for _, log := range relayLogCache {
		if hasTimeFilter {
			if log.Time < int64(*startTime) || log.Time > int64(*endTime) {
				continue
			}
		}
		// hasError 筛选：只保留有非空错误信息的日志
		if hasError && strings.TrimSpace(log.Error) == "" {
			continue
		}
		// apiKeyNames 筛选
		if len(apiKeyNames) > 0 {
			matched := false
			for _, name := range apiKeyNames {
				if log.RequestAPIKeyName == name {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		// modelNames 筛选
		if len(modelNames) > 0 {
			matched := false
			for _, name := range modelNames {
				if log.RequestModelName == name {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		log.RequestContent = ""
		log.ResponseContent = ""
		log.DebugContent = ""
		cachedLogs = append(cachedLogs, log)
	}
	relayLogCacheLock.Unlock()

	// 反转缓存日志顺序（原本新的在末尾，反转后新的在前面，方便分页）
	for i, j := 0, len(cachedLogs)-1; i < j; i, j = i+1, j-1 {
		cachedLogs[i], cachedLogs[j] = cachedLogs[j], cachedLogs[i]
	}

	cacheCount := len(cachedLogs)
	offset := (page - 1) * pageSize

	var result []model.RelayLog

	// 先从缓存中取（缓存是最新的日志）
	if offset < cacheCount {
		cacheEnd := offset + pageSize
		if cacheEnd > cacheCount {
			cacheEnd = cacheCount
		}
		result = append(result, cachedLogs[offset:cacheEnd]...)
	}

	// 如果启用了日志保存，缓存不够时从数据库补充
	if enabled {
		remaining := pageSize - len(result)
		if remaining > 0 {
			dbOffset := 0
			if offset > cacheCount {
				dbOffset = offset - cacheCount
			}

			query := db.GetDB().WithContext(ctx).Omit("request_content", "response_content", "debug_content")
			if hasTimeFilter {
				query = query.Where("time >= ? AND time <= ?", *startTime, *endTime)
			}
			if hasError {
				// 与缓存路径保持一致：排除空白错误
				query = query.Where("error IS NOT NULL AND error != '' AND TRIM(error) != ''")
			}
			if len(apiKeyNames) > 0 {
				query = query.Where("request_api_key_name IN ?", apiKeyNames)
			}
			if len(modelNames) > 0 {
				query = query.Where("request_model_name IN ?", modelNames)
			}

			var dbLogs []model.RelayLog
			if err := query.Order("id DESC").Offset(dbOffset).Limit(remaining).Find(&dbLogs).Error; err != nil {
				return nil, err
			}
			result = append(result, dbLogs...)
		}
	}

	return result, nil
}

// RelayLogClear 清空日志: 内存缓冲与数据库一并清空。
func RelayLogClear(ctx context.Context) error {
	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	relayLogCacheLock.Unlock()
	return db.GetDB().WithContext(ctx).Where("1 = 1").Delete(&model.RelayLog{}).Error
}

// RelayLogGet 获取单条日志详情（含完整的 request_content 和 response_content）
func RelayLogGet(ctx context.Context, id int64) (*model.RelayLog, error) {
	// 先从缓存中查找
	relayLogCacheLock.Lock()
	for _, log := range relayLogCache {
		if log.ID == id {
			result := log
			relayLogCacheLock.Unlock()
			return &result, nil
		}
	}
	relayLogCacheLock.Unlock()

	// 缓存未命中，从数据库查询
	var log model.RelayLog
	if err := db.GetDB().WithContext(ctx).Where("id = ?", id).First(&log).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

// RelayLogAttemptsByChannel 按渠道 ID 返回其在保留期内被尝试的每次调用明细。
//
// 数据来源:relay_logs.attempts(JSON serializer 字段)。历史日志全在 DB
// (relayLogCache 只是 ≤20 条 flush 缓冲,不覆盖历史)。
//
// 策略「先限缩再展开」:
//  1. 读 relay_log_keep_period 算 cutoff;
//  2. 按 channel_id 粗筛:attempts LIKE '%"channel_id":<id>%' 三库统一(SQLite/MySQL/PG 均走 LIKE,
//     不依赖 dialect jsonb)。注意整数字段无引号、无空格,模板需与 jsoniter 实际序列化字节匹配。
//     LIKE 粗筛可能误匹配前缀相同的大 ID(如 90 误匹配 900),由 step 3 Go 层精确过滤兜底;
//  3. Go 层遍历 GORM 已反序列化的 attempts → 过滤 a.ChannelID == channelID(本次尝试渠道,非 relay_logs.channel 最终渠道)
//     → 拼装 ChannelAttemptDetail(带 request_id 溯源);
//  4. 按 request_time 倒序,分页用展开后 matches 切片(total = len(matches),精确);
//  5. 粗筛行数超 relayLogChannelAttemptsScanLimit 截断,truncated=true。
//
// 未启用日志保存(RelayLogKeepEnabled=false)时 DB 无历史数据,直接返回空。
func RelayLogAttemptsByChannel(ctx context.Context, channelID int, page, pageSize int) (list []model.ChannelAttemptDetail, total int, truncated bool, err error) {
	enabled, eerr := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if eerr != nil {
		return nil, 0, false, eerr
	}
	if !enabled {
		// 未启用日志保存,DB 无历史 attempts,返回空
		return nil, 0, false, nil
	}

	// step 1:读保留期算 cutoff(照 relayLogCleanup 写法)
	keepPeriod, perr := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if perr != nil {
		return nil, 0, false, perr
	}
	if keepPeriod <= 0 {
		// 无保留期限制:不按时间过滤(仍受粗筛行数上界保护)
		keepPeriod = 0
	}
	var cutoff int64
	if keepPeriod > 0 {
		cutoff = time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	}

	// step 2:粗筛拉回候选行(仅取展开所需列,避开大字段 request_content 等)
	likePattern := fmt.Sprintf(`%%"channel_id":%d%%`, channelID)
	query := db.GetDB().WithContext(ctx).
		Model(&model.RelayLog{}).
		Select("id, time, request_model_name, error, attempts")
	if cutoff > 0 {
		query = query.Where("time >= ?", cutoff)
	}
	query = query.Where("attempts LIKE ?", likePattern).
		Order("time DESC").
		Limit(relayLogChannelAttemptsScanLimit)

	var rows []model.RelayLog
	if err = query.Find(&rows).Error; err != nil {
		return nil, 0, false, err
	}

	// 粗筛是否触顶:命中上界即视为可能还有未扫到的行
	if len(rows) >= relayLogChannelAttemptsScanLimit {
		truncated = true
	}

	// step 3:Go 层展开并精确过滤
	matches := make([]model.ChannelAttemptDetail, 0, len(rows))
	for _, log := range rows {
		// GORM serializer:json 已在 Find 时将 attempts 列反序列化为 log.Attempts,
		// 此处无需再 json.Unmarshal,直接遍历过滤。
		for _, a := range log.Attempts {
			if a.ChannelID != channelID {
				// 粗筛误匹配(如 90 误匹配 900)在此被精确过滤
				continue
			}
			matches = append(matches, model.ChannelAttemptDetail{
				RequestID:     log.ID,
				RequestTime:   log.Time,
				RequestModel:  log.RequestModelName,
				RequestError:  log.Error,
				AttemptNum:    a.AttemptNum,
				Status:        a.Status,
				ChannelID:     a.ChannelID,
				ChannelName:   a.ChannelName,
				ChannelKeyRem: a.ChannelKeyRemark,
				ModelName:     a.ModelName,
				Duration:      a.Duration,
				Sticky:        a.Sticky,
				Msg:           a.Msg,
			})
		}
	}

	// step 4:按 request_time 倒序(同时间按 request_id 倒序稳定),分页
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].RequestTime != matches[j].RequestTime {
			return matches[i].RequestTime > matches[j].RequestTime
		}
		return matches[i].RequestID > matches[j].RequestID
	})
	total = len(matches)

	// 分页参数收敛
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	offset := (page - 1) * pageSize
	if offset >= total {
		return nil, total, truncated, nil
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	return matches[offset:end], total, truncated, nil
}
