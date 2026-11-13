package task

import "testing"

// TestFetchMissCounter 验证"拉不到模型清单"的连续计数: 够轮数才清理, 中途拉到清单即归零。
func TestFetchMissCounter(t *testing.T) {
	key := fetchMissKey{channelID: 1, keyName: "key-a"}
	clearFetchMiss(key)
	t.Cleanup(func() { clearFetchMiss(key) })

	if got := noteFetchMiss(key); got != 1 {
		t.Fatalf("第一轮连续次数 = %d, 期望 1", got)
	}
	// 第一轮未达阈值: 调用方据此跳过清理, 只累计。
	if 1 >= fetchMissThreshold {
		t.Fatal("第一轮不应达到清理阈值")
	}
	if got := noteFetchMiss(key); got != fetchMissThreshold {
		t.Fatalf("第二轮连续次数 = %d, 期望 %d", got, fetchMissThreshold)
	}

	// 拉到清单后归零, 下一轮重新从 1 开始。
	clearFetchMiss(key)
	if got := noteFetchMiss(key); got != 1 {
		t.Fatalf("归零后的连续次数 = %d, 期望 1", got)
	}

	// 不同凭据各自计数, 互不影响。
	other := fetchMissKey{channelID: 1, keyName: "key-b"}
	clearFetchMiss(other)
	t.Cleanup(func() { clearFetchMiss(other) })
	if got := noteFetchMiss(other); got != 1 {
		t.Fatalf("另一凭据的连续次数 = %d, 期望 1", got)
	}
}
