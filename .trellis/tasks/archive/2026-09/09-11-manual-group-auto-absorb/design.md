# 技术设计:手动分组自动吸纳

## 现状梳理(结论均已在代码核实)

- 分组成员表 `group_items`,按 `channel_grant_id` 引用授权(渠道×模型×凭据);成员优先级 `priority`,分组的 `active_item_id` 指向人工选定成员。
- `internal/op/group.go`:
  - `syncGroupItems(tx, groupID, requested)` — **全替换**语义:按提交集合增/删/重排,不可直接复用(吸纳要只增不减)。
  - `syncRegexGroupItems(tx, groupID, regex)` — 正则分组整体替换:遍历 `channelGrantCache`,命中模型的授权按 (channel, model, key) 稳定排序后交给 `syncGroupItems`。
  - `GroupRegexSync(ctx)` — 从 DB 查 `member_regex <> ''` 的分组逐组重算,单组失败仅告警;末尾 `groupRefreshCache`。
- `internal/op/channel.go` 4 处调用 `GroupRegexSync`:`ChannelCreate`(117)、`ChannelUpdate`(196)、`ChannelEnabled`(302)、`ChannelDel`(467)。授权的全部增改都在 `ChannelCreate`/`ChannelUpdate` 内经 `syncChannelGrants`(803)完成;两处调用点执行时渠道三类缓存(grant/model/key)已刷新(`syncRegexGroupItems` 依赖此前提,已成立)。
- `internal/task/init.go:53`:兜底任务 `TaskGroupRegexSync`,间隔 5 分钟,启动即跑一轮。
- 并发防护现状:`GroupRegexSync` 未加锁,靠「先查后写 + 单组事务」;本设计沿用同一现状,不引入新锁。

## 方案

### 新入口:`GroupManualAbsorb`

`internal/op/group.go` 新增:

```go
// GroupManualAbsorb 吸纳全部手动分组(member_regex 为空)中模型名包含分组名的授权。
// channelIDs 非空时只考虑这些渠道的授权(渠道增改后的即时触发);
// 为空时考虑全部授权(定时兜底)。append-only:只补缺失成员,不动既有成员。
func GroupManualAbsorb(ctx context.Context, channelIDs ...int) error
```

流程:

1. DB 查 `member_regex = ''` 的分组(Preload Items);没有则直接返回(与 `GroupRegexSync` 同款短路)。
2. 候选授权:遍历 `channelGrantCache.GetAll()`,按 `channelIDs` 过滤(为空不过滤),补齐 model/key 缓存字段(取不到的跳过,与 `syncRegexGroupItems` 同款容错)。
3. 每组独立事务:
   - `groupKey := strings.ToLower(strings.TrimSpace(group.Name))`;空则跳过(防御,UI 不允许空名)。
   - 命中:`strings.Contains(strings.ToLower(modelName), groupKey)` —— 与前端 `matchesGroupName`/`normalizeKey` 完全同口径。
   - 已有成员按 `channel_grant_id` 建集合;仅对未存在的命中授权插 `group_items`。
   - 新成员排序复用正则吸纳的 (channelID, modelName, keyName) 稳定排序;`priority` 从该组现有最大值 +1 起连续递增(空组从 1 起)。
   - 不删除任何成员、不触碰 `active_item_id`。
   - 单组失败记 `log.Warnf` 不中断其余分组(与 `GroupRegexSync` 同款)。
4. 有吸纳发生或无差异都调 `groupRefreshCache`(与正则同步保持同构,成本一次查询;仅在有分组时才走到这)。

### 触发点接线

- `ChannelCreate` / `ChannelUpdate`:在现有 `GroupRegexSync(ctx)` 调用之后追加 `GroupManualAbsorb(ctx, channelID)`(带渠道过滤,只吸纳该渠道的授权)。
- `GroupCreate`:手动分组(member_regex 为空)创建事务提交后,立即对该分组跑一轮全渠道吸纳,再带成员返回——与正则分组创建即吸纳(`group.go:98-108`)对称。UI 验证发现:先建渠道后建分组时,渠道侧触发点不会回补,用户被迫按「自动添加」按钮(2026-09-11 实测)。
- `GroupUpdate`:手动分组且**名称发生变更**时,提交后立即按新名吸纳。未改名的成员编辑(增删成员、排序)不触发,否则刚删的成员保存即回吸,与「移除记忆缺失」叠加成死循环。
- `internal/task/init.go` 的 `TaskGroupRegexSync` 回调:`GroupRegexSync` 后串行追加 `GroupManualAbsorb(ctx)`(全量兜底)。注册代码注释同步更新为「正则重算 + 手动分组吸纳兜底」。
- `ChannelEnabled`/`ChannelDel` 不挂:不产生新授权(Del 的级联清理已有 `clearActiveItems` 路径,吸纳不介入删除)。

### 明确不做

- 不动 `group_items` 索引。勘误(质检发现):`model/group.go:76-77` 已存在 `(group_id, channel_grant_id)` 唯一索引 `idx_group_grant`,并发吸纳的重复插入会被 DB 拒绝(单组事务回滚+告警),幂等本就有硬保护,无需新增约束。
- 不做移除记忆、不做渠道侧配置。
- 不改前端、不改 API、不改 schema。

## 权衡与备选

- **备选1:吸纳逻辑塞进 `GroupRegexSync` 统一入口** — 否。两者语义相反(整体替换 vs 只增不减),分开命名各自可测,合并会把「哪些分组、哪种替换」搅在一个函数里。
- **备选2:吸纳时也走 `syncGroupItems`(提交 既有成员∪新成员)** — 否。全替换路径会重写全部优先级,违背 R3「既有成员不动」,且失败面更大。
- **备选3:只挂事件不挂兜底** — 否。兜底幂等、成本一次缓存遍历,能兜住「改库/恢复备份后缓存漂移」一类正则兜底已在防的场景,行为对齐。

## 兼容与回滚

- 纯行为新增,无 schema/API 变更;回滚 = revert 单个 commit。
- 已有手动分组首次触发(或首个兜底轮)会立刻吸纳存量匹配授权——这正是本任务的目的,但需在交付说明里提示用户:存量分组可能「突然多出成员」,属预期。
