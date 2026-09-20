# 统计永久化:趋势曲线与按天排名不再跟随日志保留期

## Goal

首页「趋势曲线」与「排名按天」目前跟随「日志保存天数」清理,超期即空。改为**永久留存**:曲线停止清理;按天排名把每日渠道/模型汇总**落成永久表**,原始日志照原样按保留期清理(日志是工作集,汇总是永久账)。

## Background(2026-09-20 用户要求)

- 现状(已核实):`relayLogCleanup` 删过期日志时顺手删过期小时行(`op/log.go:144`);按天排名端点 `StatsRankDaily` 实时聚合 `relay_logs`,保留期外返回 `available=false`。
- 用户设置保留期 1 天 → 昨天起的曲线与排名皆空。
- 汇总口径:渠道榜按 `channel_id`(名称 MAX,防同日改名拆行),模型榜按 `request_model_name`(即分组名);指标为请求成功/失败、input/output token、cost。聚合 SQL 已在 `StatsRankDaily` 内实现,可复用。

## Requirements

- R1 **曲线永久**:`stats_hourlies` 不再清理(移除 `relayLogCleanup` 里的 `StatsHourlyCleanupBefore` 调用,并清理随之无用的代码);数据按 (date, hour) 持续留存(24 行/天)。
- R2 **每日汇总表**:新增 `stats_channel_dailies`(PK: date+channel_id)与 `stats_model_dailies`(PK: date+model_name),含名称与 `StatsMetrics`。
- R3 **折叠写入(幂等)**:`StatsDailyRankFold(ctx, dates)`——对给定日期从 `relay_logs` 聚合后**整体替换**该日汇总行(事务内 delete+insert),重复执行结果一致。
- R4 **两个触发点**:(a) 周期任务(挂在统计落盘节奏)折叠**今天与昨天**,保证汇总始终接近实时;(b) `relayLogCleanup` 删日志前,折叠**即将被删的日期**,确保清理不丢账。
- R5 **读路径**:`StatsRankDaily(date)` 优先读汇总表;该日无汇总行时**回退实时聚合**(覆盖首次折叠前的窗口);不再因保留期返回不可用。
- R6 **前端**:移除「超出日志保留期」提示(数据永久,不再存在该状态);空数据仍显示「暂无数据」。i18n 三语同步清理无用键。
- R7 迁移:新表由 AutoMigrate 建立(不写空迁移;需要版本行时用真实 Up);历史日志在首次清理时自动折叠入库。

## Non-goals

- 不改日志保留策略本身(原始日志仍按设置天数清理)。
- 不做按小时维度的渠道/模型汇总(仅日级)。
- 不改热力图/汇总卡/累计排名(本就永久)。

## Acceptance Criteria

- [ ] AC1 曲线:超期后历史小时行仍在,趋势图可查任意历史日期
- [ ] AC2 折叠幂等:同一日期重复折叠,汇总行不重复、数值与日志聚合一致
- [ ] AC3 清理联动:清理掉某日日志后,该日排名仍可从汇总表读出(账不丢)
- [ ] AC4 读回退:尚无汇总行的日期,排名仍能实时聚合出数据
- [ ] AC5 前端不再出现「超出保留期」提示,空数据为「暂无数据」
- [ ] AC6 既有 stats 测试零语义回归;octopus-verify 通过(UI 人审:昨天/前天曲线与排名可见)

## Notes

- 轻量偏中:涉及 2 张新表 + 1 个折叠函数 + 2 个触发点 + 端点读路径 + 前端提示。PRD-only + design 要点写在本文件,实施按 PRD 走。
- 汇总表体量:每渠道每日 1 行 + 每模型每日 1 行,一年 ≈ 渠道数×365,可忽略。
