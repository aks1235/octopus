# 手动分组自动吸纳契约

> 任务 09-11-manual-group-auto-absorb 落地。与 [group-member-regex.md](./group-member-regex.md) 划界:正则分组成员由正则整体替换定稿;手动分组(member_regex 为空)按本契约「只增不减」吸纳。两份契约共享「成员 = channel_grant(渠道×模型×凭据)」的粒度。

## Scenario: 手动分组自动吸纳(append-only)

### 1. Scope / Trigger

- 手动分组(member_regex = '',含 manual 与 failover 两种 mode)的成员自动补齐行为。
- 触碰 `internal/op/group.go` 的吸纳逻辑、`internal/op/channel.go` 的挂调点、`internal/task/init.go` 的兜底任务,或前端 `web/src/components/modules/group/` 的匹配口径时,本契约生效。

### 2. Signatures

```go
// internal/op/group.go
GroupManualAbsorb(ctx context.Context, channelIDs ...int) error   // 批量:全部手动分组;channelIDs 非空时候选授权按渠道过滤
absorbManualGroupAllChannels(ctx context.Context, groupID int) error // 单组:全渠道候选,供分组创建/改名后即时吸纳
```

挂调点(勿增删,语义见 §7):

| 位置 | 传参 | 说明 |
|---|---|---|
| `ChannelCreate` / `ChannelUpdate`(`GroupRegexSync` 之后) | `channelID` | 渠道事件即时吸纳该渠道 |
| `GroupCreate` 手动分支(Create 提交后) | `groupID` | 创建即吸纳存量,响应带吸纳后成员 |
| `GroupUpdate`(member_regex 最终值为空**且改名**) | `groupID` | 改名即按新名吸纳,响应带吸纳后成员 |
| `TaskGroupRegexSync` 兜底(5min + 启动) | 无 | 全量幂等兜住漏算 |

`ChannelEnabled`/`ChannelDel` 不挂:不产生新授权。

### 3. Contracts

- **匹配口径(与前端同源,两处必须一起改)**:`strings.Contains(strings.ToLower(模型名), strings.ToLower(strings.TrimSpace(分组名)))`。前端对应 `web/src/components/modules/group/utils.ts` 的 `matchesGroupName`/`normalizeKey`(模型名只 lower 不 trim——入库前 `normalizeChannelDetail` 已统一 TrimSpace)。
- **吸纳粒度**:命中模型的全部授权(同渠道同模型多凭据一并纳入),不看渠道/凭据启停(可用性由 `groupSnapshot` 的 Available 表达)。
- **append-only**:只插缺失的 (group_id, channel_grant_id);既有成员的行主键、优先级、`active_item_id` 一律不动;新成员按 (channelID, modelName, keyName) 稳定排序,`priority` 从该组现有最大值 +1 连续续排(空组从 1 起)。
- **响应形状**:GroupCreate/GroupUpdate 触发吸纳后 Preload 重载 Items 再返回——前端拿到的成员即吸纳后终态,无需二次刷新获取。

### 4. Validation & Error Matrix

| 条件 | 行为 |
|---|---|
| 分组名为空(trim 后) | 跳过该组(防御,UI 不允许) |
| 分组 member_regex 非空 | 绝不走手动吸纳(单组入口 DB 重读校验,双重防御) |
| 候选的 model/key 缓存缺失 | 跳过该候选(与 `syncRegexGroupItems` 同款容错) |
| 批量路径单组失败 | `log.Warnf` 不中断其余组 |
| GroupCreate/GroupUpdate 吸纳失败 | 返回错误(与正则分支同款;主事务已提交,响应报错但分组已存在) |
| 并发重复插入 | `group_items` 唯一索引 `idx_group_grant` 拒绝,单组事务回滚+告警,幂等有 DB 硬保护 |

### 5. Good/Base/Bad Cases

- Good:分组 `deepseek` 存在 → 新建渠道带模型 `my-deepseek-test` → 保存即吸纳,无需任何人工操作。
- Base:先建渠道后建分组 `pro` → 创建响应直接带全部 `deepseek-v4-pro` 授权(创建即吸纳)。
- Bad(预期内的"怪"):手动删掉匹配成员,下次渠道变更/兜底轮**会吸回来**——无移除记忆,v1 同款语义;规避 = 改分组名或改用正则分组。短分组名(如 `gpt`)吸纳面极广,用户自担。

### 6. Tests Required

`internal/op/manual_group_absorb_test.go`(11 用例),关键断言点:同一行主键+优先级不变(锁定 append-only)、整行集合比对(锁定幂等)、正则分组响应与 DB 双零(锁定路径隔离)、未改名删成员保存后仅剩删后集合(锁定不回吸)、`TestGroupUpdate_regexToManualWithRenameAbsorbsByNewName` 锁定正则→手动转换 × 改名边界。改吸纳逻辑必须全量过这批用例。

### 7. Wrong vs Correct

#### Wrong

```go
// 用 syncGroupItems(全替换)实现吸纳:既有成员优先级被重写,删除的成员永不回归,
// 且失败面扩大到整组成员 —— 违背 append-only 契约
syncGroupItems(tx, groupID, append(existingInputs, newInputs...))
// GroupUpdate 里无条件吸纳:用户刚删的成员一保存就回吸,删除操作永远无法生效
GroupManualAbsorb(ctx) // 在每次 GroupUpdate 后调用
```

#### Correct

```go
// 只插缺失授权,新成员尾部续排;触发条件精确到「渠道变更 / 分组创建 / 分组改名」
if memberRegex == "" && oldName != group.Name {
    if err := absorbManualGroupAllChannels(ctx, group.ID); err != nil { return nil, err }
}
```
