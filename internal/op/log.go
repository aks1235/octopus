package op

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/snowflake"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// relayLogMaxSize 启用日志保存时内存缓冲的条数上限, 达到即批量落库。
const relayLogMaxSize = 20

// relayLogMaxSizeNoDB 未启用日志保存时允许的更大缓存上限, 仅用于实时查询最近日志。
const relayLogMaxSizeNoDB = 100

var relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
var relayLogCacheLock sync.Mutex

var relayLogFlushLock sync.Mutex

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

	// 日志行与其 attempts 规范化行在同一事务内落盘: 渠道调用明细只读 relay_log_attempts,
	// 不允许出现「日志已落库、尝试行缺失」的中间态(否则明细查不到刚发生的调用)。
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(&batch, 100).Error; err != nil {
			return err
		}
		return relayLogAttemptsInsert(tx, batch)
	})
	if err != nil {
		return err
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

// relayLogAttemptsInsert 把一批日志的 attempts 展开成 relay_log_attempts 行写入(须与日志同事务调用)。
// OnConflict DoNothing 以 (log_id, attempt_num) 主键去重: 同一日志重复落盘(重试、回填重跑)不产生重复行, 幂等。
func relayLogAttemptsInsert(tx *gorm.DB, logs []model.RelayLog) error {
	rows := make([]model.RelayLogAttempt, 0, len(logs))
	for _, relayLog := range logs {
		rows = append(rows, relayLogAttemptRows(relayLog)...)
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 200).Error
}

// relayLogAttemptRows 把单条日志的 attempts 展开为规范化行。
// 无 attempts(请求未发往任何上游)时返回 nil, 该日志在明细表中零行。
// 同一日志内 attempt_num 重复的脏数据(仅可能来自历史迁移数据, v2 生成路径按 len+1 递增)按首次出现保留,
// 避免 (log_id, attempt_num) 主键撞键丢行时结果不确定。
func relayLogAttemptRows(relayLog model.RelayLog) []model.RelayLogAttempt {
	if len(relayLog.Attempts) == 0 {
		return nil
	}
	rows := make([]model.RelayLogAttempt, 0, len(relayLog.Attempts))
	seen := make(map[int]struct{}, len(relayLog.Attempts))
	for _, a := range relayLog.Attempts {
		if _, ok := seen[a.AttemptNum]; ok {
			continue
		}
		seen[a.AttemptNum] = struct{}{}
		rows = append(rows, model.RelayLogAttempt{
			LogID:            relayLog.ID,
			AttemptNum:       a.AttemptNum,
			ChannelID:        a.ChannelID,
			Time:             relayLog.Time,
			ChannelName:      a.ChannelName,
			ChannelKeyID:     a.ChannelKeyID,
			ChannelKeyRemark: a.ChannelKeyRemark,
			ModelName:        a.ModelName,
			Status:           a.Status,
			Duration:         a.Duration,
			Sticky:           a.Sticky,
			Msg:              a.Msg,
		})
	}
	return rows
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

// relayLogCleanup 按 relay_log_keep_period(天)清理过期转发日志。
//
// 四处约定:
//  1. 曲线永久: 删日志不再顺带删小时统计行(stats_hourlies 按 (date, hour) 永久留存)。
//  2. 汇总不丢账: 只按「整日」淘汰 —— cutoff 取整到本地日边界, 一次只删整天的日志。
//     这样删之前折叠这些日期时, 其日志必然完整, 折叠出的当日汇总与日志聚合一致;
//     若按滑动时刻删(旧行为), 同一天会被多次部分删除, 折叠只能看到残缺日志, 会把先前
//     折叠好的当日汇总覆盖小 —— 那是丢账。整日淘汰 + 删前折叠 + 折叠幂等, 三者合起来才成立。
//  3. 折叠失败则本轮不删: 宁可日志晚一轮清理, 也不删掉尚未入账的日志; 下一轮幂等重试。
//  4. 尝试行随日志同事务删除: relay_log_attempts 的保留期口径就是日志的保留期, 日志被删则其
//     尝试行一并消失(不留孤儿行); 与日志同事务保证不会出现「日志已删、尝试行还在」的中间态。
func relayLogCleanup(ctx context.Context) error {
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return err
	}

	if keepPeriod <= 0 {
		return nil
	}

	// cutoff 取「now - keep 天」所在本地日的 0 点: 早于该时刻的整天日志整批淘汰。
	cutoffDate := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Format("20060102")
	cutoffDay, err := time.ParseInLocation("20060102", cutoffDate, time.Local)
	if err != nil {
		return err
	}
	cutoff := cutoffDay.Unix()

	// 删日志前先把即将被删的日期折叠进永久汇总表: 汇总表是永久账, 清理不能丢账。
	if err := statsDailyRankFoldBeforeLogPurge(ctx, cutoff); err != nil {
		// 折叠失败不删日志(见上方约定 3), 交由下一轮清理重试。
		return err
	}

	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先删尝试行(子查询仍需命中尚未删除的日志行), 再删日志行。
		// 子查询按 time 取待删日志 id: 单条 SQL 完成, 不受待删行数影响。
		purgedLogIDs := tx.Model(&model.RelayLog{}).Select("id").Where("time < ?", cutoff)
		if err := tx.Where("log_id IN (?)", purgedLogIDs).Delete(&model.RelayLogAttempt{}).Error; err != nil {
			return err
		}
		return tx.Where("time < ?", cutoff).Delete(&model.RelayLog{}).Error
	})
}

