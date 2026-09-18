package op

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

// statsHourlyKey 小时统计的 (date, hour) 复合键, 与 model.StatsHourly 的复合主键同构。
type statsHourlyKey struct {
	date string
	hour int
}

// statsHourlyCache 按 (date, hour) 留存小时统计, 不再是 24 格环形: 昨天的曲线不会被今天覆盖。
var statsHourlyCache = make(map[statsHourlyKey]model.StatsHourly)
var statsHourlyCacheLock sync.RWMutex

var channelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道 ID。
var channelStatsNeedUpdateLock sync.Mutex           // 保护渠道统计累加和待写集合。

var channelModelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道模型 ID。
var channelModelStatsNeedUpdateLock sync.Mutex           // 保护渠道模型统计累加和待写集合。

var channelKeyStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道凭据 ID。
var channelKeyStatsNeedUpdateLock sync.Mutex           // 保护渠道凭据统计累加和待写集合。

var statsAPIKeyCache = cache.New[int, model.StatsAPIKey](16)
var statsAPIKeyCacheNeedUpdate = make(map[int]struct{})
var statsAPIKeyCacheNeedUpdateLock sync.Mutex

func StatsSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log.Debugf("stats save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("stats save db task finished, save time: %s", time.Since(startTime))
	}()
	if err := StatsSaveDB(ctx); err != nil {
		log.Errorf("stats save db error: %v", err)
		return
	}
}

func StatsSaveDB(ctx context.Context) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsDailyCacheLock.RLock()
	dailySnap := statsDailyCache
	statsDailyCacheLock.RUnlock()

	hourlyAll := statsHourlySnapshot()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailySnap, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}
	return nil
}

// statsHourlySnapshot 在锁内深拷贝全部小时行。map 是引用类型, 锁外直接赋值会与并发写入共享底层存储。
func statsHourlySnapshot() []model.StatsHourly {
	statsHourlyCacheLock.RLock()
	defer statsHourlyCacheLock.RUnlock()
	snapshot := make([]model.StatsHourly, 0, len(statsHourlyCache))
	for _, v := range statsHourlyCache {
		snapshot = append(snapshot, v)
	}
	return snapshot
}

// drainDirtySet 取出并清空一个待写集合。
func drainDirtySet(lock *sync.Mutex, set map[int]struct{}) []int {
	lock.Lock()
	defer lock.Unlock()
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
		delete(set, id)
	}
	return ids
}

// restoreDirtySet 把一批主键放回待写集合, 用于持久化失败后重试。
func restoreDirtySet(lock *sync.Mutex, set map[int]struct{}, ids []int) {
	lock.Lock()
	defer lock.Unlock()
	for _, id := range ids {
		set[id] = struct{}{}
	}
}

// restoreStatsDirty 在统计持久化失败后恢复本批待写标记。
func restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs []int) {
	restoreDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate, channelIDs)
	restoreDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate, modelIDs)
	restoreDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate, keyIDs)
	restoreDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate, apiKeyIDs)
}

func persistStatsSnapshots(
	ctx context.Context,
	totalSnap model.StatsTotal,
	dailySnap model.StatsDaily,
	hourlyAll []model.StatsHourly,
	channelIDs []int,
	modelIDs []int,
	keyIDs []int,
	apiKeyIDs []int,
) error {
	dbConn := db.GetDB().WithContext(ctx)

	if result := dbConn.Save(&totalSnap); result.Error != nil {
		return result.Error
	}
	if result := dbConn.Save(&dailySnap); result.Error != nil {
		return result.Error
	}

	// 小时行按 (date, hour) 复合主键 upsert: 每行携带自己的日期, 跨天后新日期自然开新行, 旧行不被覆盖。
	if len(hourlyAll) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "date"}, {Name: "hour"}},
			UpdateAll: true,
		}).Create(&hourlyAll); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range channelIDs {
		channel, ok := channelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.Channel{}).
			Where("id = ?", channel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range modelIDs {
		channelModel, ok := channelModelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelModel{}).
			Where("id = ?", channelModel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelModel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range keyIDs {
		channelKey, ok := channelKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelKey{}).
			Where("id = ?", channelKey.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelKey); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range apiKeyIDs {
		ak, ok := statsAPIKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Save(&ak); result.Error != nil {
			return result.Error
		}
	}

	return nil
}

func statsSaveDBWithDailyOverride(ctx context.Context, dailyOverride model.StatsDaily) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	hourlyAll := statsHourlySnapshot()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailyOverride, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}
	return nil
}

