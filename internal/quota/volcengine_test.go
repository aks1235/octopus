package quota

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestSignV4MatchesReference 用一份独立实现(与官方 @volcengine/openapi 逐字节一致的签名脚本)
// 在固定时间戳下算出的结果作为基准。签名只要有一处换行或头的顺序不同就会整体不同,
// 故这条用例是判断"能不能调通控制面"的第一道关卡。
func TestSignV4MatchesReference(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	body := []byte("{}")

	endpoint, headers := SignV4("AKLTtestAccessKey", "testSecretKey", "GetAFPUsage", body, now)

	const wantURL = "https://open.volcengineapi.com/?Action=GetAFPUsage&Version=2024-01-01"
	if endpoint != wantURL {
		t.Fatalf("endpoint mismatch:\n got %s\nwant %s", endpoint, wantURL)
	}
	if got := headers["x-date"]; got != "20260102T030405Z" {
		t.Errorf("x-date = %q, want 20260102T030405Z", got)
	}
	const wantContentSHA = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	if got := headers["x-content-sha256"]; got != wantContentSHA {
		t.Errorf("x-content-sha256 = %q, want %q", got, wantContentSHA)
	}
	const wantAuthorization = "HMAC-SHA256 Credential=AKLTtestAccessKey/20260102/cn-beijing/ark/request, " +
		"SignedHeaders=host;x-content-sha256;x-date, " +
		"Signature=236488689c4638a42f233fd12c24c5176f4ca9c9e6c7429423ccd34672bee9d8"
	if got := headers["authorization"]; got != wantAuthorization {
		t.Errorf("authorization mismatch:\n got %s\nwant %s", got, wantAuthorization)
	}
}

const agentPlanPayload = `{
  "ResponseMetadata": {"RequestId": "test", "Action": "GetAFPUsage", "Version": "2024-01-01"},
  "Result": {
    "PlanType": "Medium",
    "AFPFiveHour": {"Quota": "20000", "Used": "12400", "ResetTime": 1767235000000, "SubscribeTime": 1767217000000},
    "AFPDaily":    {"Quota": "100000", "Used": "5000", "ResetTime": 1767300000000, "SubscribeTime": 1767216000000},
    "AFPWeekly":   {"Quota": "70000", "Used": "54600", "ResetTime": 1767600000000, "SubscribeTime": 1767000000000},
    "AFPMonthly":  {"Quota": "200000", "Used": "182000", "ResetTime": 1768800000000, "SubscribeTime": 1767000000000}
  }
}`

func decodeAgentPlan(t *testing.T, raw string) *Snapshot {
	t.Helper()
	var payload volcAFPResponse
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return payload.Result.toSnapshot()
}

// TestParseAgentPlanWindows 验证三个窗口的顺序、百分比与毫秒到秒的换算。
func TestParseAgentPlanWindows(t *testing.T) {
	snapshot := decodeAgentPlan(t, agentPlanPayload)

	if snapshot.PlanName != "Medium" {
		t.Errorf("PlanName = %q, want Medium", snapshot.PlanName)
	}
	wantKeys := []WindowKey{WindowFiveHour, WindowWeekly, WindowMonthly}
	if len(snapshot.Windows) != len(wantKeys) {
		t.Fatalf("windows = %d, want %d (AFPDaily 不应出现)", len(snapshot.Windows), len(wantKeys))
	}
	for i, key := range wantKeys {
		if snapshot.Windows[i].Key != key {
			t.Errorf("windows[%d].Key = %q, want %q", i, snapshot.Windows[i].Key, key)
		}
	}

	fiveHour := snapshot.Windows[0]
	if fiveHour.Percent < 61.99 || fiveHour.Percent > 62.01 {
		t.Errorf("5h percent = %v, want 62", fiveHour.Percent)
	}
	if fiveHour.ResetAt != 1767235000 {
		t.Errorf("5h ResetAt = %d, want 1767235000 (毫秒应换算为秒)", fiveHour.ResetAt)
	}
}

// TestParseAgentPlanSkipsEmptyWindow 验证 Quota 为 0 的窗口被跳过: 那是"该档位不带此窗口", 不是 0 额度。
func TestParseAgentPlanSkipsEmptyWindow(t *testing.T) {
	raw := `{"Result": {
      "AFPFiveHour": {"Quota": "20000", "Used": "1000", "ResetTime": 1767235000000},
      "AFPWeekly":   {"Quota": "0", "Used": "0", "ResetTime": 0},
      "AFPMonthly":  {"Quota": "200000", "Used": "1000", "ResetTime": 1768800000000}
    }}`
	snapshot := decodeAgentPlan(t, raw)

	if len(snapshot.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(snapshot.Windows))
	}
	if snapshot.Windows[0].Key != WindowFiveHour || snapshot.Windows[1].Key != WindowMonthly {
		t.Errorf("unexpected windows: %+v", snapshot.Windows)
	}
}

// TestParseAgentPlanNoReset 验证 ResetTime 为 0 时按"未排定重置"处理, 不落成 1970 年。
func TestParseAgentPlanNoReset(t *testing.T) {
	raw := `{"Result": {"AFPFiveHour": {"Quota": "20000", "Used": "1000", "ResetTime": 0}}}`
	snapshot := decodeAgentPlan(t, raw)

	if len(snapshot.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(snapshot.Windows))
	}
	if snapshot.Windows[0].ResetAt != 0 {
		t.Errorf("ResetAt = %d, want 0", snapshot.Windows[0].ResetAt)
	}
}

// TestParseAgentPlanClampsPercent 验证 Used 超过 Quota 时百分比封顶 100, 不给前端越界的进度条。
func TestParseAgentPlanClampsPercent(t *testing.T) {
	raw := `{"Result": {"AFPFiveHour": {"Quota": "1000", "Used": "1500", "ResetTime": 1767235000000}}}`
	snapshot := decodeAgentPlan(t, raw)

	if len(snapshot.Windows) != 1 {
		t.Fatalf("windows = %d, want 1", len(snapshot.Windows))
	}
	if snapshot.Windows[0].Percent != 100 {
		t.Errorf("percent = %v, want 100", snapshot.Windows[0].Percent)
	}
}

// TestUnknownActionDetection 保证只有"不认识这个 Action"才触发换名重试。
func TestUnknownActionDetection(t *testing.T) {
	if !isUnknownAction(&volcAPIError{Action: "GetAFPUsage", Code: "InvalidAction"}) {
		t.Error("InvalidAction should be treated as unknown action")
	}
	if isUnknownAction(&volcAPIError{Action: "GetAFPUsage", Code: "InvalidAccessKey"}) {
		t.Error("InvalidAccessKey must not trigger a retry with another action")
	}
	if isUnknownAction(errors.New("network error")) {
		t.Error("non-api error must not be treated as unknown action")
	}
}
