package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/log").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listLog),
		).
		AddRoute(
			router.NewRoute("/channel-attempts", http.MethodGet).
				Handle(channelAttempts),
		).
		AddRoute(
			router.NewRoute("/clear", http.MethodDelete).
				Handle(clearLog),
		).
		AddRoute(
			router.NewRoute("/stream-token", http.MethodGet).
				Handle(getStreamToken),
		).
		AddRoute(
			router.NewRoute("/active", http.MethodGet).
				Handle(listActiveRequests),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodGet).
				Handle(getLogDetail),
		)

	router.NewGroupRouter("/api/v1/log").
		AddRoute(
			router.NewRoute("/stream", http.MethodGet).
				Handle(streamLog),
		)
}

func listLog(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	startTimeStr := c.Query("start_time")
	endTimeStr := c.Query("end_time")
	hasError := c.Query("has_error") == "true"
	apiKeyNamesStr := c.Query("api_key_names")
	var apiKeyNames []string
	if apiKeyNamesStr != "" {
		apiKeyNames = strings.Split(apiKeyNamesStr, ",")
	}
	modelNamesStr := c.Query("model_names")
	var modelNames []string
	if modelNamesStr != "" {
		modelNames = strings.Split(modelNamesStr, ",")
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var startTime, endTime *int
	if startTimeStr != "" && endTimeStr != "" {
		st, err := strconv.Atoi(startTimeStr)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		et, err := strconv.Atoi(endTimeStr)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		startTime = &st
		endTime = &et
	}

	logs, err := op.RelayLogList(c.Request.Context(), startTime, endTime, page, pageSize, hasError, apiKeyNames, modelNames)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp.Success(c, logs)
}

// channelAttemptsResponse 按渠道查调用明细的响应体。
type channelAttemptsResponse struct {
	List      []model.ChannelAttemptDetail `json:"list"`
	Total     int                          `json:"total"`
	Truncated bool                         `json:"truncated"` // 粗筛行数触顶时为 true,前端提示"仅展示最近 N 条"
}

// channelAttempts 按渠道返回其保留期内的每次调用明细(展开 attempts),只读,挂鉴权组。
func channelAttempts(c *gin.Context) {
	channelIDStr := c.Query("channel_id")
	if channelIDStr == "" {
		resp.Error(c, http.StatusBadRequest, "missing channel_id")
		return
	}
	channelID, err := strconv.Atoi(channelIDStr)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid channel_id")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	list, total, truncated, err := op.RelayLogAttemptsByChannel(c.Request.Context(), channelID, page, pageSize)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		// 返回非 null 的空数组,前端解构更稳
		list = []model.ChannelAttemptDetail{}
	}

	resp.Success(c, channelAttemptsResponse{
		List:      list,
		Total:     total,
		Truncated: truncated,
	})
}

func clearLog(c *gin.Context) {
	if err := op.RelayLogClear(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func getStreamToken(c *gin.Context) {
	token, err := op.RelayLogStreamTokenCreate()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"token": token})
}

func getLogDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid log id")
		return
	}

	log, err := op.RelayLogGet(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "log not found")
		return
	}

	resp.Success(c, log)
}

func listActiveRequests(c *gin.Context) {
	activeRequests := op.ActiveRequestList()
	resp.Success(c, activeRequests)
}

func streamLog(c *gin.Context) {
	token := c.Query("token")
	if token == "" || !op.RelayLogStreamTokenVerify(token) {
		resp.Error(c, http.StatusUnauthorized, "invalid stream token")
		return
	}

	op.RelayLogStreamTokenRevoke(token)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	if _, err := c.Writer.Write([]byte(":connected\n\n")); err != nil {
		return
	}
	c.Writer.Flush()

	logChan := op.RelayLogSubscribe()
	defer op.RelayLogUnsubscribe(logChan)

	activeChan := op.ActiveRequestSubscribe()
	defer op.ActiveRequestUnsubscribe(activeChan)

	// 先推送当前活跃请求快照
	for _, req := range op.ActiveRequestList() {
		event := op.ActiveRequestEvent{Type: "active_register", Request: req}
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		if _, err := c.Writer.Write([]byte(fmt.Sprintf("event: active\ndata: %s\n\n", data))); err != nil {
			return
		}
	}
	c.Writer.Flush()

	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case log, ok := <-logChan:
			if !ok {
				return
			}
			data, err := json.Marshal(log)
			if err != nil {
				continue
			}
			if _, err := c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", data))); err != nil {
				return
			}
			c.Writer.Flush()
		case event, ok := <-activeChan:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := c.Writer.Write([]byte(fmt.Sprintf("event: active\ndata: %s\n\n", data))); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
