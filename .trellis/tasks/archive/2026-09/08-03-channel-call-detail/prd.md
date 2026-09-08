# 渠道调用详情页（按渠道查日志）

> 任务: `08-03-channel-call-detail`

## 背景

当前 Octopus 的请求日志按「请求」组织,每条 `relay_log` 是一次完整的客户端请求,`attempts` 数组里嵌着该请求在各渠道上的逐次尝试(成功/失败/熔断/跳过)。

排查 504/524 / 渠道健康度时,运维常需要**反过来按某个渠道看它被调用的明细**——"这个渠道最近每次请求是成功还是失败、耗时多少、什么错误、出现在第几个 attempt、有没有被熔断跳过"。目前:

- 前端日志页(`web/src/components/modules/log/index.tsx`)只能按时间/error/API Key/模型筛选,**无渠道筛选**;`attempts` 渠道链只在单条日志详情的 tooltip/详情区显示,无法跨请求聚合查看某个渠道。
- 后端 `RelayLogList`(`internal/op/log.go:197`)支持的过滤只有 time / has_error / api_key_names / model_names,**无 channel 过滤**。

结论:目前无法在前端"按渠道"查看调用明细。本任务补这个能力。

## 目标(Goal)

在渠道管理页为每个渠道提供「查看调用详情」入口,跳转到该渠道的专属视图,逐条展示其在保留期内被尝试的每次调用明细(状态/模型/耗时/错误/所在请求),让运维无需手扒 attempts 即可评估单渠道健康状况。

## 范围边界(In Scope / Out of Scope)

### In Scope
- 渠道管理页每个渠道卡片/行上加「调用详情」入口,跳转到该渠道详情视图。
- 详情视图:逐条列出该渠道保留期内(7 天)的每次尝试明细。
- 明细字段:所在请求 id、请求时间、请求模型、本次 attempt 在请求中的序号、状态(success/failed/circuit_break/skipped)、被试模型、耗时(ms)、是否 sticky、错误信息(msg)。
- 顶部一行概览统计:总尝试数、各状态计数。
- 后端新增「按渠道查 attempts 明细」的查询接口。

### Out of Scope(本期不做)
- 不做跨渠道横向对比页 / 渠道排行榜(后续可另起任务)。
- 不做"渠道是最终成功渠道"专用视图(本期放 attempts 全量,不限最终渠道)。
- 不做时间范围 picker(本期复用保留期 7 天全量,不做 UI 时间筛选)。
- 不改 Log 页现有列表/详情 UI。
- 不改 熔断 / 对账相关逻辑。

## 需求(Requirements)

### 后端
1. 新增查询函数,按渠道 ID(或渠道名)返回其在保留期内的 attempts 明细,按时间倒序。
   - 数据源:`relay_logs.attempts`(JSON serializer 字段)展开。**注意**:attempts 是 JSON 串存在单字段,无法用 SQL 直接展开,需在 Go 层全量拉回(按 7 天保留期 + 复用现有清理保证行数可控)再展开过滤。
   - 返回字段:见"明细字段"。每条明细需带携带 `request_id`(所在 relay_log.id)用于跳回请求详情。
   - 性能/边界:7 天内请求量可能较大,需考虑分页或上限;本次复用 `relay_log_keep_period` 防止无限回溯。
2. 新增 HTTP 接口暴露该查询,挂到现有 `/api/v1/log` 路由组(需鉴权)。请求参数至少含 `channel_id`。

### 前端
3. 渠道管理页(`web/src/components/modules/channel/index.tsx` / `Card.tsx`)加「调用详情」入口,点击进入该渠道详情视图。
4. 新增渠道详情视图组件:顶部概览统计 + 逐条明细列表。
5. 入口跳转复用现有**客户端状态路由机制**(`NavItem` + `ContentLoader` 模式,非文件路由)。具体方案在 design 中定:
   - 选项 A:新增一个 `NavItem`(如 `channel-detail`),`activeItem` 切到它时用全局 store 传 `channelId` 渲染详情。
   - 选项 B:在现有 `channel` NavItem 下做子视图切换(用 zustand 局部状态切 list / detail)。
   - design 选定其一并说明理由。

### 验收标准(Acceptance Criteria)
- [ ] 渠道管理页任意渠道可点「调用详情」,进入该渠道专属视图,URL/状态正确切换无报错。
- [ ] 视图顶部显示该渠道在保留期(7天)内的总尝试数与各状态(success/failed/circuit_break/skipped)计数。
- [ ] 视图按时间倒序列出每次尝试,字段完整:请求 id、时间、请求模型、attempt 序号、状态、被试模型、耗时、sticky、错误信息。
- [ ] 明细中"所在请求 id"可点击跳回请求详情(复用现有 Log 详情弹窗或链路,不重新实现)。
- [ ] 保留期内数据完整且与 `relay_logs` 表 `attempts` 内容一致(抽查若干条与原始 attempts 对得上)。
- [ ] 7 天保留期随 `relay_log_keep_period` 设置联动(改设置后行为相应变化,或至少不永久回溯超出保留期)。
- [ ] 不破坏现有日志列表/详情、渠道管理页原功能。
- [ ] 后端接口需鉴权;只读查询,不写库。

## 风险与注意

- **性能**:7 天 attempts 全量展开可能数据量大;Go 层展开需控制单次返回上限或分页,避免内存/延迟尖峰。design 需给出上限策略。
- **路由**:纯静态导出(`output: export`)无文件路由,新视图必须走 `NavItem` 或模块内子路由方案,不能依赖 Next dynamic route 文件。
- **跳回请求详情**:现有 Log 详情是 `getLogDetail(id)` 懒加载在日志页弹窗里的;跨页跳回需确认复用方式(design 落实)。
- **attempts JSON 含义**:`attempts[].status` 取值 `success/failed/circuit_break/skipped`(`internal/model/log.go:18-23`),`channel_name` 为尝试渠道名(非最终成功渠道);展开匹配用 `channel_id`(更稳)还是 `channel_name` 在 design 定。
