package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// setupStatsTestDB 建独立测试库并重置统计缓存, 各测试互不串台。
func setupStatsTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "stats-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}

	// 重置进程级统计缓存为当天新行, 避免同包其他测试留下的脏行串台(与 InitCache 空库行为一致)。
	statsDailyCacheLock.Lock()
	statsDailyCache = model.StatsDaily{Date: time.Now().Format("20060102")}
	statsDailyCacheLock.Unlock()
	statsHourlyCacheLock.Lock()
	statsHourlyCache = make(map[statsHourlyKey]model.StatsHourly)
	statsHourlyCacheLock.Unlock()
}

// at 构造 daysAgo 天前、hour 点整的本地时间, 供跨天边界用例注入。
func at(daysAgo, hour int) time.Time {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -daysAgo)
}

// TestStatsHourlyUpdateAt_crossDayKeepsBothRows 锁定跨天边界:
// 不同日期同一 hour 各自成行, 翻日写新行不覆盖旧行。
func TestStatsHourlyUpdateAt_crossDayKeepsBothRows(t *testing.T) {
	setupStatsTestDB(t)

	// 今天这一行用当前真实 hour, 保证它已到达(StatsHourlyGet 对今天只给已到达的小时)。
	hour := time.Now().Hour()
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 3, InputToken: 30}, at(1, hour)); err != nil {
		t.Fatalf("update yesterday: %v", err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 5, InputToken: 50}, at(0, hour)); err != nil {
		t.Fatalf("update today: %v", err)
	}

	statsHourlyCacheLock.RLock()
	lenCache := len(statsHourlyCache)
	statsHourlyCacheLock.RUnlock()
	if lenCache != 2 {
		t.Fatalf("cache rows = %d, want 2 (跨天同 hour 必须是两行)", lenCache)
	}

	yesterday, err := StatsHourlyGet(at(1, 0).Format("20060102"))
	if err != nil {
		t.Fatalf("get yesterday: %v", err)
	}
	if len(yesterday) != 24 {
		t.Fatalf("yesterday rows = %d, want 24", len(yesterday))
	}
	if yesterday[hour].RequestSuccess != 3 || yesterday[hour].InputToken != 30 {
		t.Fatalf("yesterday hour%d = %+v, want unmodified yesterday row", hour, yesterday[hour])
	}

	today, err := StatsHourlyGet(at(0, 0).Format("20060102"))
	if err != nil {
		t.Fatalf("get today: %v", err)
	}
	if len(today) != hour+1 { // 0..当前 hour, 今天未到达的 hour 无行
		t.Fatalf("today rows = %d, want %d", len(today), hour+1)
	}
	if today[hour].RequestSuccess != 5 || today[hour].InputToken != 50 {
		t.Fatalf("today hour%d = %+v, want today's own row", hour, today[hour])
	}
}

// TestStatsSaveDB_persistsPerDateHour 锁定落盘语义: 两天各自 upsert, 重复落盘不产生重复行。
func TestStatsSaveDB_persistsPerDateHour(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()

	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 1, InputToken: 10}, at(1, 8)); err != nil {
		t.Fatalf("update yesterday: %v", err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 2, InputToken: 20}, at(0, 8)); err != nil {
		t.Fatalf("update today: %v", err)
	}
	if err := StatsSaveDB(ctx); err != nil {
		t.Fatalf("StatsSaveDB() error = %v", err)
	}

	// 再次累加后落盘: 同 (date, hour) 行更新而不是新增。
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 1, InputToken: 5}, at(0, 8)); err != nil {
		t.Fatalf("update today again: %v", err)
	}
	if err := StatsSaveDB(ctx); err != nil {
		t.Fatalf("StatsSaveDB() second error = %v", err)
	}

	var rows []model.StatsHourly
	if err := db.GetDB().WithContext(ctx).Order("date, hour").Find(&rows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("db rows = %d, want 2", len(rows))
	}
	if rows[0].Date != at(1, 0).Format("20060102") || rows[0].RequestSuccess != 1 || rows[0].InputToken != 10 {
		t.Fatalf("yesterday row = %+v, want 1 success/10 input", rows[0])
	}
	if rows[1].Date != at(0, 0).Format("20060102") || rows[1].RequestSuccess != 3 || rows[1].InputToken != 25 {
		t.Fatalf("today row = %+v, want 3 success/25 input after upsert", rows[1])
	}
}

