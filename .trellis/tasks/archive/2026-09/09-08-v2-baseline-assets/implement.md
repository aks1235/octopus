# implement: dev-v2 基线与部署资产

> 前置:无(首个子任务)。完成后 `.trellis/tasks/09-08-v2-db-migration` 与功能包子任务才可开工。

## 执行清单

### Step 1 建分支
- [ ] `git fetch upstream && git branch dev-v2 27aa40d && git checkout dev-v2`
- [ ] 验证:`git merge-base dev-v2 upstream/master` 指向 27aa40d

### Step 2 仓库指向 4 处
- [ ] `internal/conf/version.go`:Author/Repo 默认值
- [ ] `internal/update/update.go`:updateUrl/updateApiUrl 由 conf.Repo 拼接
- [ ] `web/src/components/modules/setting/Info.tsx`:VITE_GITHUB_REPO fallback
- [ ] `scripts/build.sh`:ldflags 补 Repo/Author(先读上游 build.sh 现状再改)

### Step 3 部署资产平移
- [ ] 从 dev 分支拷 `deploy.sh`,按 design.md §2 表逐项适配(a-g)
- [ ] 核对并更新 `docker-compose.yml`(镜像 raynmy/octopus:v2.0.0 占位,dockerfile 路径按上游实际核对)
- [ ] 拷 `.github/workflows/release.yaml` 并适配 vite env 注入;删除上游 `build.yaml`/`template-check.yaml`
- [ ] `.gitignore` 追加 `data-v2/`(确认 `.octopus-verified`/`.pnpm-store/` 条目在)

### Step 4 冒烟
- [ ] 写 `docker-compose.smoke.yml`(8081/独立容器名/data-v2)
- [ ] 本地跑 deploy.sh 的构建路径(或直接 smoke compose build)产出镜像
- [ ] 起 smoke 容器:健康检查 `curl 127.0.0.1:8081` 200;观察首启迁移日志无报错
- [ ] 浏览器过 UI:首页/渠道(多KEY编辑)/日志(SSE 实时)/设置-Info(仓库指向 aks1235)
- [ ] 确认生产容器 octopus-new 未受影响(`docker ps` 两侧并存)

### Step 5 验证与收尾
- [ ] 走 octopus-verify 全流程(smoke 实例),人看 UI 后写 `.octopus-verified` marker
- [ ] 提交首个 commit;更新父任务 prd 任务地图勾选
- [ ] **不推远端、不打 tag**(推送属 v2-smoke-switch)

## 验证命令

```bash
git merge-base dev-v2 upstream/master        # 期望 27aa40d…
docker compose -f docker-compose.smoke.yml up -d --build
curl -sf http://127.0.0.1:8081/ >/dev/null && echo OK
docker logs octopus-v2-smoke 2>&1 | grep -iE 'error|fail' || echo clean
docker ps --format '{{.Names}} {{.Status}}'   # octopus-new 与 smoke 并存
```

## 回滚点

- 任一步失败:`git checkout dev && git branch -D dev-v2`,smoke 容器 `down -v` + 删 `data-v2/`
- 生产零接触,无需生产回滚
