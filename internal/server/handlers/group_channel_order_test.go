package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// seedOrderHandlerDB 建一个渠道(两模型)与一个正则分组、一个手动分组, 供顺序端点测试。
// 渠道 1(chan-a/key-a)带 glm-4.6-flash 与 glm-4.6-pro; 分组 rgx 是正则分组, manual 是手动分组。
func seedOrderHandlerDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "order-handler-test.db"), false); err != nil {
		t.Fatalf("InitDB() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.GetDB().Create(&model.Channel{
		ID:            1,
		ChannelConfig: model.ChannelConfig{Name: "chan-a", BaseURL: "http://upstream"},
	}).Error; err != nil {
		t.Fatalf("create channel 1: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelKey{
		ID: 1, ChannelID: 1,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: "key-a", Key: "sk-key-a"},
	}).Error; err != nil {
		t.Fatalf("create channel key 1: %v", err)
	}
	for i, name := range []string{"glm-4.6-flash", "glm-4.6-pro"} {
		if err := db.GetDB().Create(&model.ChannelModel{ID: i + 1, ChannelID: 1, Name: name}).Error; err != nil {
			t.Fatalf("create channel model %d: %v", i+1, err)
		}
		if err := db.GetDB().Create(&model.ChannelGrant{
			ID: i + 1, ChannelModelID: i + 1, ChannelKeyID: 1,
			Protocols: model.ProtocolOpenAIChatCompletion,
		}).Error; err != nil {
			t.Fatalf("create channel grant %d: %v", i+1, err)
		}
	}
	groups := []model.Group{
		{ID: 1, Name: "rgx", Mode: model.GroupModeFailover, MemberRegex: "^glm-",
			RelayConfig: model.DefaultGroupRelayConfig()},
		{ID: 2, Name: "manual", Mode: model.GroupModeManual,
			RelayConfig: model.DefaultGroupRelayConfig()},
	}
	for i := range groups {
		if err := db.GetDB().Create(&groups[i]).Error; err != nil {
			t.Fatalf("create group %d: %v", groups[i].ID, err)
		}
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("InitCache() error = %v", err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatalf("UserInit() error = %v", err)
	}
}

// orderHandlerEngineOnce 保证整个测试进程只构建一次引擎:
// router.RegisterAll 注册完全局路由表后会将其清空, 第二次调用会拿到空表, 全部路由 404。
var (
	orderHandlerEngineOnce sync.Once
	orderHandlerEngine     *gin.Engine
)

// newOrderHandlerEngine 返回挂完整分组路由与鉴权中间件的共享测试引擎。
// 直接复用 handlers 包 init 里注册的路由表: 走真实中间件链(Auth + RequireJSON),
// 断言的是线上路径的行为而非裸函数。
func newOrderHandlerEngine(t *testing.T) *gin.Engine {
	t.Helper()
	orderHandlerEngineOnce.Do(func() {
		gin.SetMode(gin.TestMode)
		orderHandlerEngine = gin.New()
		if err := router.RegisterAll(orderHandlerEngine); err != nil {
			t.Fatalf("router.RegisterAll() error = %v", err)
		}
	})
	return orderHandlerEngine
}

// doOrderRequest 以登录用户身份发起顺序端点请求, 返回状态码与解析后的响应体。
func doOrderRequest(t *testing.T, engine *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	token, _, err := auth.GenerateJWTToken(600)
	if err != nil {
		t.Fatalf("GenerateJWTToken() error = %v", err)
	}
	var req *http.Request
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err = http.NewRequest(method, path, strings.NewReader(string(payload)))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
	}
	req.AddCookie(&http.Cookie{Name: "auth", Value: token})
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	var parsed map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal response %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, parsed
}

// TestSaveGroupChannelOrder_rejectsDuplicateChannelIDs 锁定 handler 层去重校验:
// channel_ids 重复直接 400, 不落任何顺序行。
func TestSaveGroupChannelOrder_rejectsDuplicateChannelIDs(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/channel-order/1", gin.H{
		"channel_ids": []int{1, 1},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %v, want 400 for duplicate channel ids", code, body)
	}
	var rows []model.GroupChannelOrder
	if err := db.GetDB().Where("group_id = ?", 1).Find(&rows).Error; err != nil {
		t.Fatalf("load order rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("order rows = %+v, want none persisted on 400", rows)
	}
}

// TestSaveGroupChannelOrder_dropsDanglingChannelID 锁定端点层的悬空渠道容错(回归 500):
// channel_ids 含库内不存在的渠道时返回 200 且该 ID 不落库, 而不是外键冲突导致 500。
func TestSaveGroupChannelOrder_dropsDanglingChannelID(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/channel-order/1", gin.H{
		"channel_ids": []int{1, 9999},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d body = %v, want 200 for dangling channel id", code, body)
	}
	var rows []model.GroupChannelOrder
	if err := db.GetDB().Where("group_id = ?", 1).Find(&rows).Error; err != nil {
		t.Fatalf("load order rows: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelID != 1 {
		t.Fatalf("order rows = %+v, want only channel 1 persisted", rows)
	}
}

// TestSaveGroupChannelOrder_manualGroupAndMissingGroup 锁定错误映射:
// 手动分组调顺序端点 400, 不存在的分组 404。
func TestSaveGroupChannelOrder_manualGroupAndMissingGroup(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/channel-order/2", gin.H{
		"channel_ids": []int{1},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("manual group status = %d body = %v, want 400", code, body)
	}
	code, body = doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/channel-order/99", gin.H{
		"channel_ids": []int{1},
	})
	if code != http.StatusNotFound {
		t.Fatalf("missing group status = %d body = %v, want 404", code, body)
	}
}

// TestSaveGroupChannelOrder_appliesOrder 锁定正常路径:
// 保存顺序返回重算后的分组(成员带合成优先级), DELETE 清空后回到自然序。
func TestSaveGroupChannelOrder_appliesOrder(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)

	code, body := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/channel-order/1", gin.H{
		"channel_ids": []int{1},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d body = %v, want 200", code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		t.Fatalf("response data missing: %v", body)
	}
	runtime, _ := data["runtime"].(map[string]any)
	if runtime == nil {
		t.Fatalf("response runtime missing: %v", data)
	}
	// 响应分组带两个成员(正则 ^glm- 命中两个模型授权)。
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("response items = %v, want 2 members", data["items"])
	}

	code, body = doOrderRequest(t, engine, http.MethodDelete, "/api/v1/group/channel-order/1", nil)
	if code != http.StatusOK {
		t.Fatalf("delete status = %d body = %v, want 200", code, body)
	}
	var rows []model.GroupChannelOrder
	if err := db.GetDB().Where("group_id = ?", 1).Find(&rows).Error; err != nil {
		t.Fatalf("load order rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("order rows = %+v, want cleared after DELETE", rows)
	}
	// 重算不报错且成员仍在(顺序回到自然序, 成员集合不变)。
	group, err := op.GroupGet(1)
	if err != nil {
		t.Fatalf("GroupGet(1) error = %v", err)
	}
	if len(group.Items) != 2 {
		t.Fatalf("group items = %+v, want 2 members after reset", group.Items)
	}
}
