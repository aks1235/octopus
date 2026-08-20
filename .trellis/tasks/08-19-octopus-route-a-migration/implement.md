# Implement — Octopus 路线 A 迁移（父任务执行计划）

> 父任务不直接实现，本文档定义整体执行顺序、验证命令、回滚点。各子功能执行细节由其自身 `implement.md` 承担。

## Step 0 — 骨架与干净起点（前置，必做）

- [ ] 0.1 从 `upstream/master`（8d04257）切 `feat/my-features` 分支
  ```bash
  git checkout -b feat/my-features upstream/master
  ```
- [ ] 0.2 验证上游能编译
  ```bash
  go build ./...
  go test ./...
  ```
- [ ] 0.3 验证前端能构建
  ```bash
  cd web && pnpm install && pnpm build
  ```
- [ ] 0.4 临时空 `data.db` 启动一次，确认迁移序列 006 跑通、服务能起
  ```bash
  # 备份当前 data.db → data.db.dev-backup
  # 空 data.db 启动确认 migrate 006 全过
  go run main.go start
  ```
- [ ] 0.5 渠道配置导出（A2）—— 从旧库导 channels 表，按降级规则拆多 KEY/多 URL 为多 channel，存 JSON 待导入
  ```bash
  python3 scripts/export_channels.py data/data.db.dev-backup data/channels-export.json
  ```
  （脚本骨架阶段写，放 `scripts/` 下）
- **验证门**: 0.2/0.3/0.4 全过才进 Step 1。任一失败 → 上游本身环境问题，停下报告。
- **回滚点**: 0.4 之前若上游跑不起来，删 `feat/my-features` 回 dev，重新评估路线。

## Step 1–7 — 子功能实现（顺序）

每步 = 一个子任务的完整规划→实现→验证→归档周期。子任务 `implement.md` 各自定义细节。父任务只跟踪：

- [ ] 1. ① sync-fail-auto-disable（见子任务）
- [ ] 2. ② groupitem-reconcile
- [ ] 3. ③ model-connectivity-test
- [ ] 4. ④ client-ua-detect
- [ ] 5. ⑤ group-channel-dto
- [ ] 6. ⑥ channel-call-detail
- [ ] 7. ⑦ circuit-breaker-port（硬关卡：粒度方案 X/Y 确认后进实现）

### 每步通用验证命令

```bash
go build ./...
go test ./...
cd web && pnpm build       # 若该步涉及前端
go run main.go start       # 该步 smoke test
```

### 每步提交规范

- 一个子功能一个 commit（或子功能内逻辑拆多 commit，但聚合在一次实现里）
- commit message 沿用 dev 原 commit 写法（feat/fix + 中文正文说明 why）
- 不在 commit 里混入其他子功能改动

## Step 8 — 集成验收(父任务 own)

- [x] 8.1 确认基底未漂移:feat/my-features rebase 到 `4928a04`(upstream/master HEAD `aca27ff` 自身不可编译:`27a29a3` 删 helper/price.go 留 4 处调用 + LLMList/ChannelLLMList 签名不一致,故退到 27a29a3 前的可编译提交 `4928a04`)
- [x] 8.2 全量编译与测试通过(`GOTOOLCHAIN=auto`)
- [x] 8.3 渠道配置导入(A2):`scripts/export_channels.py` 从 dev 009 库导出 103 渠道(多URL/多KEY 降级单条)→ `scripts/import_channels.py` 登录 feat + 批量 POST create,103/103 成功
- [x] 8.4 全功能 smoke(①–⑦ 已在各自子任务 smoke;父任务集成层起容器+导入+健康过)
- [x] 8.5 熔断器集成测试:threshold=3/cooldown=10,死址 ch2 连打 3 次→Closed→Open(tripCount=1)→CAS `GroupAdvanceActiveItem` 推进 ch4(持久化 active_item_id=2,请求走 ch4 200)→冷却 10s→Open→HalfOpen→探测失败/tripCount++/退避 20→40s→恢复 ch2 健康→HalfOpen→Closed(成功)→重置。日志 11:50~12:00 全周期可见
- [x] 8.6 与用户 review 交付物(用户确认 rebase 时机=现在,基底退到 4928a04 因 upstream HEAD 坏;dev 不动,生产仍 v1.0.3)

## Step 9 — 规范更新(Trellis 3.3)

- [x] 9.1 更新 spec:`quality-guidelines.md`(GOTOOLCHAIN=auto/go:embed/签名同步铁律)、`logging-guidelines.md`(charmbracelet/log + 熔断日志关键字)、`index.md` 状态;`database-guidelines.md` 已含三层维护契约
- [x] 9.2 commit 最终状态(`40f2a63`:smoke 三件套 + .gitignore + spec 更新 + 父任务 Step 8 验收)

## 风险与回滚

| 风险 | 触发 | 应对 |
|---|---|---|
| 上游本身编译/迁移失败 | Step 0.2/0.4 失败 | 删 feat/my-features，回 dev 重评路线 |
| 某子功能与上游结构冲突 | 子任务实现阶段编译反复失败 | 子任务 prd 退回，细化设计或降级方案 |
| ⑦ 熔断器粒度方案阻塞 | ⑦ 规划阶段 X/Y 难定 | 先跳 ⑦ 完成其他 6 项，⑦ 单独处理 |
| 渠道导入数据损失 | A2 导入后渠道数量/配置异常 | 保留 dev-backup 库，手动补 |
| 上游 upstream 漂移 | 迁移期间误 fetch upstream | 禁止迁移期间 `git fetch upstream`；基底锁 8d04257 |

## 跨子任务协作契约

- `model/setting.go`、`model/channel.go`、`model/group.go`、`model/log.go` 多个子任务改同一文件 → 每个子任务实现前先 pull 该文件最新状态，commit 时只动自己那部分字段，不误删他人字段。
- 前端 `web/src/locales/*.json` 三语同步 → 每个子任务加 key 时 en/zh_hans/zh_hant 三份一起加。
- ⑦ 接入 `execution.go` 可能与 ③④ 的接入点冲突 → ⑦ 放最后，实现前通读 ③④ 已改的 execution.go。