func StatsDailyUpdate(ctx context.Context, metrics model.StatsMetrics) error {
	today := time.Now().Format("20060102")

	statsDailyCacheLock.Lock()
	if statsDailyCache.Date == today {
		statsDailyCache.StatsMetrics.Add(metrics)
		statsDailyCacheLock.Unlock()
		return nil
	}

	prevDaily := statsDailyCache
	statsDailyCache = model.StatsDaily{Date: today}
	statsDailyCache.StatsMetrics.Add(metrics)
	statsDailyCacheLock.Unlock()

	return statsSaveDBWithDailyOverride(ctx, prevDaily)
}

func StatsTotalUpdate(metrics model.StatsMetrics) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	if statsTotalCache.ID == 0 {
		statsTotalCache.ID = 1
	}
	statsTotalCache.StatsMetrics.Add(metrics)
	return nil
}

func StatsHourlyUpdate(metrics model.StatsMetrics) error {
	return statsHourlyUpdateAt(metrics, time.Now())
}

// statsHourlyUpdateAt 把指标累加进 now 所在 (日期, 小时) 的小时行。
// 时间作参数注入, 测试可以覆盖午夜翻日边界; 生产路径恒传 time.Now()。
func statsHourlyUpdateAt(metrics model.StatsMetrics, now time.Time) error {
	key := statsHourlyKey{date: now.Format("20060102"), hour: now.Hour()}

	statsHourlyCacheLock.Lock()
	defer statsHourlyCacheLock.Unlock()

	entry, ok := statsHourlyCache[key]
	if !ok {
		entry = model.StatsHourly{
			Date: key.date,
			Hour: key.hour,
		}
	}
	entry.StatsMetrics.Add(metrics)
	statsHourlyCache[key] = entry
	return nil
}

// ChannelModelStatsUpdate 累加渠道模型统计并标记对应模型待持久化。
func ChannelModelStatsUpdate(channelModelID int, metrics model.StatsMetrics) error {
	channelModelStatsNeedUpdateLock.Lock()
	defer channelModelStatsNeedUpdateLock.Unlock()
	channelModel, ok := channelModelCache.Get(channelModelID)
	if !ok {
		return nil
	}
	channelModel.StatsMetrics.Add(metrics)
	channelModelCache.Set(channelModelID, channelModel)
	channelModelStatsNeedUpdate[channelModelID] = struct{}{}
	return nil
}

// ChannelKeyStatsUpdate 累加渠道凭据统计并标记对应凭据待持久化。
func ChannelKeyStatsUpdate(channelKeyID int, metrics model.StatsMetrics) error {
	channelKeyStatsNeedUpdateLock.Lock()
	defer channelKeyStatsNeedUpdateLock.Unlock()
	channelKey, ok := channelKeyCache.Get(channelKeyID)
	if !ok {
		return nil
	}
	channelKey.StatsMetrics.Add(metrics)
	channelKeyCache.Set(channelKeyID, channelKey)
	channelKeyStatsNeedUpdate[channelKeyID] = struct{}{}
	return nil
}

// ChannelStatsUpdate 累加渠道统计并标记对应渠道待持久化。
func ChannelStatsUpdate(channelID int, metrics model.StatsMetrics) error {
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return nil
	}
	channel.StatsMetrics.Add(metrics)
	channelCache.Set(channelID, channel)
	channelStatsNeedUpdate[channelID] = struct{}{}
	return nil
}

