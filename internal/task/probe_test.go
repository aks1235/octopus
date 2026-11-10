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
}
