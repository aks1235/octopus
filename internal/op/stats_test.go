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

// TestStatsHourlyCleanupBefore 锁定清理边界: cutoff 之前的天(DB 与内存缓存)被删, 当天及以后保留。
func TestStatsHourlyCleanupBefore(t *testing.T) {
	setupStatsTestDB(t)
	ctx := context.Background()

	keepDate := at(1, 0).Format("20060102")
	expireDate := at(3, 0).Format("20060102")
	// 三行: 过期日两行, 保留日一行。
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 1}, at(3, 1)); err != nil {
		t.Fatalf("update expired hour1: %v", err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 1}, at(3, 9)); err != nil {
		t.Fatalf("update expired hour9: %v", err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{RequestSuccess: 1}, at(1, 2)); err != nil {
		t.Fatalf("update kept hour2: %v", err)
	}
	if expireDate >= keepDate {
		t.Fatalf("test dates inverted: expire=%s keep=%s", expireDate, keepDate)
	}
	if err := StatsSaveDB(ctx); err != nil {
		t.Fatalf("StatsSaveDB() error = %v", err)
	}

	// cutoff=keepDate: 早于 keepDate 的行删除, keepDate 及以后保留。
	if err := StatsHourlyCleanupBefore(ctx, keepDate); err != nil {
		t.Fatalf("StatsHourlyCleanupBefore() error = %v", err)
	}

	var dbRows []model.StatsHourly
	if err := db.GetDB().WithContext(ctx).Find(&dbRows).Error; err != nil {
		t.Fatalf("load rows: %v", err)
	}
	if len(dbRows) != 1 || dbRows[0].Date != keepDate {
		t.Fatalf("db rows = %+v, want only %s kept", dbRows, keepDate)
	}

	statsHourlyCacheLock.RLock()
	for key := range statsHourlyCache {
		if key.date < keepDate {
			statsHourlyCacheLock.RUnlock()
			t.Fatalf("cache still holds expired row %v", key)
		}
	}
	statsHourlyCacheLock.RUnlock()
}