func StatsAPIKeyUpdate(apiKeyID int, metrics model.StatsMetrics) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	apiKeyCache, ok := statsAPIKeyCache.Get(apiKeyID)
	if !ok {
		apiKeyCache = model.StatsAPIKey{
			APIKeyID: apiKeyID,
		}
	}
	apiKeyCache.StatsMetrics.Add(metrics)
	statsAPIKeyCache.Set(apiKeyID, apiKeyCache)
	statsAPIKeyCacheNeedUpdate[apiKeyID] = struct{}{}
	return nil
}

func StatsAPIKeyDel(id int) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	if _, ok := statsAPIKeyCache.Get(id); !ok {
		statsAPIKeyCacheNeedUpdateLock.Unlock()
		return nil
	}
	statsAPIKeyCache.Del(id)
	delete(statsAPIKeyCacheNeedUpdate, id)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	return db.GetDB().Delete(&model.StatsAPIKey{}, id).Error
}

func StatsTotalGet() model.StatsTotal {
	statsTotalCacheLock.RLock()
	defer statsTotalCacheLock.RUnlock()
	return statsTotalCache
}

func StatsAPIKeyGet(id int) model.StatsAPIKey {
	if stats, ok := statsAPIKeyCache.Get(id); ok {
		return stats
	}
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	stats, ok := statsAPIKeyCache.Get(id)
	if !ok {
		tmp := model.StatsAPIKey{
			APIKeyID: id,
		}
		statsAPIKeyCache.Set(id, tmp)
		statsAPIKeyCacheNeedUpdate[id] = struct{}{}
		return tmp
	}
	return stats
}

func StatsAPIKeyList() []model.StatsAPIKey {
	apiKeys := make([]model.StatsAPIKey, 0, statsAPIKeyCache.Len())
	for _, v := range statsAPIKeyCache.GetAll() {
		apiKeys = append(apiKeys, v)
	}
	return apiKeys
}

// StatsHourlyGet 返回 date(YYYYMMDD) 当天 0 点起的小时统计行, 缺失的小时补零值行。
// 今天只给已到达的 hour(实时积累语义), 历史日给全天 24 行(静态快照语义)。
// 数据以内存缓存为准: 启动时全量载入 DB, 运行期小时行都先经缓存落盘。
func StatsHourlyGet(date string) ([]model.StatsHourly, error) {
	if _, err := time.ParseInLocation("20060102", date, time.Local); err != nil {
		return nil, fmt.Errorf("invalid date %q, expect YYYYMMDD", date)
	}

	now := time.Now()
	lastHour := 23
	if date == now.Format("20060102") {
		lastHour = now.Hour()
	}

	statsHourlyCacheLock.RLock()
	defer statsHourlyCacheLock.RUnlock()

	result := make([]model.StatsHourly, 0, lastHour+1)

	for hour := 0; hour <= lastHour; hour++ {
		if entry, ok := statsHourlyCache[statsHourlyKey{date: date, hour: hour}]; ok {
			result = append(result, entry)
		} else {
			result = append(result, model.StatsHourly{
				Hour: hour,
				Date: date,
			})
		}
	}

	return result, nil
}

// StatsHourlyCleanupBefore 删除 date 早于 cutoffDate(YYYYMMDD) 的小时统计行, 并同步淘汰内存缓存。
// 由日志清理任务调用, 与 relay_logs 共用 relay_log_keep_period 保留期口径, 不另开任务。
func StatsHourlyCleanupBefore(ctx context.Context, cutoffDate string) error {
	if err := db.GetDB().WithContext(ctx).Where("date < ?", cutoffDate).Delete(&model.StatsHourly{}).Error; err != nil {
		return err
	}

	statsHourlyCacheLock.Lock()
	for key := range statsHourlyCache {
		// YYYYMMDD 字典序即时间序, 字符串比较即日期比较。
		if key.date < cutoffDate {
			delete(statsHourlyCache, key)
		}
	}
	statsHourlyCacheLock.Unlock()
	return nil
}

