# ADR-0008: 部署资产平移适配,仓库指向一并修正

- 状态: 已接受(2026-09-08)

## 背景

fork 有已跑通的部署/发版链路:deploy.sh(本地构建+卷缓存加速+`-a` 强制重编+版本号去 `-dirty`)、docker-compose(钉 `raynmy/octopus` 镜像)、release.yaml CI(tag push 全自动:changelog、多平台镜像、Release、二进制,3 个 secrets 已配)。上游 v0.13.2 换 vite 前端且自带 CI workflow,两边构建管线冲突。

feat 线踩坑清单(2026-08-21,直接复用):容器 alpine/musl 必须 `CGO_ENABLED=0`;go build 加 `-buildvcs=false`;vite outDir 在 node 容器内解析为 `/static/out` 需显式挂载。

遗留问题③:Info 页 banner 仓库指向 chicring(`conf.Repo` 默认值等 4 处),方案存归档任务 `08-21-fix-sse-spinning-and-repo-info`。

## 决策

- deploy.sh / docker-compose / release.yaml 平移到 dev-v2 并按 vite 构建适配;上游自带 CI workflow 让路(移除或禁用)
- `-static/out` 相关构建仍强制 `-a` 重编译(go:embed 缓存误判)
- 顺手关闭遗留③:仓库指向 4 处(version.go 默认值、build.sh ldflags 注入 Repo、update.go、Info.tsx)统一改 `aks1235/octopus`

## 后果

- 发版链路与现状一致,无新学习成本
- 与上游 CI 文件的冲突在每次跟进上游时需重检(可接受,频率低)
- 「管理面板立即更新」按钮仍旧禁用原则:容器升级只走 `docker compose pull && up -d`
