package relay

import (
	"context"
	"testing"
)

// 日志来源标识决定这条测试请求在转发日志里长什么样:
// 人工点"测试全部成员"记"分组测试", 定时探测记"定时探测"。两者混淆会让"哪些请求是探测产生的"无法区分,
// 统计口径(排除测试流量)也会漏掉一类。
func TestTestClientNameOf_defaultAndProbe(t *testing.T) {
	clientName, userAgent := testClientNameOf(context.Background())
	if clientName != groupTestClientName || userAgent != groupTestUserAgent {
		t.Fatalf("default = %q/%q, want %q/%q", clientName, userAgent, groupTestClientName, groupTestUserAgent)
	}

	clientName, userAgent = testClientNameOf(WithProbeClientName(context.Background()))
	if clientName != ProbeClientName || userAgent != ProbeUserAgent {
		t.Fatalf("probe = %q/%q, want %q/%q", clientName, userAgent, ProbeClientName, ProbeUserAgent)
	}
}
