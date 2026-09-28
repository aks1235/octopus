# 技术设计:分组成员级测试 + 冷却重置

## 总览

两个能力共用"分组成员 = 渠道授权"这一既有抽象,全部落在现有分层内,无 DB 迁移:

| 层 | 变更 |
|---|---|
| `internal/relay` | 新增 `TestGroupMembers`(成员级测试,复用 keytest 机制)+ `ClearGroupCooldowns`(冷却清理) |
| `internal/model` | 新增 `GroupMemberTestResult` 响应类型 |
| `internal/server/handlers/group.go` | 新增 `POST /test/:id`、`POST /cooldown-reset/:id` 两个路由 |
| `web/src/api/group.ts` | 新增两个 mutation 与结果类型 |
| `web/.../group/Card.tsx` + `ItemList.tsx` | 卡片头两个图标按钮;成员行测试结果徽标 |
| `web/src/locales/*.json` | 三语文案 |

## 后端

### 1. relay.ClearGroupCooldowns(冷却重置核心)

`internal/relay/route.go` 新增:

```go
// ClearGroupCooldowns 清空分组的全部成员冷却与连续冷却计数并释放探测占用, 供人工重置入口使用。
// 与 ResetRouteState 的差异: 只恢复候选资格, 当前路由/亲和/轮询计数全部保留 ——
// 重置语义是"让冷却成员重新可选", 不是"路由从头再来"。
func ClearGroupCooldowns(groupID int)
```

实现要点(持 `routeMu`):
- `route == nil` 时直接返回(无状态即无冷却);
- `route.Cooldowns`、`route.trips` 整表清空;`route.ProbeItemID` 归 0(探测闸门与冷却同生命周期,只清冷却留闸门会让"到期放行一个探测"的成员被永久卡住);
- `CurrentItemID` / `AffinityUntil` / `affinityArmed` / 轮询计数器**不动**(R2.2);
- `publishRouteLocked` 发布 → SSE `runtime` 事件 → 前端徽标即时消失。

### 2. relay.TestGroupMembers(成员级测试核心)

`internal/relay/keytest.go` 新增导出函数(与 `TestChannelKey` 同文件,共用机制):

```go
// TestGroupMembers 对分组全部成员逐个发起最小真实请求, 返回逐成员结果。
func TestGroupMembers(ctx context.Context, group model.Group) []model.GroupMemberTestResult
```

**授权解析**(复用转发链路既有路径,`handler.go:127-146` 同款):
`op.ChannelGrantGet(item.ChannelGrantID)` → `grant.ChannelModel` / `grant.ChannelKey` → `op.ChannelGet(channelModel.ChannelID)`。
解析失败(不可用成员)不发请求,`Error` 给原因,`Success=false`(R1.5)。

**逐成员试测**:直接复用 `testChannelKeyModel(ctx, channel, channelKey, modelName, protocols)`。
协议位取该授权自身的 `grant.Protocols`(授权位就是"模型 × 凭据"的实际协议位,无需 grants 数组匹配——这是与渠道 key 测试入参的差异点)。

**协议展开顺序**:与渠道 key 测试共用 `keyTestProtocols`(message → responses → chat),按 `grant.Protocols` 位过滤后依序试测,不新增顺序常量(用户确认两侧顺序保持一致)。首个成功的协议即返回(`testChannelKeyModel` 已有此语义,成功即停),成功协议记入结果的 `Protocol` 字段(R3.2)。

**成功清冷却**(R1.3,新私有函数 `clearMemberCooldownLocked` 语义):
- `delete(route.Cooldowns, itemID)`、`delete(route.trips, itemID)`;
- 若 `route.ProbeItemID == itemID` 则归 0;
- **不**改 `CurrentItemID`/亲和:测试是旁路观测,清掉冷却后扫描自然轮到它,不强制切流量(与 `recordRouteSuccess` 的探测分支相比刻意少了"立即切回",权衡:重置/测试不应改变主路由,避免测试行为干扰正在进行的请求序列);
- 发布路由状态。
- 结果里 `CooldownCleared=true` 当且仅当清之前该成员确在冷却中(前端可提示"已恢复")。

**并发**:成员间有界并发(信号量上限 4)。理由:渠道 key 测试串行的约束是"同渠道压力 + 计费顺序",而分组成员常跨渠道,串行会让慢渠道(实测天翼云单次测试 14s)拖住整组;上限 4 兼顾两者。结果按成员提交序返回(预分配切片按下标写)。
ctx 取消时剩余成员不再发往上游,已发出的按取消记失败(R1.7,与 `TestChannelKey` 的取消语义一致)。

**日志**(R1.6):复用 `keyTestLogFinalize` 的组装逻辑,参数化客户端标识:
- `keyTestLogFinalize` 增加签名参数(或新增变体),分组测试写 `client_name="分组测试"`、`user_agent="octopus-group-test"`,与渠道测试("面板测试"/"octopus-key-test")、真实转发(claude-code 等)在日志页两个维度均可区分;
- 每成员一条日志,`RequestModelName`/`ActualModelName` 用上游模型名(与渠道测试同口径,日志页模型列才有意义),`ChannelId`/`ChannelName` 带成员所属渠道。

### 3. model 类型

`internal/model/group.go` 新增:

