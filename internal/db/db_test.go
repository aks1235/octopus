package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// TestConcurrentWriteTransactionsNoBusy 对应 PRD AC1: 多个 goroutine 同时开事务
// 先读后写(复刻 GORM 延迟事务模式)。_txlock=immediate + busy_timeout 下应零错误;
// 若 DSN 未生效退回延迟事务, WAL 下该模式会撞 SQLITE_BUSY_SNAPSHOT(517)。
func TestConcurrentWriteTransactionsNoBusy(t *testing.T) {
	if err := InitDB("sqlite", filepath.Join(t.TempDir(), "busy-concurrent-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	gdb := GetDB()
	if err := gdb.Exec(`CREATE TABLE IF NOT EXISTS busy_probe (id INTEGER PRIMARY KEY AUTOINCREMENT, worker INTEGER NOT NULL)`).Error; err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	const workers = 4
	const txPerWorker = 25
	errs := make(chan error, workers*txPerWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < txPerWorker; i++ {
				// 先读后写: 延迟事务下读旧快照后别的写者提交, 写阶段即报 517;
				// IMMEDIATE 下 BEGIN 即拿写锁, 拿不到在驱动层排队等待而不是失败。
				err := gdb.Transaction(func(tx *gorm.DB) error {
					var n int64
					if err := tx.Raw(`SELECT COUNT(*) FROM busy_probe`).Scan(&n).Error; err != nil {
						return err
					}
					return tx.Exec(`INSERT INTO busy_probe (worker) VALUES (?)`, worker).Error
				})
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent transaction failed: %v", err)
	}
	var total int64
	if err := gdb.Raw(`SELECT COUNT(*) FROM busy_probe`).Scan(&total).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != workers*txPerWorker {
		t.Fatalf("expected %d rows, got %d", workers*txPerWorker, total)
	}
}

// TestIsBusyErrorWithRealDriverBusy 用真实驱动错误验证 BUSY 判定:
// 连接 A 开 IMMEDIATE 事务写一行但不提交(持有写锁), 连接 B 以极短 busy_timeout
// 竞争开 IMMEDIATE 事务, 必然得到真实 SQLITE_BUSY; 同时验证非 BUSY 的驱动错误
// 与字符串兜底路径的判定边界。
func TestIsBusyErrorWithRealDriverBusy(t *testing.T) {
	if err := InitDB("sqlite", filepath.Join(t.TempDir(), "busy-classify-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	gdb := GetDB()
	if err := gdb.Exec(`CREATE TABLE IF NOT EXISTS busy_probe2 (id INTEGER PRIMARY KEY, v INTEGER)`).Error; err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	ctx := context.Background()
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}

	// 持锁者: 开 IMMEDIATE 事务写一行不提交, 占住写锁
	holder, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("get holder conn: %v", err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	if _, err := holder.ExecContext(ctx, `INSERT INTO busy_probe2 (v) VALUES (1)`); err != nil {
		t.Fatalf("holder insert: %v", err)
	}

	// 竞争者: 另一条连接压低 busy_timeout 到 50ms, 让 BUSY 快速浮出, 不拖慢测试
	competitor, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatalf("get competitor conn: %v", err)
	}
	defer competitor.Close()
	if _, err := competitor.ExecContext(ctx, `PRAGMA busy_timeout(50)`); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	_, err = competitor.ExecContext(ctx, `BEGIN IMMEDIATE`)
	if err == nil {
		t.Fatal("expected busy error from competing BEGIN IMMEDIATE, got nil")
	}
	if !IsBusyError(err) {
		t.Fatalf("expected real driver busy error to be classified busy, got %v", err)
	}

	// 非 BUSY 的驱动错误(SQLITE_ERROR=1)不应被误判为 BUSY
	err = gdb.Exec(`SELECT * FROM definitely_missing_table_for_busy_test`).Error
	if err == nil {
		t.Fatal("expected error from missing table query, got nil")
	}
	if IsBusyError(err) {
		t.Fatalf("non-busy driver error misclassified as busy: %v", err)
	}

	// 字符串兜底: 普通 error 但文案是驱动 BUSY 文案
	if !IsBusyError(errors.New("database is locked (517) (SQLITE_BUSY)")) {
		t.Error("wrapped busy-style error should be classified busy")
	}
	if IsBusyError(errors.New("invalid regexp: (")) {
		t.Error("plain non-busy error should not be classified busy")
	}
	if IsBusyError(nil) {
		t.Error("nil error should not be classified busy")
	}
}
