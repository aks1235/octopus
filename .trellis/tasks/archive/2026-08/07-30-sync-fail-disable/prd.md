# 同步失败标记 + 连续失败自动禁用渠道(子任务 A)

> 父任务:`07-30-channel-group-model-sync`

## 背景

`internal/task/sync.go:38-42` 在 `FetchModels` 返回 err 时只 `log.Warnf` 后 `continue`:不动 `Channel.Model`、不写任何失败标记、不改 `Enabled`。`model.Channel`(`internal/model/channel.go:19-35`)也没有失败状态字段。结果:上游失效后渠道仍以 `Enabled=true` 被路由选中,运维只能扒日志才知道。

## Goal

让同步失败可观测、可收敛:渠道结构新增失败状态字段,每次同步失败递增计数并落最近错误信息;连续失败次数达到可配阈值时,自动置 `Enabled=false`,使 balancer 不再选中它;同步成功时清零计数。

## Requirements

- `model.Channel` 新增字段(经 GORM AutoMigrate 增量加列,非破坏):
  - `SyncFailCount int` —— 连续同步失败次数(成功时归零)
  - `LastSyncError string` —— 最近一次失败的简短错误信息(成功时清空)
  - `LastSyncAt *time.Time` —— 最近一次同步尝试时间(无论成败)
- `SyncModelsTask`(`internal/task/sync.go`)每个渠道分支改造:
  - `FetchModels` 返回 err → 调 `ChannelUpdate`(或新增的轻量更新函数)写 `SyncFailCount+1`、`LastSyncError=err.String()`(截断到合理长度)、`LastSyncAt=now`,**不再静默 continue 后什么都不做**;仍不更新 `Channel.Model`。
  - 当 `SyncFailCount` 达到阈值 → 额外置 `Enabled=false`(通过 `ChannelUpdate` 的 `Select` 指定字段更新,事务内完成)。
  - `FetchModels` 成功 → 在更新 `Channel.Model` 的同一次 `ChannelUpdate` 里把 `SyncFailCount=0`、`LastSyncError=""`、`LastSyncAt=now` 一并写入;**同时若渠道此前因连续失败被自动禁用(`Enabled=false`),此处自动置回 `Enabled=true`**(恢复可达即自动解禁,无需运维介入)。
- 阈值配置:`SettingKeySyncFailThreshold`(int,默认 3)走现有 setting 机制;`internal/task/init.go` 或 `SyncModelsTask` 内读取。
- 前端不加新 UI(展示态由子任务 C 统一处理),但渠道列表/详情接口因字段新增会自动多回这三个字段——需确认 `ChannelUpdate` 的 `selectFields` 白名单包含新字段,否则更新写不进去。
- `ChannelUpdate`(`internal/op/channel.go:226-300`)当前用 `Select(selectFields).Updates(...)`,需把新字段加入白名单,或新增一个专用 `ChannelUpdateSyncStatus` 更新函数避免污染 `selectFields` 语义。**推荐后者**(语义更清晰,且 `ChannelUpdate` 是面向用户编辑的接口,不该混入同步状态)。

## Acceptance Criteria

- [ ] 渠道表新增 `sync_fail_count`/`last_sync_error`/`last_sync_at` 三列,AutoMigrate 后容器内 DB 存在。
- [ ] Docker 构建运行后,手动让某渠道上游不可达(改 BaseUrl 到无效地址),触发同步任务:第 1、2 次失败后渠道仍 `Enabled=true` 但 `SyncFailCount` 递增、`LastSyncError` 非空;第 3 次(默认阈值)失败后渠道 `Enabled=false`。
- [ ] 同一渠道恢复可达并触发同步后,`SyncFailCount` 归零、`LastSyncError` 为空,且若该渠道此前因连续失败被自动禁用(`Enabled=false`),则同步成功时自动置回 `Enabled=true`(自动解禁)。
- [ ] 阈值通过 setting 可调,改阈值后新同步周期生效。
- [ ] 日志中同步失败由 `log.Warnf` 升级为含 `channel_id`/`SyncFailCount`/阈值的结构化信息,便于定位。
- [ ] Docker 构建(`docker compose build`)通过,容器内 `octopus start` 正常启动、AutoMigrate 不报错。

## Notes

- 字段约定对子任务 C 可见:C 用 `Enabled=false` 判定"渠道已禁用",用 `channel_id` 在 channel 表中查不到判定"渠道已删除"。本任务不动 `ChannelDel`。
- 验证一律 Docker 构建,不在本机 `go run`。