// TestStatsHourlyGet_zeroFillsMissingHours 锁定 Get(date): 历史日 0-24 全行, 缺失小时补零值行。
func TestStatsHourlyGet_zeroFillsMissingHours(t *testing.T) {
	setupStatsTestDB(t)

	date := at(1, 0).Format("20060102")
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestFailed: 2}, at(1, 23)); err != nil {
		t.Fatalf("update: %v", err)
	}

	rows, err := StatsHourlyGet(date)
	if err != nil {
		t.Fatalf("StatsHourlyGet() error = %v", err)
	}
	if len(rows) != 24 {
		t.Fatalf("rows = %d, want 24", len(rows))
	}
	if rows[0].Hour != 0 || rows[0].Date != date || rows[0].RequestFailed != 0 {
		t.Fatalf("row 0 = %+v, want zero-filled row for missing hour", rows[0])
	}
	if rows[23].RequestFailed != 2 {
		t.Fatalf("row 23 = %+v, want the written row", rows[23])
	}

	// 非法日期直接报错, 不返回半截数据。
	if _, err := StatsHourlyGet("2026-09-18"); err == nil {
		t.Fatalf("expected error for invalid date")
	}
}

// seedRankTestLogs 向 relay_logs 写入按天排名的样本日志: 昨天(alpha 两笔一成一败 + beta 一笔)、
// 前天(alpha 一笔), 覆盖渠道榜与模型榜的分组、成败与费用求和口径。
func seedRankTestLogs(t *testing.T) (yDate, bDate string) {
	t.Helper()
	yDate = at(1, 0).Format("20060102")
	bDate = at(2, 0).Format("20060102")
	logs := []model.RelayLog{
		{ID: 1, Time: at(1, 9).Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 100, OutputTokens: 10, Cost: 1.5},
		{ID: 2, Time: at(1, 10).Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 50, OutputTokens: 5, Cost: 0.5, Error: "boom"},
		{ID: 3, Time: at(1, 11).Unix(), ChannelId: 2, ChannelName: "beta", RequestModelName: "claude-3",
			InputTokens: 200, OutputTokens: 20, Cost: 2.0},
		{ID: 4, Time: at(2, 9).Unix(), ChannelId: 1, ChannelName: "alpha", RequestModelName: "gpt-4o",
			InputTokens: 999, OutputTokens: 99, Cost: 9.9},
	}
	if err := db.GetDB().CreateInBatches(&logs, 10).Error; err != nil {
		t.Fatalf("seed relay logs: %v", err)
	}
	return yDate, bDate
}

