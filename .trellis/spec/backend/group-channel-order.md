# 正则分组渠道人工排序契约

> 2026-09-16 v2.1.0 落地。正则分组的成员集合仍由正则重算定稿(整体替换),但**渠道顺序**支持人工干预且不被重算覆盖——解决「手动调高的渠道优先级被 5 分钟兜底刷回」。

---

## 场景:member_regex 分组的顺序干预(跨层:API + schema + 前端拖拽)

### 1. 范围 / 触发

- 正则分组(`member_regex <> ''`)的成员顺序需要人工控制时。手动分组不适用:其 items 整体提交本就是顺序的书写方式。
- 触发 code-spec 深度的原因:新表 + 新 API 签名 + 重算排序合成交叉三层。

### 2. 签名与实现位置

- 表 `group_channel_orders`(`internal/model/group_channel_order.go`):`ID / GroupID / ChannelID / Position`;唯一索引 `idx_group_channel (group_id, channel_id)`;读取索引 `idx_group_position (group_id, position)`(2026-09-16 修正:曾误标在 ChannelID 上合成 (channel_id, position),不服务 `WHERE group_id=? ORDER BY position`);双外键 OnDelete:CASCADE(Group + Channel)。
- API(`internal/server/handlers/group.go`):
  - `POST /api/v1/group/channel-order/:id`,body `{"channel_ids":[3,1,2]}` → 保存顺序并按新顺序重算成员,返回 groupResponse + publishGroupEvent
  - `DELETE /api/v1/group/channel-order/:id` → 清空人工顺序回到自然序
- op(`internal/op/group.go`):`GroupChannelOrderApply(groupID, channelIDs, ctx)` 事务内「查存在渠道 → 过滤 → 清删旧行 → 插入 → syncRegexGroupItems 重算」;`GroupChannelOrderReset` 同构。
- 排序合成:`syncRegexGroupItems` 内 `applyGroupChannelOrder`——基础排序 (channel_id, model_name, key_name) 后,按人工顺序做 `Sort.SliceStable` 二次排序(stable 保住渠道块内自然序)。

### 3. 契约

- **顺序语义 = (人工渠道顺序, channel_id, model_name, key_name)**:排过的渠道按 position 在前段;未排过的(含新吸纳)按自然序追加在尾段;顺序表挂分组,同一渠道在不同分组互不干扰。
- **拖拽单位 = 渠道块**:前端把拖拽后的成员列表折算为去重 channel_id 首次出现序(`web/src/components/modules/group/utils.ts channelOrderOf`,卡片与编辑器共用),渠道内顺序永远由后端自然序定稿,UI 不表达渠道内成员排序。
- **成员集合仍只读**:正则分组的 items 不开放手动增删(卡片 `onRemove` 不挂、编辑器 MemberList 不传 onRemove);顺序端点只写顺序表,priority 仍由重算定稿(`syncGroupItems` 主键保留逻辑不变)。
- **保存即生效**:顺序端点成功后 handler 层调 `relay.ResetRouteState(groupID)`——顺序变了,旧的当前成员/冷却/亲和基于旧顺序不再适用,不重置则要等亲和窗口(默认 300s)过去新顺序才生效。**必须在 handler 层**:`internal/op` 不能导入 `internal/relay`(relay 反向依赖 op,成环)。
- 前端编辑提交按**提交终态**分流(`Card.tsx handleSubmitEdit`):`finalRegexGroup = values.member_regex !== ''` 决定 items 是否提交、顺序端点先后——按「当前」类型分流会让「手动↔正则转换 + 同时拖拽」的整体丢失(2026-09-16 质检修复)。
- 备份:`DBDump.GroupChannelOrders`(omitempty,GroupItems 之后),导出 Find + 导入 createDoNothing 对齐 GroupItems;dump 版本**不递增**,旧备份缺字段 = 回自然序。

### 4. 验证与错误矩阵

| 条件 | 行为 |
|---|---|
| channel_ids 含重复 | 400(handler 层,前端 bug 不该发生) |
| 分组不存在 | 404 |
| `member_regex = ''` 的分组调此端点 | 400(手动分组顺序走 items 提交,不归此端点) |
| channel_ids 含不存在的渠道 ID | **事务内过滤丢弃**(2026-09-16 修正:曾设计为「悬空无害不校验」,被证伪——channel_id 是真外键且运行时 foreign_keys(ON),未知 ID 插入即撞外键→整次 500。可达路径:多会话下别的会话刚删渠道、本会话成员列表仍是旧数据时拖拽提交) |
| 过滤后为空 | 等价清空顺序(合法) |
| 顺序读取失败(DB 错误) | 降级自然序 + log.Warnf(偏好不作正确性约束) |

### 5. 正反例

- Good:分组 [渠道A, 渠道B] 设顺序 [B, A] → 重算后 B 的成员整体在前,A 在后;两渠道内部各自按 (model, key) 自然序。
- Base:新渠道 C 命中正则吸纳 → C 追加在 A、B 之后;再拖 C 到最前 → 下轮重算 C 在前。
- Bad:把顺序表挂渠道实体上做「全局权重」——同一渠道在不同分组需要不同顺位,全局表达不了。
- Bad:op 层直接调 relay.ResetRouteState —— import cycle,编译都过不了。

### 6. 测试要求

- `internal/op/regex_group_channel_order_test.go`:合成顺序(块间人工序+块内自然序)、新渠道落尾、幂等、渠道删除级联清理顺序行、重加不复活、正则改宽顺序稳定、手动分组隔离(回归红线)、Reset 回自然序、悬空 ID 丢弃、全悬空=清空。
- `internal/server/handlers/group_channel_order_test.go`:重复 400、手动 400/不存在 404、悬空过滤后 200(锁定 500→200)。
- handler 测试基建:`router.RegisterAll` 会清空全局路由表,engine 用 `sync.Once` 共享。

### 7. Wrong vs Correct

#### Wrong

```go
// 空迁移占位版本号 —— migrate.go 对 nil Up 直接报错, InitDB 失败应用起不来
RegisterAfterAutoMigration(Migration{Version: 13, Up: nil})
```

#### Correct

```go
// 建表交给 AutoMigrate(模型已注册), 不注册任何迁移; 需要数据回填时再加真实的 Up
// (2026-09-16: 工作区曾出现空 013.go, 若随版发布会启动崩溃, 已删除)
```

---

## 关联

- 成员集合语义(整体替换)与方言 → [group-member-regex.md](./group-member-regex.md)
- 手动分组吸纳(append-only)为何不受本契约影响 → [group-manual-absorb.md](./group-manual-absorb.md)
- 顺序即 priority,选路按序遍历 → [relay-routing.md](./relay-routing.md)
