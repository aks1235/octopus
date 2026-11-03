# 备份导入契约:按天然键重映射,冲突策略由调用方选择

## What

- 导出:`DBExportAll`(`internal/op/backup.go`)把整库读成 `model.DBDump`,版本号 `dbDumpVersion`。
- 导入:`DBImportIncremental(ctx, dump, policy)`(`internal/op/backup_import.go`),
  入口 `POST /api/v1/setting/import`,策略入参 `conflict=skip|overwrite|rename`(缺省 `skip`)。

**身份判定一律用天然键,不用主键**:

| 表 | 身份 |
|---|---|
| `channels` | `name` |
| `channel_keys` | (`channel_id`, `name`) |
| `channel_models` | (`channel_id`, `name`) |
| `channel_grants` | (`channel_model_id`, `channel_key_id`) |
| `groups` | `name` |
| `group_items` | (`group_id`, `channel_grant_id`) |
| `group_channel_orders` | (`group_id`, `channel_id`) |
| `llm_infos` | `name` |
| `api_keys` | `api_key`(表上无唯一名称,同名不同值是两个凭据) |
| `settings` | `key` |

两类主键在导入时**必须重映射**:`channel_keys`/`channel_models`/`channel_grants` 引用的父主键,
`group_items`/`group_channel_orders` 引用的分组与渠道主键,`group_items` 自身主键(`active_item_id` 指向它),
以及 `stats_api_key.api_key_id`。子表一律用映射后的新主键写入。

## Why

两个实例的主键空间各自独立:实例 A 的 `channel id=1` 是"腾讯云",实例 B 的 `channel id=1` 可能是"WONG"。
按主键合并的三种后果(2026-11-03 实测复现,见 `internal/op/backup_import_test.go`):

1. **主键相同、名称不同** → 冲突被静默吞掉(`OnConflict` 未指定列时不报错),
   但 `channel_keys`/`channel_models`/`channel_grants` 是 `ON CONFLICT (id) DO UPDATE`,
   **目标实例的凭据与模型被备份行整行覆盖** —— 配置串到不相关渠道上,而界面无任何提示。
2. **渠道同名、主键不同** → 渠道因 `unique(name)` 被跳过,子行照旧插入,
   外键指向不存在的渠道 → `FOREIGN KEY constraint failed`,整笔回滚。
3. **同渠道同名凭据、凭据主键不同** → `ON CONFLICT (id) DO UPDATE` 不处理 `(channel_id, name)`
   唯一键冲突 → `UNIQUE constraint failed` → 整笔回滚。这是用户看到的"从一个导不到另一个"。

## 冲突策略语义

| 策略 | 同名渠道/分组 | 同名凭据/模型 | 授权 | 模型价格/设置 | API Key |
|---|---|---|---|---|---|
| `skip`(默认) | 复用目标对象,配置与统计均不改 | 跳过,保留目标 | 跳过 | 跳过 | 跳过 |
| `overwrite` | 用备份内容覆盖(含统计) | 覆盖 key/enabled/统计 | 覆盖 `protocols` | 覆盖 | 覆盖名称/开关/额度 |
| `rename` | 另建 `<name>-2` 等 | **跟随新渠道**,即整条分支复制 | 新建 | 跳过(无独立名称) | 跳过(重复 key 值语义模糊) |

边界:

- `rename` 是**按顶层对象级联**的:渠道被改名另建后,其凭据/模型/授权都挂到新渠道下,原渠道完全不受影响。
  因此凭据级的 rename 分支实际不可达,保留为防御。
- `skip` 下若渠道被复用,备份里**目标没有的**凭据/模型仍会新建 —— 它们是"当前不存在的对象"。
- 统计表(`stats_total`/`stats_daily`/`stats_hourly`/`stats_api_key`)是聚合账,始终按既有自然键覆盖写入,
  不受冲突策略影响;`stats_api_key` 必须用映射后的 `api_key_id`。
- `active_item_id` 指向分组成员主键,导入时先置 0,成员写入完成后回填映射值。
- 渠道健康状态列(`health_fail_count`/`last_health_error`/`last_health_at`/`auto_disabled`)不随备份迁移,
  由目标实例自行探测。

## 测试锚点

`internal/op/backup_import_test.go`:

- `TestImportRemapsByNaturalKeyKeepsTargetObjects` —— 主键相同名称不同时,目标渠道的凭据/模型不被改动,
  备份渠道另建一行且凭据/模型/授权/分组成员全部指向新主键(覆盖故第 1 类事故)。
- `TestImportSameNameDifferentIDDoesNotFail` —— 渠道同名不同主键不再触发外键错误(第 2 类)。
- `TestImportKeyNameConflictRespectsPolicy` —— 同渠道同名凭据在 skip/overwrite 下的行为差异(第 3 类)。
- `TestImportRenameKeepsBothChannels` / `TestImportRenameDoesNotTouchOriginalKeys` —— rename 的级联与隔离。
- `TestImportRemapsActiveItem` —— `active_item_id` 重映射。
- `TestImportIntoEmptyInstance` —— 空实例全量导入与旧行为等价。

改动导入口径时,若把任一表的身份退回主键,上述用例应出现失败;新增表须同时补映射与用例。