// TestStatsDailyRankFold_matchesLogsAndIsIdempotent 锁定 R3:
// 折叠出的当日汇总与 relay_logs 聚合一致(含渠道按 channel_id 归组、模型按分组名归组),
// 且重复折叠行数与数值不变(整体替换语义, 不累加、不重复)。
func TestStatsDailyRankFold_matchesLogsAndIsIdempotent(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()
	yDate, bDate := seedRankTestLogs(t)

	if err := StatsDailyRankFold(ctx, []string{yDate, bDate}); err != nil {
		t.Fatalf("StatsDailyRankFold() error = %v", err)
	}

	loadChannels := func() []model.StatsChannelDaily {
		var rows []model.StatsChannelDaily
		if err := db.GetDB().WithContext(ctx).Where("date = ?", yDate).Order("channel_id").Find(&rows).Error; err != nil {
			t.Fatalf("load channel dailies: %v", err)
		}
		return rows
	}
	loadModels := func() []model.StatsModelDaily {
		var rows []model.StatsModelDaily
		if err := db.GetDB().WithContext(ctx).Where("date = ?", yDate).Order("model_name").Find(&rows).Error; err != nil {
			t.Fatalf("load model dailies: %v", err)
		}
		return rows
	}

	channels := loadChannels()
	if len(channels) != 2 {
		t.Fatalf("channel rows = %d, want 2", len(channels))
	}
	alpha, beta := channels[0], channels[1]
	if alpha.ChannelID != 1 || alpha.ChannelName != "alpha" || alpha.RequestSuccess != 1 || alpha.RequestFailed != 1 ||
		alpha.InputToken != 150 || alpha.OutputToken != 15 || alpha.InputCost != 2.0 {
		t.Fatalf("alpha = %+v, want 1成功1失败/150/15/2.0", alpha)
	}
	if beta.ChannelID != 2 || beta.ChannelName != "beta" || beta.RequestSuccess != 1 || beta.RequestFailed != 0 ||
		beta.InputToken != 200 || beta.OutputToken != 20 {
		t.Fatalf("beta = %+v, want 1成功/200/20", beta)
	}

	models := loadModels()
	if len(models) != 2 {
		t.Fatalf("model rows = %d, want 2", len(models))
	}
	if models[0].ModelName != "claude-3" || models[0].InputToken != 200 {
		t.Fatalf("claude-3 = %+v, want 200 input", models[0])
	}
	if models[1].ModelName != "gpt-4o" || models[1].RequestSuccess != 1 || models[1].RequestFailed != 1 || models[1].InputToken != 150 {
		t.Fatalf("gpt-4o = %+v, want 1成功1失败/150", models[1])
	}

	// 前天只有 alpha 一笔: 两日互不串台。
	var before []model.StatsChannelDaily
	if err := db.GetDB().WithContext(ctx).Where("date = ?", bDate).Find(&before).Error; err != nil {
		t.Fatalf("load day-before: %v", err)
	}
	if len(before) != 1 || before[0].ChannelID != 1 || before[0].InputToken != 999 {
		t.Fatalf("day-before rows = %+v, want only alpha 999", before)
	}

	// 幂等: 同一日期重复折叠, 行数与数值不变。
	if err := StatsDailyRankFold(ctx, []string{yDate, bDate}); err != nil {
		t.Fatalf("second fold error = %v", err)
	}
	channelsAgain := loadChannels()
	if len(channelsAgain) != len(channels) {
		t.Fatalf("channel rows after refold = %d, want %d (幂等)", len(channelsAgain), len(channels))
	}
	if channelsAgain[0] != channels[0] || channelsAgain[1] != channels[1] {
		t.Fatalf("channel rows changed after refold: %+v -> %+v", channels, channelsAgain)
	}
	modelsAgain := loadModels()
	if len(modelsAgain) != len(models) || modelsAgain[0] != models[0] || modelsAgain[1] != models[1] {
		t.Fatalf("model rows changed after refold: %+v -> %+v", models, modelsAgain)
	}
}

