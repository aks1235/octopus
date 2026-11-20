package update

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestUpdateCoreRejectsConcurrentUpdate 验证同一时刻只允许一个更新流程。
// 并发时第二个请求必须快速失败, 而不是和第一个交叉 rename/copy 二进制。
func TestUpdateCoreRejectsConcurrentUpdate(t *testing.T) {
	orig := updateCoreImpl
	t.Cleanup(func() { updateCoreImpl = orig })

	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once

	updateCoreImpl = func() (string, error) {
		once.Do(func() { close(started) })
		<-release
		return "/app/octopus", nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := UpdateCore(); err != nil {
			t.Errorf("首个更新不应失败: %v", err)
		}
	}()

	// 等首个更新真正进入临界区, 避免调度时序导致误判。
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("首个更新未在 2 秒内进入临界区")
	}

	if _, err := UpdateCore(); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("并发更新返回 %v, 期望 errUpdateInProgress", err)
	}

	close(release)
	<-done

	// 首个更新结束后必须能再次更新, 锁不能泄漏。
	updateCoreImpl = func() (string, error) { return "/app/octopus", nil }
	if _, err := UpdateCore(); err != nil {
		t.Fatalf("前一次更新结束后再次更新失败: %v", err)
	}
}
