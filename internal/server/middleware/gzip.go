package middleware

import (
	"compress/gzip"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GzipJSON 压缩 JSON 响应。
//
// 只压 application/json: 转发面板上真正的大响应都是 JSON(渠道统计、分组列表、日志),
// 而流式接口走 text/event-stream 与 application/x-ndjson —— 它们靠逐块 Flush 推进,
// 套上压缩会引入缓冲, 把"边生成边显示"变成"攒够一块才吐"。范围收窄到 JSON 就没有这个风险。
//
// 已经带 Content-Encoding 的响应(静态文件由 static 中间件预压缩)不重复处理。
func GzipJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isRelayPath(c.Request.URL.Path) || !acceptsGzip(c.GetHeader("Accept-Encoding")) {
			c.Next()
			return
		}
		writer := &gzipJSONWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()
		writer.close()
	}
}

// isRelayPath 判断是否为对外转发接口(/v1)。
//
// 这些响应由第三方客户端消费: 压缩虽在 HTTP 规范内, 但网关在响应上多做一层变换就多一类兼容面
// (声明了 Accept-Encoding 却不按规范解压的客户端确实存在)。压缩的收益点在面板自身的 /api,
// 那里两端都由我们控制; 对外接口保持原样, 出问题时不至于先怀疑这一层。
func isRelayPath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

// acceptsGzip 判断客户端是否接受 gzip。
// 只做子串匹配: 面板的目标浏览器与 curl 都会明确声明 gzip, 不必解析 q 值。
func acceptsGzip(header string) bool {
	return strings.Contains(strings.ToLower(header), "gzip")
}

// gzipJSONWriter 延迟到真正写 body 时才决定是否压缩。
//
// 不能在看 WriteHeader 时就定: gin 的 Render 顺序是「先 c.Status(code) 写头, 再设 Content-Type,
// 最后写 body」, 写头那一刻 Content-Type 还是空的, 据此判断永远压不上。所以这里只记下状态码,
// 把「是否压缩 + 把响应头交给底层」一起推后到第一次 Write(或流式 Flush、收尾 close)。
type gzipJSONWriter struct {
	gin.ResponseWriter
	writer      *gzip.Writer
	status      int  // 处理器要求的状态码; 0 表示尚未设置。
	headerReady bool // 响应头是否已经交给底层。
	compress    bool
}

// WriteHeader 只记录状态码, 不写底层。
func (w *gzipJSONWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
}

// WriteHeaderNow 同样只记录: gin 在 c.Status() 与 Render 内部都会调用它,
// 此时 Content-Type 仍未设置, 提前落头就失去了压缩的机会。
func (w *gzipJSONWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = w.ResponseWriter.Status()
	}
}

// writeHeader 把攒下的状态码与压缩决定一起落到响应头, 保证只执行一次。
func (w *gzipJSONWriter) writeHeader() {
	if w.headerReady {
		return
	}
	w.headerReady = true
	code := w.status
	if code == 0 {
		code = http.StatusOK
	}
	if code >= 200 && code < 300 && w.shouldCompress() {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.writer = gzip.NewWriter(w.ResponseWriter)
		w.compress = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipJSONWriter) Write(data []byte) (int, error) {
	w.writeHeader()
	if w.compress {
		return w.writer.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

// Flush 让流式接口在未压缩时保持原有行为; 压缩时先把攒着的块吐出去, 否则流式会卡在压缩缓冲里。
func (w *gzipJSONWriter) Flush() {
	w.writeHeader()
	if w.writer != nil {
		_ = w.writer.Flush()
	}
	w.ResponseWriter.Flush()
}

// close 兜住"只设状态码、没有 body"的响应(204、304 一类), 保证响应头最终仍然发出。
func (w *gzipJSONWriter) close() {
	w.writeHeader()
	if w.writer == nil {
		return
	}
	_ = w.writer.Close()
	w.writer = nil
}

// shouldCompress 只认 JSON, 且跳过流式方言与已压缩响应。
func (w *gzipJSONWriter) shouldCompress() bool {
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	return strings.HasPrefix(contentType, "application/json")
}