// TestStatsDailyRankFold_replacesAfterLogChange 锁定整体替换: 折叠后新增日志再折叠,
// 汇总行按最新日志整体重算而非累加(同一渠道仍只有一行)。
func TestStatsDailyRankFold_replacesAfterLogChange(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()
	yDate, _ := seedRankTestLogs(t)

	if err := StatsDailyRankFold(ctx, []string{yDate}); err != nil {
		t.Fatalf("fold error = %v", err)
	}

	extra := model.RelayLog{ID: 5, Time: at(1, 12).Unix(), ChannelId: 1, ChannelName: "alpha",
		RequestModelName: "gpt-4o", InputTokens: 7, OutputTokens: 1, Cost: 0.1}
	if err := db.GetDB().Create(&extra).Error; err != nil {
		t.Fatalf("add log: %v", err)
	}
	if err := StatsDailyRankFold(ctx, []string{yDate}); err != nil {
		t.Fatalf("refold error = %v", err)
	}

	var rows []model.StatsChannelDaily
	if err := db.GetDB().WithContext(ctx).Where("date = ? AND channel_id = 1", yDate).Find(&rows).Error; err != nil {
		t.Fatalf("load alpha: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("alpha rows = %d, want 1 (整体替换不累加)", len(rows))
	}
	if rows[0].InputToken != 157 || rows[0].RequestSuccess != 2 {
		t.Fatalf("alpha = %+v, want 157 input / 2 success", rows[0])
	}
}

// TestStatsRankDaily_fallsBackToLogsThenReadsDaily 锁定 R5 读路径:
// 尚无汇总行时回退实时聚合 relay_logs; 折叠入库并删日志后仍能从永久汇总表读出(账不丢)。
func TestStatsRankDaily_fallsBackToLogsThenReadsDaily(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()
	yDate, _ := seedRankTestLogs(t)

	// 尚无汇总行: 回退实时聚合。
	fromLogs, err := StatsRankDaily(ctx, yDate)
	if err != nil {
		t.Fatalf("StatsRankDaily() error = %v", err)
	}
	if !fromLogs.Available || len(fromLogs.Channels) != 2 {
		t.Fatalf("fallback rank = %+v, want available with 2 channels", fromLogs)
	}

	if err := StatsDailyRankFold(ctx, []string{yDate}); err != nil {
		t.Fatalf("fold error = %v", err)
	}
	// 模拟日志清理: 删光 relay_logs。
	if err := db.GetDB().WithContext(ctx).Where("1 = 1").Delete(&model.RelayLog{}).Error; err != nil {
		t.Fatalf("purge logs: %v", err)
	}

	fromDaily, err := StatsRankDaily(ctx, yDate)
	if err != nil {
		t.Fatalf("StatsRankDaily() after purge error = %v", err)
	}
	if !fromDaily.Available {
		t.Fatalf("available = false, want true(统计永久化后恒有数据来源)")
	}
	if len(fromDaily.Channels) != len(fromLogs.Channels) || len(fromDaily.Models) != len(fromLogs.Models) {
		t.Fatalf("daily rank sizes = %d/%d, want %d/%d", len(fromDaily.Channels), len(fromDaily.Models),
			len(fromLogs.Channels), len(fromLogs.Models))
	}
	byName := map[string]StatsRankEntry{}
	for _, ch := range fromDaily.Channels {
		byName[ch.Name] = ch
	}
	if byName["alpha"].InputToken != 150 || byName["beta"].InputToken != 200 {
		t.Fatalf("daily rank = %+v, want alpha 150 / beta 200", fromDaily.Channels)
	}

	// 完全无来源(无汇总行且无日志)时: 仍是 available=true + 非 nil 空数组(前端显示「暂无数据」)。
	empty, err := StatsRankDaily(ctx, at(5, 0).Format("20060102"))
	if err != nil {
		t.Fatalf("StatsRankDaily(empty) error = %v", err)
	}
	if !empty.Available || empty.Channels == nil || empty.Models == nil ||
		len(empty.Channels) != 0 || len(empty.Models) != 0 {
		t.Fatalf("empty day = %+v, want available with non-nil empty arrays", empty)
	}
}

// TestStatsRankDaily_availableBeyondRetention 锁定 R5 语义变更(有意变更):
// 整日超出 relay_log_keep_period 不再返回不可用, 只要日志仍在就回退聚合出数据。
func TestStatsRankDaily_availableBeyondRetention(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()
	_, bDate := seedRankTestLogs(t)

	if err := SettingSetInt(model.SettingKeyRelayLogKeepPeriod, 1); err != nil {
		t.Fatalf("set keep period: %v", err)
	}

	rank, err := StatsRankDaily(ctx, bDate)
	if err != nil {
		t.Fatalf("StatsRankDaily() error = %v", err)
	}
	if !rank.Available || len(rank.Channels) != 1 || rank.Channels[0].InputToken != 999 {
		t.Fatalf("rank = %+v, want available with alpha 999 (旧实现此处 available=false)", rank)
	}
}

// TestStatsDailyRankFold_emptyAggregateGuard 锁定 R3 空聚合护栏:
// 历史日聚合为空(该日日志已不在)不抹既有汇总; 今天/昨天聚合为空照常替换(真实无流量)。
func TestStatsDailyRankFold_emptyAggregateGuard(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()

	// 历史日: 预置已封账汇总, 但无该日日志(模拟清空历史日志)。
	histDate := at(3, 0).Format("20060102")
	if err := db.GetDB().Create(&model.StatsChannelDaily{
		Date: histDate, ChannelID: 1, ChannelName: "alpha",
		StatsMetrics: model.StatsMetrics{RequestSuccess: 7, InputToken: 700},
	}).Error; err != nil {
		t.Fatalf("seed hist daily: %v", err)
	}

	// 对空的历史日折叠: 应跳过而非整日 delete, 既有汇总保留。
	if err := StatsDailyRankFold(ctx, []string{histDate}); err != nil {
		t.Fatalf("fold hist empty error = %v", err)
	}
	var histRows []model.StatsChannelDaily
	if err := db.GetDB().Where("date = ?", histDate).Find(&histRows).Error; err != nil {
		t.Fatalf("load hist rows: %v", err)
	}
	if len(histRows) != 1 || histRows[0].InputToken != 700 || histRows[0].RequestSuccess != 7 {
		t.Fatalf("历史日空聚合抹账了: %+v, want 保留 700 input / 7 success", histRows)
	}

	// 今天: 预置一份旧的汇总, 空聚合折叠应照常整体替换(清空), 因为今天空 = 真实无流量。
	today := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.StatsChannelDaily{
		Date: today, ChannelID: 9, ChannelName: "stale",
		StatsMetrics: model.StatsMetrics{RequestSuccess: 5, InputToken: 55},
	}).Error; err != nil {
		t.Fatalf("seed today daily: %v", err)
	}
	if err := StatsDailyRankFold(ctx, []string{today}); err != nil {
		t.Fatalf("fold today empty error = %v", err)
	}
	var todayRows []model.StatsChannelDaily
	if err := db.GetDB().Where("date = ?", today).Find(&todayRows).Error; err != nil {
		t.Fatalf("load today rows: %v", err)
	}
	if len(todayRows) != 0 {
		t.Fatalf("今天空聚合应照常替换清空, 残留 %+v", todayRows)
	}
}

