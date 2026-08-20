# ⑥ 渠道调用详情页

> 父任务: `08-19-octopus-route-a-migration`。本子任务对应 7 项功能中的第 6 项。
> 来源: dev `f551b95`(渠道调用详情页),但上游日志架构与 dev 完全不同,需基于上游内存结构重写,不照搬 dev 实现。

## Goal

在渠道管理页为每个渠道提供「查看调用详情」入口,弹出该渠道在**当前进程内存日志窗口内**被尝试的每次调用明细(状态/模型/耗时/错误/所属请求),让运维无需手扒单条请求详情即可评估单渠道健康状况。

## 背景(与 dev 的架构差异 — 决策依据)

dev 版本(`08-03-channel-call-detail` prd)基于 `relay_logs` gorm 表 + `attempts` JSON 数组列持久化,可从 DB 反查 7 天历史 attempts。**上游完全不同**:

- 上游日志是**纯内存 SSE**(`internal/relay/log.go`):`logRecords map[uint64]LogRecord`,trim 到 `maxLogHistoryRecords=50`,重启即失,**无 RelayLog gorm 模型、无 relay_logs 表**。
- `LogRecord` 当前**只存 `currentAttempt *LogAttempt` 执行中游标**,完成的 attempt 不进任何历史切片——只经 `logDetail` 通道实时推送后丢弃。**无法像 dev 那样从存储展开 attempts 历史。**
- 因此 ⑥ 必须先在内存**补存 attempts 历史**,再建按渠道查询;这是与 dev 的根本区别。

详见 `design.md` §2。

## 范围边界

### In Scope
- 后端:扩展 `LogRecord` 累积已完成 attempts 历史;扩展 `LogAttempt` 补 `Status`/`Duration`;新增 `GetLogAttemptsByChannel(channelName)` 查询 + `GET /api/v1/log/channel-attempts` 接口(鉴权,只读)。
- 前端:渠道卡片(`channel/Card.tsx`)加「调用详情」入口,弹出 `MorphingDialog`(复用上游日志详情同款模态模式),顶部概览统计 + 逐条明细列表。
- 明细字段:所属请求 id、请求状态、请求时间、请求模型、最终渠道、attempt 序号、状态(success/failed/skipped)、被试模型、耗时(ms)、错误。
- 三语 i18n(en/zh_hans/zh_hant)同步新增 key。

### Out of Scope(本期不做)
- 不做 7 天历史回溯——上游是内存 trim 50,无持久化;数据窗口即当前进程内存内(≤50 条请求,重启清空)。
- 不做时间范围 picker(复用内存全量窗口)。
- 不做跨渠道横向对比/排行榜。
- 不做 circuit_break 状态(上游无熔断器,⑦ 移植后再加)。
- 不改 Log 页现有列表/详情 UI;不新增顶级 NavItem(渠道详情是模态,非路由)。
- 「点 request_id 跳回请求详情」本期降级为:行内展示请求级上下文 + 跳到 Log 页(`setCurrentPage('log')`);自动打开特定请求的 MorphingDialog 需跨页状态,留作 stretch(见 design §5)。

## Requirements

### 后端
1. `LogRecord` 累积已完成 attempts 历史(当前只有 currentAttempt 游标)。终态事件(`attempt.finished`/`response.committed`)时把该次 attempt 追加到历史切片;非终态事件保留已累积历史不丢失。
2. `LogAttempt` 补 `Status`(success/failed/skipped)与 `Duration`(ms)字段,在 execution.go 各终态调用点设置:
   - `recordUnavailableTarget` → skipped(目标不可用,未发上游请求)
   - `handleAttemptFailure` → failed(上游请求失败)
   - `commitAttempt` → success(上游返回可用响应并提交)
3. 新增 `GetLogAttemptsByChannel(channelName string) []ChannelAttemptDetail`:遍历 `logRecords`,匹配 `attempt.ChannelName == channelName`,拼装请求级上下文(ID/State/StartedAt/RequestModel/FinalChannelName),按 StartedAt 倒序。
4. 新增 `GET /api/v1/log/channel-attempts?channel_name=xxx`(挂现有 `/api/v1/log` 组,Auth 鉴权,只读)。

### 前端
5. `channel/Card.tsx` 加「调用详情」按钮,点击打开 `MorphingDialog` 渲染渠道详情视图。
6. 新增渠道详情视图组件:打开时 `useChannelAttempts(channel.name)` 拉取明细;顶部概览(总尝试数 + 各状态计数);逐条明细列表(字段见 In Scope)。
7. 行内 request_id 可点击 → `setCurrentPage('log')`(跳到日志页;自动打开特定请求详情为 stretch)。
8. 新增 `useChannelAttempts` hook(api/log.ts,`useQuery` + `apiRequest`),按 channel_name 查。
9. 三语 i18n 同步。

### buildChannelNameByModelKey 评估(⑤ 留待 ⑥ 决断)
- **结论:保留,不删。** 该函数仍被 `log/Item.tsx:332` 用于解析分组成员渠道名(数据源是 `modelChannels`,非 attempts),与 ⑥ 的 channel-attempts 视图无关。⑥ 不触碰该函数,⑥ 的 attempts 明细直接用 `LogAttempt.ChannelName`(上游已自带渠道名,无需反查)。⑤ 的保留判断正确。

## Acceptance Criteria
- [ ] 渠道管理页任意渠道可点「调用详情」,弹出 MorphingDialog 无报错。
- [ ] 弹窗顶部显示该渠道在内存窗口内的总尝试数与各状态(success/failed/skipped)计数。
- [ ] 弹窗按时间倒序列出每次尝试,字段完整:请求 id、请求状态、时间、请求模型、最终渠道、attempt 序号、状态、被试模型、耗时、错误。
- [ ] 后端 `GET /api/v1/log/channel-attempts?channel_name=` 鉴权可用,只读不写库;返回与内存 `logRecords` 中该渠道 attempts 一致(抽查对得上)。
- [ ] 内存窗口随上游 trim 50 自然收敛,不无限增长;重启清空符合预期。
- [ ] 不破坏现有日志概览/详情 SSE 流、渠道管理页、日志详情 MorphingDialog 原功能。
- [ ] `LogAttempt` 新增字段不破坏现有 `streamDetail` SSE 负载(前端 `RelayAttempt` 类型可兼容扩展,Item.tsx 不报错)。
- [ ] ④ 已加的 `user_agent`/`client_name` 字段不受影响(不同结构,字段名不冲突)。
- [ ] go build/test -tags=jsoniter 通过;web pnpm build 通过;lint 自改文件无新增问题(49 baseline 不修)。

## Notes
- ⑥ 与 ④ 共改 `internal/relay/log.go`:④ 改 `LogOverview`(加 user_agent/client_name),⑥ 改 `LogRecord`/`LogAttempt`/`applyLog`/新增查询函数——不同结构体,字段名不冲突。
- 容器 + UI smoke 留父任务 Step 8 集成验收;本子任务验证到 go build/test + pnpm build + lint 关卡。
- 上游无 channel_id on attempt,过滤键定为 channel_name(渠道名 unique,安全)。
