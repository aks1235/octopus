# design: dev-v2 基线与部署资产

## 1. 分支与首个 commit

```
git branch dev-v2 27aa40d && git checkout dev-v2
```

首个 commit 内容 = 本任务全部产物(部署资产 + 仓库指向 + smoke compose + gitignore)。
提交信息建议:`chore: dev-v2 基线——部署资产平移+仓库指向修正`。

## 2. deploy.sh 适配点(dev 版 2402B 为底)

| # | dev 现状 | v0.13.2 适配 |
|---|---|---|
| a | `git pull aks-fork dev` | 改 `git pull aks-fork dev-v2` |
| b | Next 构建:容器挂 `web/`(`-w /w`),env `NEXT_PUBLIC_APP_VERSION` | vite:改挂**仓库根** `-v "$PWD":/src -w /src/web`。原因:vite outDir=`../static/out` 相对解析,挂子目录时会落到容器根 `/static/out` 随容器销毁(feat 线实踩) |
| c | 前端产物拷贝 `web/out/* → static/out/` | 删除该步骤:vite 直接产出到 `static/out` |
| d | env 变量名 | `VITE_APP_VERSION`(Info.tsx 用 `import.meta.env.VITE_APP_VERSION`) |
| e | `golang:1.25` 容器 | 按上游 go.mod 实际版本核对(feat 线经验:上游曾要求 go 1.26.x) |
| f | `-tags=jsoniter` | 实施时核对上游 go.mod/build 脚本是否仍用 jsoniter,不用则去掉 |
| g | `-a`、`CGO_ENABLED=0`、`-buildvcs=false`、ldflags 注入 | 保留(全部是实踩换来的) |
| h | `docker compose down/build/up`(生产) | 保留;冒烟走独立 compose 文件不经过这里 |

ldflags 包路径核对:`github.com/bestruirui/octopus/internal/conf.*`(上游 module 路径未变则不动,实施时确认)。

## 3. CI 让路策略

- `.github/workflows/release.yaml`:用 dev 版覆盖(同名)。适配点:前端构建步骤 env 改 `VITE_GITHUB_REPO=https://github.com/aks1235/octopus`、`VITE_APP_VERSION=${{ 版本 }}`;后端 ldflags 补 `Repo` 注入(dev 版只注入了 Author)
- 删除 `build.yaml`、`template-check.yaml`(上游自用,与我们发版链路无关)
- 冒烟不推远端;CI 实测留到 v2-smoke-switch 发 v2.0.0 时

## 4. 仓库指向 4 处(v0.13.2 实测代码位置)

1. `internal/conf/version.go:7-8`:`Author="bestrui"`、`Repo="https://github.com/bestruirui/octopus"` → `aks1235` / `https://github.com/aks1235/octopus`(默认值兜底,构建时 ldflags 覆盖)
2. `internal/update/update.go:22-23`:`updateUrl`/`updateApiUrl` 硬编码 → 改由 `conf.Repo` 拼接(dev 线 b7a7dd6 同款做法)
3. `web/src/components/modules/setting/Info.tsx:9`:`VITE_GITHUB_REPO` fallback 改 aks1235(即使 CI 忘注入也对)
4. `scripts/build.sh`:LDFLAGS 补 Repo 注入 + Author 改 aks1235(上游 build.sh 与 deploy.sh 双路都要)

## 5. 冒烟环境

`docker-compose.smoke.yml`(feat 线验证过的模式):

- 端口 `8081:8080`,容器名 `octopus-v2-smoke`,数据卷 `./data-v2:/app/data`(空目录,首启自动建库跑上游迁移链 003-012)
- 与生产完全隔离:`octopus-new`(8080, ./data)不动
- `.gitignore` 追加 `data-v2/`

## 6. 风险与回退

- 上游迁移链在全新库上跑 = 用户真实升级路径的纯上游部分,若失败即冒烟失败——正是本任务要暴露的
- 回退 = 删分支,零影响(不碰生产、不推远端)
- go 容器版本/vite 细节与上游有出入时,以「上游 scripts/build.sh 能跑通」为准绳对齐,不发明新构建方式
