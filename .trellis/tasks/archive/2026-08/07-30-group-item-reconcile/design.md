# design.md — group-item-reconcile(子任务 B)

> 父任务:`07-30-channel-group-model-sync`

## 目标

周期对账扫描全部 GroupItem,清理两类孤儿:(1)渠道已删除(`channel_id` 在 channel 表不存在);(2)模型已下架(`model_name` 不在该渠道的 `Model+CustomModel` 列表)。作为所有遗漏路径(尤其 `AutoSync=false` 手改模型)的统一兜底。与 A 不冲突(A 置 `Enabled=false` 不删渠道,对账不据此清理)。

## 数据源

- 全量 GroupItem:新增 `op.GroupItemListAll(ctx) ([]model.GroupItem, error)`(现有 `GroupItemList` 只按 groupID 查,无全量)。直接 `Find` 不带条件。
- 渠道信息:`op.ChannelList(ctx)`(返 `channelCache.GetAll()`),每渠道 model set = `xstrings.SplitTrimCompact(",", channel.Model, channel.CustomModel)` 入 `map[int]map[string]struct{}`。

## 对账逻辑(`internal/task/reconcile.go` 新建 `GroupItemReconcileTask`)

```
ctx 超时背景
items = op.GroupItemListAll(ctx)
channels = op.ChannelList(ctx)
chModelSet := map[channelID]→ set[modelName]   // 仅渠道存在且有该模型的记录
existChID := set[channelID]
for ch in channels: existChID[ch.ID]=struct{}{}; for m in split(Model,CustomModel): chModelSet[ch.ID][m]=struct{}{}

toDel := []
affectedGroupIDs := set
for item in items:
    if _, ok := existChID[item.ChannelID]; !ok {
        toDel = append(toDel, {ChannelID, ModelName}); affectedGroupIDs.add(item.GroupID)  // 渠道已删
        continue
    }
    if _, ok := chModelSet[item.ChannelID][item.ModelName]; !ok {
        toDel = append(toDel, {ChannelID, ModelName}); affectedGroupIDs.add(item.GroupID)  // 模型已下架
    }
if len(toDel)>0:
    op.GroupItemBatchDelByChannelAndModels(toDel, ctx)  // 内部已 groupRefreshCacheByIDs
    log.Infof("reconcile deleted %d orphan group items, affected groups=%v", len(toDel), affectedGroupIDs)
else:
    log.Debugf("reconcile: no orphan group items")
```

`GroupItemBatchDelByChannelAndModels`(`op/group.go:328`)已 Distinct groupID + 删 + `groupRefreshCacheByIDs`,冲突幂等。

注意:`GroupItemBatchDelByChannelAndModels` 按 `(channel_id, model_name)` 删——会删掉该 (channel,model) 在**所有 group** 中的条目,与 affectedGroupIDs 一致。单次对账把所有孤儿合并成一批调用即可,无需逐 group。

## setting + 注册

- `internal/model/setting.go`:新增 `SettingKeyGroupReconcileInterval SettingKey = "group_reconcile_interval"`(单位分钟),DefaultSettings 加默认 `"60"`,Validate 的整数 case 追加该键。
- `internal/task/init.go`:仿照其它任务读取 `op.SettingGetInt(SettingKeyGroupReconcileInterval)`,err 取默认 60,`Register("group_reconcile", interval, true, GroupItemReconcileTask)`。runOnStart=true(启动即跑一次清存量)。

## 边界

- 与 `SyncModelsTask` 的 AutoSync 删除:`SyncModelsTask` 删的是 AutoSync 渠道 diff 出的 deletedModels;对账删的是"模型不在渠道列表"的全部 GroupItem,是超集,重复无副作用(幂等)。
- A 的 `Enabled=false`/`AutoDisabled`:对账**不**据此判断,只看渠道行存在性与模型在列。A 禁用的渠道 GroupItem 保留(禁用≠删除,仅路由不选)。
- 渠道正常但模型名大小写:对账用原文比对(渠道侧 model 名与 GroupItem.ModelName 同源,均不归一化,与 `SyncModelsTask` 用 raw `newModels` 一致),不引入归一化以免误删。

## 兼容性

- 新增 setting 键/任务,无破坏。
- `GroupItemListAll` 新公开函数,不影响现有 `GroupItemList(groupID)`。

## 验证(Docker 统一在最后)

容器内造两类孤儿(删渠道残留/手改模型残留),缩周期或手动触发 `GroupItemReconcileTask` 一次(可临时把 setting 调小),验证孤儿清除 + 缓存刷新;连续两次第二次清理数为 0。
