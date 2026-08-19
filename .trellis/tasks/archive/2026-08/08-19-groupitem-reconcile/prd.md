# PRD — ② GroupItem 孤儿对账定时任务

> 父任务: 08-19-octopus-route-a-migration
> 基底: feat/my-features(8d04257 + ① 已完成)
> 来源: dev commit 6f8e156

## Goal

在上游 master 基底(含 ①)上重新实现「GroupItem 孤儿对账定时任务」。
作为 ChannelAutoGroup 只增不减路径的统一兜底:扫描全量 GroupItem,
清理两类孤儿引用——(1)渠道已删除、(2)模型已下架(不在该渠道 Model+CustomModel 列表)。

## 来源实现摘要(dev 6f8e156)

- 新增 `internal/task/reconcile.go` GroupItemReconcileTask
- `task/init.go` 注册 TaskGroupReconcile(SettingKeyGroupReconcileInterval 默认 60 分钟,runOnStart=true)
- 新增 `op.GroupItemListAll` 全量查询
- 用 `op.GroupItemBatchDelByChannelAndModels` 批删(上游已有,内部刷缓存)
- 判定只按「渠道行存在 + 模型在渠道列表」,不据 Enabled/AutoDisabled(禁用≠删除)
- .gitignore 加 .claude/ .trellis/ web/.pnpm-store/(本项与功能无关,迁移单独处理)

## Requirements

### 功能需求

- 新增 `internal/task/reconcile.go`:
  - `GroupItemReconcileTask()`:10 分钟超时 ctx,调 `op.GroupItemListAll` 拿全量 items
  - 调 `op.ChannelList` 拿全量渠道,建 existChID 集合 + chModelSet(渠道→模型名集合,Model+CustomModel split)
  - 遍历 items 收集孤儿:渠道不存在 / 模型不在该渠道模型集合 → 加入 toDel
  - 调 `op.GroupItemBatchDelByChannelAndModels(toDel)` 批删(幂等)
  - 无孤儿时 debug 日志,有删时记 affected group 数
- 新增 `op.GroupItemListAll(ctx)`:全量查 GroupItem(无 group_id 过滤,参考 GroupItemList 写法)
- `task/init.go` 注册 TaskGroupReconcile:
  - 常量 `TaskGroupReconcile = "group_reconcile"`
  - 读 `SettingKeyGroupReconcileInterval`(默认 60),`Register(TaskGroupReconcile, interval, true, GroupItemReconcileTask)`(runOnStart=true 启动清存量)
- `model/setting.go` 加 `SettingKeyGroupReconcileInterval`(默认 "60")+ DefaultSettings + Validate 整数

### 技术约束(上游差异点)

- **log import**: dev 用 `internal/utils/log`,上游无此包 → 改用 `github.com/charmbracelet/log`(与上游 task/sync.go 一致)
- **Register 签名**: 上游 `Register(name string, interval, runOnStart bool, fn func())` — 与 dev 一致,直接用
- **GroupItemBatchDelByChannelAndModels**: 上游已有,签名 `(keys []GroupIDAndLLMName, ctx) error`,内部已调 `groupRefreshCacheByIDs` 刷缓存 → reconcile.go 注释「内部已刷新」成立
- **GroupIDAndLLMName**: 上游 `model/group.go:50` 已定义,字段 ChannelID + ModelName 一致
- **SplitTrimCompact**: 上游签名 `SplitTrimCompact(sep string, parts ...string)`,多参数版 `SplitTrimCompact(",", ch.Model, ch.CustomModel)` 直接可用
- **ChannelList**: 上游返回 `[]model.Channel`(从缓存),含 ch.Model 和 ch.CustomModel 字段,够用
- **迁移 ① 已加的 setting**: ② 在同一 setting.go 继续加 GroupReconcileInterval,不误删 ① 的 SyncFailThreshold
- .gitignore 的 .claude/.trellis/ 忽略项:dev 已有,fet/my-features(上游)没有。**本任务不碰 .gitignore**(迁移基底是上游,这些本地工具产物本就不该入库上游,留到集成阶段统一决定是否加)

## Acceptance Criteria

- [ ] `go build -tags=jsoniter ./internal/...` 通过(BACKEND_BUILD_OK)
- [ ] `go test -tags=jsoniter ./internal/... ./cmd/...` 无 FAIL
- [ ] `internal/task/reconcile.go` 新增,含 GroupItemReconcileTask,逻辑与 dev 一致但用 charmbracelet/log
- [ ] `internal/op/group.go` 含 GroupItemListAll(ctx) 全量查询函数
- [ ] `internal/task/init.go` 含 TaskGroupReconcile 常量 + Register 注册(runOnStart=true)
- [ ] `internal/model/setting.go` 含 SettingKeyGroupReconcileInterval(默认 60)+ Validate
- [ ] setting.go 不误删 ① 的 SettingKeySyncFailThreshold
- [ ] 空库启动容器,确认任务注册日志不报错(任务调度初始化正常)
- [ ] smoke test:构造孤儿 GroupItem(渠道已删/模型下架),跑对账任务,确认被清理

## 范围外

- .gitignore 忽略项调整(集成阶段统一处理)
- 前端展示对账结果(纯后端任务)