// statsDailyRankFoldBeforeLogPurge 折叠所有含「time < cutoff」日志的日期, 供清理前封账。
// cutoff 已在 relayLogCleanup 对齐到本地日边界, 故命中的都是被整天淘汰的日期, 其日志此刻完整。
func statsDailyRankFoldBeforeLogPurge(ctx context.Context, cutoff int64) error {
	dates, err := relayLogDistinctDatesBefore(ctx, cutoff)
	if err != nil {
		return err
	}
	return StatsDailyRankFold(ctx, dates)
}

// relayLogDistinctDatesBefore 返回「存在 time < cutoff 日志」的本地日期集合(YYYYMMDD), 供清理前封账。
//
// 只取最早一条日志的时刻(MIN(time)), 再在 Go 侧按其本地日逐日展开到 cutoff 所在日。
// 刻意不用 SQL 方言折算日期: mysql 的 FROM_UNIXTIME / postgres 的 to_char 取的是**会话/服务器时区**,
// 与 Go 进程的 time.Local 不一致时会多返回一个已封账日期, 折叠该日时日志早已不在、聚合为空,
// 若照常整日 delete+insert 就把永久汇总抹空 —— 那是丢账。MIN 是纯数值比较, 与时区无关。
func relayLogDistinctDatesBefore(ctx context.Context, cutoff int64) ([]string, error) {
	var minTime sql.NullInt64
	if err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Where("time < ?", cutoff).
		Select("MIN(time)").
		Row().Scan(&minTime); err != nil {
		return nil, err
	}
	if !minTime.Valid {
		// 无早于 cutoff 的日志: 没有待封账的日期。
		return nil, nil
	}

	// 归一到本地日 0 点后按日历日推进(AddDate 自动处理 DST), 与 relayLogCleanup 的整日口径一致。
	startOfDay := func(t time.Time) time.Time {
		t = t.In(time.Local)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	}
	day := startOfDay(time.Unix(minTime.Int64, 0))
	last := startOfDay(time.Unix(cutoff, 0))
	dates := make([]string, 0, 8)
	for ; !day.After(last); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day.Format("20060102"))
	}
	return dates, nil
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