// TestStatsRankDaily_todayRealtimeHistoricalSummary 锁定 R4 读路径分流:
// 今天直接实时聚合(日志改动立即反映, 不读滞后一个周期的汇总表); 历史日优先读永久汇总。
func TestStatsRankDaily_todayRealtimeHistoricalSummary(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()

	// 今天: 预置一份过时汇总 + 一条今日日志; 读出来必须是日志的实时值而非汇总值。
	today := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.StatsChannelDaily{
		Date: today, ChannelID: 1, ChannelName: "stale",
		StatsMetrics: model.StatsMetrics{RequestSuccess: 99, InputToken: 9999},
	}).Error; err != nil {
		t.Fatalf("seed stale today daily: %v", err)
	}
	live := model.RelayLog{ID: 1, Time: time.Now().Unix(), ChannelId: 1, ChannelName: "live",
		RequestModelName: "gpt-4o", InputTokens: 42, OutputTokens: 4}
	if err := db.GetDB().Create(&live).Error; err != nil {
		t.Fatalf("seed live log: %v", err)
	}
	todayRank, err := StatsRankDaily(ctx, today)
	if err != nil {
		t.Fatalf("StatsRankDaily(today) error = %v", err)
	}
	if len(todayRank.Channels) != 1 || todayRank.Channels[0].Name != "live" || todayRank.Channels[0].InputToken != 42 {
		t.Fatalf("今日应走实时聚合(live/42), got %+v", todayRank.Channels)
	}

	// 历史日: 折叠入库并删日志后, 仍从永久汇总表读出(不依赖实时聚合)。
	histDate := at(2, 0).Format("20060102")
	histLog := model.RelayLog{ID: 2, Time: at(2, 9).Unix(), ChannelId: 2, ChannelName: "beta",
		RequestModelName: "claude-3", InputTokens: 200}
	if err := db.GetDB().Create(&histLog).Error; err != nil {
		t.Fatalf("seed hist log: %v", err)
	}
	if err := StatsDailyRankFold(ctx, []string{histDate}); err != nil {
		t.Fatalf("fold hist error = %v", err)
	}
	if err := db.GetDB().Where("1 = 1").Delete(&model.RelayLog{}).Error; err != nil {
		t.Fatalf("purge logs: %v", err)
	}
	histRank, err := StatsRankDaily(ctx, histDate)
	if err != nil {
		t.Fatalf("StatsRankDaily(hist) error = %v", err)
	}
	if len(histRank.Channels) != 1 || histRank.Channels[0].Name != "beta" || histRank.Channels[0].InputToken != 200 {
		t.Fatalf("历史日应读永久汇总(beta/200), got %+v", histRank.Channels)
	}
}
