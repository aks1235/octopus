# Design — Octopus 路线 A 迁移

> 本设计针对父任务整体迁移流程。各子功能的技术设计由其自身 `design.md` 承担，本文档只定义跨子任务的结构、基底、集成点与硬关卡。

## 1. 分支与基底

- **基底**: 本地 `upstream/master`（commit `8d04257`，已 fetch，不重新 clone）
- **工作分支**: `feat/my-features`，从 `upstream/master` 切出
- **dev 保留**: 全程不动。作为
  - 功能参考来源（`git show dev:<path>` 读原始实现）
  - 回退保险与当前生产版本（容器跑的 v1.0.4）
- **remote `upstream`**: 基底锁定 `8d04257`，迁移期间在该基底上做 ①–⑦。**基底漂移记录**: 2026-08-19 发现上游已推进到 `1db150c`（3 个新提交: 小米模型预设、图表数据优化、日志布局）。已确认这 3 个提交与 ①② 无技术冲突——`1db150c` 对 `channel.go` 的改动纯注释重写（字段结构/gorm 标签/字段名未动），其他文件（price/main/updatePrice/chart/log-Item/model-icons）与 ①② 改动无交集。**策略**: 保持基底 8d04257 继续 ②–⑦，全部完成后把 feat/my-features **一次性 rebase 到当时 upstream/master 最新**，统一解决冲突（预计仅 channel.go 注释块需重解，无技术冲突）。不再逐新提交打断节奏。

## 2. 上游架构事实（决策依据）

verified via `git show upstream/master:...`：

- **transformer 层**: 已替换为 `github.com/looplj/axonhub/llm`。octopus 只剩 `internal/relay/transformers.go`（20 行胶水），relay 主逻辑在 `execution.go`/`forward.go`/`converted.go`，调用 axonhub pipeline。
- **前端**: Vite + React 19（非 Next.js）。组件在 `web/src/components/modules/`，按模块目录组织（`channel/`、`group/`、`log/`、`home/`、`setting/`）。i18n 在 `web/src/locales/*.json`。
- **负载均衡**: 无 balancer 包。`execution.go` 用「分组活动渠道」模型——一个分组同时只有一个活动渠道，失败按 `group.RetryInterval` 等待重试，靠 `GroupActiveItemChangeSignal()` 通知切换。**无渠道级熔断**。
- **migrate**: 上游只到 **006**。channel 为 **单 BaseURL + 单 Key** 结构（`af2787d` 删了多 URL/多 KEY）。字段含 `Type/Enabled/BaseURL/Key/Model/CustomModel/Proxy/AutoSync/CustomHeader/ParamOverride/ChannelProxy/MatchRegex`。
- **setting**: `SettingKey` 常量较少，不含 `CircuitBreaker*`、`SyncFailThreshold`、`GroupReconcileInterval`（均需随对应子功能加回）。
- **日志**: 有 SSE 实时流（`/api/v1/log/overview/stream`、`/api/v1/log/{id}/stream`），有 RequestState。但无 UA 识别、无 RPS/TPS、无模型连通性测试、无按渠道查 attempts 详情页。
- **熔断器**: `d279e1d` 已删除（-176 行），`internal/relay/balancer/` 不存在。
- **Go 工具链（Step 0 实测发现）**: 上游 `go.mod` 要求 `go 1.26.4`。`octopus-verify` skill 与 dev 的 `deploy.sh` 都配 `golang:1.25`，直接跑报 `go.mod requires go >= 1.26.4 (GOTOOLCHAIN=local)`。**解法**: 构建/测试容器内加 `-e GOTOOLCHAIN=auto`，go 自动下载 1.26.4 工具链（已验证可行）。这是走路线 A 带来的工具链升级成本，`octopus-verify` skill 的 `golang:1.25` 命令需补此 env，`deploy.sh` 也要改（否则发布构建会失败）。已在 Step 0 用 `GOTOOLCHAIN=auto` 跑通上游 `./internal/...` 编译。

## 3. 数据迁移决策（A2 + B1）

### A2：导出渠道配置再导入（保渠道，丢历史 log）

- **导出源**: 旧 `data/data.db`（009 schema）的 `channels` 表
- **降级规则**: 旧库的多 KEY 结构 → 每个 key 导出为一个独立 channel（name 加后缀区分）。多 BaseURL 同理拆成多 channel。这与上游单 BaseURL+单 Key 结构对齐。
- **导入方式**: 新库启动后通过 `feat/my-features` 的 channel API 批量导入；或直接构造 SQL/JSON 注入。实现细节留在子任务 ⑤ 或专门的迁移脚本（见 §8）。
- **丢弃**: `relay_logs` 历史、`stats_*` 历史统计、`migration_records`（新库从 006 起走上游迁移序列）。
- **回退**: 导出文件保留，dev 分支保留库本体，失败可回。