// RelayLogClear 清空日志: 内存缓冲、数据库日志行与其 attempts 规范化行一并清空(同事务, 不留孤儿)。
func RelayLogClear(ctx context.Context) error {
	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	relayLogCacheLock.Unlock()
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&model.RelayLogAttempt{}).Error; err != nil {
			return err
		}
		return tx.Where("1 = 1").Delete(&model.RelayLog{}).Error
	})
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
// 数据来源: 规范化表 relay_log_attempts(一行 = 一次尝试, 索引 (channel_id, log_id)),
// 由 relayLogFlushToDB 随日志同事务写入、relayLogAttemptsBackfill 回填历史日志。
// 历史日志全在 DB(relayLogCache 只是 ≤20 条 flush 缓冲, 不覆盖历史)。
//
// 与旧实现(LIKE 粗筛 attempts JSON + Go 层展开)的差异:
//   - 查询只做一次索引过滤 + COUNT + 一页索引排序取行, 代价只与该渠道命中行数相关, 与日志总量无关;
//     不再读 attempts 巨型文本列、不再在 Go 层展开(那正是打开「渠道调用详情」慢的根因)。
//   - 排序键 (time DESC, log_id DESC, attempt_num ASC) 与旧实现的 (request_time DESC, request_id DESC,
//     再按展开顺序即 attempt_num ASC) 等价, 故分页结果与旧实现一致。
//   - total 由 COUNT 精确给出(旧实现是展开后切片长度, 在粗筛触顶时是截断值)。
//   - truncated 恒为 false: 旧语义是「LIKE 粗筛命中 5000 行上界, 可能还有未扫到的行」;
//     分页现在完全在 SQL 层按索引完成、结果不再截断, 该提示不再成立。
//     字段保留仅为维持前端契约(前端仅在 true 时提示"仅展示最近 N 条")。
//
// 未启用日志保存(RelayLogKeepEnabled=false)时 DB 无历史数据, 直接返回空。
func RelayLogAttemptsByChannel(ctx context.Context, channelID int, page, pageSize int) (list []model.ChannelAttemptDetail, total int, truncated bool, err error) {
	enabled, eerr := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if eerr != nil {
		return nil, 0, false, eerr
	}
	if !enabled {
		// 未启用日志保存,DB 无历史 attempts,返回空
		return nil, 0, false, nil
	}

	// step 1:读保留期算 cutoff(照 relayLogCleanup 写法)。keep_period<=0 表示无期限, 不按时间过滤。
	keepPeriod, perr := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if perr != nil {
		return nil, 0, false, perr
	}
	var cutoff int64
	if keepPeriod > 0 {
		cutoff = time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	}

	// 每次重建查询链: Count 与 Find 各用一条独立语句, 不复用同一 *gorm.DB。
	filtered := func() *gorm.DB {
		query := db.GetDB().WithContext(ctx).
			Model(&model.RelayLogAttempt{}).
			Where("channel_id = ?", channelID)
		if cutoff > 0 {
			query = query.Where("time >= ?", cutoff)
		}
		return query
	}

	// step 2:total 精确计数(索引过滤, 不取行)
	var counted int64
	if err = filtered().Count(&counted).Error; err != nil {
		return nil, 0, false, err
	}
	total = int(counted)

	// 分页参数收敛(与旧实现一致)
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
		// 与旧实现一致: 越界页返回 nil 列表(handler 会归一化为空数组)
		return nil, total, false, nil
	}

	// step 3:分页取行(排序键与旧实现等价, 见函数注释)
	var rows []model.RelayLogAttempt
	if err = filtered().
		Order("time DESC, log_id DESC, attempt_num ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, false, err
	}
	if len(rows) == 0 {
		return nil, total, false, nil
	}

	// step 4:请求级字段(request_model/request_error)归属 relay_logs, 按本页命中的 log_id 批量取。
	// 只取三列, 且 IN 主键命中最多 pageSize 个 id, 与日志总量无关。
	logIDs := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.LogID]; ok {
			continue
		}
		seen[row.LogID] = struct{}{}
		logIDs = append(logIDs, row.LogID)
	}
	var logs []model.RelayLog
	if err = db.GetDB().WithContext(ctx).
		Model(&model.RelayLog{}).
		Select("id", "request_model_name", "error").
		Where("id IN ?", logIDs).
		Find(&logs).Error; err != nil {
		return nil, 0, false, err
	}
	logMeta := make(map[int64]model.RelayLog, len(logs))
	for _, relayLog := range logs {
		logMeta[relayLog.ID] = relayLog
	}

	// step 5:拼装响应(字段名与旧实现逐一对齐)
	list = make([]model.ChannelAttemptDetail, 0, len(rows))
	for _, row := range rows {
		// 孤儿行(理论上不存在: 写入与清理都与日志同事务)时请求级字段为空, 明细行本身仍返回。
		meta := logMeta[row.LogID]
		list = append(list, model.ChannelAttemptDetail{
			RequestID:     row.LogID,
			RequestTime:   row.Time,
			RequestModel:  meta.RequestModelName,
			RequestError:  meta.Error,
			AttemptNum:    row.AttemptNum,
			Status:        row.Status,
			ChannelID:     row.ChannelID,
			ChannelName:   row.ChannelName,
			ChannelKeyRem: row.ChannelKeyRemark,
			ModelName:     row.ModelName,
			Duration:      row.Duration,
			Sticky:        row.Sticky,
			Msg:           row.Msg,
		})
	}
	return list, total, false, nil
}

