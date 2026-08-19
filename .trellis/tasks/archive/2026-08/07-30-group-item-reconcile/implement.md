# implement.md — group-item-reconcile(子任务 B)

> 设计依据:`design.md`

## 执行清单

1. **`internal/model/setting.go`** — 加 `SettingKeyGroupReconcileInterval` 常量(`"group_reconcile_interval"`,注释:对账周期分钟);DefaultSettings 加 `{Key: SettingKeyGroupReconcileInterval, Value: "60"}`;Validate 整数 case 追加该键。
2. **`internal/op/group.go`** — 新增 `GroupItemListAll(ctx) ([]model.GroupItem, error)`,直接 `Find` 无条件。
3. **`internal/task/reconcile.go`**(新建)— `GroupItemReconcileTask`:取 GroupItemListAll + ChannelList,构建 channel 存在 set + (channelID→model set),遍历 items 找孤儿,合并成 `[]model.GroupIDAndLLMName` 调 `GroupItemBatchDelByChannelAndModels`;日志(有清理 Infof / 无清理 Debugf)。
4. **`internal/task/init.go`** — 仿现有任务读 `SettingKeyGroupReconcileInterval`(err 取 60),`Register("group_reconcile", interval分钟, true, GroupItemReconcileTask)`。
5. **import** — reconcile.go 要 import `context`/`log`/`op`/`model`/`xstrings`。

## 验证命令(Docker 单独编译)

```bash
mkdir -p static/out && echo '<!--ph-->' > static/out/.placeholder
docker run --rm -v "$PWD":/src -w /src golang:1.25 sh -c "go build -tags=jsoniter ./internal/... && echo OK"
rm -rf static/out
```

## Review Gate

- 对账只按"渠道存在"+"模型在渠道列表"判断,不据 `Enabled`/`AutoDisabled`(A 禁用的渠道 GroupItem 保留)。
- 用 `GroupItemBatchDelByChannelAndModels` 批删 + 内部刷缓存,幂等。
- setting 周期可调;runOnStart=true 启动清存量。

## 验证(Docker 统一在 A/C/B 全完成后)

完整构建起容器,造两类孤儿,缩对账周期或手动触发,验证清除 + 幂等。
