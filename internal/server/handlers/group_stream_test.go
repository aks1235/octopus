package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/gin-gonic/gin"
)

func groupStreamRequest(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := auth.GenerateJWTToken(600)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	recorder := httptest.NewRecorder()
	newOrderHandlerEngine(t).ServeHTTP(recorder, request)
	return recorder
}

func decodeGroupTestEvents(t *testing.T, recorder *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK || !recorder.Flushed || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("stream response: status=%d flushed=%v headers=%v body=%s", recorder.Code, recorder.Flushed, recorder.Header(), recorder.Body.String())
	}
	decoder := json.NewDecoder(recorder.Body)
	var events []map[string]any
	for {
		var event map[string]any
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestGroupMemberStream_emptyGroup(t *testing.T) {
	seedOrderHandlerDB(t)
	for _, mode := range []string{"full"} {
		events := decodeGroupTestEvents(t, groupStreamRequest(t, "/api/v1/group/test/2/stream?mode="+mode))
		if len(events) != 2 || events[0]["type"] != "start" || events[0]["total"] != float64(0) || events[1]["type"] != "done" {
			t.Fatalf("%s events = %v, want start(total=0), done", mode, events)
		}
	}
}

func TestGroupMemberStream_memberEvents(t *testing.T) {
	seedOrderHandlerDB(t)
	items := []model.GroupItemInput{{ChannelGrantID: 1}, {ChannelGrantID: 2}}
	if _, err := op.GroupUpdate(2, &model.GroupUpdateRequest{Items: &items}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", 1).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"full"} {
		events := decodeGroupTestEvents(t, groupStreamRequest(t, "/api/v1/group/test/2/stream?mode="+mode))
		if len(events) != 4 || events[0]["type"] != "start" || events[0]["total"] != float64(2) || events[3]["type"] != "done" {
			t.Fatalf("events = %v, want start, two results, done", events)
		}
		seen := make(map[float64]bool)
		for _, event := range events[1:3] {
			result, ok := event["result"].(map[string]any)
			if event["type"] != "result" || !ok || result["success"] != false || result["status"] != "unavailable" {
				t.Fatalf("unexpected result event: %v", event)
			}
			itemID := result["item_id"].(float64)
			if seen[itemID] {
				t.Fatalf("duplicate result: %v", result)
			}
			seen[itemID] = true
		}
	}
}

func TestGroupMemberStream_validationAndExclusion(t *testing.T) {
	seedOrderHandlerDB(t)
	engine := newOrderHandlerEngine(t)
	for _, check := range []struct {
		path   string
		status int
	}{
		{"/api/v1/group/test/99/stream", http.StatusNotFound},
		{"/api/v1/group/test/invalid/stream", http.StatusBadRequest},
		{"/api/v1/group/test/2/stream?mode=invalid", http.StatusBadRequest},
	} {
		code, _ := doOrderRequest(t, engine, http.MethodPost, check.path, gin.H{})
		if code != check.status {
			t.Fatalf("%s status = %d, want %d", check.path, code, check.status)
		}
	}
	code, _ := doOrderRequest(t, engine, http.MethodPost, "/api/v1/group/test/2/stream", nil)
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing JSON content type status = %d", code)
	}
	groupTests.Store(2, struct{}{})
	defer groupTests.Delete(2)
	for _, path := range []string{"/api/v1/group/test/2", "/api/v1/group/test/2/stream?mode=full"} {
		code, _ := doOrderRequest(t, engine, http.MethodPost, path, gin.H{})
		if code != http.StatusConflict {
			t.Fatalf("%s status = %d, want conflict", path, code)
		}
	}
}
