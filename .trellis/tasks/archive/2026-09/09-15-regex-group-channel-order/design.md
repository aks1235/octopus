# 技术设计:正则分组渠道人工排序

> 2026-09-15。工作区已存在部分实现(model/GroupChannelOrder、AutoMigrate 注册、syncRegexGroupItems 顺序合成、op 三函数),经核对方向与本设计一致,作为既有起点收编;缺口与问题见「现状盘点」。

## 现状盘点(2026-09-15 核实)

**已有实现(工作区未提交,收编)**:

- `internal/model/group_channel_order.go`:GroupChannelOrder 表,`idx_group_channel` 唯一索引 (group_id, channel_id),双外键 OnDelete:CASCADE(Group + Channel)。
- `internal/db/db.go`:`&model.GroupChannelOrder{}` 已入 AutoMigrate 列表。
- `internal/op/group.go` `syncRegexGroupItems`:按已有人工顺序做 `Sort.SliceStable` 稳定二次排序——有人工顺序的渠道按 position 在前,**保持渠道内 (model, key) 自然序**(原排序在前,stable sort 只重排渠道块间顺序)。语义正确。
- op 层三函数:`GroupChannelOrderGet` / `GroupChannelOrderSave` / `GroupChannelOrderReset`。

**缺口(本设计补齐)**:

1. 无 API 路由与 handler——顺序无法从前端提交。
2. 无前端交互——分组卡片对正则分组虽已可拖拽(见下),但 `submitMembers` 走 `GroupUpdate` 的 items 整体提交,不是顺序端点;编辑器内 `RegexMemberSection` 完全只读。
3. `syncRegexGroupItems` 排序合成的单测缺失。
4. 备份导出/导入(`internal/op/backup.go` / `internal/model/backup.go`)未覆盖新表。
5. `internal/db/migrate/013.go` 是空迁移(Up 为 nil)——AutoMigrate 建表已足够,该文件要么删掉要么留作版本占位,需定夺。

**关键现状(与最初认知不同)**:分组**卡片**(`Card.tsx`)的正则分组成员今天就是可拖拽的——`MemberList` 不区分正则与否,拖拽后 `submitMembers` 提交 items 给 `GroupUpdate`,写库成功、然后被 5 分钟兜底重算覆盖。用户的原始 bug 报告正是这条路。编辑器弹窗内才是只读(`RegexMemberSection`)。

## 方案

### 1. API:独立顺序端点(PRD R7,用户已确认)

`internal/server/handlers/group.go` 注册:

```
POST /api/v1/group/channel-order/:id   body: {"channel_ids": [3,1,2]}   → 保存并返回重算后的分组
DELETE /api/v1/group/channel-order/:id                                 → 清空人工顺序(回到自然序)
```

- 请求模型进 `internal/model/group.go`:`GroupChannelOrderRequest { ChannelIDs []int json:"channel_ids" binding:"required" }`(空数组=清空,与 DELETE 等价,允许二者并存)。
- 保存 handler 流程:`GroupChannelOrderSave` → 对该分组跑 `syncRegexGroupItems`(事务内,同 `GroupUpdate` 的正则重算路径)→ `groupRefreshCache` → 返回 groupResponse 并 `publishGroupEvent`(与 updateGroup 同款,其他会话同步)。
- 校验:channel_ids 里不允许重复(重复=前端 bug,直接 400)。**悬空渠道 ID 在 op 层事务内过滤丢弃**(不再"留着无害"):`channel_id` 有真外键约束,未知 ID 写入会撞外键→整次保存 500,故先查 `channels` 存在的 ID 再过滤,过滤后为空等价清空;保留外键与级联清理。分组不存在 → 404,分组 `member_regex = ''` → 400(手动分组 items 提交本就管顺序,不归这个端点)。
- 路由状态重置:handler 在保存成功后调 `relay.ResetRouteState(groupID)`(op 不能导入 relay,会成环)——顺序变了,旧的当前成员/冷却/亲和基于旧顺序不再适用,不重置则要等亲和窗口(默认 300s)过去新顺序才生效。与 updateGroup 的模式变更分支、deleteGroup 同款。
- op 层把「保存顺序 + 重算该组」组合为一个事务函数 `GroupChannelOrderApply(groupID, channelIDs, ctx)`,handler 只调它;`GroupChannelOrderSave` 变为其内部实现(外部唯一调用点收拢,`GroupChannelOrderReset` 供 DELETE 用)。

### 2. 重算合成(已有,收编 + 收口)

`syncRegexGroupItems` 内:基础排序 (channel_id, model_name, key_name) → 读取该组 `GroupChannelOrder` 按 position 建 `orderRank` → `Sort.SliceStable` 按 (有 rank?, rank) 重排。效果:

- 有人工顺序的渠道按人工序在前段,渠道内成员仍按 (model, key) 自然序(stable sort 保住渠道块内部)。
- 未排过/新吸纳的渠道在尾段按 channel_id 自然序。
- 顺序表里指向已删渠道的行被外键级联清掉;新渠道(新主键)无 rank,自然落尾。

**不改** `sortGroupItems`、`groupRefreshCache`、`syncGroupItems`(priority 仍由合成顺序定稿,主键保留逻辑不动)。

