package relay

import (
	"net/http"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

// customHeaderChannel 构一个仅带自定义 Header 的渠道, 其余配置留空(本测试只覆盖 Header 追加逻辑)。
func customHeaderChannel(headers ...model.CustomHeader) model.Channel {
	return model.Channel{ChannelConfig: model.ChannelConfig{CustomHeader: headers}}
}

// TestApplyChannelConfig_clientHeaderPlaceholder 验证自定义 Header 值中的 {client_header:NAME}
// 占位符被替换为客户端同名请求头的实际值: 取不到替换为空串, 无占位符的值原样写入。
func TestApplyChannelConfig_clientHeaderPlaceholder(t *testing.T) {
	channel := customHeaderChannel(
		model.CustomHeader{HeaderKey: "X-Vendor", HeaderValue: "agent={client_header:User-Agent}"},
		model.CustomHeader{HeaderKey: "X-Missing", HeaderValue: "v={client_header:X-Not-Sent}"},
		model.CustomHeader{HeaderKey: "X-Plain", HeaderValue: "static-value"},
		model.CustomHeader{HeaderKey: "X-Multi", HeaderValue: "{client_header:X-A}/{client_header:X-B}"},
	)
	request := &httpclient.Request{Headers: http.Header{
		"User-Agent": []string{"my-client/1.0"},
		"X-A":        []string{"a"},
	}}

	if err := applyChannelConfig(channel, request); err != nil {
		t.Fatalf("applyChannelConfig() error = %v", err)
	}

	// 客户端真实请求头替换进值里, 与字面量前缀共存。
	if got := request.Headers.Get("X-Vendor"); got != "agent=my-client/1.0" {
		t.Errorf("X-Vendor = %q, want agent=my-client/1.0", got)
	}
	// 客户端未发送的请求头替换为空串。
	if got := request.Headers.Get("X-Missing"); got != "v=" {
		t.Errorf("X-Missing = %q, want v=", got)
	}
	// 无占位符的值零变化。
	if got := request.Headers.Get("X-Plain"); got != "static-value" {
		t.Errorf("X-Plain = %q, want static-value", got)
	}
	// 同一值内的多个占位符逐个替换。
	if got := request.Headers.Get("X-Multi"); got != "a/" {
		t.Errorf("X-Multi = %q, want a/", got)
	}
}

// TestApplyChannelConfig_sensitiveHeaderStillSkipped 验证敏感 Header 跳过逻辑不被占位符改动:
// 出站请求已有值的敏感 Header 不被自定义配置覆盖, 占位符也不参与替换。
func TestApplyChannelConfig_sensitiveHeaderStillSkipped(t *testing.T) {
	channel := customHeaderChannel(
		model.CustomHeader{HeaderKey: "Authorization", HeaderValue: "{client_header:User-Agent}"},
	)
	request := &httpclient.Request{Headers: http.Header{
		"Authorization": []string{"Bearer channel-key"},
		"User-Agent":    []string{"my-client/1.0"},
	}}

	if err := applyChannelConfig(channel, request); err != nil {
		t.Fatalf("applyChannelConfig() error = %v", err)
	}
	if got := request.Headers.Get("Authorization"); got != "Bearer channel-key" {
		t.Errorf("Authorization = %q, want Bearer channel-key (sensitive header must be skipped)", got)
	}
}
