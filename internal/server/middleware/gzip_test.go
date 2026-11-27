package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newGzipTestEngine(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GzipJSON())
	r.GET("/json", handler)
	return r
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// TestGzipJSON_compressesJSON 锁定主路径: JSON 响应压缩, 且解压后与原文一致。
func TestGzipJSON_compressesJSON(t *testing.T) {
	payload := `{"models":["` + strings.Repeat("deepseek-v4.1-flash,", 200) + `"]}`
	engine := newGzipTestEngine(func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": payload})
	})

	request := httptest.NewRequest(http.MethodGet, "/json", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := response.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length 应被清掉, 实际 %q", got)
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !strings.Contains(string(decompressed), "deepseek-v4.1-flash") {
		t.Fatalf("解压内容不完整: %s", string(decompressed)[:80])
	}
}

// TestGzipJSON_skipsStreaming 锁定流式响应不被压缩: 事件流靠逐块 Flush 推进, 压缩会引入缓冲。
func TestGzipJSON_skipsStreaming(t *testing.T) {
	engine := newGzipTestEngine(func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.String(http.StatusOK, "data: one\n\n")
	})

	request := httptest.NewRequest(http.MethodGet, "/json", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("流式响应不应被压缩, Content-Encoding = %q", got)
	}
	if body := readBody(t, response.Result()); body != "data: one\n\n" {
		t.Fatalf("body = %q", body)
	}
}

// TestGzipJSON_skipsRelayPath 锁定对外接口不压缩: /v1 的响应由第三方客户端消费, 少一层变换少一类兼容问题。
func TestGzipJSON_skipsRelayPath(t *testing.T) {
	engine := gin.New()
	engine.Use(GzipJSON())
	engine.GET("/v1/models", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": strings.Repeat("model,", 200)})
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("对外接口不应被压缩, Content-Encoding = %q", got)
	}
}

// TestGzipJSON_respectsNoAcceptEncoding 客户端没声明 gzip 时保持明文, 否则浏览器收到无法解析的字节。
func TestGzipJSON_respectsNoAcceptEncoding(t *testing.T) {
	engine := newGzipTestEngine(func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/json", nil))

	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("未声明 gzip 时不应压缩, Content-Encoding = %q", got)
	}
	if body := readBody(t, response.Result()); body != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
}
