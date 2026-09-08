# Design: SSE 日志页转圈修复 + 版本信息仓库地址

## 方案总览

两个独立修复,均小改动,不涉及架构变更。

| # | 问题 | 方案 | 文件 |
|---|------|------|------|
| 1 | SSE 转圈 | `streamOverview` 连接后立即发 `:connected\n\n` + Flush | `internal/server/handlers/log.go` |
| 2 | 仓库地址 | 统一改成 `aks1235/octopus`,build 时 ldflags 注入,默认值兜底 | 5 个文件 |

## 修复1:SSE 转圈

### 改动点

`internal/server/handlers/log.go` 的 `streamOverview` 函数,在 `prepareSSE(c)` 之后、`OpenLogOverview()` 之前(或之后、循环之前),插入立即发注释帧 + Flush:

```go
func streamOverview(c *gin.Context) {
	prepareSSE(c)
	// 连接建立后立即发送注释帧并 flush，确保响应头马上发出，
	// 前端 EventSource 才能及时触发 onopen（空日志时不再等待首次心跳）。
	if _, err := c.Writer.Write([]byte(":connected\n\n")); err != nil {
		return
	}
	c.Writer.Flush()

	snapshot, updates := relay.OpenLogOverview()
	// ... 原循环不变
```

### 为什么有效

- gin 在第一次 `c.Writer.Write` 时隐式写 HTTP 响应头。`:connected\n\n` 是 SSE 注释帧(浏览器不触发事件),但写入会触发响应头发出 + `Flush()` 立即推到客户端。
- 浏览器 `EventSource` 收到响应头 → `onopen` 触发 → `useLogs` 的 `setIsLoading(false)` → 转圈停止,空日志立即显示"暂无日志"。
- 与上游 `9f8ad4d` 修复思路一致(适配重构后的 `streamOverview` 函数名)。

### 对称性

同文件 `streamDetail`(156 行)在 `prepareSSE` 后已有 `c.Writer.Flush()`(173 行),详情页正常。本次给 `streamOverview` 补上等价的前置 flush,消除两个流的不对称。

### 不改的点

- 不改 `OpenLogOverview` 单连接替换模型:`:connected` + Flush 已解决转圈,模型重构超出范围。
- 不改心跳间隔(保持 15s)。
- 不改前端 `useLogs`:前端逻辑正确(`onopen` 置 loading=false),问题在后端响应延迟。

## 修复2:仓库地址统一

### 改动点

**2.1 `scripts/build.sh`(10 行 LDFLAGS)**

补 `Repo` 注入,`Author` 改成 `aks1235/octopus`:

```bash
readonly LDFLAGS="-X 'github.com/bestruirui/${APP_NAME}/internal/conf.Version=${VERSION}' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.BuildTime=$(TZ='Asia/Shanghai' date +'%F %T %z')' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Author=aks1235/octopus' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Repo=https://github.com/aks1235/octopus' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Commit=${COMMIT}' \
                  -s -w"
```

注:`github.com/bestruirui/octopus` 是 Go module path(import 路径),**不是**仓库地址,保持不变。ldflags 的 `-X` 用 module path 定位变量。

**2.2 `internal/conf/version.go`**

默认值兜底(本地 `go build` 无 ldflags 时也正确):

```go
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
	Author    = "aks1235/octopus"
	Repo      = "https://github.com/aks1235/octopus"
)
```

**2.3 `internal/update/update.go`(22-23 行)**

从硬编码改用 `conf.Repo` 拼接,消除与 `conf.Repo` 不一致风险:

```go
var (
	updateUrl    = conf.Repo + "/releases/latest/download"
	updateApiUrl = "https://api.github.com/repos" + strings.TrimPrefix(conf.Repo, "https://github.com") + "/releases/latest"
)
```

注意:`update.go` 当前未 import `strings`,需补 import。`conf.Repo` 形如 `https://github.com/aks1235/octopus`,TrimPrefix 去掉 `https://github.com` 得 `/aks1235/octopus`,拼接成 `https://api.github.com/repos/aks1235/octopus/releases/latest`,正确。

**2.4 `web/src/components/modules/setting/Info.tsx`(9 行)**

```typescript
const GITHUB_REPO = import.meta.env.VITE_GITHUB_REPO || 'https://github.com/aks1235/octopus';
```

**2.5 `.github/workflows/release.yaml`(前端 build 步骤,约 58-64 行)**

注入环境变量(与默认值一致,双保险;CI 产物不依赖本地默认值):

```yaml
      - name: Build frontend
        working-directory: web
        run: |
          pnpm install --frozen-lockfile
          pnpm run build
        env:
          VITE_APP_VERSION: ${{ steps.version.outputs.version }}
          VITE_GITHUB_REPO: https://github.com/aks1235/octopus
```

## 数据流 / 调用链

### SSE(修复1)

```
浏览器 EventSource('/api/v1/log/overview/stream')
  → middleware.Auth() 校验 cookie
  → streamOverview handler
    → prepareSSE (设响应头 map)
    → Write(":connected\n\n")  ← 新增:触发响应头发出
    → Flush()                  ← 新增:立即推送
    → OpenLogOverview() (snapshot + updates chan)
    → 循环 snapshot + heartbeat + updates
浏览器收到响应头 → onopen → setIsLoading(false) → 不转圈
```

### 版本信息(修复2)

```
build.sh ldflags → conf.Repo / conf.Author (运行时)
启动 banner (main.go) 读 conf.Repo/Author → 显示
update.go updateUrl/updateApiUrl 读 conf.Repo → 在线更新指向 aks1235
Info.tsx VITE_GITHUB_REPO (build 时 env 或默认值) → 设置页链接
```

## 兼容性

- 修复1:纯新增注释帧,不改变 SSE 事件协议,前端无需配合改动。已有 onerror/onopen 行为不变。
- 修复2:`conf.Repo` 值变化,在线更新从指向上游改为指向 `aks1235/octopus`(前提:用户在该仓库发 release)。本地 `go build` 无 ldflags 也能用正确默认值。
- module path `github.com/bestruirui/octopus` 保持不变,不影响 import 与 go build。

## 验证

- AC1/AC2:本地起容器,浏览器开日志页,秒开不转圈。
- AC3:`docker logs` 看启动 banner Repo/Author。
- AC4:设置页 Info 链接。
- AC5:octopus-verify 全流程。
- AC6:`go vet ./...`、`gofmt -l`、lint baseline。

## 回滚

- 全部改动在 6 个文件,`git checkout` 即可回滚。
- 镜像回滚:用旧 tag `v1.1.0` 重启容器(docker-compose 改回 v1.1.0)。
