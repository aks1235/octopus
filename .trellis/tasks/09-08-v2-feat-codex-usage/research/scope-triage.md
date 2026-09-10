# Research: 范围甄别(逐项结论+证据)

- **Query**: Key 级统计与统计增强包耦合度;连通性测试 vs 原生探测;Key 轮询 vs 上游 failover;9c06c03 是否被覆盖;4d569fc 是否纳入;o_auth_sessions 迁移保留
- **Scope**: internal(fork git + dev-v2 工作树交叉核对)
- **Date**: 2026-09-09

## T1 Key 级统计(ba5deca)与统计增强包(ce929b9 族)的耦合度 → 可干净切出

- **零代码耦合**。ba5deca 的 Key 统计改动落在:`internal/model/channel.go`(ChannelKey 加 3 列)、`internal/op/channel.go`(ChannelGetKey 轮询)、`internal/op/stats.go`(+3 行)、`internal/helper/test.go`、`internal/server/handlers/channel.go`、前端 channel 模块。**不触碰 realtime_stats 模型与 stats 前端页**。
- 统计增强包(ce929b9/e21f9c3/447f269,ADR-0002:25 排除)动的是 `internal/model/realtime_stats.go`(fork 独有文件)、TPS/RPM 任务、趋势图/排行榜前端——与本包 6 子项无文件交集。
- v2 侧对照:上游 ChannelKey 已内嵌 StatsMetrics 且 relay 已按 key 累加(handler.go:242)——**fork 需求已被上游白拿**,剩余仅"读侧端点+UI"小增量(ChannelStats 目前只到模型粒度,model/channel.go:119-137)。

## T2 模型连通性测试+选择对话框(f9534b5)vs 编辑器内按凭据探测 → 大半被原生替代,建议收缩

对照 ops-obs 勘误先例(ADR-0002:35-47 的判定模式:核对所依附机制是否已被原生替代):

- fork 对话框解决的痛点:刷新模型列表后从一长串名字里挑选 → v2 `useModelProbe`(probe.ts)探测结果**直接合并** models+grants,未探到的保留,无挑选步骤;探测还顺带判定协议位。
- fork 连通性测试(testSingleModel 发 "1+1=?" max_tokens=1)的残余价值:**v2 probe 只证明 /models 端点可用,不证明具体模型可调用**;且 **Codex 渠道连 /models 端点都没有**(库 codex/constants.go 注释:无稳定公开 models 端点,靠 DefaultModels() 静态表)——Codex 渠道的模型只能靠真实请求验证或直接信任静态表。
- 结论:**建议收缩**——"模型选择对话框"不移植(痛点已被自动合并消解);"按 key×模型发最小请求的连通性测试"视 Codex 渠道落地后的验证需要决定(可作为 Codex 包的配套验证工具移植,helper/test.go 改用 axonhub outbound 构请求)。属规划期与用户对齐点。

## T3 Key 轮询(ba5deca/9c06c03)vs 上游多 KEY/failover → 语义不重合,是真实缺口但需重新设计

- 上游选路(route.go:70 pickGroupItem):**sticky 优先级**——同模型多 key 建多条 grant/多个成员后,行为是"主 key 失败进冷却才切备 key",**没有请求分摊**;另有恢复探测+亲和反而强化 stickiness。
- fork 语义(ba5deca commit message:"避免总选同一个 Key"):最低成本优先 + 同成本 round-robin,把请求摊到全部 key——**这是 Codex 多账号摊 5h/周配额窗口的核心用法**,与上游"主备"语义不同。
- 9c06c03(渠道内穷举重试)**已被上游覆盖**:上游 grant=模型×key 一对一 + 分组成员级 failover(handler.go 每轮失败换成员),甚至更细(cooldown/probe/affinity);fork 渠道内穷举在其模型下无意义。**9c06c03 不移植**。
- Key 轮询的 v2 落地是设计决策:在分组选路层加轮询模式(动 route.go 核心算法,影响面大),或接受 sticky 语义(多账号场景配额不摊),或 Codex dialect 内部自轮换 key(不动路由)。**规划期与用户对齐点**。

## T4 密钥行 Switch(bb33f79)→ 白拿关闭

