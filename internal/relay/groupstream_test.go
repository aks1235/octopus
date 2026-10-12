package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

func TestGroupMembersStream_reportsFastMemberFirst(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Model == "model-group-test-live" {
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(chatCompletionBody()))
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	if err := db.GetDB().Create(&model.ChannelModel{ID: 3, ChannelID: 1, Name: "stream-fast"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.ChannelGrant{ID: 3, ChannelModelID: 3, ChannelKeyID: 1, Protocols: model.ProtocolOpenAIChatCompletion}).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	group.Items = []model.GroupItem{groupTestItem(t, group, 1), {ID: 999, ChannelID: 1, ChannelGrantID: 3}}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan model.GroupMemberTestResult, len(group.Items))
	done := make(chan struct{})
	go func() {
		TestGroupMembersStream(ctx, group, true, func(result model.GroupMemberTestResult) { results <- result })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	select {
	case result := <-results:
		if result.ItemID != 999 || !result.Success || result.Status != "passed" {
			t.Fatalf("first result = %+v, want fast member success", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fast result held behind slow member")
	}
	select {
	case <-done:
		t.Fatal("test completed while slow member still blocked")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream failed to finish after releasing slow member")
	}
	if result := <-results; !result.Success || result.Status != "passed" {
		t.Fatalf("slow result = %+v, want success", result)
	}
}

func TestGroupMembersStream_cancelsQueuedMembersAndLimitsChannelConcurrency(t *testing.T) {
	started := make(chan struct{}, 16)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		hits.Add(1)
		started <- struct{}{}
		<-request.Context().Done()
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	live := groupTestItem(t, group, 1)
	group.Items = nil
	for index := range 12 {
		item := live
		item.ID = 100 + index
		group.Items = append(group.Items, item)
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan model.GroupMemberTestResult, len(group.Items))
	done := make(chan struct{})
	go func() {
		TestGroupMembersStream(ctx, group, true, func(result model.GroupMemberTestResult) { results <- result })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("expected two concurrent upstream requests")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queued members did not cancel promptly")
	}
	if hits.Load() != 2 || len(results) != len(group.Items) {
		t.Fatalf("hits=%d results=%d, want two requests and %d results", hits.Load(), len(results), len(group.Items))
	}
	seen := make(map[int]bool)
	for range group.Items {
		result := <-results
		if result.Success || result.Status != "canceled" || seen[result.ItemID] {
			t.Fatalf("unexpected canceled result: %+v", result)
		}
		seen[result.ItemID] = true
	}
}

func TestGroupMemberWithTimeout_marksTimeoutAndKeepsCooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	live := groupTestItem(t, group, 1)
	deadline := time.Now().Add(time.Minute).UnixMilli()
	routeMu.Lock()
	routes[group.ID] = &RouteState{CurrentItemID: live.ID, Cooldowns: map[int]int64{live.ID: deadline}, trips: map[int]int{live.ID: 1}}
	routeMu.Unlock()
	defer ResetRouteState(group.ID)
	result := testGroupMemberWithTimeout(context.Background(), group, live, 30*time.Millisecond)
	if result.Success || result.Status != "timeout" || result.CooldownCleared {
		t.Fatalf("result=%+v, want timeout without recovery", result)
	}
	routeMu.Lock()
	remaining := routes[group.ID].Cooldowns[live.ID]
	routeMu.Unlock()
	if remaining != deadline {
		t.Fatalf("cooldown=%d, want unchanged %d", remaining, deadline)
	}
}

func TestGroupMemberWithTimeout_doesNotFallbackAfterTimeout(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		hits.Add(1)
		if request.URL.Path == "/v1/chat/completions" {
			<-request.Context().Done()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(chatCompletionBody()))
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	if err := db.GetDB().Model(&model.ChannelGrant{}).Where("id = ?", 1).Update("protocols", model.ProtocolAnthropicMessage|model.ProtocolOpenAIChatCompletion).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	result := testGroupMemberWithTimeout(context.Background(), group, groupTestItem(t, group, 1), 50*time.Millisecond)
	if result.Success || result.Status != "timeout" || hits.Load() != 1 {
		t.Fatalf("result = %+v hits=%d, want timeout without fallback", result, hits.Load())
	}
}

func TestGroupMemberWithTimeout_stopsProtocolFallbackAfterTimeout(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		hits.Add(1)
		<-request.Context().Done()
	}))
	defer server.Close()
	group := seedGroupTestDB(t, server.URL)
	if err := db.GetDB().Model(&model.ChannelGrant{}).Where("id = ?", 1).Update("protocols", model.ProtocolAnthropicMessage|model.ProtocolOpenAIResponse|model.ProtocolOpenAIChatCompletion).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	group.Items = []model.GroupItem{groupTestItem(t, group, 1)}
	if err := db.GetDB().Model(&model.ChannelGrant{}).Where("id = ?", 1).Update("protocols", model.ProtocolAnthropicMessage|model.ProtocolOpenAIChatCompletion).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	result := testGroupMemberWithTimeout(context.Background(), group, group.Items[0], 30*time.Millisecond)
	if result.Success || result.Status != "timeout" || hits.Load() != 1 {
		t.Fatalf("result=%+v hits=%d, want one timed-out attempt and no fallback", result, hits.Load())
	}
}
