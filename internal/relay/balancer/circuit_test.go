package balancer

import (
	"testing"
	"time"
)

// 测试无 DB 环境:settingCache 为空,op.SettingGetInt 返回 err,
// getThreshold/GetCooldown 走默认值(threshold=5, cooldown=60, maxCooldown=600)。

// TestCircuitBreaker_TripsAfterThreshold 连续失败达阈值熔断,成功后恢复。
func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	globalBreaker.Delete("1:gpt-4")

	for i := 0; i < 5; i++ {
		RecordFailure(1, "gpt-4")
	}
	tripped, _ := IsTripped(1, "gpt-4")
	if !tripped {
		t.Error("circuit should be tripped after 5 consecutive failures")
	}

	RecordSuccess(1, "gpt-4")
	tripped, _ = IsTripped(1, "gpt-4")
	if tripped {
		t.Error("circuit should be closed after success")
	}

	globalBreaker.Delete("1:gpt-4")
}

// TestCircuitBreaker_ModelScoped 熔断按 model 隔离:同渠道不同模型互不影响。
func TestCircuitBreaker_ModelScoped(t *testing.T) {
	globalBreaker.Delete("1:gpt-4")
	globalBreaker.Delete("1:gpt-3.5")

	for i := 0; i < 5; i++ {
		RecordFailure(1, "gpt-4")
	}
	tripped, _ := IsTripped(1, "gpt-4")
	if !tripped {
		t.Error("gpt-4 should be tripped")
	}
	tripped, _ = IsTripped(1, "gpt-3.5")
	if tripped {
		t.Error("gpt-3.5 should NOT be tripped (different model)")
	}

	RecordSuccess(1, "gpt-4")
	globalBreaker.Delete("1:gpt-4")
	globalBreaker.Delete("1:gpt-3.5")
}

// TestCircuitBreaker_HalfOpenTimeoutFallback HalfOpen 探测挂死 30s 应回退 Open。
func TestCircuitBreaker_HalfOpenTimeoutFallback(t *testing.T) {
	globalBreaker.Delete("1:gpt-4")

	for i := 0; i < 5; i++ {
		RecordFailure(1, "gpt-4")
	}
	tripped, _ := IsTripped(1, "gpt-4")
	if !tripped {
		t.Fatal("circuit should be tripped")
	}

	// 手动将 HalfOpenTime 设为 31s 前,模拟探测请求挂死
	key := circuitKey(1, "gpt-4")
	v, _ := globalBreaker.Load(key)
	entry := v.(*circuitEntry)
	entry.mu.Lock()
	entry.State = StateHalfOpen
	entry.HalfOpenTime = time.Now().Add(-31 * time.Second)
	entry.mu.Unlock()

	// IsTripped 应检测到 HalfOpen 超时,回退到 Open
	tripped, remaining := IsTripped(1, "gpt-4")
	if !tripped {
		t.Error("should still be tripped after HalfOpen timeout fallback to Open")
	}
	if remaining <= 0 {
		t.Error("should have remaining cooldown after fallback to Open")
	}

	globalBreaker.Delete("1:gpt-4")
}

// TestCircuitBreaker_CooldownToHalfOpen 冷却到期转 HalfOpen,探测成功恢复 Closed。
func TestCircuitBreaker_CooldownToHalfOpen(t *testing.T) {
	globalBreaker.Delete("1:gpt-4")

	for i := 0; i < 5; i++ {
		RecordFailure(1, "gpt-4")
	} // trip → Open, tripCount=1

	// 手动把失败时间提前到冷却已过(GetCooldown(1)=60s 默认)
	key := circuitKey(1, "gpt-4")
	v, _ := globalBreaker.Load(key)
	entry := v.(*circuitEntry)
	entry.mu.Lock()
	entry.LastFailureTime = time.Now().Add(-GetCooldown(entry.TripCount) - time.Second)
	entry.mu.Unlock()

	// IsTripped 应转 HalfOpen 并放行(返回 tripped=false)
	tripped, _ := IsTripped(1, "gpt-4")
	if tripped {
		t.Error("should transition to HalfOpen and allow request after cooldown elapsed")
	}
	entry.mu.Lock()
	if entry.State != StateHalfOpen {
		t.Errorf("state should be HalfOpen, got %v", entry.State)
	}
	entry.mu.Unlock()

	// 探测成功 → Closed,tripCount 归零
	RecordSuccess(1, "gpt-4")
	entry.mu.Lock()
	if entry.State != StateClosed {
		t.Errorf("should be Closed after probe success, got %v", entry.State)
	}
	if entry.TripCount != 0 {
		t.Errorf("tripCount should reset to 0, got %d", entry.TripCount)
	}
	entry.mu.Unlock()

	globalBreaker.Delete("1:gpt-4")
}

// TestCircuitBreaker_ExponentialBackoff 指数退避:base*2^(tripCount-1),封顶 maxCooldown。
func TestCircuitBreaker_ExponentialBackoff(t *testing.T) {
	// 默认 base=60s, max=600s(settingCache 空 → 默认值)
	if cd := GetCooldown(1); cd != 60*time.Second {
		t.Errorf("tripCount=1: want 60s, got %v", cd)
	}
	if cd := GetCooldown(2); cd != 120*time.Second {
		t.Errorf("tripCount=2: want 120s, got %v", cd)
	}
	if cd := GetCooldown(3); cd != 240*time.Second {
		t.Errorf("tripCount=3: want 240s, got %v", cd)
	}
	if cd := GetCooldown(4); cd != 480*time.Second {
		t.Errorf("tripCount=4: want 480s, got %v", cd)
	}
	if cd := GetCooldown(5); cd != 600*time.Second {
		t.Errorf("tripCount=5: want 600s (capped), got %v", cd)
	}
	if cd := GetCooldown(100); cd != 600*time.Second {
		t.Errorf("tripCount=100: want 600s (capped), got %v", cd)
	}
}