### B1：熔断器降级为 `channelID:modelName` 维度

- 原 dev key: `channelID:keyID:modelName`（三段）
- 上游无 keyID 概念 → 降级为 **`channelID:modelName`**（两段）
- 熔断器其余能力不变：三态（Closed/Open/HalfOpen）、指数退避、配置项、HalfOpen 试探。
- **影响 ⑦ 实现**: `circuitKey` 函数改签名；上游 `execution.go` 接入点只需 channel+model。粒度方案（叠加 vs 移植负载均衡）见 §7，⑦ 规划阶段仍需与用户最终确认。

## 4. 子任务映射与依赖

子任务树位置不代表执行依赖；依赖写在此处与各子任务 prd：

```
① sync-fail-auto-disable      独立，无依赖
② groupitem-reconcile         独立，但 GroupItem 补全依赖 ① 的「禁用渠道被保留」语义（对账不禁用，只删孤儿引用）
③ model-connectivity-test     独立，后端测试 API 可复用 relay 链路
④ client-ua-detect            依赖上游 relay 入口（execution.go 拿 UA）+ log model 加字段
⑤ group-channel-dto           依赖上游 group/card 前端结构清晰后再做；与 ② 共用 GroupItem 模型
⑥ channel-call-detail         依赖上游 log 结构（ae295af 后）+ ④ UA 字段若要展示客户端
⑦ circuit-breaker-port        最后做；硬关卡=粒度方案确认（见 §7）；接入点依赖上游 execution.go 稳定
```

**建议执行顺序**: ①→②→③→④→⑤→⑥→⑦（与 prd 清单一致）。每个子任务完成后父任务进度 +1。

## 5. 跨子任务集成点

- **setting.go 演进**: ① 加 `SyncFailThreshold`、② 加 `GroupReconcileInterval`、⑦ 加 `CircuitBreaker*`。各自在子任务内增量加，不集中改一次。
- **channel.go 演进**: ① 加 sync_fail_count/last_sync_error/last_sync_at/auto_disabled 四列。
- **GroupItem 模型**: ② 依赖 `op.GroupItemListAll`；⑤ 加非持久化 `ChannelName/ChannelEnabled` 字段。两者改同一文件 `model/group.go`，注意合并。
- **log model**: ④ 加 UA 字段；⑥ 要基于上游 log 结构重写 attempts 查询（不照搬 dev）。两者改 `model/log.go`，注意合并。
- **前端 locales**: ③④⑤⑥ 都可能加 i18n key。上游用 `web/src/locales/{en,zh_hans,zh_hant}.json`，注意三语同步。

## 6. 验证关卡（集成层）

每个子任务完成时单独验证（go build/test + 对应前端）。父任务集成层额外验证：

- `git log feat/my-features..upstream/master` 应为空（基底未漂移）
- `go build ./...` 通过
- `go test ./...` 通过
- `cd web && pnpm build` 通过
- `go run main.go start` 起服务，导入渠道配置（A2），手动跑各功能 smoke test
- 熔断器 ⑦ 需集成测试：模拟连续失败触发 Open，冷却后 HalfOpen 试探，成功恢复 Closed

## 7. ⑦ 熔断器硬关卡（规划阶段未决）

虽然 B1 已定 key 维度=`channelID:modelName`，但接入方式仍有两个选项，⑦ 规划阶段需与用户确认：

- **选项 X（叠加，推荐）**: 保留上游「分组活动渠道」负载均衡模型，在其上叠加熔断器——`executeAttempt` 前查熔断状态，`handleAttemptFailure` 更新计数；熔断的渠道由熔断器短期隔离，活动渠道切换仍走上游 `GroupActiveItemChangeSignal`。改动小，与上游模型不冲突。
- **选项 Y（移植负载均衡）**: 连带移植 dev 的 `balancer/`（iterator/session/circuit），替换上游活动渠道模型。更完整（恢复多渠道并行），但改动深、与上游模型背离，可能引发后续同步上游困难。

记录于父 prd「⑦ 熔断器特殊约束」。到 ⑦ 规划阶段以 AskUserQuestion 确认。

## 8. 骨架优先原则

父任务 implement.md 第 0 步：先建 `feat/my-features` + 验证上游能编译运行 + 确认数据库迁移序列（006）在你的环境跑通，再进 ①。早发现上游本身的环境坑。

A2 的渠道导出/导入脚本作为骨架阶段的一部分（或独立子步骤），保证有「干净起点 + 渠道可恢复」。
