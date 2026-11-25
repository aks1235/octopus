package task

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
)

// 公益成员的间隔必须落在 6–12 小时: 短于 6 小时会把探测频率翻倍,
// 长于 12 小时则拉长判死结论的保鲜期, 让死成员在结论失效前一直占着位置。
func TestNextProbeAt_communityWithinInterval(t *testing.T) {
	now := time.Now()
	for i := 0; i < 500; i++ {
		next := time.Unix(nextProbeAt(now, false), 0)
		gap := next.Sub(now)
		if gap < probeIntervalMin || gap >= probeIntervalMax {
			t.Fatalf("nextProbeAt(gap) = %v, want [%v, %v)", gap, probeIntervalMin, probeIntervalMax)
		}
	}
}

// 官方成员必须落在夜间窗口内, 且至少隔 18 小时:
// 窗口保证"只在夜里探", 最小间隔保证手动探测或重启不会让它在同一天被连探两次。
func TestNextOfficialProbeAt_landsInWindow(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-official-window.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	hour := officialProbeHour()
	now := time.Now()
	for i := 0; i < 200; i++ {
		next := time.Unix(nextProbeAt(now, true), 0)
		if gap := next.Sub(now); gap < officialProbeMinGap {
			t.Fatalf("官方成员间隔 = %v, want >= %v", gap, officialProbeMinGap)
		}
		// 落在 [hour, hour+window) 内: 跨零点的窗口按 24 小时折算后再比较。
		offset := next.Hour() - hour
		if offset < 0 {
			offset += 24
		}
		if time.Duration(offset)*time.Hour >= officialProbeWindow {
			t.Fatalf("官方成员到期时刻 %v 不在 %d 点起的 %v 窗口内", next, hour, officialProbeWindow)
		}
	}
}

// 没排过期的成员要按主键铺开, 而不是"立刻到期":
// 否则进程一启动, 二百多个成员同时到期, 又回到一次性全量开打。
func TestProbeDueAt_spreadsUnseenItems(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-schedule.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	resetProbeScheduleForTest()
	t.Cleanup(resetProbeScheduleForTest)

	now := time.Now().Unix()
	for _, itemID := range []int{1, 7, 43200, 99999} {
		dueAt := probeDueAt(itemID, now, false)
		if dueAt < now || dueAt >= now+probeScheduleSpreadSeconds {
			t.Fatalf("item %d 初始到期偏移 = %d, want [0, %d)", itemID, dueAt-now, probeScheduleSpreadSeconds)
		}
		// 已排期的成员再问一次必须得到同一时刻: 铺开是确定性的, 不能每次扫描重新掷骰子。
		if again := probeDueAt(itemID, now, false); again != dueAt {
			t.Fatalf("item %d 两次读取不一致: %d != %d", itemID, again, dueAt)
		}
	}
}

// 空库跑一次人工全量轮: 没有分组时直接收尾, 不 panic 也不留残留的运行标记。
func TestRunGroupProbeRound_emptyDB(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-round.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if got := RunGroupProbeRound(); got != 0 {
		t.Fatalf("RunGroupProbeRound() = %d, want 0", got)
	}
	if probeRoundRunning.Load() {
		t.Fatal("probeRoundRunning still true after round")
	}
	status := ProbeStatusSnapshot()
	if status.ManualRunning {
		t.Fatal("status.ManualRunning still true after round")
	}
	if status.LastFinishedAt == 0 {
		t.Fatal("status.LastFinishedAt not set after round")
	}
}

// resetProbeScheduleForTest 清空内存排期, 供测试隔离使用。
func resetProbeScheduleForTest() {
	probeScheduleMu.Lock()
	probeSchedule = make(map[int]int64)
	probeScheduleMu.Unlock()
}
