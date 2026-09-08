# dev-v2 基线与部署资产

> 父任务:`.trellis/tasks/09-08-dev-v2-redo`(决策索引见其 prd)。本任务是全部子任务的前置。

## Goal

建立 `dev-v2` 分支(基线 `upstream/master@27aa40d`,v0.13.2),平移并适配自有部署资产,修正仓库指向,空库起容器冒烟通过——产出一条「可构建、可部署、可发版」的干净基线,供后续功能包在其上叠加。

## Requirements

1. **分支**:新建 `dev-v2`,基点 27aa40d,首个 commit 即本任务产物;不携带 dev 任何代码
2. **部署资产平移**(出处 dev,适配见 design.md):
   - `deploy.sh`(改拉取分支、适配 vite 构建)
   - `docker-compose.yml`(镜像名 raynmy/octopus 保持,版本占位 v2.0.0)
   - `.github/workflows/release.yaml`(覆盖上游同名文件;注入 `VITE_GITHUB_REPO`/`VITE_APP_VERSION`)
   - 上游自带 `build.yaml`、`template-check.yaml` 让路(删除)
3. **冒烟环境**:`docker-compose.smoke.yml`(独立端口 8081、独立数据目录、独立容器名),不触碰生产容器 `octopus-new` 与 `./data`
4. **仓库指向修正**(遗留③,4 处):`internal/conf/version.go`、`internal/update/update.go`、`web/src/components/modules/setting/Info.tsx`、构建链 ldflags/env 注入——统一 `aks1235/octopus`
5. **gitignore**:带上 dev 侧经验条目(`.octopus-verified`、`.pnpm-store/`、smoke 数据目录)

## Acceptance Criteria

- [ ] `git merge-base dev-v2 upstream/master` = `27aa40d…`
- [ ] 空库(全新数据目录)起容器:健康检查 HTTP 200,UI 可打开
- [ ] 上游白拿项肉眼可见可用:渠道多 KEY 编辑、日志页 SSE 实时流、渠道统计页
- [ ] 启动 banner 与 Info 页显示 `aks1235/octopus`,版本号为注入值
- [ ] `deploy.sh` 在 dev-v2 分支上可跑通(拉取→前端→后端→容器)不报错
- [ ] CI release.yaml 语法有效(actionslint 或人工核对),推送 tag 的路径不被上游 workflow 干扰
- [ ] 生产容器 `octopus-new`(v1.0.6)全程未被触碰
- [ ] 通过 octopus-verify 闭环并写 marker

## Out of scope

- 任何功能包移植(后续子任务)
- 数据迁移(v2-db-migration)
- 生产切换(v2-smoke-switch)
