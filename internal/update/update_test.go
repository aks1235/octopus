package update

import (
	"errors"
	"testing"
	"time"
)

// resetLatestCache 清空包级缓存, 让每个用例从干净状态开始。
func resetLatestCache(t *testing.T) {
	t.Helper()
	latestMu.Lock()
	defer latestMu.Unlock()
	latestCached = nil
	latestFetchedAt = time.Time{}
}

// TestGetLatestInfoCachesResult 验证缓存期内不再回源, 且返回的是副本。
func TestGetLatestInfoCachesResult(t *testing.T) {
	resetLatestCache(t)
	orig := fetchLatestFn
	t.Cleanup(func() {
		fetchLatestFn = orig
		resetLatestCache(t)
	})

	calls := 0
	fetchLatestFn = func() (*LatestInfo, error) {
		calls++
		return &LatestInfo{TagName: "v9.9.9"}, nil
	}

	first, err := GetLatestInfo()
	if err != nil {
		t.Fatalf("首次调用返回错误: %v", err)
	}
	if first.TagName != "v9.9.9" {
		t.Fatalf("首次调用版本号 = %q, 期望 v9.9.9", first.TagName)
	}

	// 调用方改返回值不应污染缓存。
	first.TagName = "mutated"

	second, err := GetLatestInfo()
	if err != nil {
		t.Fatalf("二次调用返回错误: %v", err)
	}
	if second.TagName != "v9.9.9" {
		t.Fatalf("二次调用版本号 = %q, 期望 v9.9.9(缓存被返回值污染)", second.TagName)
	}
	if calls != 1 {
		t.Fatalf("缓存期内回源 %d 次, 期望 1 次", calls)
	}
}

// TestGetLatestInfoFallsBackToStaleOnError 验证拉取失败时退回上次成功的结果。
func TestGetLatestInfoFallsBackToStaleOnError(t *testing.T) {
	resetLatestCache(t)
	orig := fetchLatestFn
	t.Cleanup(func() {
		fetchLatestFn = orig
		resetLatestCache(t)
	})

	fetchLatestFn = func() (*LatestInfo, error) {
		return &LatestInfo{TagName: "v9.9.9", PublishedAt: "2026-11-20T00:00:00Z"}, nil
	}
	if _, err := GetLatestInfo(); err != nil {
		t.Fatalf("预热缓存失败: %v", err)
	}

	// 让缓存过期, 同时模拟 GitHub 按 IP 限流。
	latestMu.Lock()
	latestFetchedAt = time.Now().Add(-2 * latestCacheTTL)
	latestMu.Unlock()
	fetchLatestFn = func() (*LatestInfo, error) {
		return nil, errors.New("API rate limit exceeded")
	}

	got, err := GetLatestInfo()
	if err != nil {
		t.Fatalf("拉取失败时应退回缓存, 却返回错误: %v", err)
	}
	if got.TagName != "v9.9.9" {
		t.Fatalf("退回的版本号 = %q, 期望 v9.9.9", got.TagName)
	}
}

// TestGetLatestInfoPropagatesErrorWithoutCache 验证从未成功过时错误要如实上报。
func TestGetLatestInfoPropagatesErrorWithoutCache(t *testing.T) {
	resetLatestCache(t)
	orig := fetchLatestFn
	t.Cleanup(func() {
		fetchLatestFn = orig
		resetLatestCache(t)
	})

	fetchLatestFn = func() (*LatestInfo, error) {
		return nil, errors.New("API rate limit exceeded")
	}

	if _, err := GetLatestInfo(); err == nil {
		t.Fatal("没有可用缓存时应返回错误")
	}
}
