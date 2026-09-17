# 技术设计:故障转移请求内游标 + 轮询模式回归

> 2026-09-16。语义已在 PRD 与用户逐条定稿(回炉是特性、全员冷却维持现状等待、亲和保留、中止不计失败),本设计只解决"怎么落"。

## 现状梳理(结论均已在代码核实)

- **选路引擎**:`internal/relay/route.go pickGroupItem`——从队头扫到 `CurrentItemID` 为止(抢占式:谁恢复谁上),失败后 `recordRouteFailure` 置冷却、清 `CurrentItemID`,下一轮又从队头扫。请求内没有"走到哪了"的记忆,这是回炉根因。
- **handler 循环**(`internal/relay/handler.go:96` 起):`for { 重读分组 → pickGroupItem → 试上游 → 失败 recordRouteFailure → continue }`。同成员重试(`failures < MemberMaxAttempts`)靠 `CurrentItemID` 粘住——`pickGroupItem` 扫到 CurrentItemID 即 break,尾部返回它。
- **无成员可选时**:handler 等 `MemberRetryIntervalSeconds` 再重试,**无上限**(直到客户端断开)。本任务维持此行为不变(2026-09-16 纠正后定稿)。
- **模式**:`GroupMode string`,`oneof=manual failover` 三处 binding(模型/创建/更新)。
- **v1 参考**(`git show v1.5.3:internal/relay/balancer/balancer.go`):RoundRobin = per-group 原子计数器,`items[(idx+i)%n]` 旋转;Failover = 纯优先级排序,请求内顺序遍历由 relay 循环完成。
- **渠道主序已成立**:priority 合成排序(渠道序/渠道ID → 模型 → 凭据)保证同渠道成员在列表内连续,游标顺走即渠道块遍历,R5 无需新结构,只需测试锁定。

## 方案

### 1. 游标引擎(route.go 重构,核心)

新请求作用域游标,`pickGroupItem` 改签名收游标:

```go
// routeWalk 是单个转发请求内的选路游标: 记录本轮"走到哪个成员", 决定下一次从哪继续。
// 游标只在本请求内存活; 跨请求的亲和/冷却/探测仍在 RouteState。
type routeWalk struct {
    lastItemID int // 最近一次选中并尝试的成员; 0 表示尚未开始。
}

func pickGroupItem(group model.Group, walk *routeWalk) model.GroupItem
```

选中规则(按优先):

1. **粘住当前**:`walk.lastItemID` 仍在成员列表、可选、未冷却 → 返回它(同成员重试,替代原 CurrentItemID 粘滞的请求内职责)。
2. **向下走**:从 `lastItemID` 在(当前)优先级序中的位置**之后**找第一个未冷却成员;到尾**绕回头部**。冷却中的跳过(含"冷却中"与"冷却已过期但探测名额被占"两种)。用 item ID 定位而非下标——分组每轮重读,成员集合可能变,ID 稳定。
3. **起点**:`lastItemID == 0` 时按模式定——failover:亲和窗口内从 `CurrentItemID` 起,否则队头;roundrobin:per-group 计数器旋转位置(见 §2);manual:不走游标,维持 active_item_id 原逻辑。
4. **绕完一圈无候选**(全员冷却):返回零值。handler 沿用既有 `wait(MemberRetryIntervalSeconds)` 重试循环——等待语义与现状完全一致(2026-09-16 用户纠正:不新增上限/报错),冷却到期后下轮 pick 自然命中。
5. 选中后照旧更新 `RouteState.CurrentItemID`(亲和锚点 + 前端"当前成员"展示)并 publish。

**删掉的**:原"从队头扫到 CurrentItemID 即 break"的抢占逻辑。恢复的高优先级成员要等游标绕回——这正是用户要的语义。

**保留的**:探测单飞(`ProbeItemID`)跨请求防雷群——游标走到已恢复成员时,名额被占则跳过继续向下,下次绕回再试;`recordRouteFailure`(计 failures→冷却/清 CurrentItemID)、`recordRouteSuccess`(探测解除/亲和武装)、`releaseRouteProbe` 全部不动。

### 2. 轮询模式(模型 + 起点)

- `GroupModeRoundRobin GroupMode = "roundrobin"`,三处 `oneof` 加值;DB 列是字符串,存量行不受影响。
- per-group 计数器:`sync.Map[int]*uint64`(v1 同款),请求**起点** = `atomic.AddUint64(counter,1) % n` 旋转位;起点之后的游标行为与 failover 完全一致(粘住→向下→绕圈→冷却门控)。
- 轮询**不参与亲和**(旋转是它的存在意义);冷却/探测/max_attempts 照常生效。
- `updateGroup` 模式变更分支已有的 `relay.ResetRouteState(id)` 覆盖切到/切出 roundrobin,无需新增。