```go
// GroupMemberTestResult 分组成员连通性测试的单个成员结果。
type GroupMemberTestResult struct {
    ItemID          int      `json:"item_id"`          // 分组成员 ID。
    ChannelName     string   `json:"channel_name"`     // 成员所属渠道名称。
    ModelName       string   `json:"model_name"`       // 成员引用的上游模型名称。
    KeyName         string   `json:"key_name"`          // 成员引用的凭据名称。
    Success         bool     `json:"success"`           // 任一协议取得可解析 2xx 响应即为成功。
    Protocol        Protocol `json:"protocol"`          // 成功时使用的协议位; 失败时为 0。
    Error           string   `json:"error"`             // 失败/不可用原因摘要, 已截断; 成功时为空。
    UseTime         int64    `json:"use_time"`          // 该成员整体测试耗时(毫秒, 含多协议累计)。
    CooldownCleared bool     `json:"cooldown_cleared"`  // 成功且原本在冷却中时为真。
}
```

### 4. handler 路由

`internal/server/handlers/group.go`,沿用现有 group 路由组(Auth + RequireJSON):

- `POST /api/v1/group/test/:id` → `testGroupMembers`:`op.GroupGet(id)` 404 兜底;`relay.TestGroupMembers(ctx, group)`;`resp.Success(c, results)`。空 items 分组返回空数组。
- `POST /api/v1/group/cooldown-reset/:id` → `resetGroupCooldown`:`op.GroupGet` 404 兜底;`relay.ClearGroupCooldowns(id)`;响应 `groupResponse{Group, Runtime: relay.RouteStateOf(group)}`(与 `getGroup` 同款,前端拿到最新 runtime 即可对齐缓存)。

**风险项**:group 路由组挂了 `RequireJSON` 中间件,空 body POST 是否放行需实现时确认(看 `middleware.RequireJSON` 实现);若要求非空 body,前端发 `{}` 即可,handler 不需要任何 body 字段。

## 前端

### 5. API 层(`web/src/api/group.ts`)

- `GroupMemberTestResult` 接口(与后端 JSON 对齐);
- `useTestGroupMembers`:`mutationFn: (id) => apiRequest<GroupMemberTestResult[]>(`/api/v1/group/test/${id}`, { method: 'POST', body: {} })`;
- `useResetGroupCooldown`:响应为 `Group`,onSuccess 走 `writeGroupCache` 对齐缓存。

### 6. UI(`group/Card.tsx` + `ItemList.tsx`)

- **测试按钮**:卡片头 `FlaskConical` 图标(编辑/复制/删除按钮同款 `IconButton` 样式);`isPending` 时 spinner 且禁用;完成后 toast 汇总(`{success}/{total} 可用`),失败成员数 > 0 时用 warning toast。
- **结果展示**:本地 state `Record<number, GroupMemberTestResult>`(key 为 item_id),传入 `MemberList` → `MemberItem` 在 `MemberStatus` 旁渲染结果徽标:成功绿色 `CircleCheck`(tooltip 显示所用协议,冷却被清时文案"已恢复"),失败红色 `CircleX`(tooltip 显示错误摘要),不可用灰色。重新测试覆盖;卡片重挂载自然清空(结果是一次性观测,不持久化——KISS)。
- **重置冷却按钮**:卡片头 `RotateCcw` 图标;仅当 `group.mode !== 'manual'` 且 `runtime.cooldowns` 存在未到期项时渲染(手动模式冷却不适用,R2.4;无冷却时按钮无意义,不渲染比禁用更干净);点击后 toast。
- SSE 已有:重置/测试清冷却都会 publish runtime,徽标消失不需要前端额外轮询。

### 7. i18n

`web/src/locales/{zh_hans,zh_hant,en}.json` 的 `group.card` 命名空间新增:测试按钮 tooltip、测试中、结果 toast(成功/部分失败/全失败)、重置冷却 tooltip 与 toast、成员结果 tooltip 文案(成功协议/失败原因/不可用)。

## 边界与兼容

- **纯新增**:两个新端点、两个新 relay 函数、前端新增按钮;既有端点/函数签名不动(`keyTestLogFinalize` 参数化是包内私有函数,无外部影响)。
- **并发安全**:所有路由状态变更持 `routeMu`;测试与真实转发并发时,清冷却与 `recordRouteFailure/Success` 互斥,无竞争窗口。
- **正则分组**:成员只读不影响测试/重置(两者都不改成员集合)。
- **计费**:每成员最多试 3 个协议、每协议 1 token 生成上限,与渠道 key 测试同量级;不可用成员 0 请求。

## 测试策略

- `route_test.go`:ClearGroupCooldowns(冷却清空/probe 释放/current+affinity 保留/无状态 no-op/已删成员残留清理不回归)。
- `keytest_test.go`(或新文件):TestGroupMembers(不可用成员跳过且带原因、成功清冷却、失败不动状态、ctx 取消、结果按提交序、并发上限不超 4)——复用现有假上游测试基建。
- handler 测试:404、空分组、RequireJSON 空 body 行为。
- 前端:lint + build(既有 CI 口径),UI 走人工验证(octopus-verify 起容器看界面)。

## 回滚

单 commit 纯新增,revert 即回滚;无 DB 迁移、无配置格式变更。
