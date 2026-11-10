package op

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 探测结论的判定口径是白名单而非比率: 确定性错误(模型不存在/凭据失效/额度耗尽)一次即判死,
// 抖动类(5xx/超时/形状不符)连续三次才判死。下面两个测试锚点分别盯住"判据"和"落库状态机"。

func TestProbeFailureIsFatal(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		// 生产实测的确定性形态: 重试不会自愈。
		{"410 gone", `upstream responded 410 Gone: {"error":{"message":"The model 'deepseek-ai/deepseek-v4-pro-0' does not exist"}}`, true},
		{"503 model_not_found", `upstream responded 503 Service Unavailable: {"error":{"code":"model_not_found"}}`, true},
		{"400 unknown provider", `upstream responded 400 Bad Request: {"error":{"message":"unknown provider for model deepseek-v4-pro"}}`, true},
		{"429 insufficient_credits", `upstream responded 429 Too Many Requests: {"error":{"message":"insufficient_credits: Your account"}}`, true},
		{"401 unauthorized", `upstream responded 401 Unauthorized: invalid api key`, true},
		{"403 keyword", `upstream responded 403 Forbidden: permission_error`, true},
		{"额度中文", `上游返回：余额不足`, true},

		// 抖动类: 只计数, 不在单次判定里定性。
		{"502 html", `upstream responded 502 Bad Gateway: <!DOCTYPE html>`, false},
		{"503 gateway", `upstream responded 503 Service Unavailable: {"error":{"message":"Gateway attempt budget exhausted"}}`, false},
		{"500", `upstream responded 500 Internal Server Error`, false},
		{"non-stream timeout", `upstream non-stream response timeout`, false},
		{"stream first event timeout", `upstream stream first event timeout`, false},
		{"400 参数类无关键词", `upstream responded 400 Bad Request: invalid max_tokens`, false},

		// 状态码只在"像状态行"的位置才算: 前有 status/responded, 或后接 Gone/Unauthorized 等短语。
		{"URL 端口不误判", `POST - https://api.4010.com/v1/chat/completions with status 502 Bad Gateway`, false},
		{"token 数字不误判", `upstream responded 429: used 4102 of 410 tokens`, false},
		{"裸 token 计数不误判", `used 410 of 5000 tokens`, false},
		{"裸状态行仍然认", `410 Gone`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeFailureIsFatal(tc.reason); got != tc.want {
				t.Fatalf("probeFailureIsFatal(%q) = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

// seedProbeGrant 建一条最小可用的渠道/凭据/模型/授权/分组成员, 并把授权放进进程内缓存
// (探测结论写库后同步改缓存, 分组读取的展示字段取自缓存)。
func seedProbeGrant(t *testing.T) model.ChannelGrant {
	t.Helper()
	engine := db.GetDB()
	channel := model.Channel{ID: 1, ChannelConfig: model.ChannelConfig{
		Name: "c1", Dialect: model.DialectGeneric, BaseURL: "https://upstream.example",
	}}
	if err := engine.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-1", Enabled: true}}
	if err := engine.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	channelModel := model.ChannelModel{ID: 1, ChannelID: 1, Name: "m1"}
	if err := engine.Create(&channelModel).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	grant := model.ChannelGrant{ID: 1, ChannelModelID: 1, ChannelKeyID: 1, Protocols: 14}
	if err := engine.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	group := model.Group{ID: 1, Name: "g1", Mode: model.GroupModeFailover}
	if err := engine.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	item := model.GroupItem{ID: 1, GroupID: 1, ChannelGrantID: 1, Priority: 1}
	if err := engine.Create(&item).Error; err != nil {
		t.Fatalf("create group item: %v", err)
	}
	channelGrantCache.Set(grant.ID, grant)
	return grant
}

func probeGrantFromDB(t *testing.T) model.ChannelGrant {
	t.Helper()
	var grant model.ChannelGrant
	if err := db.GetDB().First(&grant, 1).Error; err != nil {
		t.Fatalf("reload grant: %v", err)
	}
	return grant
}

func TestGroupProbeApply_verdicts(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	seedProbeGrant(t)

	// 不可用与取消都不是探测结论: 前者没发上游请求, 后者是客户端断开。
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "unavailable", Error: "channel disabled"},
		{ItemID: 1, Status: "canceled", Error: "context canceled"},
	}); err != nil {
		t.Fatalf("apply unavailable/canceled: %v", err)
	}
	if got := probeGrantFromDB(t); got.ProbeAt != 0 || got.ProbeStatus != ProbeStatusUnknown {
		t.Fatalf("unavailable/canceled must not be recorded, got %+v", got)
	}

	// 抖动第一次: 只记录原因与时间, 不定性(既不绿也不死)。
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "failed", Error: "upstream responded 502 Bad Gateway"},
	}); err != nil {
		t.Fatalf("apply 502: %v", err)
	}
	got := probeGrantFromDB(t)
	if got.ProbeStatus != ProbeStatusUnknown || got.ProbeFailCount != 1 || got.ProbeAt == 0 {
		t.Fatalf("after first jitter: %+v, want unknown/count=1/probe_at>0", got)
	}

	// 抖动第二次仍不定性, 第三次判死。
	for i := 0; i < 2; i++ {
		if err := GroupProbeApply(1, []model.GroupMemberTestResult{
			{ItemID: 1, Status: "timeout", Error: "upstream stream first event timeout"},
		}); err != nil {
			t.Fatalf("apply timeout: %v", err)
		}
	}
	if got := probeGrantFromDB(t); got.ProbeStatus != ProbeStatusDead || got.ProbeFailCount != ProbeDeadAfterFailures {
		t.Fatalf("after %d jitters: %+v, want dead", ProbeDeadAfterFailures, got)
	}

	// 成功即回到 ok 并清零, 原因清空。
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "passed", Success: true},
	}); err != nil {
		t.Fatalf("apply success: %v", err)
	}
	got = probeGrantFromDB(t)
	if got.ProbeStatus != ProbeStatusOK || got.ProbeFailCount != 0 || got.ProbeReason != "" {
		t.Fatalf("after success: %+v, want ok/0/empty", got)
	}

	// 确定性错误一次即判死, 且与已有的 ok 无关。
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "failed", Error: `upstream responded 410 Gone: The model 'm1' does not exist`},
	}); err != nil {
		t.Fatalf("apply fatal: %v", err)
	}
	got = probeGrantFromDB(t)
	if got.ProbeStatus != ProbeStatusDead {
		t.Fatalf("after fatal: %+v, want dead", got)
	}
	// 缓存与库同步: 分组读取的展示字段取自缓存, 不同步就会看到旧状态。
	cached, ok := channelGrantCache.Get(1)
	if !ok || cached.ProbeStatus != ProbeStatusDead {
		t.Fatalf("cache not synced: ok=%v grant=%+v", ok, cached)
	}
}

func TestGroupProbeApply_ignoresUnknownItem(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe2.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	seedProbeGrant(t)
	// 成员已被移除(测试进行中移除)时结果仍会回来, 不得为此报错或错误归因到别的授权。
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 999, Status: "failed", Error: "upstream responded 410 Gone"},
	}); err != nil {
		t.Fatalf("apply unknown item: %v", err)
	}
	if got := probeGrantFromDB(t); got.ProbeAt != 0 {
		t.Fatalf("unknown item must not touch grant, got %+v", got)
	}
}