### 3. 全员冷却(handler,零新增)

零值分支**完全沿用现状**:`wait(MemberRetryIntervalSeconds)` → continue,无上限、无新报错。设计上不做"最早到期唤醒"优化——多省几次空转唤醒,不值得为它加接口面(2026-09-16 用户定:等待语义跟之前一样)。

### 3b. 客户端中止不计失败(handler 顺序调整,2026-09-16 并入)

- **pre-commit 取消**:`attempts = append(...AttemptFailed...)`(`handler.go:215`)移到 `ctx.Err() != nil` 检查**之后**——取消轮次不追加失败尝试。取舍:该轮确实发往过上游,但日志 error=context canceled 已表达取消,attempts 只留真实失败,与「中止不算渠道故障」的既有注释语义对齐。
- **流式中途取消**:流式收尾块的 `metrics.RequestFailed = 1` 写统计加 `ctx.Err() == nil` 条件——客户端断开不记渠道失败。
- 冷却/探测本就不受中止影响(取消路径先于 recordRouteFailure 返回;post-commit 不碰),不改。

### 4. handler 接线

`for` 循环外建 `walk := &routeWalk{}`,每轮 `pickGroupItem(group, walk)`,选中后 `walk.lastItemID = item.ID`。其余循环体(grant 校验、超时、统计、attempts 记录)零改动。

### 5. 前端

- Editor 模式选择器 + Card 模式徽标加 `roundrobin`(i18n 三语:轮询)。
- 亲和旋钮在 roundrobin 模式下置灰 + 提示不适用(展示层约束,后端不硬拒)。
- `web/src/api/group.ts` GroupMode 类型加值;无新旋钮(等待上限已裁撤)。

## 权衡与备选

| 决策 | 取舍 |
|---|---|
| 游标放请求作用域而非 RouteState | 游标是单请求语义,进 RouteState 会与跨请求状态(亲和/冷却)搅在一起,并发请求互相踩;请求作用域 + itemID 定位天然容忍成员集中途变化。 |
| itemID 定位而非下标 | 分组每轮重读,成员增删会让下标漂移;ID 稳定,"之后第一个"在列表变化后仍语义正确。 |
| 轮询与 failover 共用游标引擎 | 只差起点(计数器 vs 亲和/队头),统一后冷却/探测/绕圈逻辑单份维护;备选"独立 balancer"是 v1 结构,但 v2 的冷却/探测在 RouteState 里,拆开两头改。 |
| 全员冷却等待零新增(不加旋钮/上限/报错) | 用户纠正:等待语义跟之前一样。加戏(预算上限+新错误)引入第 7 个旋钮与新的失败终态,收益仅是提前报错,非所要。 |
| 探测单飞保留 | 游标绕回已恢复成员时若无并发限制,多请求同时砸恢复渠道(雷群);单飞让其余请求绕过去,下圈再试,与游标语义自洽。 |
| 不做渠道级失败聚合 | 渠道主序已由排序连续性保证;渠道内多凭据/多变体各试一遍符合"成员=授权"模型,聚合到渠道级是另一套冷却语义,超出本任务。 |

## 兼容与回滚

- failover 行为变化即本任务目的(抢占→游标);manual 零变化;无 schema 迁移(Mode 新枚举值 + RelayConfig 新列由 AutoMigrate 增量加列)。
- `pickGroupItem` 签名变更是包内私有函数,无外部调用方。
- 回滚 = revert 单 commit;残留 mode="roundrobin" 行在旧代码下 binding 拒绝写入但读路径不炸(展示原样字符串)。
- spec 落点:`relay-routing.md` 的「已知问题」节在完成后改写为正式契约(游标语义)。

## 测试设计(`internal/relay/route_cursor_test.go` 新建 + 既有回归)

- 粘住:同成员 failures 未满 → 重复返回同成员。
- 中止不计失败:pre-commit 取消 → attempts 无新增;流式中途取消 → 渠道统计无 RequestFailed。
- 向下:10 成员前 5 冷却,从 #6 起失败 → 下一选 #7 而非 #1;#1 中途恢复不插队,绕圈后才轮到。
- 绕圈:#10 失败 → 回 #1(已过冷却则选中,未过则 #2)。
- 全员冷却:返回零值,handler 沿用既有 retry-interval 等待循环(零新增)。
- 探测单飞:恢复成员名额被占 → 游标跳过,绕回可再试。
- 亲和起点:窗口内新请求游标从 CurrentItemID 起。
- 轮询:连续请求起点按 n 取模旋转;不同分组计数器隔离;冷却成员被跳过后旋转不断链。
- 渠道连续性:构造多渠道多成员,断言游标序内同渠道成员连续。
- manual / 亲和成功武装 / 探测成功解冷:既有用例回归不动。
