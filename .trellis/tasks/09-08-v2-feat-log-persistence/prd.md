# 移植:日志持久化包

## Goal

TBD.(任务启动时完整规划)

## 范围增补(2026-09-09,经用户确认)

原属任务3(09-08-v2-feat-ops-obs)的**渠道调用详情页**(按渠道聚合 attempts 明细,fork 对照 `f551b95`)挪入本任务:它查询 relay_logs 持久化数据,与本任务的持久化底座同源,避免查询层做两遍。规划时作为本任务交付物之一纳入需求与验收。

前置事实(已核实):上游 v0.13.2 日志纯内存 SSE(internal/relay/state.go),migrate/009 会 DROP relay_logs;转换脚本已将 fork 库 1284 条 relay_logs 带入 v2 库——**本任务须先拦截 migrate/009 删表**(父任务地图既有要求),否则迁移数据上线即被清。

## Requirements

- TBD

## Acceptance Criteria

- [ ] TBD

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only; complex tasks need `design.md` + `implement.md` before `task.py start`.
