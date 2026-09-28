# 实施计划:健康检查模型对账

前置:按 design.md 分层推进,每步独立验证;全部完成后走 octopus-verify。
回滚点:每步各自成 commit 候选(最终合并单 commit),revert 即回滚,无 DB 迁移。
依赖:与 09-24-group-usability 无代码冲突;建议其先发布观察一轮再上本任务。

## Step 1 probe 层:探测返回清单

- [x] `internal/probe/models.go`:新增 `KeyProbeResult` 类型与 `FetchModelsDetailed`(逐凭据两侧全测、不短路、复用 FetchOpenAIModels/FetchAnthropicModels);`FetchModels` 改为委托后聚合 bool,签名不变。
- [x] 单测:结果形状、bool 聚合与改造前等价(假上游)。

验证:`go test ./internal/probe/`(注意 probe 无既有测试文件,新建)。✅ 已通过

## Step 2 op 层:单凭据对账

- [x] `internal/op/channel.go`:新增 `ChannelModelReconcile(ctx, channelID, keyName, fetched)`——事务内替换该凭据名下授权(按名匹配保留主键/统计)、模型集合按授权表重导出、`modelNameKept` 过滤收缩、无变更短路;与 `syncChannelChildren` 提取共享助手,保存路径行为不变。
- [x] 单测:替换语义/保留统计/空 fetched 拒绝/无变更短路/孤立模型清理/过滤收缩。

验证:`go test ./internal/op/` ✅ 已通过

实现备注(与 design 的偏差记录):
- 共享方式:未改动 `syncChannelChildren` 及其三个子函数,而是在事务内把「其他凭据现状 + 本凭据过滤后的探测结果」组装成目标集合,直接复用保存路径的 `syncChannelModels` + `syncChannelGrants` 落库 —— 替换语义与保存路径同构,保存路径零改动(红线达成)。
- `modelNameKept` 从 `internal/server/handlers/channel.go` 上移为 `op.ModelNameKept`(函数体逐字不变),handlers 委托调用:两层过滤正则保持"唯一判定口径"单一实现,手动拉取路径行为零变化;其既有测试 `channel_model_filter_test.go` 同步改为调用 `op.ModelNameKept`。
- 过滤收缩只作用于本凭据的探测清单(R3 字面口径「对账结果过黑名单+白名单」),不触碰其他凭据名下的存量授权 —— 前端手动探测在表单里会顺带收缩其他凭据的存量,后端定时对账不做这一步(单凭据粒度,不确定的不动);全部启用凭据逐轮对账后收敛效果等价。

## Step 3 task 层:健康检查接入对账

- [x] `internal/task/health.go`:`checkChannelHealth` 改用 `FetchModelsDetailed`;判活聚合不变;成功凭据逐个对账(失败仅日志);轮超时不落对账;有变更时 `GroupRegexSync` + `GroupManualAbsorb`。
- [x] 单测:失败凭据跳过对账、空列表跳过、轮超时不落、变更触发重算。

验证:`go test ./internal/...` ✅ 已通过

实现备注:空列表护栏取严格口径 —— 任一侧 2xx 但列表为空即整个凭据本轮不对账(另一侧非空也不并用),避免用可疑的部分结果改写协议位;两侧都失败同样跳过;对账落库包 `runWithBusyRetry`,单凭据对账用独立 30s 时限(`healthReconcileTimeout`),不与探测轮抢时限。

## Step 4 质量检查(全量)

- [x] `go test ./internal/...` 全绿;`go vet`;gofmt 新增代码。(质检子代理复核 8 项全过,零修复)
- [x] 自查:R1–R7 逐条对照;与 design 偏差逐条说明。

## Step 5 octopus-verify 全流程

- [x] 起验证容器(smoke+verify 叠加,tag 换 `verify-model-reconcile`),人工验证:
  - AC1/AC2:改一个测试渠道的上游模型列表(或用可控假上游),观察下一轮健康检查后渠道模型自动增删、分组成员级联;(本地起可控假上游实测:上游改 beta+gamma → 渠道自动增删 gamma/级联 alpha,协议位按两侧实测并集落位)
  - AC3:上游返回空列表时本地模型不被误删。(实测:200 空列表模型原样保留;杀掉上游 fail 计数+1 且模型原样保留)
- [x] 写 `.octopus-verified` marker。

## Step 6 收尾

- [x] spec 补记:`.trellis/spec/backend/`(健康检查对账语义、与手动探测/保存路径的三方一致性约定)。(新增 channel-health-reconcile.md,index.md 挂链)
- [x] 提交 dev-v2(单 commit);不推送、不打 tag,发布另行决定。

## 风险与回退

- 最大风险是误删,R4 护栏三层;实现期若发现 `syncChannelChildren` 提取共享助手会大动保存路径,允许退而求其次"新写对账函数但逐字段对照保存路径语义",偏差记录到本文件。
