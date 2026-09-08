# Implement: SSE 日志页转圈修复 + 版本信息仓库地址

## 执行清单(按顺序)

### Step 1:修复 SSE 转圈

- [ ] 1.1 编辑 `internal/server/handlers/log.go` 的 `streamOverview`:在 `prepareSSE(c)` 之后、`relay.OpenLogOverview()` 之前,插入:
  ```go
  // 连接建立后立即发送注释帧并 flush，确保响应头马上发出，
  // 前端 EventSource 才能及时触发 onopen（空日志时不再等待首次心跳）。
  if _, err := c.Writer.Write([]byte(":connected\n\n")); err != nil {
      return
  }
  c.Writer.Flush()
  ```
- [ ] 1.2 确认 `streamDetail` 已有 Flush(173 行),不动。
- **验证点**:`go build -tags=jsoniter .` 通过。

### Step 2:修复仓库地址 — 后端

- [ ] 2.1 编辑 `internal/conf/version.go`:`Author` 改 `aks1235/octopus`,`Repo` 改 `https://github.com/aks1235/octopus`。
- [ ] 2.2 编辑 `internal/update/update.go`:`updateUrl`/`updateApiUrl` 改用 `conf.Repo` 拼接;补 `strings` import。
  ```go
  var (
      updateUrl    = conf.Repo + "/releases/latest/download"
      updateApiUrl = "https://api.github.com/repos" + strings.TrimPrefix(conf.Repo, "https://github.com") + "/releases/latest"
  )
  ```
- [ ] 2.3 编辑 `scripts/build.sh` LDFLAGS:`Author` 改 `aks1235/octopus`,新增 `Repo=https://github.com/aks1235/octopus` 行。
- **验证点**:`go build -tags=jsoniter .`、`go vet ./...` 通过。

### Step 3:修复仓库地址 — 前端 + CI

- [ ] 3.1 编辑 `web/src/components/modules/setting/Info.tsx`:`VITE_GITHUB_REPO` 默认值改 `https://github.com/aks1235/octopus`。
- [ ] 3.2 编辑 `.github/workflows/release.yaml` 前端 build 步骤,env 加 `VITE_GITHUB_REPO: https://github.com/aks1235/octopus`。
- **验证点**:`pnpm build` 通过。

### Step 4:质量检查

- [ ] 4.1 `gofmt -l internal/`(无输出或只列未改文件)。
- [ ] 4.2 `go vet ./...` 无新增问题。
- [ ] 4.3 后端测试:`go test -tags=jsoniter ./...`。
- [ ] 4.4 前端 lint:`pnpm lint`(不超 baseline)。

### Step 5:octopus-verify 全流程

走 `/octopus-verify` skill:
- [ ] 5.1 容器内编译。
- [ ] 5.2 后端自动测试。
- [ ] 5.3 前端 lint + build。
- [ ] 5.4 本地起容器。
- [ ] 5.5 健康检查。
- [ ] 5.6 写验证 marker。

### Step 6:功能验收(浏览器)

- [ ] 6.1 打开日志页,空日志 1 秒内显示"暂无日志",不转圈。
- [ ] 6.2 触发一次请求后打开日志页,1 秒内显示日志卡片。
- [ ] 6.3 `docker logs octopus-new` 看 banner:`Repo: https://github.com/aks1235/octopus`、`Built By: aks1235/octopus`。
- [ ] 6.4 设置页 Info 的 GitHub 链接显示 `aks1235/octopus`。

## Review Gates

- Gate A(Step 1-3 后):代码改动完成,go build + pnpm build 绿,暂停让你 review diff。
- Gate B(Step 5 后):octopus-verify marker 写出,再进行浏览器验收。
- Gate C(Step 6 后):全部 AC 达标,准备 commit(发布走 octopus-publish,另议)。

## 回滚点

- 任何 Step 失败:`git checkout -- <file>` 回滚该文件。
- 镜像起不来:docker-compose 改回 `raynmy/octopus:v1.1.0` 重启。
