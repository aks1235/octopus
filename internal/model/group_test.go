package model

import "testing"

// TestDefaultGroupRelayConfig_maxCooldown 验证默认冷却上限为基础冷却的十倍(60s / 600s)。
func TestDefaultGroupRelayConfig_maxCooldown(t *testing.T) {
	defaults := DefaultGroupRelayConfig()
	if defaults.MemberCooldownSeconds != 60 || defaults.MemberMaxCooldownSeconds != 600 {
		t.Fatalf("默认冷却 = %ds / 上限 %ds, want 60 / 600", defaults.MemberCooldownSeconds, defaults.MemberMaxCooldownSeconds)
	}
}

// TestNormalizeGroupRelayConfig_maxCooldown 验证冷却上限的补齐, 以及不小于基础冷却的约束。
func TestNormalizeGroupRelayConfig_maxCooldown(t *testing.T) {
	// 零值配置整体取默认。
	zero := GroupRelayConfig{}
	NormalizeGroupRelayConfig(&zero)
	if zero.MemberCooldownSeconds != 60 || zero.MemberMaxCooldownSeconds != 600 {
		t.Fatalf("零值配置补齐后 = %ds / 上限 %ds, want 60 / 600", zero.MemberCooldownSeconds, zero.MemberMaxCooldownSeconds)
	}

	// 上限缺省时先补默认, 若默认小于基础冷却则抬到基础冷却。
	raised := GroupRelayConfig{
		MemberMaxAttempts:                     1,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 1,
		MemberStreamFirstEventTimeoutSeconds:  1,
		MemberCooldownSeconds:                 900,
	}
	NormalizeGroupRelayConfig(&raised)
	if raised.MemberMaxCooldownSeconds != 900 {
		t.Fatalf("上限 = %ds, want 900s (不小于基础冷却 900s)", raised.MemberMaxCooldownSeconds)
	}

	// 显式给出不小于基础冷却的上限时原样保留。
	explicit := GroupRelayConfig{
		MemberMaxAttempts:                     1,
		MemberRetryIntervalSeconds:            1,
		MemberNonStreamResponseTimeoutSeconds: 1,
		MemberStreamFirstEventTimeoutSeconds:  1,
		MemberCooldownSeconds:                 60,
		MemberMaxCooldownSeconds:              3600,
	}
	NormalizeGroupRelayConfig(&explicit)
	if explicit.MemberMaxCooldownSeconds != 3600 {
		t.Fatalf("上限 = %ds, want 3600s (显式值保留)", explicit.MemberMaxCooldownSeconds)
	}
}
