# 出站 uTLS 指纹伪装 — 技术设计

> 对应 `prd.md`。本文只写「怎么改」,需求/根因/验收见 PRD。

## 0. 研究依据(已落地)

- 根因已用 smart-search + 权威源确认:`http.Transport.DialTLSContext` 一旦非空,stdlib 关闭自动 HTTP/2 配置;且 `transport.go` 对返回 conn 做 `*tls.Conn` 类型断言读 ALPN,`*utls.UConn` 不匹配 → ALPN 读不到 → **静默退化 HTTP/1.1**(golang/go#78201、refraction-networking/utls#16)。
- 若 uTLS ALPN 仍广告 `h2`、服务端协商 h2,但 client 侧是 HTTP/1.1 → **协议错**。故「裸替 DialTLSContext + 保留 h2 ALPN」不安全。
- 参考实现:`getlantern/dnstt/utls.go` 的 `utlsRoundTripper`(bootstrap 握手 → 按 ALPN 缓存 h2/http1 transport)。
- 约束:`utlsRoundTripper` **仅能在协商出相同 ALPN 的服务端间复用**;而 Octopus 的 `systemDirectClient` 跨所有上游共享 → 裸用冲突。

## 1. 方案选型

| 方案 | 保 h2 | 共享 client 兼容 | 复杂度 | 回归面 | 取舍 |
|---|---|---|---|---|---|
| A. bogdanfinn/tls-client | ✅ | ❌(换 fork 的 fhttp) | 高 | 大 | 过杀(1010 是 JA3 维度,非完整 HTTP 指纹) |
| B. utlsRoundTripper wrapper | ✅ | ❌(需按 host/ALPN 分桶重构缓存) | 中 | 中 | 与共享 client 架构冲突,首版过重 |
| **C-safe. uTLS + `NextProtos=["http/1.1"]`** | ❌(强 1.1) | ✅ | 低 | 仅 opted-in 渠道 | **推荐** |

### 推荐:C-safe + per-channel opt-in

理由:

1. **共享 client 兼容**:强 `http/1.1` 后,所有 uTLS 连接协商一致,无需按 host/ALPN 分桶,沿用现有缓存模型。
2. **KISS / PRD Out-of-Scope**:PRD 明示「首版固定一个主流浏览器 profile 即可,不做 JA3 轮换」。C-safe 是最简可行。
3. **零回归**:per-channel opt-in,默认关。非 Cloudflare 上游完全不动,保留 stdlib h2;只有被 1010 拦的渠道开 uTLS(强 1.1),它们本来就不通,h2→1.1 是非回归。SSE 在 HTTP/1.1 上原生工作。
4. **HTTP/2 取舍**:h2 仅在 opted-in 被拦渠道上降级。若后续发现某 provider 强依赖 h2(未发现),再升级到 Option B——届时需把 `systemDirectClient` 改成按 host 缓存。

> ⚠️ 这是本设计的关键决策点(h2 降级可接受)。若评审认为「h2 必须全保留」,则改走 Option B 并接受客户端缓存架构重构,见 §8 风险。

## 2. 接入边界

- **改动主文件**:`internal/client/http.go`(唯一 transport 构造点,三个函数都在此)。
- **派发器**:`internal/helper/channel.go:ChannelHttpClient` 加一个分支:`channel.UTLS` 为 true 时走 uTLS client。
- **不动**:`internal/relay/`、`internal/transformer/`——它们拿 `*http.Client` 透传,无感。
- **新增 model 字段**:`model.Channel.UTLS`(见 §4)。

## 3. uTLS 拨号实现

参考 `getlantern/dnstt/utls.go`,精简为单文件函数(不走 RoundTripper wrapper,因为 C-safe 直接挂 `DialTLSContext`):

```go
// internal/client/utls.go(新文件)
package client

import (
    "context"
    "crypto/tls"
    "fmt"
    "net"
    utls "github.com/refraction-networking/utls"
)

// utlsDial 在已建立的底层 TCP 连接(直连/socks5/CONNECT 隧道)上做 uTLS 握手。
// 强制 http/1.1,避免 h2 协议错(C-safe)。
func utlsDial(ctx context.Context, rawConn net.Conn, host string) (*utls.UConn, error) {
    cfg := &utls.Config{
        ServerName: host,
        NextProtos: []string{"http/1.1"}, // 强 http/1.1
        MinVersion: tls.VersionTLS12,
    }
    uconn := utls.UClient(rawConn, cfg, utls.HelloChrome_Auto)
    if err := uconn.BuildHandshakeState(); err != nil {
        rawConn.Close()
        return nil, fmt.Errorf("utls build handshake: %w", err)
    }
    // Go 1.25 curveForCurveID 仅支持 X25519/P256/P384/P521/X25519MLKEM768;
    // 部分 Chrome preset 含 FFDHE / draft PQ 曲线需剥除,否则 makeClientHello 失败。
    filterUnsupportedCurves(uconn)
    // 用 uconn.Conn.Handshake() 绕过 UConn.handshakeContext(会重应用 spec 曲线)。
    // 参考 utls#75:必须先 Handshake 才稳定使用选中指纹。
    if err := uconn.Conn.Handshake(); err != nil {
        rawConn.Close()
        return nil, fmt.Errorf("utls handshake: %w", err)
    }
    return uconn, nil
}
```

- **ClientHelloID**:`utls.HelloChrome_Auto`(固定 Chrome;PRD 不做轮换)。
- **NextProtos**:`["http/1.1"]`——强制服务端 http/1.1,杜绝 h2 协议错。
- **曲线过滤**:照搬 dnstt 注释的过滤逻辑(FFDHE 256/257、X25519Kyber768Draft00 等剥除)。
- **Handshake**:用 `uconn.Conn.Handshake()` 而非 `uconn.HandshakeContext()`,原因见 utls#75(Read 先于 Write 会退回 crypto/tls 指纹)。

## 4. 配置面

- 新增 `model.Channel.UTLS bool` `gorm:"default:false"`(per-channel opt-in,默认关)。
- 列由 GORM AutoMigrate 自动添加,**无需手写 SQL 迁移**;如需文档化可加 `internal/db/migrate/008.go`(空 Before/记录性)。
- 前端 `web/` 渠道编辑页加开关(与 `Proxy` / `ChannelProxy` 同区)。
- **不加全局 setting**:避免 default-on 触发非 Cloudflare 上游回归。per-channel 粒度足够。

## 5. 客户端缓存(键升级)

缓存键从 `(proxy-config)` 升级为 `(proxy-config, utls)`。`channel.go` 派发:

| channel.UTLS | channel.Proxy | 分支 |
|---|---|---|
| false | * | **现有三函数,零改动** |
| true | false | uTLS 直连(新缓存 `systemDirectUTLSClient`) |
| true | true(系统代理) | uTLS + 系统代理(新缓存 `systemProxyUTLSClient`) |
| true | 自定义代理 | uTLS + 该代理(每次新建,同现状) |

签名建议:**不改 `GetHTTPClientSystemProxy` 公开签名**(调用面广),改为在 `client` 包内新增 `getHTTPClientUTLS(proxyURL string)` 内部函数,由 `channel.go` 在 `channel.UTLS` 分支调用。uTLS client 的 Transport = 克隆 `http.DefaultTransport` 后:
- `Proxy = nil`(proxy 逻辑下沉到 dial 函数,见 §6)
- `DialTLSContext = func(ctx, network, addr) { ... }`

## 6. 代理交互(统一到 dial 函数)

设 `DialTLSContext` 后,transport 的 `Proxy` 字段对 https 失效(绕过 proxy CONNECT)。故 proxy 逻辑全部下沉到 dial 函数,按类型组合底层 `rawConn`:

| 代理类型 | 底层 rawConn 获取 | uTLS 握手 |
|---|---|---|
| 直连 | `(&net.Dialer{}).DialContext` | utlsDial over rawConn |
| socks5 | `proxy.FromURL(proxyURL, proxy.Direct).Dial` | utlsDial over rawConn(复用现有 socks 逻辑) |
| http/https | 手写 CONNECT:`Dial` 代理 → 写 `CONNECT host:port HTTP/1.1` → 读 `200` → 得隧道 conn | utlsDial over 隧道 |

- socks5 / 直连:**直接复用现有 `proxy.FromURL` 逻辑**,零新增。
- http 代理 + uTLS:新增 ~15 行 CONNECT 隧道代码。首版若评估过重,可先只支持「直连 + socks5」,http 代理 + UTLS 组合在 `channel.go` 层降级为「UTLS 忽略 + 日志 warn + 走非 uTLS」,标注为已知限制。

> 与现状对比:现状 http 代理靠 `cloned.Proxy = http.ProxyURL(...)`(transport 内部 CONNECT);uTLS 路径把 CONNECT 移到 dial 函数里手写。行为等价。

## 7. 兼容性与回归

- **SSE**:HTTP/1.1 chunked/stream。Octopus `internal/relay` SSE 转发不依赖 h2 framing,**✓**。
- **keep-alive**:HTTP/1.1 keep-alive 默认开,连接复用正常,**✓**。
- **自定义 CA**:当前 relay/client 路径**未实现** RootCAs(仅 `internal/usagecard` 有 `tls.Config{MinVersion}`),无既存功能可破。`utls.Config` 预留 `RootCAs` 钩子(未来按需)。
- **非 Cloudflare 上游**:UTLS 默认关,通道完全不动,**✓**。
- **HTTP/2**:opted-in 渠道降级 1.1(见 §1 取舍);非 opted-in 渠道保留 stdlib h2,**✓**。
- **熔断/SSE 断流**:不动 balancer/circuit breaker,无交互。

## 8. 风险与回滚

| 风险 | 概率 | 缓解 |
|---|---|---|
| Chrome profile 仍被某上游拦(非 JA3 维度) | 低 | 1010 本质 JA3;留 `HelloFirefox_Auto` 备选,profile 可配 |
| Go 1.25 + utls 版本不兼容 | 中 | `go get` 选最新 release(v1.6+),`go mod tidy` + 编译验证;曲线过滤兼容 Go 1.25 `curveForCurveID` |
| http 代理 + uTLS 组合未支持(首版) | 中 | §6 降级策略;或实现 CONNECT(~15 行) |
| Option B 升级时缓存重构 | — | 后续,首版不涉及 |
| **h2 降级影响某 provider** | 低 | §1 已评估;若出现改 Option B |

**回滚**:
- 运行时:`channel.UTLS=false` 即逐渠道关闭。
- 代码:单文件 `internal/client/http.go` + `internal/client/utls.go`(新)+ `channel.go` 分支 + `model.Channel.UTLS` 字段 + 前端开关。DB 列留存无害,回滚不丢数据。

## 9. 依赖

- 新增:`github.com/refraction-networking/utls`(最新 release,Go 1.25 兼容)。
- `go.mod` go 1.25.0;`go mod tidy` 后 CI 编译验证。
- 无新 indirect 破坏预期(utls 依赖 `golang.org/x/crypto`、`golang.org/x/net`,Octopus 已有)。

## 10. 验证计划

- **单测**:`utlsDial` 对 `httptest.NewTLSServer` 握手成功、`NegotiatedProtocol == "http/1.1"`;CONNECT 隧道函数对 http 代理拨号成功。
- **集成**:对 PRD 复现渠道(最近 24h 80 条 403/1010)开 `UTLS`,验证 2xx + SSE 流式。
- **回归**:非 Cloudflare 渠道(自建 OpenAI 兼容 / 内网)开/关 UTLS 均正常;SSE 首 token 时间无显著退化。
- **发版**:`/octopus-verify` 全流程通过后再 `/octopus-publish`(v1.0.7)。
