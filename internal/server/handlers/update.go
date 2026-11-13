package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, *latestInfo)
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

// restartDelay 是刷出响应到重启之间的余量, 留给客户端读完响应体并释放连接。
const restartDelay = 500 * time.Millisecond

func updateFunc(c *gin.Context) {
	if err := update.UpdateCore(); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp.Success(c, "update success")

	// 必须先把响应真正刷给客户端, 再重启进程。
	// 重启会关停 HTTP 服务器, 而这条连接上还有尚未送达的响应体; 早一步重启, 客户端
	// 就只能收到网络错误, 前端据此提示 "升级失败" —— 而二进制其实已经替换完成,
	// 用户刷新页面看到的也是新版本。
	c.Writer.Flush()
	go func() {
		time.Sleep(restartDelay)
		update.Restart()
	}()
}
