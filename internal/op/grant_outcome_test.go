package op

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func setupGrantOutcomeTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "grant-outcome-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// TestGrantOutcomeSaveLoad_roundTrip 锁定快照的读写往返: 重复写同一凭据是覆盖而非追加。
// 追加会让启动回填时 Map 里后写覆盖先写, 看着没坏, 但表会随重启次数无限增长。
func TestGrantOutcomeSaveLoad_roundTrip(t *testing.T) {
	setupGrantOutcomeTestDB(t)

	rows := []model.GrantOutcome{{GrantID: 7, Bits: 0b101, Count: 3, UpdatedAt: 100}}
	if err := GrantOutcomeSave(rows); err != nil {
		t.Fatalf("GrantOutcomeSave() error = %v", err)
	}
	rows[0].Bits, rows[0].Count, rows[0].UpdatedAt = 0b11, 2, 200
	if err := GrantOutcomeSave(rows); err != nil {
		t.Fatalf("GrantOutcomeSave() 覆盖写 error = %v", err)
	}

	loaded, err := GrantOutcomeLoad()
	if err != nil {
		t.Fatalf("GrantOutcomeLoad() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("load 行数 = %d, want 1: %+v", len(loaded), loaded)
	}
	if got := loaded[0]; got.Bits != 0b11 || got.Count != 2 || got.UpdatedAt != 200 {
		t.Fatalf("load = %+v, want bits=3 count=2 updated_at=200", got)
	}
}