// relayLogAttemptsBackfillBatch 单批回填的日志行数: 批内一次事务提交, 批间可中断续跑。
const relayLogAttemptsBackfillBatch = 500

// relayLogAttemptsBackfillDone 标记本进程内已跑完一轮完整回填。
// 回填只针对「上线前已存在的日志」: R2 之后新日志的尝试行由 relayLogFlushToDB 同步写入, 不会漏,
// 故一次完整回填(走到表尾)即是终点, 之后每个周期直接跳过, 不再扫表。
var relayLogAttemptsBackfillDone atomic.Bool

// relayLogAttemptsBackfillRunning 单飞行保护: 上一轮回填未结束(周期比回填耗时短)时跳过本轮。
var relayLogAttemptsBackfillRunning atomic.Bool

// RelayLogAttemptsBackfill 把现存 relay_logs 的 attempts JSON 展开写入 relay_log_attempts(R6)。
//
// 为什么需要: 规范化表上线时对已存在的日志是空的, 不回填则旧日志的「渠道调用详情」查不到。
//
// 性质:
//   - 幂等: 只挑「本表尚无尝试行」的日志(id 升序 + NOT EXISTS 探针, 走主键索引),
//     批内 OnConflict DoNothing 兜底; 重复执行(含中断后重跑、并发重跑)不产生重复行。
//   - 可重入/分批: 每批 relayLogAttemptsBackfillBatch 条日志一个事务, 按 id 水位推进;
//     ctx 预算用尽就返回(fn 返回 nil 不算错误), 下个周期从表头重新探测续跑(已回填的行被索引探针跳过)。
//   - 幂等键: 同一日志的尝试行一旦齐备, 后续重跑只做探针不做写入, 稳态下每次启动只多一次表扫描。
//
// 不阻塞启动: 由 task.Init 注册为 runOnStart 的后台任务(异步 goroutine), 不参与 InitDB/HTTP 启动路径。
func RelayLogAttemptsBackfill(ctx context.Context) error {
	if relayLogAttemptsBackfillDone.Load() {
		return nil
	}
	if !relayLogAttemptsBackfillRunning.CompareAndSwap(false, true) {
		return nil
	}
	defer relayLogAttemptsBackfillRunning.Store(false)

	watermark := int64(0)
	for {
		if ctx.Err() != nil {
			// 本轮时间预算用尽: 未置 done, 下个周期接着回填。
			return nil
		}

		var logs []model.RelayLog
		err := db.GetDB().WithContext(ctx).
			Model(&model.RelayLog{}).
			Select("id", "time", "attempts").
			Where("id > ?", watermark).
			// 空 attempts 的日志(请求未发往任何上游)在明细表中本就零行, 不必读出大文本列再展开。
			Where("attempts IS NOT NULL AND attempts != '' AND attempts != '[]' AND attempts != 'null'").
			// 已有尝试行的日志跳过(其主键 log_id 为索引首列, 探针为索引命中, 不展开 JSON)。
			Where("NOT EXISTS (SELECT 1 FROM relay_log_attempts WHERE relay_log_attempts.log_id = relay_logs.id)").
			Order("id").
			Limit(relayLogAttemptsBackfillBatch).
			Find(&logs).Error
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			relayLogAttemptsBackfillDone.Store(true)
			return nil
		}

		if err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return relayLogAttemptsInsert(tx, logs)
		}); err != nil {
			return err
		}

		watermark = logs[len(logs)-1].ID
		if len(logs) < relayLogAttemptsBackfillBatch {
			relayLogAttemptsBackfillDone.Store(true)
			return nil
		}
	}
}
