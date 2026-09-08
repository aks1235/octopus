# 出站 uTLS 指纹伪装 — 执行清单

> 技术设计见 `design.md`。本文件按顺序执行,每步带验证命令与回滚点。
> 策略:C-safe(uTLS + `NextProtos=["http/1.1"]`)+ per-channel opt-in(`Channel.UTLS`)。

## 步骤

### S1. 引入 utls 依赖
- [ ] `go get github.com/refraction-networking/utls@latest`,选 Go 1.25 兼容版本。
- [ ] `go mod tidy`。
- 验证:`go build ./internal/client/...` 通过。
- 回滚:`git checkout go.mod go.sum`。

### S2. 新增 `internal/client/utls.go`
- [ ] 实现 `utlsDial(ctx, rawConn, host)`:UClient + `HelloChrome_Auto` + `NextProtos=["http/1.1"]` + `BuildHandshakeState` + 曲线过滤 + `uconn.Conn.Handshake()`。
- [ ] 实现 `httpProxyConnect(ctx, proxyAddr, targetAddr) (net.Conn, error)`:写 `CONNECT`、读 `200 Connection established`。
- [ ] 实现统一拨号 `utlsDialContext(ctx, network, addr, proxyURL)`:按 proxyURL 选底层 rawConn(直连/socks5/CONNECT),再调 `utlsDial`。
- 验证:`go vet ./internal/client/`;`go build ./...`。

### S3. 单测 `internal/client/utls_test.go`
- [ ] `TestUTLSDial_HTTP11ALPN`:对 `httptest.NewTLSServer` 拨号,断言 `NegotiatedProtocol == "http/1.1"`。
- [ ] `TestHttpProxyConnect`:对本地 http 代理 + TLS 后端,断言隧道建立 + 握手成功。
- 验证:`go test ./internal/client/...`。

### S4. model 字段 + 迁移
- [ ] `internal/model/channel.go`:`Channel` 加 `UTLS bool \`json:"utls" gorm:"default:false"\``。
- [ ] (可选)`internal/db/migrate/008.go`:空 Before,仅记录性(列由 AutoMigrate 加)。
- [ ] 启动一次 `go run main.go start`,确认 AutoMigrate 加列成功(查 `channels` 表有 `utls` 列)。
- 回滚:`model.Channel.UTLS` 字段移除;DB 列留存无害。

### S5. client 缓存 + channel.go 派发
- [ ] `internal/client/http.go`:新增内部 `getHTTPClientUTLS(proxyURL string)`,克隆 DefaultTransport、`Proxy=nil`、`DialTLSContext=utlsDialContext`,按现状缓存(direct/系统代理 各一;自定义代理 每次新建)。
- [ ] `internal/helper/channel.go:ChannelHttpClient`:`channel.UTLS` 分支调用 `client.getHTTPClientUTLS(...)`;http 代理 + uTLS 若未实现则降级(warn + 走非 uTLS)。
- 验证:`go build ./...`;`go test ./internal/helper/...`(若有)。

### S6. 前端开关
- [ ] `web/src/` 渠道编辑表单加 `utls` 开关(与 Proxy 同区,复用 Switch 组件)。
- [ ] 渠道详情/列表展示该字段(可选)。
- 验证:`cd web && pnpm run lint && pnpm run build`。

### S7. 集成验证
- [ ] 对 PRD 复现渠道(24h 80 条 1010)开 `UTLS`,发请求验证 2xx + SSE。
- [ ] 非 Cloudflare 渠道(自建 OpenAI 兼容)开/关 UTLS 均正常。
- [ ] SSE 首 token 时间对比(无显著退化)。
- [ ] 直连 / socks5 代理 / http 代理 三种渠道各验一遍(后者走降级或 CONNECT)。

### S8. 全流程验证
- [ ] `/octopus-verify` 全流程通过(容器内编译 + 后端测试 + 前端 lint/build + 起容器 + 健康检查 + 写 marker)。
- [ ] (发版)`/octopus-publish` 打 v1.0.7(需用户明确同意推送)。

## Review Gates
- S3 后:uTLS 拨号路径单测绿,再继续 S5。
- S5 后:`go build ./...` + 现有 `go test ./...` 不退化。
- S7 后:集成复现渠道确实从 1010 转为 2xx。
- S8:`/octopus-verify` marker 写出,方可发版。

## 回滚点
- 任意步失败:`git stash` / 对应文件 `git checkout`;运行时 `channel.UTLS=false` 逐渠道关。