// StatsGetDaily 返回 since 当天及其之后的每日统计, since 为 20060102 格式。
// 只取窗口内的数据: 界面上的热力图与趋势图都有固定跨度, 全量返回会随运行时长无界增长。
func StatsGetDaily(ctx context.Context, since string) ([]model.StatsDaily, error) {
	var statsDaily []model.StatsDaily
	result := db.GetDB().WithContext(ctx).Where("date >= ?", since).Order("date").Find(&statsDaily)
	if result.Error != nil {
		return nil, result.Error
	}
	return statsDaily, nil
}

func statsRefreshCache(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	var loadedDaily model.StatsDaily
	result := dbConn.Last(&loadedDaily)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get daily stats: %v", result.Error)
	}
	if result.RowsAffected == 0 || loadedDaily.Date != today {
		loadedDaily = model.StatsDaily{Date: today}
	}

	var loadedTotal model.StatsTotal
	result = dbConn.First(&loadedTotal)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get total stats: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		loadedTotal = model.StatsTotal{ID: 1}
	} else if loadedTotal.ID == 0 {
		loadedTotal.ID = 1
	}

	var loadedHourly []model.StatsHourly
	result = dbConn.Find(&loadedHourly)
	if result.Error != nil {
		return fmt.Errorf("failed to get hourly stats: %v", result.Error)
	}

	statsDailyCacheLock.Lock()
	statsDailyCache = loadedDaily
	statsDailyCacheLock.Unlock()

	statsTotalCacheLock.Lock()
	statsTotalCache = loadedTotal
	statsTotalCacheLock.Unlock()

	var loadedAPIKeys []model.StatsAPIKey
	result = dbConn.Find(&loadedAPIKeys)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key stats: %v", result.Error)
	}

	statsAPIKeyCache.Clear()
	// 就地清空而非重新赋值: drainDirtySet 与 restoreDirtySet 持有该 map 的引用, 换 map 会让它们写到旧对象上。
	statsAPIKeyCacheNeedUpdateLock.Lock()
	clear(statsAPIKeyCacheNeedUpdate)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeys {
		statsAPIKeyCache.Set(v.APIKeyID, v)
	}

	statsHourlyCacheLock.Lock()
	statsHourlyCache = make(map[statsHourlyKey]model.StatsHourly, len(loadedHourly))
	for _, v := range loadedHourly {
		if v.Hour >= 0 && v.Hour < 24 {
			statsHourlyCache[statsHourlyKey{date: v.Date, hour: v.Hour}] = v
		}
	}
	statsHourlyCacheLock.Unlock()

	return nil
}

// StatsRankEntry 按天排名的单条聚合, 渠道榜与模型榜共用。
type StatsRankEntry struct {
	ChannelID int    `json:"channel_id"` // 渠道榜为渠道 ID; 模型榜恒为 0。
	Name      string `json:"name"`       // 渠道榜为渠道名; 模型榜为模型(分组)名。
	model.StatsMetrics
}

// StatsDailyRank 按天排名的聚合结果。Available 为 false 表示该日期提供不了按天数据
// (日志保存关闭或整日落入保留期之外), 前端据此区分「没有流量」与「数据不可用」。
type StatsDailyRank struct {
	Available bool             `json:"available"`
	Channels  []StatsRankEntry `json:"channels"`
	Models    []StatsRankEntry `json:"models"`
}

// relayLogChannelAgg relay_logs 按渠道聚合的扫描行。
type relayLogChannelAgg struct {
	ChannelID    int
	ChannelName  string
	RequestTotal int64
	SuccessTotal int64
	InputToken   int64
	OutputToken  int64
	CostTotal    float64
}

// relayLogModelAgg relay_logs 按模型(分组名)聚合的扫描行。
type relayLogModelAgg struct {
	RequestModelName string
	RequestTotal     int64
	SuccessTotal     int64
	InputToken       int64
	OutputToken      int64
	CostTotal        float64
}

