# PRD — ① 渠道同步失败标记 + 自动禁用/解禁

> 父任务: 08-19-octopus-route-a-migration
> 基底: feat/my-features(从 upstream/master 8d04257 切出)
> 来源: dev commit 55ffcbf

## Goal

在上游 master 基底上重新实现「渠道同步失败标记 + 连续失败自动禁用 + 自动解禁」。
SyncModelsTask 失败时不再静默 warn+continue,而是累计失败计数、达阈值禁用渠道,
成功时归零并自动解禁曾被自动禁用的渠道,运维启停一律清自动标记。

## 来源实现摘要(dev 55ffcbf)

- `model/channel.go`: Channel 加 4 列 SyncFailCount/LastSyncError/LastSyncAt/AutoDisabled
- `model/setting.go`: 加 SettingKeySyncFailThreshold(默认3) + Validate 校验为整数
- `op/channel.go`: 新增 ChannelUpdateSyncStatus 轻量更新;ChannelUpdate/ChannelEnabled 改为清 auto_disabled
- `task/sync.go`: 失败累计 + 达阈值禁用;成功归零 + 自动解禁

## Requirements

### 功能需求

- Channel 新增 4 字段(非运维编辑,由 SyncModelsTask 维护):
  - `SyncFailCount int`(连续同步失败次数,成功归零)
  - `LastSyncError string`(最近一次失败错误信息,截断 ≤500 字符)
  - `LastSyncAt *time.Time`(最近同步尝试时间)
  - `AutoDisabled bool`(是否因连续同步失败被自动禁用)
- 新增配置 `SettingKeySyncFailThreshold`,默认 3
- SyncModelsTask 失败分支:
  - 读取阈值(读失败取默认 3,不阻断主流程)
  - failCount = 当前 SyncFailCount + 1
  - failCount ≥ 阈值 → enabled=false + auto_disabled=true
  - 调用同步状态更新函数落库 + 刷缓存
  - 日志带 count/threshold/disabled
- SyncModelsTask 成功分支:
  - 如 AutoDisabled && !Enabled → 自动解禁(enabled=true + auto_disabled=false)
  - 失败计数归零
- 运维接管启停清自动标记:
  - ChannelUpdate 设 Enabled 时同时置 auto_disabled=false
  - ChannelEnabled(id, enabled) 同时置 auto_disabled=false
- 新增 op 层轻量更新函数(仅写同步状态字段,不混入 ChannelUpdate 的用户编辑语义)

### 技术约束(上游差异点)

- **基底已是上游架构**,迁移时参照 dev 原逻辑,但适配上游现状:
  - 上游 `model/channel.go` 是单 BaseURL+单 Key 结构(无 BaseUrls 字段),新增的 4 字段加在 Channel 末尾即可
  - 上游 `model/setting.go` 的 `const SettingKey` 块和 `Validate()` 都要加 SyncFailThreshold;上游 DefaultSettings 没有熔断项(熔断器是 ⑦ 才加),本任务只加 SyncFailThreshold,不加 GroupReconcileInterval(那是 ②)
  - 上游 `op/channel.go` 的 ChannelUpdate/ChannelEnabled 签名与 dev 一致,channelRefreshCacheByID 上游也有 → 直接复用
  - 上游 `task/sync.go` 与 dev 同构(for channels → FetchModels → 失败 continue / 成功更新),插入点位置一致
- 4 字段通过 GORM AutoMigrate 增量加列,向后兼容(上游 migrate 序列到 006,本任务不改 migrate 文件,靠 AutoMigrate 兜底加列)
- 不引入熔断器相关 setting(那是 ⑦),不引入对账相关 setting(那是 ②)

## Acceptance Criteria

- [ ] `go build ./...` 通过
- [ ] `go test ./...` 通过
- [ ] model/channel.go 含 4 个新字段,gorm tag 正确(default/type)
- [ ] model/setting.go 含 SettingKeySyncFailThreshold,DefaultSettings 默认 "3",Validate 校验整数
- [ ] op/channel.go 含 ChannelUpdateSyncStatus 函数,签名与 dev 一致
- [ ] op/channel.go ChannelUpdate 设 Enabled 时同步清 auto_disabled
- [ ] op/channel.go ChannelEnabled 同时更新 enabled + auto_disabled=false 并刷缓存
- [ ] task/sync.go 失败分支累计计数 + 达阈值禁用 + 日志带 count/threshold
- [ ] task/sync.go 成功分支归零 + 自动解禁(AutoDisabled&&!Enabled 才解)
- [ ] 启动服务,AutoMigrate 自动加 4 列不报错
- [ ] smoke test:制造一个会失败的渠道,跑 SyncModelsTask,观察 count 累加→达阈值禁用;修正后重跑,观察归零+解禁

## 范围外

- 前端展示同步失败状态(本任务纯后端,前端字段展示留待后续或用户单独提)
- 熔断器(⑦)、GroupItem 对账(②)
