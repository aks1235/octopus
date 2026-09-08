# PRD: 修复 SSE 日志页转圈 + 版本信息仓库地址

## 背景

用户反馈两个问题:

1. **日志页一直转圈**:浏览器打开 octopus → 点击"日志"选项卡 → Loader2 转圈,过了很久(约 15 秒)才显示"暂无日志"。
2. **版本信息仓库地址仍指向上游**:容器启动 banner 显示 `Repo: https://github.com/bestruirui/octopus`、`Built By: bestrui`,设置页 Info 的 GitHub 链接也是 bestruirui。

### 根因(已实测定位)

**问题1 — SSE 响应头延迟发出**

`internal/server/handlers/log.go` 的 `streamOverview`(117-153 行):`prepareSSE(c)` 设置响应头后,在 snapshot 为空时,循环跳过 `sse.Encode`,直接进入 15 秒心跳等待。gin 在第一次 `c.Writer.Write` 时才隐式发出 HTTP 响应头,因此响应头要等到第一次心跳(15s)才随 ping 一起发出。

浏览器 `EventSource` 必须收到响应头才触发 `onopen`。前端 `web/src/api/log.ts` 的 `useLogs`:`isLoading` 初始为 `true`,只在 `onopen` 或收到 `log` 事件时置 `false`。空日志时无 `log` 事件 → 至少转圈 15 秒。

放大因素:`relay.OpenLogOverview()`(`internal/relay/log.go:245-254`)是单连接替换模型,每个新连接 close 掉上一个连接的 channel,多个客户端/标签页会互相踢,重连活不过 15 秒心跳期 → 可能无限转圈。

Node 模拟浏览器 EventSource 实测:
```
[15014ms] headers received: 200 text/event-stream
[15015ms] data: ": ping"
```

**这是迁移/rebase 引入的回归,不是上游原生问题。** 上游 dev 分支 commit `9f8ad4d`(2026-04-13,作者 chenjh)早就修过:`streamLog` 连接后立即发 `:connected\n\n` + Flush。但 `9f8ad4d` 只在 dev 线 / aks-fork 线,不在 `upstream/master`。feat/my-features rebase 到上游基点 `4928a04`(upstream/master 线)时丢了该修复;后续 dev 线的重构(`1cb93e8`/`ae295af`)也未保留。当前 `feat/my-features` 的 `log.go` 不含 `connected` 注释帧。

**问题2 — 多处硬编码上游仓库地址**

- `internal/conf/version.go:7-8`:`Author="bestrui"`、`Repo="https://github.com/bestruirui/octopus"` 为默认值。
- `scripts/build.sh:10` LDFLAGS 只注入 `Author=bestrui`,**未注入 Repo**,Author 也是上游值。
- `internal/update/update.go:22-23`:`updateUrl`/`updateApiUrl` 硬编码 `bestruirui/octopus`,在线更新会误导向上游 release。
- `web/src/components/modules/setting/Info.tsx:9`:`VITE_GITHUB_REPO` 默认值 `bestruirui/octopus`,设置页显示上游地址。
- `.github/workflows/release.yaml` build 步骤未注入 `VITE_GITHUB_REPO`。

### 仓库归属(用户确认)

- `bestruirui/octopus` — 用户最初 fork 的上游
- `chicring/octopus` — 用户 clone 下来的(origin remote)
- `aks1235/octopus` — **用户自己的仓库**(aks-fork remote, SSH 可写),改动最终推送目标

## 目标

- 日志页打开后立即响应(无 15s 延迟),空日志时不再长时间转圈。
- 版本信息(启动 banner、设置页 Info、cmd version 输出)统一显示 `aks1235/octopus`,在线更新指向用户自己的仓库。

## 范围

### In scope

1. `internal/server/handlers/log.go`:`streamOverview` 连接建立后立即发 `:connected\n\n` + Flush。
2. `internal/update/update.go`:`updateUrl`/`updateApiUrl` 改用 `conf.Repo` 拼接。
3. `internal/conf/version.go`:默认值 `Repo`/`Author` 改成 `aks1235/octopus` 相关值(build.sh 会覆盖,但默认值兜底)。
4. `scripts/build.sh`:LDFLAGS 补 `-X ...Repo=...` 注入,Author 改 `aks1235/octopus`。
5. `web/src/components/modules/setting/Info.tsx`:`VITE_GITHUB_REPO` 默认值改 `aks1235/octopus`。
6. `.github/workflows/release.yaml`:前端 build 步骤注入 `VITE_GITHUB_REPO`(对齐默认值,双保险)。
7. 本地重新 build 镜像,走 octopus-verify 全流程验证。

### Out of scope

- 不动 feat/my-features 分支基点,不整理 git 历史。
- 不碰 `OpenLogOverview` 单连接替换模型(修复 1 已足够解决转圈,模型重构另开任务)。
- 不改 SSE 心跳间隔。
- 不改其他业务功能。

## 验收标准

- [ ] AC1:浏览器打开日志页,空日志时 1 秒内显示"暂无日志",不转圈(>2s)。
- [ ] AC2:浏览器打开日志页,有日志时 1 秒内显示日志卡片。
- [ ] AC3:容器启动 banner 显示 `Repo: https://github.com/aks1235/octopus`、`Built By: aks1235/octopus`。
- [ ] AC4:设置页 Info 的 GitHub 链接显示 `aks1235/octopus` 且点击跳转正确。
- [ ] AC5:octopus-verify 全流程通过(后端 build/test、前端 lint+build、容器健康检查),写验证 marker。
- [ ] AC6:go vet / gofmt 干净,lint 不新增 baseline 问题。

## 风险

- **R1**:`update.go` 改用 `conf.Repo` 拼接后,`conf.Repo` 在 `go build`(非 build.sh,无 ldflags)时为 `version.go` 默认值。需确保默认值正确,否则本地 `go build` 出的二进制更新地址错。
- **R2**:前端 `Info.tsx` 改默认值后,需重新 build 前端并 embed 进二进制才生效。
- **R3**:镜像重新 build 需走 octopus-publish 流程才能推到远端,本次只做本地验证,发布另议。