// StatsRankDaily 按选中日从 relay_logs 聚合渠道排名与模型排名, 时间窗为 [当日 0 点, 次日 0 点)。
// 按天数据与日志同一可用口径: 日志保存关闭或整日超出 relay_log_keep_period 保留期时,
// 返回 Available=false 且空数组(数组恒非 nil, 见 api-serialization 规范)。
// relay_logs 行内已冗余渠道 ID/名称与请求模型名, 按天聚合无需 join 任何表。
func StatsRankDaily(ctx context.Context, date string) (*StatsDailyRank, error) {
	dayStart, err := time.ParseInLocation("20060102", date, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q, expect YYYYMMDD", date)
	}
	dayEnd := dayStart.AddDate(0, 0, 1)

	rank := &StatsDailyRank{
		Available: true,
		Channels:  make([]StatsRankEntry, 0),
		Models:    make([]StatsRankEntry, 0),
	}

	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	if !enabled {
		rank.Available = false
		return rank, nil
	}
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return nil, err
	}
	// 与 relayLogCleanup 同口径的删除线: 整日都在保留期之外(次日 0 点 <= cutoff)才视为不可用,
	// 部分落入保留期的日期仍返回现存数据。
	if keepPeriod > 0 && !dayEnd.After(time.Now().Add(-time.Duration(keepPeriod)*24*time.Hour)) {
		rank.Available = false
		return rank, nil
	}

	successExpr := "SUM(CASE WHEN COALESCE(error, '') = '' THEN 1 ELSE 0 END)"
	timeRange := "time >= ? AND time < ?"

	var channelRows []relayLogChannelAgg
	// GROUP BY 只按 channel_id: 同日改名的渠道聚合为一行, 名称取字典序最大值——
	// 若把名称并入分组键, 改名会把同渠道拆成两行, 且前端以 channel_id 作 React key 会撞键。
	if err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Select("channel_id, MAX(channel_name) AS channel_name, COUNT(*) AS request_total, "+successExpr+" AS success_total, "+
			"SUM(input_tokens) AS input_token, SUM(output_tokens) AS output_token, SUM(cost) AS cost_total").
		Where(timeRange, dayStart.Unix(), dayEnd.Unix()).
		Group("channel_id").
		Order("request_total DESC").
		Scan(&channelRows).Error; err != nil {
		return nil, err
	}
	rank.Channels = make([]StatsRankEntry, 0, len(channelRows))
	for _, row := range channelRows {
		entry := StatsRankEntry{ChannelID: row.ChannelID, Name: row.ChannelName}
		entry.InputToken = row.InputToken
		entry.OutputToken = row.OutputToken
		// relay_logs 只有总费用, 记入输入侧; 前端 total_cost = input + output, 合计口径不受影响。
		entry.InputCost = row.CostTotal
		entry.RequestSuccess = row.SuccessTotal
		entry.RequestFailed = row.RequestTotal - row.SuccessTotal
		rank.Channels = append(rank.Channels, entry)
	}

	var modelRows []relayLogModelAgg
	if err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Select("request_model_name, COUNT(*) AS request_total, "+successExpr+" AS success_total, "+
			"SUM(input_tokens) AS input_token, SUM(output_tokens) AS output_token, SUM(cost) AS cost_total").
		Where(timeRange, dayStart.Unix(), dayEnd.Unix()).
		Group("request_model_name").
		Order("request_total DESC").
		Scan(&modelRows).Error; err != nil {
		return nil, err
	}
	rank.Models = make([]StatsRankEntry, 0, len(modelRows))
	for _, row := range modelRows {
		entry := StatsRankEntry{Name: row.RequestModelName}
		entry.InputToken = row.InputToken
		entry.OutputToken = row.OutputToken
		entry.InputCost = row.CostTotal
		entry.RequestSuccess = row.SuccessTotal
		entry.RequestFailed = row.RequestTotal - row.SuccessTotal
		rank.Models = append(rank.Models, entry)
	}

	return rank, nil
}