上游 `FormKeys.tsx:113-118` 每行凭据已有 enabled Switch(写 ChannelKeyConfig.enabled,禁用后不参与选路但保留统计,model/channel.go:64 注释)。fork bb33f79 的原生等价存在且形态更好。**不移植,验证后关闭**(同 GroupItem DTO 勘误先例)。

## T5 4d569fc(Provider 预设+OAuth 基建)是否纳入 → 建议排除后端,前端预设并入渠道落地

- 4d569fc 是 3711 行的大 commit:后端 provider 注册表/presets.json/builtin schema/OAuth session 表/设备码流。
- v2 中 provider 概念已被「协议路径+Dialect」取代(model/channel.go:13-21),预设已是纯前端(`web/src/lib/channel-presets.tsx`,13 服务商)——**后端 provider 体系在 v2 无挂载点,移植即死代码**。
- 其中真正有存活价值的是 OAuth 机制本身,但 v2 更优路径是 axonhub/llm 库(oauth 包 + codex transformer,见 upstream-v2-reality.md §6),fork 的 oauth_web/oauth_device/crypto 全套自研在库面前无保留价值。
- **建议**:不纳入 4d569fc 本体;本包落地 Codex 渠道时在 channel-presets.tsx 加一个 Codex 预设(dialect 待定)+ 自建最小授权会话(若需要)。ADR-0002 登记表无需为此扩包,属实现细节。

## T6 o_auth_sessions 表是否随迁移保留(ADR-0005)→ 建议保留表但数据迁移价值存疑

- ADR-0005 决策:"随功能包带:relay_logs、usage_cards、o_auth_sessions(功能移植时保持表形状兼容)"。
- 实况核对:(a) 表是**授权会话**——pending→completed/failed 的临时状态,fork 有 `CleanupExpiredOAuthSessions`(completed/failed 24h 后删);(b) **fork 自己的 DBDump 备份都不含它**(model/backup.go 无 OAuthSession 字段)——fork 自己就没把它当持久数据;(c) 迁移脚本已把它列进 CONDITIONAL_TABLES(migrate_v1_to_v2.py:67),模板库一旦出现该表自动携带列交集。
- **建议**:v2 若仍用"会话表+手动回调"授权模式则按 fork 列名建表(见 migration-impact.md),脚本无需改码自动生效;但生产库该表大概率只剩空/过期残留,**数据本身无迁移价值,可接受"表在、数据空"**。若 v2 改用库的 DeviceFlowProvider(设备码轮询,无需回调会话),则整表可不建——**规划期决策点**。

## T7 Usage Card 与统计增强包/日志持久化包的依赖 → 独立,无耦合

- usagecard 包数据源是**外部 HTTP 端点**(wham/usage 等),快照存自身 last_result 列;不读 stats 表、不读 relay_logs。
- 唯一跨包依赖:codex 模板的凭证刷新依赖 oauth_codex.go(随子项 1 一起处理)。

## 汇总判定表

| ADR-0002 登记项 | 判定 | 依据 |
|---|---|---|
| Codex OAuth 渠道 | **重想象移植**(库白拿底座) | 上游零 OAuth;axonhub 有 codex transformer+oauth 全套 |
| Usage Card | **移植**(后端近乎原样) | 上游完全空白;表形状按 ADR-0005 保持 |
| auth 文件导入 | **移植**(解析可换库) | 依赖渠道落地;DecodeAuthJSON 可用 |
| 密钥行 Switch | **白拿关闭** | FormKeys.tsx:113 原生已有 |
| Key 轮询 | **设计决策**(语义缺口) | 上游 sticky,无分摊;Codex 多账号摊配额是真需求 |
| Key 级统计 | **白拿+小增量** | 写侧原生;读侧端点/UI 空白 |
| 单 Key 测试 | **移植**(小) | 上游无;probe 不发真实请求 |
| 模型连通性测试+对话框 | **收缩**(对话框关,测试待定) | 探测自动合并已消解挑选痛点;Codex 无 models 端点 |
| (9c06c03 穷举重试) | **不移植** | grant 级 failover 原生覆盖 |
| (4d569fc Provider 预设) | **建议排除** | v2 无 provider 概念;预设已前端化;OAuth 走库 |

## Caveats / 待人工确认

- T2/T3/T6 三个决策点需与用户对齐后才能冻结 PRD
- Codex 渠道在 v2 的落点(Dialect 新值 vs 独立渠道类型)影响 T2/T3 的实现路径,本研究只列事实不预设方案
