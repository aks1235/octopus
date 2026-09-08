# 出站 TLS 指纹伪装(uTLS)修复 Cloudflare 1010

## Goal

修复部分 Cloudflare 防护的上游渠道对 Octopus 出站请求返回 `403 Error 1010: Access denied, blocked based on your browser's signature` 的问题。同样上游直连 Claude Code(Node.js TLS)不触发该错误。

## Background / Root Cause(2026-09-04 调查结论)

- 错误体:`Error 1010: Access denied... blocked based on your browser's signature`。
- 代码层:只有 `internal/transformer/outbound/codex/codex.go` 主动设了 `User-Agent: codex-tui/0.118.0`;其它渠道靠 `relay.go copyHeaders` 透传客户端 UA。UA 不是差异点(直连 Claude Code 用同 UA 能过)。
- 差异在 **TLS 指纹(JA3)**:Octopus 用 Go `net/http` 的 ClientHello,被 Cloudflare 判为非浏览器签名;Claude Code 直连用 Node 的 TLS,JA3 被放行。
- 「用一段时间后才出现」:Cloudflare 对 Go-JA3 的速率/信誉累积触发(纯 JA3 会一开始就拦)。
- 复现渠道:最近 24h 失败 attempt 中 403 共 80 条,样本均为 Cloudflare 1010。

## Requirements

- 引入 `github.com/refraction-networking/utls`(或等价方案),为 Octopus 出站 HTTPS 连接伪装浏览器 ClientHello(Chrome / Firefox profile),绕过 Cloudflare 1010 的浏览器签名检测。
- 改动点:`internal/client/` 的 HTTP client 构造(`ChannelHttpClient` / `GetHTTPClientSystemProxy` / `GetHTTPClientCustomProxy`),把底层 `http.Transport` 的 `DialTLSContext` 替换为 uTLS 拨号;保留现有代理、自定义 CA、超时等配置。
- 可配置(建议):允许按渠道/全局开关 uTLS(默认开),避免对自建/内网上游的兼容性回归。
- 不破坏现有能力:SSE 流式、HTTP/2、channel proxy、自定义 Header、连接复用(keep-alive)。

## Acceptance Criteria

- [ ] 之前返回 1010 的渠道,改动后能正常转发(返回 2xx + 正常 SSE 流式)。
- [ ] 非 Cloudflare 上游(自建 OpenAI 兼容、内网服务)不受影响,回归通过。
- [ ] 代理(`channel.proxy` / `channel_proxy`)与自定义 CA 仍生效。
- [ ] SSE 流式转发正常,首 token 时间无显著退化。
- [ ] 单测/回归:至少补一条 uTLS 拨号路径的测试或集成验证。
- [ ] 经 `/octopus-verify` 全流程通过后再发布。

## Out of Scope

- 不改入站(客户端→Octopus)TLS。
- 不做 JA3 轮换/随机化等高级规避(首版固定一个主流浏览器 profile 即可)。

## Notes

- 这是「client_disconnect 熔断修复」之后单独开出的任务;1+2 已在 v1.0.6 修复,本任务独立发布(预计 v1.0.7)。
- 备选方案:若 uTLS 接入成本过高,退而求其次——给被拦渠道单独配代理走浏览器指纹出口(非代码改动,运维手段)。
