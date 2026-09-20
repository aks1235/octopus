# 渠道调用详情性能:attempts 拆表 + relay_logs 索引

## Goal

修复「渠道调用详情」打开慢:根因是 `relay_logs` **零索引** + 对 `attempts` 巨型 JSON 文本列做**前缀通配 LIKE 全表扫描**。把 attempts 规范化为独立表(按渠道建索引),查询走索引秒回;顺带给 `relay_logs(time)` 加索引(日志列表页同样受益)。

## Background(2026-09-20 实测定位)

- 查询(`internal/op/log.go RelayLogAttemptsByChannel`):
  `WHERE time >= cutoff AND attempts LIKE '%"channel_id":120%' ORDER BY time DESC LIMIT 5000`,
  然后在 Go 层展开每个 attempt 精确过滤。
- 路径解释:日志行的 `channel_id` 列只记**最终**渠道,而「调用详情」要列出该渠道在 failover 中间撞过的每次尝试——那些只存在于 attempts JSON 里,所以当初绕过了列、改用 LIKE。
- 实测(生产库 485 行):**205ms / 25 行命中**;attempts 单条最大 **37,700 字节**、总计 245KB。代价随日志量线性恶化,量上来即秒级。
- `relay_logs` 上 `PRAGMA index_list` 为空;**没有 time 索引**,`ORDER BY time DESC` 也要全表排序。

## Requirements

- R1 **规范化 attempts**:新表 `relay_log_attempts`,一行一次尝试,字段:LogID、AttemptNum、ChannelID、ChannelName、ChannelKeyID、ChannelKeyRemark、ModelName、Status、Duration、Sticky、Msg;主键 (log_id, attempt_num);索引 `(channel_id, log_id)` 供渠道维度查询。加入 AutoMigrate。
- R2 **写入路径**:日志落盘时(RelayLogSaveDBTask flush)同步写入 attempts 行;与日志同一事务/批次,保证一致。
- R3 **清理联动**:`relayLogCleanup` 删日志时一并删其 attempts 行(同事务或按 log_id 批量删);保留期口径不变。
- R4 **查询重写**:`RelayLogAttemptsByChannel` 改为对 `relay_log_attempts` 按 `channel_id` + 时间窗(join 或冗余 time 列)走索引查询,分页/`total`/`truncated` 语义保持;**不再读 attempts JSON、不再 Go 层展开**。
- R5 **索引**:`relay_logs(time)` 索引(日志列表按时间倒序分页)。
- R6 **历史数据**:上线时对现存的日志做一次性回填(从 attempts JSON 展开写入新表),避免旧日志的调用详情查不到;回填幂等(重复执行不产生重复行)。
- R7 端点契约不变:`GET /api/v1/log/channel-attempts?channel_id=&page=&page_size=` 响应结构(list/total/truncated)与字段名不变,前端零改动。

## Non-goals

- 不改日志页 UI 与交互。
- 不做 attempts 的长期保留策略(随日志同生命周期)。
- 不引入外部搜索引擎/全文索引。

## Acceptance Criteria

- [ ] AC1 查询走索引:构造 ≥5000 行日志 + 大量 attempts,`channel-attempts` 响应时间与**日志总量无关**(仅与该渠道命中行数相关),对比改造前显著下降(在 5000 行量级下目标 <50ms)
- [ ] AC2 结果等价:同一数据下,新查询返回的 list 内容与旧实现一致(逐字段比对,含排序与分页)
- [ ] AC3 写入一致:新日志落盘后 attempts 行齐全(次数、字段与日志 attempts JSON 一致)
- [ ] AC4 清理联动:日志被清理时其 attempts 行同步消失,无孤儿行
- [ ] AC5 回填:上线前已存在的日志,调用详情依旧可查;回填重复执行不产生重复行
- [ ] AC6 API 契约与前端零回归;octopus-verify 通过(UI 人审:打开某渠道调用详情明显更快、数据正确)

## Notes

- 前置:等 `09-20-stats-permanent` 落地提交后再开工(两者都改 `internal/op/log.go`,避免并发改同文件)。
- 备选方案否掉:仅加索引(前缀通配 LIKE 用不上索引);SQLite 生成列(JSON 数组的"成员"无法用标量列表达)。
- 表体量:attempts 行数 = 总尝试次数,较日志行数放大 1~N 倍(失败多的请求 N 大),仍远小于原 JSON 表体积。
