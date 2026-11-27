package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	_ "github.com/bestruirui/octopus/internal/server/handlers"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/static"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

var httpSrv http.Server

func Start() error {
	if conf.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		c.Abort()
	}))

	if conf.IsDebug() {
		r.Use(middleware.Logger())
	}
	r.Use(middleware.Cors())
	r.Use(middleware.StaticEmbed("/", static.StaticFS))
	// API 的 JSON 响应统一压缩: 渠道统计这类大响应在慢速链路上是首屏的主要耗时来源。
	// 静态文件由 StaticEmbed 用预压缩的 .gz 处理, 不经过这里; 流式接口不是 JSON, 同样不受影响。
	r.Use(middleware.GzipJSON())

	if err := router.RegisterAll(r); err != nil {
		return err
	}

	httpSrv.Addr = fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.Port)
	httpSrv.Handler = r
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("http server listen and serve error: %v", err)
		}
	}()
	return nil
}

// Close 优雅关闭 HTTP 服务器: 先停止接受新连接, 等正在处理的请求把响应写完再退出。
//
// 直接 Close 会立刻掐断在途连接, 升级接口的成功响应正是这样丢掉的。
// 长连接(如流式转发)不会无限等待, 超时后退回强制关闭。
func Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		return httpSrv.Close()
	}
	return nil
}
