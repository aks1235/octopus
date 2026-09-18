package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

var activityMaxRequestCount int64     // 最近 54 周每日请求量的最大值。
var activityMaxCalculatedAt time.Time // 最大值上次计算时间。
var activityMaxMu sync.Mutex          // 保护最大值及计算时间的并发更新。

type statsDailyResponse struct {
	MaxRequestCount int64              `json:"max_request_count"` // 最近 54 周每日请求量的最大值。
	Items           []model.StatsDaily `json:"items"`             // 每日原始统计数据。
}

func init() {
	router.NewGroupRouter("/api/v1/stats").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/daily", http.MethodGet).
				Handle(getStatsDaily),
		).
		AddRoute(
			router.NewRoute("/hourly", http.MethodGet).
				Handle(getStatsHourly),
		).
		AddRoute(
			router.NewRoute("/rank", http.MethodGet).
				Handle(getStatsRank),
		).
		AddRoute(
			router.NewRoute("/total", http.MethodGet).
				Handle(getStatsTotal),
		).
		AddRoute(
			router.NewRoute("/apikey", http.MethodGet).
				Handle(getStatsAPIKey),
		)
}

func getStatsDaily(c *gin.Context) {
	now := time.Now()
	since := now.AddDate(0, 0, -(int(now.Weekday()) + 53*7)).Format("20060102")
	statsDaily, err := op.StatsGetDaily(c.Request.Context(), since)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	activityMaxMu.Lock()
	if activityMaxCalculatedAt.IsZero() || now.Sub(activityMaxCalculatedAt) >= 24*time.Hour {
		maxRequestCount := int64(0)
		for _, daily := range statsDaily {
			requestCount := daily.RequestSuccess + daily.RequestFailed
			if requestCount > maxRequestCount {
				maxRequestCount = requestCount
			}
		}
		activityMaxRequestCount = maxRequestCount
		activityMaxCalculatedAt = now
	}
	maxRequestCount := activityMaxRequestCount
	activityMaxMu.Unlock()

	resp.Success(c, statsDailyResponse{
		MaxRequestCount: maxRequestCount,
		Items:           statsDaily,
	})
}

// statsDateQuery 读取并校验 date 查询参数(YYYYMMDD); 缺省为今天。
// 校验失败时已写出 400 响应, 返回 false。
func statsDateQuery(c *gin.Context) (string, bool) {
	date := c.Query("date")
	if date == "" {
		return time.Now().Format("20060102"), true
	}
	if _, err := time.ParseInLocation("20060102", date, time.Local); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid date, expect YYYYMMDD")
		return "", false
	}
	return date, true
}

func getStatsHourly(c *gin.Context) {
	date, ok := statsDateQuery(c)
	if !ok {
		return
	}
	hourly, err := op.StatsHourlyGet(date)
	if err != nil {
		// date 已在 statsDateQuery 校验过, 走到这里只可能是内部错误。
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, hourly)
}

// getStatsRank 按选中日从 relay_logs 聚合渠道与模型排名; date 缺省为今天。
func getStatsRank(c *gin.Context) {
	date, ok := statsDateQuery(c)
	if !ok {
		return
	}
	rank, err := op.StatsRankDaily(c.Request.Context(), date)
	if err != nil {
		// date 已在 statsDateQuery 校验过, 查询/聚合失败属内部错误。
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, rank)
}

func getStatsTotal(c *gin.Context) {
	resp.Success(c, op.StatsTotalGet())
}

func getStatsAPIKey(c *gin.Context) {
	resp.Success(c, op.StatsAPIKeyList())
}
