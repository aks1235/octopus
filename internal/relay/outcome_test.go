package relay

import (
	"errors"
	"math"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// resetOutcomes 清空全局窗口, 避免用例之间互相污染。
func resetOutcomes(t *testing.T) {
	t.Helper()
	resetOutcomeWindow()
	t.Cleanup(resetOutcomeWindow)
}

// outcomeRate 读某个授权当前的失败率, 用例内断言用。
func outcomeRate(grantID int) float64 {
	outcomeMu.Lock()
	defer outcomeMu.Unlock()
	return outcomes[grantID].rate()
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestGrantOutcome_rate 锁定平滑失败率的口径: 无记录中性、一次失败即沉、一次成功即浮。
func TestGrantOutcome_rate(t *testing.T) {
	tests := []struct {
		name   string
		failed []bool // true 表示记一次失败
		want   float64
	}{
		{name: "无记录取中性", failed: nil, want: 0.5},
		{name: "一次失败", failed: []bool{true}, want: 2.0 / 3.0},
		{name: "一次成功", failed: []bool{false}, want: 1.0 / 3.0},
		{name: "三次失败", failed: []bool{true, true, true}, want: 4.0 / 5.0},
		{name: "两次失败一次成功", failed: []bool{true, true, false}, want: 3.0 / 5.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outcome := &grantOutcome{}
			for _, failed := range tc.failed {
				outcome.record(failed)
			}
			if got := outcome.rate(); !almostEqual(got, tc.want) {
				t.Fatalf("rate() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGrantOutcome_rollsWindow 验证窗口满了之后按先进先出滚动, 老记录被挤出去。
func TestGrantOutcome_rollsWindow(t *testing.T) {
	outcome := &grantOutcome{}
	for i := 0; i < outcomeWindow; i++ {
		outcome.record(true)
	}
	if outcome.count != outcomeWindow {
		t.Fatalf("count = %d, want %d", outcome.count, outcomeWindow)
	}
	if got := outcome.rate(); !almostEqual(got, 17.0/18.0) {
		t.Fatalf("满窗全失败 rate() = %v, want %v", got, 17.0/18.0)
	}

	// 再记一次成功: 挤掉最旧的一个失败, 成功率仍远未恢复。
	outcome.record(false)
	if outcome.count != outcomeWindow {
		t.Fatalf("滚动后 count = %d, want %d", outcome.count, outcomeWindow)
	}
	if got := outcome.rate(); !almostEqual(got, 16.0/18.0) {
		t.Fatalf("滚动一次 rate() = %v, want %v", got, 16.0/18.0)
	}
}

// TestOrderByOutcome_ordersByRate 验证失败率低的排在前面。
func TestOrderByOutcome_ordersByRate(t *testing.T) {
	resetOutcomes(t)
	items := []model.GroupItem{
		{ID: 1, ChannelGrantID: 101},
		{ID: 2, ChannelGrantID: 102},
		{ID: 3, ChannelGrantID: 103},
	}
	for i := 0; i < 4; i++ {
		RecordGrantOutcome(101, false) // 稳定: 1/6
	}
	for i := 0; i < 4; i++ {
		RecordGrantOutcome(102, true) // 很差: 5/6
	}
	// 103 无记录: 0.5

	got := orderByOutcome(items)
	want := []int{1, 3, 2}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("排序结果 = %+v, want 顺序 %v", idsOf(got), want)
		}
	}
	// 不能改动调用方的切片: 分组每轮重读, 原地排序会污染本次请求的游标定位。
	if items[0].ID != 1 {
		t.Fatalf("原切片被修改: %+v", idsOf(items))
	}
}

// TestOrderByOutcome_keepsManualOrderOnTie 验证同率成员保持人工优先级顺序。
func TestOrderByOutcome_keepsManualOrderOnTie(t *testing.T) {
	resetOutcomes(t)
	items := []model.GroupItem{
		{ID: 1, ChannelGrantID: 201},
		{ID: 2, ChannelGrantID: 202},
		{ID: 3, ChannelGrantID: 203},
	}
	got := orderByOutcome(items)
	for i := range items {
		if got[i].ID != items[i].ID {
			t.Fatalf("同率时顺序被打乱: %v", idsOf(got))
		}
	}
}

// TestChannelOutcomeCountable 锁定"哪些失败算渠道的责任"。
func TestChannelOutcomeCountable(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		emptyAnswer bool
		want        bool
	}{
		{name: "空正文算", err: nil, emptyAnswer: true, want: true},
		{name: "500 算", err: errors.New("upstream 500 Internal Server Error: {}"), want: true},
		{name: "503 算", err: errors.New("upstream 503 Service Unavailable"), want: true},
		{name: "429 算", err: errors.New("upstream 429 Too Many Requests"), want: true},
		{name: "401 算", err: errors.New("upstream 401 Unauthorized"), want: true},
		{name: "403 算", err: errors.New("upstream 403 Forbidden"), want: true},
		{name: "超时算", err: errors.New("upstream non-stream response timeout"), want: true},
		{name: "连接失败算", err: errors.New(`Post "https://x": dial tcp: connection refused`), want: true},
		{name: "400 不算", err: errors.New("upstream 400 Bad Request: invalid parameter"), want: false},
		{name: "404 算(模型不存在是渠道差异)", err: errors.New("upstream 404 Not Found"), want: true},
		{name: "本地错误不算", err: errors.New("invalid channel parameter override"), want: false},
		{name: "空错误不算", err: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelOutcomeCountable(tc.err, tc.emptyAnswer); got != tc.want {
				t.Fatalf("channelOutcomeCountable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRecordOutcomeOnce_onePerRequest 验证同一请求内同一渠道只记一格。
func TestRecordOutcomeOnce_onePerRequest(t *testing.T) {
	resetOutcomes(t)
	recorded := make(map[int]struct{})
	recordOutcomeOnce(recorded, 301, true)
	recordOutcomeOnce(recorded, 301, true) // 重试, 不重复记
	recordOutcomeOnce(recorded, 301, false)

	if got := outcomeRate(301); !almostEqual(got, 2.0/3.0) {
		t.Fatalf("同一请求记了多格: rate() = %v, want %v", got, 2.0/3.0)
	}
	// 下一个请求是新的 map, 可以再记。
	recordOutcomeOnce(make(map[int]struct{}), 301, false)
	if got := outcomeRate(301); !almostEqual(got, 2.0/4.0) {
		t.Fatalf("跨请求应继续累计: rate() = %v, want %v", got, 2.0/4.0)
	}
}

func idsOf(items []model.GroupItem) []int {
	ids := make([]int, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
