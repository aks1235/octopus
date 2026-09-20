# 转发日志持久化契约(dev-v2)

### 1. Scope / Trigger

任务 `09-08-v2-feat-log-persistence` 落地的日志持久化层:relay 终态落库、历史查询、渠道调用明细、保留期清理。触发 code-spec 深度因:DB schema/迁移变更 + 新 API 签名 + 前后端跨层契约。后续任务5(客户端识别/思考等级写入既有列)、任务6(usage_cards 条件携带)、任务7(冒烟矩阵)直接依赖本契约。

### 2. Signatures

**DB(表形状冻结,23 列 = fork 完整形状)**:`internal/model/log.go` 的 `RelayLog`。json tag 沿 fork(注意 `ChannelId → "channel"`、`Attempts gorm:"serializer:json"`)。**任何列裁剪都会破坏迁移「列取交集」造成 fork 数据丢弃**;任务5 只往 `user_agent`/`client_name`/`reasoning_effort` 写值,不许改列。

**迁移拦截**:`internal/db/migrate/009.go` 已删 `DropTable("relay_logs")` 段(注释引 ADR-0005)。**revert 该文件会恢复 DROP——任何含 relay_logs 数据的库再启动即删表**;回滚规程:先备份库文件再 revert。

**写库挂钩**:`internal/relay/handler.go` Forward 闭包——本地 `attempts []model.ChannelAttempt` 按轮 append(等待型 continue 不记),`defer relayLogFinalize(...)` 终态组装(成功/失败/取消全落库;token/cost 取 RequestState 定稿值,不重算)。**新增请求生命周期埋点照此模式,不要在 state.go 的 mark* 内插桩**(持全局锁不做 I/O)。

**任务5(09-08-v2-feat-client-theme)已落地的列写值**:`client_name` = `detectClient(userAgent)`(internal/relay/client_detect.go,40 客户端规则,移植 fork 最终版);`reasoning_effort` = `extractReasoningEffort(format, raw.Body)`(internal/relay/reasoning.go,三协议提取,anthropic 按 budget 阈值反推,**只读不改请求**)。两值在 `newRequestState` 创建时一次定稿并进 RequestState(JSON 字段 `client_name`/`reasoning_effort`),实时状态流与落库日志同源;持锁内仅纯内存字符串运算,不违上述契约。分组覆盖思考等级经用户决策不移植(见 ADR-0002 勘误 2026-09-09)。前端客户端图标优先 `@thesvg/react` 官方品牌图标(mono 强制 currentColor,规则 `.client-icon-badge`),无官方图标才 lucide 兜底——与上游 model-icons.tsx 的图标体系一致。

**API(全部挂 Auth 组;上游 5 个既有接口零改动)**:

| 路由 | 方法 | 说明 |
|---|---|---|
| `/api/v1/log/list` | GET | page/page_size/start_time/end_time/has_error/api_key_names/model_names(逗号多选);Omit 大字段 |
| `/api/v1/log/:id` | GET | 详情按需加载(含 request/response/debug_content + attempts) |
| `/api/v1/log/channel-attempts` | GET | channel_id/page/page_size → `{list,total,truncated}` |
| `/api/v1/log/history/clear` | DELETE | 清库+内存缓冲(**与上游 `/clear` 清进程内状态是两个语义,勿合并**) |

### 3. Contracts

- **落库缓冲**:满 20 条 flush(`op/log.go` RelayLogAdd)+ 60s 定时兜底(`task/init.go` TaskRelayLogSave,含按 `relay_log_keep_period` 天的 cleanup)。`relay_log_keep_enabled=false` 时不落库仅内存留 100 条;重开 true 后滞留缓冲随下次 flush 补入(数据不丢)。
- **迁移条件携带**:`scripts/migrate_v1_to_v2.py` 的 `CONDITIONAL_TABLES = (relay_logs, usage_cards, o_auth_sessions)` ——目标模板库有该表才走「转换」(列取交集、保留主键),否则跳过。**任务6 落地 usage_cards/o_auth_sessions 后须重跑真实副本演练验证带入**(任务4 即此流程:1285 条全量「转换」零丢列)。设置交集已含 `relay_log_keep_enabled/period`。
- **attempts 语义**:v2 转发链只产生 `success`/`failed`;`circuit_break`/`skipped` 枚举仅为 fork 迁移数据保留展示(上游冷却跳过在 pickGroupItem 内部,handler 无事件源)。渠道详情页四态渲染保留。
- **前端**:`api/log-history.ts` 全部 queryKey 用 `['log-history']` 前缀(含详情 `['log-history','detail',id]`),与实时流 `['logs']`、上游清内存 clear 的 invalidate 互不误伤;历史区模型筛选项来源 = **分组名列表**(`request_model_name` 语义即分组名,非渠道模型名);`RequestDetailDialog` 自含 JsonView 渲染绕开 morph(上游 Item.tsx 保持零改动)。
- **ftut 为近似值**(首次取得可提交响应时刻 − StartedAt);`debug_content` 恒空(上游 pipeline 无差异记录钩子)——均已注释,勿当 bug 修。

