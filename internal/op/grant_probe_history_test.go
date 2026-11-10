package op

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 探测历史是审计数据: 每次落库都要留一行, 汇总要能回答"这周挂过几次、最近错在哪"。
// 它与授权上的 probe_* 结论列是两套东西——结论只存最新一次, 历史用于回看。

func TestGroupProbeApply_writesHistory(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-history.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	seedProbeGrant(t)
	// 汇总与时间线要回查渠道/模型/凭据名称, 走的是各自的缓存, 故先按库内数据重建一次缓存。
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "passed", Success: true, Protocol: model.ProtocolOpenAIChatCompletion, UseTime: 1234},
	}, model.ProbeSourceScheduled); err != nil {
		t.Fatalf("apply success: %v", err)
	}
	if err := GroupProbeApply(1, []model.GroupMemberTestResult{
		{ItemID: 1, Status: "failed", Error: "upstream responded 410 Gone", UseTime: 88},
	}, model.ProbeSourceManual); err != nil {
		t.Fatalf("apply failure: %v", err)
	}

	var records []model.GrantProbeRecord
	if err := db.GetDB().Order("id").Find(&records).Error; err != nil {
		t.Fatalf("load records: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if !records[0].Success || records[0].Source != model.ProbeSourceScheduled || records[0].UseTime != 1234 {
		t.Fatalf("first record = %+v, want scheduled success with use_time", records[0])
	}
	if records[1].Success || records[1].Source != model.ProbeSourceManual || !strings.Contains(records[1].Reason, "410") {
		t.Fatalf("second record = %+v, want manual failure with reason", records[1])
	}

	timeline, err := GrantProbeTimeline(1, 90)
	if err != nil {
		t.Fatalf("GrantProbeTimeline() error = %v", err)
	}
	if timeline.OK != 1 || timeline.Failed != 1 || len(timeline.Records) != 2 {
		t.Fatalf("timeline = ok:%d failed:%d records:%d, want 1/1/2", timeline.OK, timeline.Failed, len(timeline.Records))
	}
	if !strings.Contains(timeline.LastError, "410") {
		t.Fatalf("last error = %q, want the 410 reason", timeline.LastError)
	}
	if timeline.ChannelName == "" || timeline.ModelName == "" || timeline.KeyName == "" {
		t.Fatalf("timeline names not filled: %+v", timeline)
	}

	rows, err := GrantProbeGroupSummary(1, 30)
	if err != nil {
		t.Fatalf("GrantProbeGroupSummary() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("summary rows = %d, want 1", len(rows))
	}
	if rows[0].OK != 1 || rows[0].Failed != 1 || !strings.Contains(rows[0].LastError, "410") {
		t.Fatalf("summary row = %+v, want ok:1 failed:1 with last error", rows[0])
	}
}

// 窗口过滤: 超出 days 的记录不进汇总, 但仍在表里(等保留期清理)。
func TestGrantProbeQueries_respectWindow(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-window.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	seedProbeGrant(t)

	now := time.Now().Unix()
	rows := []model.GrantProbeRecord{
		{GrantID: 1, At: now - 40*86400, Success: false, Reason: "old failure"},
		{GrantID: 1, At: now, Success: true},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}

	recent, err := GrantProbeTimeline(1, 7)
	if err != nil {
		t.Fatalf("GrantProbeTimeline() error = %v", err)
	}
	if recent.OK != 1 || recent.Failed != 0 {
		t.Fatalf("7-day timeline = ok:%d failed:%d, want 1/0", recent.OK, recent.Failed)
	}

	wide, err := GrantProbeTimeline(1, 90)
	if err != nil {
		t.Fatalf("GrantProbeTimeline() error = %v", err)
	}
	if wide.OK != 1 || wide.Failed != 1 {
		t.Fatalf("90-day timeline = ok:%d failed:%d, want 1/1", wide.OK, wide.Failed)
	}
}

// 保留期: 只删窗口外的行; 0 表示不清理(与转发日志 keep_period 同语义)。
func TestGrantProbePrune(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "probe-prune.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	seedProbeGrant(t)

	now := time.Now().Unix()
	rows := []model.GrantProbeRecord{
		{GrantID: 1, At: now - 100*86400, Success: false, Reason: "ancient"},
		{GrantID: 1, At: now, Success: true},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}

	if pruned, err := GrantProbePrune(0); err != nil || pruned != 0 {
		t.Fatalf("prune(0) = %d/%v, want 0/nil (disable pruning)", pruned, err)
	}
	pruned, err := GrantProbePrune(90)
	if err != nil {
		t.Fatalf("prune(90) error = %v", err)
	}
	if pruned != 1 {
		t.Fatalf("prune(90) = %d, want 1", pruned)
	}
	var left int64
	if err := db.GetDB().Model(&model.GrantProbeRecord{}).Count(&left).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if left != 1 {
		t.Fatalf("remaining records = %d, want 1", left)
	}
}
