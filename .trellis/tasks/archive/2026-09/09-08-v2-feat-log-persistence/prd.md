# 移植:日志持久化包

## Goal

在 dev-v2(上游 v0.13.2 基线)上移植 fork 的日志持久化能力:relay 请求终态后落库 `relay_logs`(Snowflake 主键、attempts 链、内容与 token 明细),提供历史查询(分页 + API Key/模型多选筛选 + 时间区间 + 错误过滤)、详情按需加载、保留期清理,并交付渠道调用详情页(按渠道聚合 attempts 明细)。上游日志页 SSE 实时流(白拿项)原样保留,两者互不干扰。

## 背景与前置事实(已核实)

- 上游 v0.13.2 日志纯内存:`internal/relay/state.go` RequestState + SSE(`/api/v1/log/overview/stream`),仅保留 50 条已结束请求,无持久化。
- 上游 `internal/db/migrate/009.go`(AfterAutoMigrate 链)会 `DROP TABLE relay_logs`——若直接把 RelayLog 加进 AutoMigrate,建表后 009 立即删表。**本任务须拦截 009 删表**(父任务地图既有要求,ADR-0005)。
- 转换脚本 `scripts/migrate_v1_to_v2.py` 对 relay_logs 是「列取交集、保留主键」的条件携带:**目标模板库存在该表才转换,否则跳过**。当前 v2 模板无此表,故任务2 演练时 fork 库 1284 条 relay_logs 走的是跳过分支(条件携带机制曾用合成模板实测 1285 条)。本任务落地后模板库出现该表,须重跑真实副本演练验证 1284 条全量带入。
- fork 侧实现出处:`internal/model/log.go`(RelayLog/ChannelAttempt/ChannelAttemptDetail)、`internal/op/log.go`(缓存+flush+查询+清理)、`internal/handlers/log.go`(/list、/channel-attempts、/:id 等)、relay 挂钩 `internal/relay/metrics.go`(metrics.Save → op.RelayLogAdd)、设置 key(`relay_log_keep_enabled` 默认 true / `relay_log_keep_period` 默认 7 天,`internal/model/setting.go`)。

## Requirements

### R1 日志持久化底座(后端)

- RelayLog/ChannelAttempt/ChannelAttemptDetail 模型平移,**列形状按 fork 完整形状**(含 user_agent/client_name/reasoning_effort),保证迁移列交集=全列、1284 条零丢弃;任务5 的识别/思考等级逻辑到位前这些列允许为空值。
- RelayLog 加入 `internal/db/db.go` AutoMigrate 列表;migrate/009 删除 `DROP relay_logs` 段(保留 base_urls 列删除),加注释说明 dev-v2 恢复日志持久化。
- relay 终态定稿处组装 RelayLog 落库:缓存聚合 + 批量 flush + 周期落盘(移植 fork 缓冲语义,不逐条写盘);attempts 链按 fork 语义重建(每轮:渠道/凭据/模型/状态/耗时/消息)。
- 设置项:`relay_log_keep_enabled`(默认 true)、`relay_log_keep_period`(默认 7 天),含设置页 UI 入口;保留期到期清理。
- 上游 SSE 实时流、request-body/response-body 按需拉取、人工中止、内存 Clear 全部不动。

### R2 历史查询接口(后端)

- `GET /api/v1/log/list`:分页 + 时间区间 + has_error + api_key_names 多选 + model_names 多选,列表排除大字段(内容列 Omit)。
- `GET /api/v1/log/:id`:详情按需加载(含 request_content/response_content/debug_content/attempts)。
- `DELETE /api/v1/log/clear`:语义冲突裁决——上游 `/clear` 清内存、fork `/clear` 清库。本任务保持上游 `/clear` 清内存不动;清库入口挂到 fork 原语义路径,以查询参数或独立路径区分(设计文档定稿)。

### R3 渠道调用详情页(前后端)

- `GET /api/v1/log/channel-attempts`:按渠道展开 attempts 明细(LIKE 粗筛 + Go 层精确过滤 + truncated 上界,fork 原方案平移)。
- channel 模块「调用详情」入口 + CallDetail 视图(概览统计+明细行+分页+就地弹请求详情),RequestDetailDialog 懒加载复用。

### R4 前端历史查询区

- 日志页保留上游 SSE 实时区,叠加历史查询区(分页/筛选/详情懒加载);具体布局(同页分区 vs tab)以设计文档为准,改动面最小化。
- i18n en/zh_hans/zh_hant 三语。

## 非目标(范围外)

- fork 行级日志 SSE(`/api/v1/log/stream` + `/stream-token`)与 `/active` 活跃请求接口**不移植**:上游 overview/stream 已覆盖实时可观测需求(ADR-0002 同理砍「一键刷新」)。
- 日志一键刷新、统计增强包(TPS/RPM/排行榜/趋势筛选)不移植(ADR-0002 排除项)。
- 客户端 UA 解析、思考等级展示/分组覆盖:列先建、逻辑归任务5。
- 日志页 SSE 实时流形态不改造(白拿项,回归验证即可)。

## Acceptance Criteria

- [ ] AC1 拦截生效:全新库启动建出 `relay_logs` 表且 009 不再删表;改后 009 对无表库不报错(幂等)。
- [ ] AC2 落库正确:真实请求(成功/失败/多轮重试)终态后 relay_logs 出现对应行,attempts 链、token 明细、内容、错误与实际一致;关闭 keep_enabled 后不落库(仅内存),重开后恢复。
- [ ] AC3 历史查询:/list 各筛选组合(时间/has_error/api_key_names/model_names 多选)与分页正确;/:id 返回完整详情;清库入口可用。
- [ ] AC4 渠道调用详情:channel 卡片入口 → CallDetail 明细与 relay_logs.attempts 一致;truncated 提示生效;点 request_id 弹详情可懒加载。
- [ ] AC5 白拿项回归:上游日志页 SSE 实时流、中止、request/response-body 拉取、内存 clear 行为不回归。
- [ ] AC6 迁移链路:data 副本重跑转换演练,relay_logs 全量带入(实测 1285 条;规划期快照为 1284)、列零丢弃(conditional_status 显示「转换」),容器加载新库启动正常、历史日志页可查到迁移数据。
- [ ] AC7 保留期:keep_period 到期行被周期清理任务删除;设置页可改两个设置项并生效。
- [ ] AC8 octopus-verify 闭环通过(容器内编译、后端测试、前端 lint+build、起容器 + 人审 UI)。

## Notes

- 范围增补(2026-09-09,经用户确认):渠道调用详情页(原任务3,fork 对照 `f551b95`)挪入本任务。
- 对 migrate 演练的影响说明(父任务要求):relay_logs 表形状 = fork 完整列形状,转换走「列取交集」全命中,无丢弃;其余表不受影响。
- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
