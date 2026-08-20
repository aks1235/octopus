package handlers

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/log").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/overview/stream", http.MethodGet).
				Handle(streamOverview),
		).
		AddRoute(
			router.NewRoute("/:id/stream", http.MethodGet).
				Handle(streamDetail),
		).
		AddRoute(
			router.NewRoute("/:id/request-body", http.MethodGet).
				Handle(getRequestBody),
		).
		AddRoute(
			router.NewRoute("/:id/response-body", http.MethodGet).
				Handle(getResponseBody),
		).
		AddRoute(
			router.NewRoute("/:request_id/:attempt_index/stop", http.MethodPost).
				Handle(interruptAttempt),
		).
		AddRoute(
			router.NewRoute("/clear", http.MethodDelete).
				Handle(clearLog),
		).
		AddRoute(
			router.NewRoute("/channel-attempts", http.MethodGet).
				Handle(getChannelAttempts),
		)
}

// interruptAttempt 中止请求当前序号匹配的上游尝试。
func interruptAttempt(c *gin.Context) {
	requestID, err := strconv.ParseUint(c.Param("request_id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	attemptIndex, err := strconv.Atoi(c.Param("attempt_index"))
	if err != nil || attemptIndex < 1 {
		resp.Error(c, http.StatusBadRequest, "invalid attempt index")
		return
	}
	relay.InterruptAttempt(requestID, attemptIndex)
	c.Status(http.StatusNoContent)
}

// clearLog 删除全部已完成的请求记录，并在释放记录引用后主动执行垃圾回收。
func clearLog(c *gin.Context) {
	relay.ClearLogs()
	runtime.GC()
	log.Debugf("relay log history cleared")
	c.Status(http.StatusNoContent)
}

// getChannelAttempts 返回指定渠道在内存日志窗口内被尝试的每次调用明细。
func getChannelAttempts(c *gin.Context) {
	name := strings.TrimSpace(c.Query("channel_name"))
	if name == "" {
		resp.Error(c, http.StatusBadRequest, "missing channel_name")
		return
	}
	resp.Success(c, relay.GetLogAttemptsByChannel(name))
}

// getRequestBody 返回指定请求的原始请求体。
func getRequestBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	body, found := relay.GetLogRequestBody(id)
	if !found {
		resp.Error(c, http.StatusNotFound, "request log not found")
		return
	}
	resp.Success(c, body)
}

// getResponseBody 返回指定请求当前保存的响应体。
func getResponseBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	body, found := relay.GetLogResponseBody(id)
	if !found {
		resp.Error(c, http.StatusNotFound, "request log not found")
		return
	}
	resp.Success(c, body)
}

// streamOverview 逐条发送建立连接时的概览及后续请求更新。
func streamOverview(c *gin.Context) {
	prepareSSE(c)
	snapshot, updates := relay.OpenLogOverview()
	log.Debugf("relay log overview stream opened: snapshot=%d", len(snapshot))
	defer func() {
		relay.CloseLogOverview(updates)
		log.Debugf("relay log overview stream closed")
	}()
	for _, overview := range snapshot {
		if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: overview}); err != nil {
			return
		}
		c.Writer.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case overview, ok := <-updates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: overview}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// streamDetail 先发送当前运行或已提交状态，再持续发送后续尝试更新。
func streamDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	updates, found := relay.OpenLogDetail(id)
	if !found {
		resp.Error(c, http.StatusNotFound, "request log not found")
		return
	}
	log.Debugf("relay log detail stream opened: request_id=%d", id)
	defer func() {
		relay.CloseLogDetail(updates)
		log.Debugf("relay log detail stream closed: request_id=%d", id)
	}()
	prepareSSE(c)
	c.Writer.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case update, ok := <-updates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: string(update.Type), Data: update}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// prepareSSE 设置实时日志连接需要的响应头。
func prepareSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}
