package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// TestGroupContextLength_roundTrip 验证上下文窗口的存读闭环:
// 声明过就出现在映射里, 未声明的分组不出现(客户端据此决定是否自行推断), 改成 0 等于取消声明。
func TestGroupContextLength_roundTrip(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "group-context-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	declared, err := GroupCreate(&model.GroupCreateRequest{Name: "declared-model", ContextLength: 262144}, ctx)
	if err != nil {
		t.Fatalf("GroupCreate(declared) error = %v", err)
	}
	if declared.ContextLength != 262144 {
		t.Fatalf("创建的 ContextLength = %d, want 262144", declared.ContextLength)
	}

	// 未填写时按默认值声明: 客户端拿不到声明会回落到家族默认值, 压缩阈值因此偏早。
	fallback, err := GroupCreate(&model.GroupCreateRequest{Name: "default-model"}, ctx)
	if err != nil {
		t.Fatalf("GroupCreate(default) error = %v", err)
	}
	if fallback.ContextLength != model.DefaultGroupContextLength {
		t.Fatalf("未填写时 ContextLength = %d, want %d", fallback.ContextLength, model.DefaultGroupContextLength)
	}

	lengths := GroupContextLengths()
	if got := lengths["declared-model"]; got != 262144 {
		t.Fatalf("GroupContextLengths()[declared-model] = %d, want 262144", got)
	}
	if got := lengths["default-model"]; got != model.DefaultGroupContextLength {
		t.Fatalf("默认分组的窗口 = %d, want %d", got, model.DefaultGroupContextLength)
	}

	// 改成 0 表示恢复默认值, 不是取消声明: 声明总是存在, 客户端不必自行推断。
	zero := 0
	if _, err := GroupUpdate(declared.ID, &model.GroupUpdateRequest{ContextLength: &zero}, ctx); err != nil {
		t.Fatalf("GroupUpdate(ContextLength=0) error = %v", err)
	}
	if got := GroupContextLengths()["declared-model"]; got != model.DefaultGroupContextLength {
		t.Fatalf("重置为 0 后 = %d, want 默认值 %d", got, model.DefaultGroupContextLength)
	}
}
