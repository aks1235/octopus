# ADR-0005: 数据迁移走转换脚本,停机一次切

- 状态: 已接受(2026-09-08)
- 关联: ADR-0002(日志持久化包)

## 背景

fork 库与上游迁移链完全分叉:上游迁移链(003-012)假设上游自己的历史中间形状(如 `channel_models` v8 表),fork 库没有;fork 库有 `migration_records`、`usage_cards`、`o_auth_sessions`、`sync_fail_count` 等 fork 专属结构。**原地升级(旧库直接跑新版)不可行**。

生产库体量:113 渠道、1284 条 relay_logs、8 张 stats 表。已有 `data/backup-v1.0.3/` 备份先例。

上游 `migrate/009.go` 会 `DROP TABLE relay_logs`——日志持久化包必须在设计上绕开/拦截此迁移。

## 决策

- 写转换脚本:读 fork 库 → 按 v2 新模型写出全新库(DB→DB,思路同 A2 脚本但直接操作库)
- 必带:渠道(取唯一 URL)、channel_keys、分组+group_items、API Key、用户、设置
- 随功能包带:`relay_logs`、`usage_cards`、`o_auth_sessions`(功能移植时保持表形状兼容)
- 不带:8 张 stats 历史表(重新攒)
- 切换方式:停机窗口一次切(停 v1.0.4 → 备份 → 转换 → 起 v2 验证),不做双库并行
- 回滚:镜像钉 v1.0.4 + 切换前数据备份,天然可回

## 后果

- 演练必须拿 `data/` 副本反复跑,正式切换窗口以演练时长为基准
- 统计累计清零,渠道配额/用量视图从头积累
- Usage Card 配额数据如依赖历史统计,需在功能包移植时确认其数据来源
