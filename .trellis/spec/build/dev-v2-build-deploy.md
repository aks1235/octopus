# dev-v2 构建与部署契约

### 1. Scope / Trigger

dev-v2 分支上任何 `.go`、`web/`、部署资产(deploy.sh / compose / CI)改动的构建、验证与容器编排。基线 commit `e86d8d9`(upstream v0.13.2 @ 27aa40d)。

### 2. Signatures(构建命令)

前端(node:22-alpine,产物 `static/out/` 含 `.gz` 预压缩):

```bash
# 关键:挂仓库根而非 web/ 子目录,-w /src/web
docker run --rm -v "$PWD":/src -w /src/web \
  -v octopus-pnpm-store:/root/.local/share/pnpm/store \
  -v octopus-node-modules:/src/web/node_modules \
  -e VITE_APP_VERSION=<版本> \
  node:22-alpine sh -c "corepack enable && pnpm install --frozen-lockfile && pnpm run lint && pnpm run build"
```

后端(golang:1.26,对齐 go.mod 的 go 1.26.4):

```bash
docker run --rm -v "$PWD":/src -w /src \
  -v octopus-go-mod:/go/pkg/mod -v octopus-go-build-cache:/root/.cache/go-build \
  golang:1.26 sh -c "CGO_ENABLED=0 go build -a -tags=jsoniter -buildvcs=false \
    -ldflags='<见下五个注入点>' -o build/docker/linux/amd64/octopus ."
```

ldflags 注入点(module 路径 `github.com/bestruirui/octopus`,五个全注,缺一会导致 Info 页/更新指向/版本显示不一致):
`conf.Version` / `conf.Commit` / `conf.BuildTime` / `conf.Author=aks1235` / `conf.Repo=https://github.com/aks1235/octopus`

### 3. Contracts(环境变量)

| env | 用途 | 默认兜底 |
|---|---|---|
| `VITE_APP_VERSION` | 前端 Info 页当前版本 | `unknown`(Info.tsx) |
| `VITE_GITHUB_REPO` | Info 页仓库链接 | `https://github.com/aks1235/octopus`(Info.tsx fallback) |

后端仓库指向兜底链:`conf.Repo`(version.go 默认 aks1235)→ update.go 用 `strings.TrimSuffix(TrimPrefix(Repo,...))` 拼接更新 API 地址,**禁止**再硬编码仓库。

### 4. Validation & Error Matrix

| 条件 | 后果/要求 |
|---|---|
| 前端容器只挂 `web/` 子目录 | vite outDir 相对解析落容器根 `/static/out`,产物随容器销毁——**必须挂仓库根** |
| 后端不加 `-a` | go build 缓存误判 go:embed 未变,static/out 改动不进二进制(前端产物滞后)——**必须 -a** |
| `go test ./...` 全输出 `[no test files]` | 上游 v0.13.2 无任何测试,**这是正常输出但不是质量证明**;回归验证只能靠 octopus-verify 冒烟闭环 |
| 上游文件出现 lint 错 | 上游 CI 不跑 lint(v0.13.2 自带 3 错已修:`ItemList.tsx` 未用变量、`chart.tsx` fast-refresh)。新出现的按最小修复原则处理并记录偏离 |
| vite build 后 `git status` 出现 `D static/out/README.md` | emptyOutDir 清掉跟踪占位,提交前 `git checkout -- static/out/README.md` 还原(已知噪音) |

### 5. Good/Base/Bad Cases

- Good:deploy.sh 全链(pull dev-v2 → 前端 → 后端 → compose)或 smoke compose 独立项目起容器
- Base:改单文件后走 octopus-verify 的对应子集(.go→容器编译;web/→lint+build+**必起容器人审**)
- Bad:本机有 go/pnpm 就跳过容器直接构建;`go test` 通过就当验证完成(无测试=无信号)

### 6. Tests Required

octopus-verify 全流程(容器编译 → 后端 test(当前为空跑) → 前端 lint+build → smoke 容器 8081 → 健康检查 `curl 200` + 日志无 fatal/panic → **人审 UI** → `.octopus-verified` marker,commit 字段须等于当时 HEAD)。

### 7. Wrong vs Correct(compose 项目名隔离)

#### Wrong

```yaml
# docker-compose.smoke.yml 不写 name,同目录另一个 docker-compose.yml
docker compose -f docker-compose.smoke.yml up -d
# 两文件项目名都默认取目录名 octopus-new,服务名都叫 octopus
# → compose 把对方容器当本服务旧实例 stop+rm 重建(2026-09-08 实际杀掉生产容器,中断 2-3 分钟)
```

#### Correct

```yaml
# docker-compose.smoke.yml 顶部显式固定项目名
name: octopus-v2-smoke
services:
  octopus: { container_name: octopus-v2-smoke, ports: ['8081:8080'], volumes: ['./data-v2:/app/data'] }
```

> **Warning**:主 `docker-compose.yml`(v2.0.0)头部有警示注释:数据迁移演练完成前**禁止**对它执行 up/down/build——生产库 `./data` 是 v1.0.x 结构,v2 迁移链含 `DROP TABLE relay_logs`,直接套库会删历史日志。切换统一走 v2-smoke-switch 任务。

---

## SQLite 活库快照契约(2026-09-08 迁移演练确立)

- 生产 `data/data.db` 是容器**正在写的活库**(WAL 模式);`cp` 主文件可能拿到不含 WAL 的不一致副本,也可能在 checkpoint 竞态下撞 `disk I/O error`。
- 快照一律用 **SQLite backup API**(从 `mode=ro` 连接 backup 到新库);ro 打开本身也要纳入重试窗口(smoke 容器活写 `data-v2/data.db` 时实测间歇失败,连 5 次重试解决)。
- 正式切换窗口应用已停,天然无竞态;但演练/对账脚本在应用运行期间取样必须走本契约。
- 参考实现:`scripts/migrate_v1_to_v2.py` 的 `copy_template_to_dst`(backup + 重试)与快照入口。
