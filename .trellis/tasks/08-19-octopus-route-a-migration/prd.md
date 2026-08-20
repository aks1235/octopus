# Octopus 路线A迁移:基于上游 master 重做 7 项功能

## Goal

将个人 fork 的 octopus 二开分支切换为"以 bestruirui/octopus 上游 master 为准"的路线 A。
在上游新架构(axonhub/llm 协议转换层)基础上,重新实现 7 项用户实际需要的功能,
丢弃 chicring fork 的其余重度功能(Codex OAuth、Usage Card、RPS/TPS 仪表盘等)。

最终交付一个以 upstream/master 为基底、含 7 项重做功能的 `feat/my-features` 分支,
验证通过后由用户决定是否替换 dev。

## 背景

- **上游真实源**: github.com/bestruirui/octopus(已 fetch 为本地 remote `upstream`,HEAD `8d04257`)
- **当前 dev**: 含 166 个提交(141 个 chicring 的 + 25 个 yuan 自己的),架构为自研 transformer 层 + Next.js 前端
- **上游架构**: 已用 `github.com/looplj/axonhub/llm` 替换自研 transformer,前端改 Vite,代码大幅精简
- **路线决策**: 用户确认走路线 A(纯跟上游),因为 axonhub/llm 协议覆盖更广(含图片生成)、维护负担更低

## Requirements

### 必做功能(7 项,按顺序)

| # | 功能 | 来源 commit | 类型 |
|---|---|---|---|
| ① | 渠道同步失败标记 + 连续失败自动禁用/解禁 | dev `55ffcbf` | 后端为主,无前端 |
| ② | GroupItem 孤儿对账定时任务 | dev `6f8e156` | 后端,新文件 |
| ③ | 模型连通性测试 | dev(fork 自 chicring `f9534b5`) | 后端 API + 前端按钮 |
| ④ | 客户端 UA 识别 | dev(fork 自 chicring `a6c2e4f`) | relay 入口 + log 字段 + 前端图标 |
| ⑤ | 分组列表渠道名 DTO 下发(禁用/已删除态区分) | dev `8185f12` | 后端参考 + 前端按 Vite 重做 |
| ⑥ | 渠道调用详情页(按渠道查 attempts 明细) | dev `f551b95` | 日志层重写 + 前端 Vite 重做 |
| ⑦ | 熔断器(需改造,最后做) | dev `internal/relay/balancer/circuit.go` | 移植 + 接入新 execution.go |

### 明确放弃的功能(不在本次范围)

- RPS/RPM/TPS/TPM 60 秒滑动窗口仪表盘(用户明确不需要)
- Codex OAuth、Usage Card、Key 级统计、主题选择器等 chicring 重度功能
- chicring 的 141 个提交整体不移植

### 技术约束

- **基底分支**: `upstream/master`(本地已 fetch,不重新 clone)
- **工作分支**: `feat/my-features`,从 `upstream/master` 切出
- **dev 保留**: 作为功能参考来源(`git show dev:<file>` 读原始实现)+ 回退保险 + 当前生产版本
- **前端**: 上游是 Vite + React 19,前端部分一律按上游现有组件风格重写,不复用老 Next.js 代码
- **后端**: 上游用 axonhub/llm,移植时只搬核心逻辑,不引入 chicring 的胶水代码
- **一个功能一个 commit**,commit message 沿用 dev 原始写法
- **不破坏上游已有能力**(图片生成 API、DeepSeek thinking、日志 SSE 流等)

### ⑦ 熔断器特殊约束

- 上游 `d279e1d` 已删除熔断器,需移植回 `internal/relay/balancer/`
- 上游是"分组活动渠道"模型(同时只一个渠道活),你的熔断器是 `channelID:keyID:modelName` 细粒度
- **粒度方案需在 ⑦ 规划阶段与用户确认**:叠加熔断 vs 同时移植负载均衡
- 上游 d279e1d 删除的配置项(SettingKeyCircuitBreaker*)需加回

## Acceptance Criteria

- [ ] `feat/my-features` 分支从 `upstream/master`(8d04257)切出,基底干净
- [ ] ① 同步失败禁用:SyncModelsTask 失败累计达阈值置 enabled=false+auto_disabled=true,成功归零,运维启停清 auto_disabled
- [ ] ② 孤儿对账:启动清存量 + 按间隔周期清理渠道已删/模型下架的 GroupItem,幂等
- [ ] ③ 连通性测试:后端有测试 API,前端有按钮,能对单渠道模型发最小请求验证
- [ ] ④ UA 识别:relay 入口解析 UA,日志展示客户端图标(用上游模型/客户端图标机制)
- [ ] ⑤ 分组渠道名 DTO:后端下发 channel_name/channel_enabled,前端区分已禁用/已删除态
- [ ] ⑥ 渠道详情页:GET /api/v1/log/channel-attempts 可用,前端有详情视图 + 分页 + 点 request_id 看明细
- [ ] ⑦ 熔断器:连续失败达阈值熔断,指数退避冷却,HalfOpen 试探,接入上游 execution.go,粒度方案经用户确认
- [ ] 全部完成后 `go build ./...` 通过 + `go test ./...` 通过
- [ ] 全部完成后 `pnpm build`(web/)通过
- [ ] 本地 `go run main.go start` 起服务,手动验证各功能
- [ ] dev 分支原封不动保留

## 任务结构

父任务(本任务)own 需求集 + 跨子任务验收 + 最终集成 review,不做直接实现目标。
7 个子任务分别对应当 7 项功能,独立规划/实现/验证/归档。

## Notes

- ⑦ 熔断器粒度方案是规划阶段硬关卡,未确认前不进入 ⑦ 实现
- ⑥ 渠道详情页的 RelayLogAttemptsByChannel 查询要基于上游 `ae295af` 重构后的 log 结构重写,不照搬
- 移植时注意 chicring 功能在 dev 里可能与本任务其他功能耦合,只取核心逻辑
- 上游 `upstream/master` 可能继续更新,本次以当前 fetch 的 8d04257 为准,做完后再考虑是否同步上游新提交
