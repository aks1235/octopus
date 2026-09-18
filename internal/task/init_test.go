package task

import (
	"errors"
	"testing"
	"time"
)

// shortenBusyRetryIntervals 把退避间隔缩到毫秒级, 避免单测真实睡眠拖慢, 测试结束自动恢复。
func shortenBusyRetryIntervals(t *testing.T) {
	t.Helper()
	orig := busyRetryIntervals
	busyRetryIntervals = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { busyRetryIntervals = orig })
}

// 注入一次 BUSY → 静默重试成功, 无错误返回(调用方即不会告警)。
func TestRunWithBusyRetryRecoversAfterBusy(t *testing.T) {
	shortenBusyRetryIntervals(t)

	calls := 0
	err := runWithBusyRetry(func() error {
		calls++
		if calls == 1 {
			return errors.New("database is locked (517) (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (1 fail + 1 retry), got %d", calls)
	}
}

// 连续 BUSY → 重试耗尽后返回最后一次错误, 调用方恰好告警一次。
func TestRunWithBusyRetryExhaustsAndReturnsError(t *testing.T) {
	shortenBusyRetryIntervals(t)

	calls := 0
	wantErr := errors.New("database is locked (5)")
	err := runWithBusyRetry(func() error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected final busy error %v, got %v", wantErr, err)
	}
	// 1 次首发 + busyRetryIntervals 两轮重试 = 3 次
	if calls != 3 {
		t.Fatalf("expected 3 calls (1 + 2 retries), got %d", calls)
	}
}

// 非 BUSY 错误(如正则编译失败)→ 不重试, 原样返回。
func TestRunWithBusyRetryDoesNotRetryOtherErrors(t *testing.T) {
	shortenBusyRetryIntervals(t)

	calls := 0
	wantErr := errors.New("invalid regexp: (")
	err := runWithBusyRetry(func() error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error passthrough, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no retry for non-busy error, got %d calls", calls)
	}
}

// BUSY 消失于最后一轮 → 仍然成功, 验证退避序列边界。
func TestRunWithBusyRetrySucceedsOnLastAttempt(t *testing.T) {
	shortenBusyRetryIntervals(t)

	calls := 0
	err := runWithBusyRetry(func() error {
		calls++
		if calls < 3 {
			return errors.New("database is locked (261) (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success on last attempt, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}
