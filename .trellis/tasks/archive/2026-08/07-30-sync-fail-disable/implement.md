# implement.md — sync-fail-disable(子任务 A)

> 设计依据:`design.md`

## 执行清单

1. **`internal/model/channel.go`** — `Channel` 加四列:
   - `SyncFailCount int` / `LastSyncError string` / `LastSyncAt *time.Time` / `AutoDisabled bool`
2. **`internal/model` setting 常量** — 加 `SettingKeySyncFailThreshold`(=`"sync_fail_threshold"`),默认值看现有常量风格(若 setting 有默认表,补默认 3)。
3. **`internal/op/channel.go`**:
   - 新增 `ChannelUpdateSyncStatus(ctx, id, syncFailCount, lastSyncError, lastSyncAt, enabled *bool, autoDisabled *bool) error`(落库 + 刷缓存)。
   - `ChannelUpdate` 的 `req.Enabled != nil` 分支(行 246-249)追加:`auto_disabled` 进 selectFields,赋 `false`(运维接管清自动标记)。
4. **`internal/task/sync.go`** — 改造每渠道分支(行 38-83):
   - 失败:累计 `SyncFailCount`,达阈值置 `enabled=false`+`auto_disabled=true`,调 `ChannelUpdateSyncStatus`;升级 `log.Warnf` 含 id/count/threshold。
   - 成功:原 diff/`ChannelUpdate{Model}`/`deletedModels` 清理/`ChannelAutoGroup` 全保留;新增成功后调 `ChannelUpdateSyncStatus(count=0, error="", at=now)`,若 `channel.AutoDisabled && !channel.Enabled` 则附 `enabled=true`+`auto_disabled=false` 自动解禁。
5. **缓存刷新对齐** — 确认 `ChannelUpdateSyncStatus` 落库后调用的缓存刷新函数与 `ChannelUpdate` 一致(读 `ChannelUpdate` 末尾写法复用)。

## 验证命令(Docker)

```bash
# 构建(前后端整体)
docker compose build

# 起容器
docker compose up -d

# 容器内查表结构(确认四列已加)
docker compose exec octopus sh -c '<查 sqlite/mysql 的 channels 表结构命令,按 data/config.json 的 DB 类型>'

# 造一个无效 BaseUrl 渠道(经 API 或直接改库),AutoSync=true
# 缩短同步周期:改 setting SettingKeySyncLLMInterval 到最小可调值,或用手动同步接口触发

# 观察日志:三次失败后 enabled=false / auto_disabled=true
docker compose logs -f octopus | grep 'sync fail'

# 改回有效 BaseUrl,手动触发同步 → enabled=true / auto_disabled=false / sync_fail_count=0
```

> 具体 DB 查询语句在执行时按 `data/config.json` 实际 DB 类型(SQLite/MySQL/PG)补。

## Review Gate

- 同步成功解禁只对 `AutoDisabled && !Enabled` 的渠道生效,不误翻活运维手动禁用的渠道。
- `ChannelUpdate` 加 `auto_disabled=false` 不破坏现有运维启用/禁用语义。
- Docker 构建通过、AutoMigrate 不报错。
- 失败/成功两条路径缓存均刷新(下一周期读到新状态)。

## 回滚点

- 四列新增是增量,回滚只需 revert 代码,列残留无害(可后续手动 drop 或保留)。
- `ChannelUpdateSyncStatus` 是新函数,删掉即回滚同步行为到原静默 warn。
