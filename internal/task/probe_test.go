package task

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
)

// 定时探测的周期契约: 落在 6–12 小时之间(用户定案"随机, 不用太固定")。
func TestRandomProbeInterval_inRange(t *testing.T) {
	for i := 0; i < 500; i++ {
		got := randomProbeInterval()
		if got < probeIntervalMin || got >= probeIntervalMax {
			t.Fatalf("randomProbeInterval() = %v, want [%v, %v)", got, probeIntervalMin, probeIntervalMax)
		}
	}
}

// 空库跑一轮: 没有分组时直接收尾, 不 panic 也不留残留的运行标记。
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

	// 轮次收尾后状态必须可读: 没在跑, 且记下了完成时间, 否则设置页永远显示"还没跑过"。
	status := ProbeRoundStatusSnapshot()
	if status.Running {
		t.Fatal("status.Running still true after round")
	}
	if status.LastFinishedAt == 0 {
		t.Fatal("status.LastFinishedAt not set after round")
	}
	if status.GroupsTotal != 0 || status.GroupsDone != 0 {
		t.Fatalf("empty round progress = %d/%d, want 0/0", status.GroupsDone, status.GroupsTotal)
	}
}
