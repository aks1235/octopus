# 技术设计:首页统计日期化

> 2026-09-18。PRD 已定稿(热力图导航/趋势任意天/排名按天,小时保留跟随日志期)。

## 现状(已在代码核实)

- `internal/op/stats.go`:`statsHourlyCache [24]model.StatsHourly` 内存环形;`StatsHourlyUpdate`(250)按 `nowHour` 写;`StatsSaveDB` 落盘 `stats_hourlies`(Hour 主键,覆盖);`StatsHourlyGet`(370)截取 `currentHour+1` 条返回。
- `model.StatsHourly{Hour int(primaryKey); Date string(仅记录)}`。
- 日志清理:`op/log.go` `RelayLogSaveDBTask` 按保留期清理过期行——小时清理挂这。
- `relay_logs` 行内含 `channel_id/channel_name/request_model_name`,按天聚合排名无需 join。
- 前端:`home/activity.tsx`(热力图,数据=stats_dailies)、`chart.tsx`(趋势,=hourly)、`rank.tsx`(排名,=useChannelStats 累计)、`store.ts`(zustand 视图状态)。

## 方案

### 1. Schema 与迁移(013,真迁移)

- `model.StatsHourly` 改为 `{Date string; Hour int}`,复合主键 (date, hour),去"仅记录"语义。
- `013.go`(RegisterBeforeAutoMigration):老表数据并入(每行带上它已有的 date 值)→ 重建表 → AutoMigrate 收尾。空 Up 会炸 InitDB(09-15 教训),Up 必须真干活。
- 清理:`RelayLogSaveDBTask` 清日志时顺手删 `date < 保留期起` 的小时行(同一保留期口径,不新开任务)。

### 2. 写路径(op/stats.go)

- `statsHourlyCache [24]` → `map[string]model.StatsHourly`(键 `date+hour`)或按需组织,`StatsHourlyUpdate` 写 `(today, nowHour)`。
- `StatsSaveDB` 落盘按 (date,hour) upsert;跨天后新日期自然开新行,旧行不再被覆盖。
- `StatsHourlyGet` 改 `StatsHourlyGet(date)`:返回该日 0-24 全量行(未到的 hour 无行);today 语义 = 实时积累,历史日 = 静态。
- `statsSaveDBWithDailyOverride` 既有每日兜底路径保持。

### 3. 按天排名端点

- `GET /api/v1/stats/rank?date=YYYYMMDD`(server/handlers/stats.go 新路由):
  - `relay_logs` 两查:`GROUP BY channel_id, channel_name` 与 `GROUP BY request_model_name`,SUM(token/cost/success/fail),WHERE time ∈ [当日 0 点, 次日 0 点)。
  - 返回 `{channels: [...], models: [...]}`,各含请求成功/失败、input/output token、cost,前端复用现有格式化。
  - date 超出日志保留期(或日志保存关闭)→ 返回空数组,由前端提示"该日期超出日志保留期,无按天数据"。
- 累计接口不动。

### 4. 前端

- `store.ts`:加 `selectedDate: string`(YYYYMMDD,默认今天)+ setter。
- `activity.tsx`:格子 onClick(≤今天)→ setSelectedDate;选中格 ring 高亮;tooltip 保留。
- `index.tsx`:页头「当前查看:X 月 X 日」+ 非今天时「回到今天」按钮。
- `chart.tsx`:hourly 查询带 selectedDate(今天实时刷新,历史日 refetch 关);空数据空态。
- `rank.tsx`:顶部加「按天 / 累计」Tab;按天走新端点,累计走现有;MetricTabs(请求/token/费用维度)两模式共用。
- `api/stats.ts`:hourly query 带 date 参数(queryKey 含 date);新增 rank 端点 client。
- i18n 三语:回到今天、按天/累计、空态文案。

### 5. 刷新语义

- selectedDate = 今天:各查询保持现有 refetch 节奏。
- selectedDate = 历史:hourly/rank refetchInterval 关闭(静态),切回今天恢复。

## 权衡

| 决策 | 取舍 |
|---|---|
| 排名按天从 relay_logs 聚合而非新统计表 | 零 schema 增量、行内字段已冗余齐全;代价:受日志保留期限制(用户已接受,保留期可调)。独立统计表是第二套写路径+清理,过重。 |
| 小时数据跟随日志保留期 | 用户已选;口径单一,清理挂既有任务。 |
| 迁移保留旧小时行并回填 date | 升级瞬间不丢今天曲线;成本 5 行 SQL。 |
| 热力图做导航而非另加日期选择器 | 组件已在页首、已按日组织,点击即得;不加新控件。 |
| rank 按天/累计做成 Tab 而非自动跟随 | 累计视角仍有价值(全部历史),强制按天会丢信息;Tab 让两种意图都可达。 |

## 回滚

- revert 单 commit;013 迁移幂等(重建表逻辑带 HasTable/HasColumn 防御,重跑安全)。
- 新端点与 date 参数为增量,旧前端不含 date 时后端按今天兜底。

## 测试设计

- op:StatsHourlyUpdate 跨天写两行(不同 date 同 hour 不互相覆盖);StatsSaveDB 落盘 upsert;StatsHourlyGet(date) 返回该日全行。
- 迁移:旧表(含 date)→ 重建 → 数据保留且主键复合。
- rank 端点:构造两天日志,断言按日聚合正确、跨保留期返回空、日志关闭返回空。
- 清理:保留期外小时行被删,期内保留。
- 既有 stats 测试回归全绿。