### 4. Validation & Error Matrix

| 条件 | 行为 |
|---|---|
| 全新库首启 | AutoMigrate 建 relay_logs → 009(改后)不碰表;migration_records 全跑 |
| 已跑过 009 的库 | migration_records 命中跳过,AutoMigrate 补建表 |
| keep_period ≤ 0 | cleanup 不删(无期限) |
| channel-attempts 粗筛行数 ≥ 5000 | truncated=true,提示仅展示最近记录 |
| LIKE 粗筛误匹配(90 命中 900) | Go 层 `a.ChannelID == channelID` 精确过滤兜底 |
| 未启用 keep_enabled 时查 channel-attempts | 直接返回空(DB 无历史语义) |

### 5. Good/Base/Bad Cases

- Good:新功能要记录请求级数据 → handler 闭包 defer finalize 模式 + op 层缓冲
- Base:任务5 写 client_name → 在 relayLogFinalize 内解析 UA 填列,列与迁移契约不变
- Bad:直接改 `RelayLog` 列形状(丢迁移数据)/ 在 state.go mark* 里做 DB I/O(持锁)/ 历史区复用 `['logs']` queryKey(与清内存互伤)

### 6. Tests Required

- `internal/op/log_test.go`:筛选组合、缓存+DB 合并分页、LIKE 误匹配精确过滤、5000 上界 truncated、cleanup cutoff、disabledKeep 空返回
- `internal/relay/log_finalize_test.go`:attempts 尾取语义(成功优先/无成功回退/空 attempts)、token/cost/ftut 组装(flush 后查 DB 断言)
- 演练对账:`conditional_status` 中 relay_logs 须「转换」且 dropped 为空

### 7. Wrong vs Correct

#### Wrong
```go
// 在 state.go 终态方法里落库(持有全局锁 mu,做 I/O 会阻塞全部请求状态发布)
func (r *RequestState) markSucceeded(...) { op.RelayLogAdd(...) }
```
#### Correct
```go
// handler.go 闭包:插桩本地无锁切片,终态出函数时 defer 统一组装落库
var attempts []model.ChannelAttempt
defer func() { relayLogFinalize(request, metadata.Model, attempts, apiKeyID, userAgent, firstValidAt) }()
```

## 关联:统计永久化(v2.3.0 起)

- StatsHourly 复合主键 (date, hour),不再是 24 格环形——历史小时曲线按日期留存,跨天写新行。**不再随日志保留期清理**(曲线永久留存,与 `relay_log_keep_period` 解耦)。
- 每日渠道/模型排名落永久汇总表 `StatsChannelDaily(date,channel_id)` / `StatsModelDaily(date,model_name)(`internal/model/stats.go`): `op.StatsDailyRankFold(ctx, dates)` 从 relay_logs 聚合后整体替换(事务内 delete+insert),幂等;渠道按 channel_id 分组名称取 MAX(防同日改名拆行撞 React key)。
- 折叠触发点两处: `StatsSaveDBTask` 每周期折叠今天+昨天(未启用日志保存时跳过);`relayLogCleanup` 删日志前折叠即将被删的整日。
- `relayLogCleanup` 只按「整日」淘汰(cutoff 取整到本地日 0 点):保证删前折叠时该日日志完整,避免同一天被多次部分删除后重复折叠把当日汇总覆盖小(丢账);折叠失败则本轮不删,下轮幂等重试。
- `/stats/rank?date=` 按日期分流:**今天**直接实时聚合 relay_logs(今日数据每分钟在变,读每周期重算的汇总表会落后一个周期;今日日志必在保留期内);**历史日**优先读汇总表,无汇总行时回退实时聚合。`available` 恒 true(统计永久化后数据来源恒存在,不再表示「超出保留期」)。
- **折叠空聚合护栏**:`statsDailyRankFoldOne` 聚合为空时,仅今天/昨天照常整体替换(这两天在保留期内,空即真实无流量);更早的历史日直接跳过,保留既有汇总——避免「清空历史日志」或历史日日志更早被清理后,整日 delete+insert 把永久汇总抹空(抹账)。
- **时区无关**:清理前定位「即将被删的日期」不折算 SQL 方言日期(`relayLogDistinctDatesBefore`,`internal/op/log.go`):先 `SELECT MIN(time) FROM relay_logs WHERE time < cutoff`(纯数值比较),再在 Go 侧用 `time.Unix(...).In(time.Local)` + `AddDate` 从最早日志的本地日逐日迭代到 cutoff 所在日。这样与 Go `time.Local` 强一致,DST 由 `AddDate` 自然处理,不再依赖数据库会话时区(旧方案 mysql/postgres 取会话时区,与 Go 不一致时会折叠错日期、抹空永久汇总)。