### 3. 前端

**卡片(`Card.tsx`,主交互面)**:

- 正则分组(`group.member_regex` 非空)的 `submitMembers` 改走新端点:把拖拽后的成员列表折算为去重渠道顺序(`m.channel_id` 首次出现序),提交 `channel_ids`,onSuccess 后端返回的分组自然带新 priority。手动分组维持现状(items 提交)。
- i18n 三语补 tooltip/提示:正则分组拖拽=调整渠道优先顺序(渠道内顺序自动保持)。

**编辑器弹窗(`Editor.tsx` RegexMemberSection)**:

- 只读列表升级为可拖拽:复用 `MemberList` + `onReorder`,但**不挂 onRemove**(集合只读语义,PRD R4/AC7)。拖拽即暂存,与现有表单一并保存:提交时若渠道顺序相对 initial 有变化,先调顺序端点再走 `GroupUpdate`(或顺序端点先、失败即停);无变化不调。
- 「一键吸纳」预览按钮维持现状(只影响预览集合,不碰顺序)。

**API 客户端(`web/src/api/group.ts`)**:新增 `useGroupChannelOrder()` mutation(POST)与 `useGroupChannelOrderReset()`(DELETE),响应走 `writeGroupCache` 更新 React Query 缓存。

### 4. 备份覆盖

- `model.DBDump` 加 `GroupChannelOrders []GroupChannelOrder json:"group_channel_orders,omitempty"`(排在 GroupItems 之后);导出 `conn.Find` 补一行。
- 导入:与 GroupItems 同款 `createDoNothing` 增量插入(表无独立自然键,主键冲突跳过)。
- dump 版本 **不递增**:旧备份无此字段导入后为空,正则分组回到自然序,行为等价于「从未设过人工顺序」,兼容成立。

### 5. 迁移

- **删除空的 013.go**。建表由 AutoMigrate 完成(现状已注册),空迁移记录只会留一条无意义版本行;若未来需要数据回填再加真实的 013。
- 渠道删除的级联清理由外键承担(`foreign_keys(ON)` 已开,`db.go:104`),无需迁移代码。

### 6. 测试(与既有 manual_group_absorb_test.go 同层)

`internal/op/regex_group_channel_order_test.go`:

- 合成顺序:设 [渠道B, 渠道A] 顺序后重算 → B 成员整体在前,A 在后;渠道内 (model, key) 自然序保持。
- 尾段追加:新渠道命中吸纳 → 追加在人工序渠道之后,人工序不破坏。
- 幂等:重复保存同顺序 → 成员集合与顺序不变,无重复行。
- 渠道删除:删渠道 → 顺序行级联消失,其余顺序保持。
- 重复 channel_ids → 400(handler 层)。
- 正则改宽吸纳新渠道后再重算 → 新渠道落尾,既有顺序稳定。
- 手动分组回归:member_regex 为空不走顺序合成(其 items 提交语义不变)。

## 权衡与备选

| 决策 | 取舍 |
|---|---|
| 独立顺序端点 vs 并入 GroupUpdate | 用户已确认独立端点。代价:编辑器保存需两次请求;换来正则组 items 保持只读、卡片拖拽路径单一。 |
| 顺序表挂分组而非渠道 | 同一渠道在不同分组顺序独立,这是需求本身;渠道全局权重表达不了。 |
| 悬空渠道 ID 在事务内过滤 | 原设计理由「悬空 ID 无害且会被级联清掉」已被证伪:`channel_id` 是真外键(OnDelete:CASCADE)且运行时 `foreign_keys(ON)`,未知 ID 根本插不进去,会撞外键约束让整次保存回滚、端点 500(可达路径:多会话下别的会话刚删渠道、本会话成员列表仍是旧数据)。改为在 `GroupChannelOrderApply` 的事务内先查 `channels` 存在的 ID 再过滤(过滤后为空等价清空)——保留外键与级联清理,又不让时序竞争变成 500。重复校验仍在 handler 层,存在性过滤只在 op 层做一次。 |
| stable sort 二次排序而非直接在候选生成时合成 | 基础排序已有渠道内保序,stable 重排渠道块间即可,改动面最小;直接合成需要把 rank 提前注入 memberRef,改动更深且两者等价。 |
| dump 版本不递增 | 新表对旧备份是纯增量字段(omitempty),导入侧容忍缺字段即回到自然序,无破坏性。 |
| 删除空 013 迁移 | AutoMigrate 建表足够;空迁移留无用版本记录。若团队惯例是「schema 变更必留版本行」,改为保留并在注释写明建表走 AutoMigrate。 |

## 兼容与回滚

- 无 schema 破坏性变更:新表 + AutoMigrate;回滚 = revert commit,残留表不影响旧代码(无代码引用即惰性)。
- 未设人工顺序的正则分组:orderRank 为空,stable sort 空操作,行为与现状逐字节一致(AC5)。
- 手动分组路径完全不动(AC6)。
- 卡片正则分组拖拽从「静默被刷回」变为「真正生效」——这是用户要的修复本身,但需在交付说明里写清行为变化。
