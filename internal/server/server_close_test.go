package server

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestCloseWaitsForInflightResponse 验证关闭服务器时不会掐断正在写回的响应。
//
// 升级接口正是靠这一点把 "update success" 送达客户端: 二进制替换完成后立刻重启的话,
// 客户端只会收到网络错误, 前端据此误报 "升级失败", 而实际版本已经是最新的。
func TestCloseWaitsForInflightResponse(t *testing.T) {
	mux := http.NewServeMux()
	release := make(chan struct{})
	mux.HandleFunc("/update", func(w http.ResponseWriter, _ *http.Request) {
		<-release // 等测试确认关闭已经开始, 再写出响应
		_, _ = io.WriteString(w, "update success")
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv = http.Server{Handler: mux}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/update")
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		done <- result{body: string(body), err: err}
	}()

	// 等请求进入处理中(连接已建立), 再触发关闭: 此时响应还没写出去。
	time.Sleep(100 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- Close() }()

	// 关闭已经开始, 现在才写出响应, 模拟真实升级接口"响应先写、进程后重启"的顺序。
	time.Sleep(100 * time.Millisecond)
	close(release)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("在途响应被掐断: %v", r.err)
		}
		if r.body != "update success" {
			t.Fatalf("响应体不完整: %q", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("请求未完成")
	}

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close 未返回")
	}
}
