# design.md — sync-fail-disable(子任务 A)

> 父任务:`07-30-channel-group-model-sync`

## 目标

`SyncModelsTask` 失败可观测、可收敛:落失败状态字段,连续失败达阈值自动禁用渠道,恢复可达自动解禁。

## 数据模型变更

`internal/model/channel.go` `Channel` 结构体新增三列(GORM AutoMigrate 增量加列,非破坏):

```go
SyncFailCount int        `json:"sync_fail_count" gorm:"default:0"`
LastSyncError string     `json:"last_sync_error" gorm:"type:text"`
LastSyncAt    *time.Time `json:"last_sync_at"`
```

`*time.Time` 用指针避免 0 值歧义。AutoMigrate 由现有 `internal/db/db.go` 注册流程自动处理(确认 `&model.Channel{}` 已在 AutoMigrate 列表,该表早已存在,只会 ADD COLUMN)。

## setting 键

`internal/model` 的 setting 常量中新增 `SettingKeySyncFailThreshold`(int,默认 3)。沿用现有 `op.SettingGetInt` 读取机制。看现有 setting 常量定义位置补一行即可。

## 新增专用更新函数

不在 `ChannelUpdate`(`internal/op/channel.go:226`)的 `selectFields` 白名单里混入同步状态字段——该函数面向用户编辑,语义不应混。新增轻量函数:

```go
// internal/op/channel.go
func ChannelUpdateSyncStatus(ctx context.Context, id int, syncFailCount int, lastSyncError string, lastSyncAt *time.Time, enabled *bool) error
```

- 单条 `UPDATE ... SET sync_fail_count=?, last_sync_error=?, last_sync_at=?, enabled=?`(仅当 `enabled != nil`)。
- 落库后刷新 channel 缓存(复用现有 channelRefreshCache 之类机制,看现有 ChannelUpdate 末尾怎么刷缓存的对齐写法)。
- `enabled` 用 `*bool` 可选:仅失败到阈值时传 `false`,成功解禁时传 `true`,未触阈值的失败更新传 `nil`(不动 enabled)。

## SyncModelsTask 改造(`internal/task/sync.go`)

每个渠道分支(行 34-83)改造为:

```
fetchModels, err := helper.FetchModels(ctx, channel)
now := time.Now()
if err != nil {
    failCount := channel.SyncFailCount + 1
    threshold := op.SettingGetInt(SettingKeySyncFailThreshold) // 默认3, err 时取默认
    var enabledPtr *bool
    if failCount >= threshold {
        enabledPtr = boolPtr(false)  // 触阈值禁用
    }
    lastSyncError := truncate(err.Error(), 500)
    op.ChannelUpdateSyncStatus(ctx, channel.ID, failCount, lastSyncError, &now, enabledPtr)
    log.Warnf("sync fail channel=%s id=%d count=%d/%d err=%v", channel.Name, channel.ID, failCount, threshold, err)
    continue
}
// 成功分支
// 原有 diff + ChannelUpdate{Model} 逻辑保留
// 在 ChannelUpdate 成功后(或同一次更新里)调:
var enabledPtr *bool
if channel.SyncFailCount > 0 && !channel.Enabled {  // 之前因失败被禁用
    enabledPtr = boolPtr(true)  // 自动解禁
}
op.ChannelUpdateSyncStatus(ctx, channel.ID, 0, "", &now, enabledPtr)
// ... 原 deletedModels 清理 + ChannelAutoGroup 保留
```

关键点:
- 成功解禁判定:`channel.SyncFailCount > 0` 且 `!channel.Enabled`(防误把运维手动禁用的渠道翻活——仅当渠道确实是被自动禁用的才解禁)。**注意**:此处无法区分"运维手动禁用"与"同步自动禁用"。若要严格区分,需额外字段 `AutoDisabled bool`。**设计决策:加 `AutoDisabled bool` 标记**,自动禁用时置 true,运维手动禁用/启用走 `ChannelUpdate` 时该字段保持/置 false。解禁条件改为 `channel.AutoDisabled && !channel.Enabled`。

→ **数据模型追加一列**:
```go
AutoDisabled bool `json:"auto_disabled" gorm:"default:false"`
```
- `ChannelUpdateSyncStatus` 触阈值禁用时同时置 `AutoDisabled=true`;解禁时置 `false`。
- 运维手动 `ChannelUpdate{Enabled}` 时需在 `ChannelUpdate` 里同步把 `AutoDisabled=false`(运维接管即清除自动标记)——在 `ChannelUpdate` 的 `req.Enabled != nil` 分支追加 `auto_disabled` 到 selectFields 并赋 `false`。

## 缓存刷新

`op.ChannelList` 走内存缓存(`channelCache`)。`ChannelUpdateSyncStatus` 落库后必须刷新该渠道缓存,否则下一次同步读到旧 `SyncFailCount`/`Enabled`。对齐 `ChannelUpdate` 末尾的缓存刷新写法。

## 边界

- `FetchModels` 超时/网络抖动一次未必真失效,但按需求"连续失败次数"累计,单次抖动不立即禁用。
- `AutoSync=false` 渠道不进 SyncModelsTask(行 35 `continue`),不受影响——其失败状态永远不更新,符合预期(它本就不自动同步)。
- 阈值 setting 读取出错时取默认 3(不阻断同步主流程)。

## 兼容性

- 新增列均有默认值,旧数据无影响。
- `ChannelUpdate` 增量加 `auto_disabled` 一处,语义不变(运维改 enabled 时清自动标记)。
- API 响应因结构体新增字段会多回 `sync_fail_count`/`last_sync_error`/`last_sync_at`/`auto_disabled`,前端忽略不影响。

## 验证(Docker)

`implement.md` 中详述。核心:容器内造一个无效 BaseUrl 渠道,手动触发同步(改 setting 周期到 1 分钟或调手动 sync 接口 `handlers/channel.go:217-220`),观察三次失败后 `enabled=false`/`auto_disabled=true`;改回有效 BaseUrl 再触发,观察 `enabled=true`/`auto_disabled=false`/`sync_fail_count=0`。
