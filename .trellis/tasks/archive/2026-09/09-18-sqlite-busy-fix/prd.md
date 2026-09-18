# SQLITE_BUSY 根治:写事务 IMMEDIATE + 重试

## Goal

根治生产日志每 5 分钟刷屏的 `database is locked (SQLITE_BUSY/517)` 告警:兜底任务(正则重算+手动吸纳)与转发路径的持续写入(统计/日志落盘)抢写锁冲突。

## Background(2026-09-15~18 生产实证)

- 日志:`failed to sync members of regex group ...: database is locked (517)`,流量大时连败 5 轮(09-14 16:21-16:36),分组员更新滞后 15 分钟+。
- 根因:WAL 模式 + GORM 延迟事务(BEGIN 后先读后写)——读到旧快照后别的写者已提交,写时快照作废即 **517(SQLITE_BUSY_SNAPSHOT)**,立刻失败,**busy_timeout 等待对它无效**;普通 BUSY(5)同理存在。
- 冲突双方:5 分钟兜底(每组一个大事务写 group_items)vs 转发路径(每请求写渠道/模型/凭据统计+日志)。
- 影响:事务干净回滚无数据损坏,但分组成员同步延迟,日志刷屏。
- 驱动已核实:`glebarez/go-sqlite v1.23.0` 原生支持 DSN `_txlock=immediate`(sqlite.go:1566)。

## Requirements

- R1 全局写事务改 `BEGIN IMMEDIATE`:DSN 加 `_txlock=immediate`,开事务即拿写锁,517 快照冲突从根上消失;读路径不受影响(WAL 读不阻塞)。
- R2 兜底任务重试兜底:TaskGroupRegexSync 回调里 `GroupRegexSync` 与 `GroupManualAbsorb` 失败(BUSY 类错误)时退避重试(2-3 次,间隔递增),仅在最终失败才告警;普通错误(正则坏等)不重试。
- R3 `busy_timeout` 提到 10000ms(5s→10s),吸收偶发长写。
- R4 行为不变:成功路径的数据结果与现在完全一致;失败才重试。

## Non-goals

- 不改兜底任务的触发节奏与入口(仍 5 分钟 + 渠道事件)。
- 不引入进程级写互斥锁(IMMEDIATE + busy_timeout 已够)。
- 不动转发路径的写入逻辑。

## Acceptance Criteria

- [ ] AC1 单测模拟并发写(两个 goroutine 同时开事务写),IMMEDIATE 下无 517,普通 BUSY 由 busy_timeout 吸收
- [ ] AC2 兜底重试:注入一次 BUSY 错误 → 重试成功且只无告警;连续失败 → 最终告警一次
- [ ] AC3 既有 op/relay/handlers 测试零回归
- [ ] AC4 容器内全量测试 + 起容器观察 ≥2 个兜底周期(10 分钟)日志无 BUSY 告警

## Notes

- 轻量任务,PRD-only;改动面:internal/db/db.go(DSN)、internal/task/init.go(重试包装)、可能一个 errors.Is(BUSY) 判定小助手。
- IMMEDIATE 会把锁竞争前移到事务开启:拿不到锁的等(busy_timeout 兜),不会更快失败,吞吐无损(SQLite 本就单写者)。
