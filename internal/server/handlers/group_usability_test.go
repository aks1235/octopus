package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// 本文件锁定分组成员级测试与冷却重置两个端点的 handler 层契约:
// 404 兜底、空分组返回空数组(非 null, 前端可直接遍历)、RequireJSON 对空 body 的放行边界。
// 深层语义(清冷却不动当前路由/亲和、逐成员结果)在 relay 层单测覆盖, 此处只走真实中间件链。

// TestGroupMemberEndpoints_notFound 锁定错误映射: 不存在的分组对两个端点都返回 404。
func TestGroupMemberEndpoints_notFound(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/test/99", gin.H{})
	if code != http.StatusNotFound {
		t.Fatalf("test missing group status = %d body = %v, want 404", code, body)
	}
	code, body = doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/cooldown-reset/99", gin.H{})
	if code != http.StatusNotFound {
		t.Fatalf("cooldown-reset missing group status = %d body = %v, want 404", code, body)
	}
}

// TestGroupMemberEndpoints_rejectsWithoutJSONContentType 锁定 RequireJSON 边界:
// POST 不带 application/json 头直接被中间件以 415 拒绝, 不会触发任何测试请求。
func TestGroupMemberEndpoints_rejectsWithoutJSONContentType(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	// body 传 nil 时不带 Content-Type, 复现"裸 POST"被拒的路径。
	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/test/2", nil)
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("bare post status = %d body = %v, want 415", code, body)
	}
}

// TestGroupMembers_emptyGroupReturnsArray 锁定空成员分组的行为:
// 空 body + JSON 头可过 RequireJSON, 测试端点返回 200 且 data 为空数组而非 null。
// 分组 2 是无成员的手动分组, 由此一并覆盖"手动模式测试端点同样可用"的入口行为。
func TestGroupMembers_emptyGroupReturnsArray(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/test/2", gin.H{})
	if code != http.StatusOK {
		t.Fatalf("empty group test status = %d body = %v, want 200", code, body)
	}
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data = %v (%T), want JSON array; nil 会让前端遍历崩溃", body["data"], body["data"])
	}
	if len(data) != 0 {
		t.Fatalf("data = %v, want empty array", data)
	}
}

// TestResetGroupCooldown_respondsWithRuntime 锁定响应形状: 与 getGroup 同款携带 runtime,
// 手动分组(无进程内路由状态)重置是安全空操作, 前端由此拿到最新状态对齐缓存。
func TestResetGroupCooldown_respondsWithRuntime(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/cooldown-reset/2", gin.H{})
	if code != http.StatusOK {
		t.Fatalf("cooldown-reset status = %d body = %v, want 200", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response data missing: %v", body)
	}
	runtime, _ := data["runtime"].(map[string]any)
	if runtime == nil {
		t.Fatalf("response runtime missing: %v", data)
	}
	cooldowns, ok := runtime["cooldowns"].(map[string]any)
	if !ok {
		t.Fatalf("runtime.cooldowns = %v (%T), want object", runtime["cooldowns"], runtime["cooldowns"])
	}
	if len(cooldowns) != 0 {
		t.Fatalf("runtime.cooldowns = %v, want empty", cooldowns)
	}
}
