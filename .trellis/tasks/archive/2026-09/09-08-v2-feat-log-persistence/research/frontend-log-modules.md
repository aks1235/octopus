# Research: 前端日志模块两侧对照(dev-v2 上游 vs fork dev)

- **Query**: 对比 dev-v2 与 fork dev 分支的日志持久化 UI / channel 调用详情页,评估融合改动面
- **Scope**: internal(两侧工作区 + `git show dev:` 读旧文件)
- **Date**: 2026-09-09
- **说明**: 标注 `(dev:)` 的引用均为 `git show dev:<path>` 读取;其余为当前工作区 dev-v2 文件。

## A. dev-v2(上游 v0.13.2 形态)

### A1. log 模块目录结构与职责

仅两个文件:

| 文件 | 行数 | 职责 |
|---|---|---|
| `web/src/components/modules/log/index.tsx` | 49 | Log 页面壳:`useLogs()` 订阅 + `VirtualizedGrid` 单列列表 |
| `web/src/components/modules/log/Item.tsx` | 474 | LogCard 概览卡片 + MorphingDialog 详情弹窗(双栏) |

页面壳 `web/src/components/modules/log/index.tsx:8-49`:
- `useLogs()` 拿实时 logs/isLoading/error(index.tsx:10)。
- 空态/断连提示(index.tsx:20-27、31-35);列表 `VirtualizedGrid layout="list" columns={{default:1}} estimateItemHeight={104} overscan={8}`(index.tsx:37-45)。
- **没有**筛选栏、分页、刷新按钮、连接开关——上游页是纯实时流视图。

### A2. API 封装与数据流(`web/src/api/log.ts`,122 行)

| Hook/函数 | 位置 | 端点 | 说明 |
|---|---|---|---|
| `useClearLogs` | log.ts:39-43 | `DELETE /api/v1/log/clear` | 清空**内存**已完成日志;useMutation |
| `useStopRound` | log.ts:46-51 | `POST /api/v1/log/{requestId}/{round}/stop` | 中止当前轮次上游调用 |
| `useLogs` | log.ts:54-101 | `GET /api/v1/log/overview/stream` | SSE 订阅,见下 |
| `useLogRequestBody` | log.ts:104-111 | `GET /api/v1/log/{id}/request-body` | 按需,`enabled` 由调用方控制,`staleTime: Infinity`,queryKey 含 `startedAt` |
| `useLogResponseBody` | log.ts:114-121 | `GET /api/v1/log/{id}/response-body` | 同上 |

SSE 连接方式(log.ts:59-98):
- `new EventSource('/api/v1/log/overview/stream', { withCredentials: true })`(log.ts:60)——**cookie 凭证,无 token**。这是上游在 Vite 同源 + cookie 鉴权下的做法。
- 监听**命名事件** `log`(`source.addEventListener('log', ...)`,log.ts:66),payload 即完整 `RelayLogOverview`。
- 增量合并:按 id 命中则原地替换,否则插入到首个更小 id 之前,避免整表重排(log.ts:76-88)。
- `onerror` 只置 error 状态、显示「正在重连」横幅(index.tsx:31-35),由浏览器原生 EventSource 自动重连;无手动断开/重连。

数据类型 `RelayLogOverview`(log.ts:20-36):`status: 'running'|'committed'|'success'|'failed'|'canceled'`、`round`、`target_channel`、`sending`、`usage`(含 cached/write_cached)、`cost`、`started_at`(RFC3339 字符串)、`duration`(纳秒)。这是实时语义,fork 的落库 `RelayLog`(unix 秒、ftut、attempts)与之不同构,详见 B5。

后端路由对照 `internal/server/handlers/log.go:18-40`:`/overview/stream`、`/:id/request-body`、`/:id/response-body`、`/:request_id/:round/stop`、`/clear`,全部挂 `middleware.Auth()`。

### A3. 上游日志页 UI 结构

**列表项(LogCard)**`web/src/components/modules/log/Item.tsx:468-474`:`memo` 包 `MorphingDialog`;卡片即 `MorphingDialogTrigger`(Item.tsx:419-458):协议位标签(Chat/Response/Message,Item.tsx:50-54)+ 请求模型 + 进行中 spinner/完成箭头 + 目标渠道 Badge + 7 格指标(耗时实时走共享时钟 `now`,Item.tsx:411-415;完成后用后端 duration,Item.tsx:60-62)+ 失败时错误摘要块(Item.tsx:451-455)。

**详情(双栏弹窗)**`LogDetail`(Item.tsx:138-397),仅在弹窗打开期间挂载(Item.tsx:460-462 经 `MorphingDialogContainer`):
- `MorphingDialogContent` 占 80vw/80vh(Item.tsx:180)。
- 展开动画 600ms 后才 `detailReady` 允许发详情请求(Item.tsx:159-163)。
- **左栏**:`Tabs` 切「分组 / 请求体」(Item.tsx:204-214)——分组页签展示分组渠道列表,手动模式可就地切渠道并 stopRound(Item.tsx:255-275);请求体页签走 `useLogRequestBody`(Item.tsx:146)。
- **右栏**按状态机切换标题与内容(Item.tsx:299、325-387):等待选渠道 / 重试轮次明细(观察累积 `rounds`,Item.tsx:165-177)/ committed 流式中 / 错误 JsonContent / 响应体 `useLogResponseBody`(Item.tsx:147,仅 success 才 enabled)。
- 内容渲染 `JsonContent`(Item.tsx:90-135):`@uiw/react-json-view` + githubDark/Light 主题,解析失败按纯文本 pre。
- 上游详情数据全走 **react-query hooks**(非 effect setState),天然受控、可复用。

**路由注册**:dev-v2 无 URL 路由,是 zustand 页面状态机——
- `web/src/stores/app.ts:7` `Page` 联合类型,`:13-20` `NAV_ITEMS` 6 项(含 log,icon=Logs)。
- `web/src/components/app.tsx:26` `lazy(() => pageImports.log())`,`:151` `{visibleItem === 'log' && <Log />}`,AnimatePresence 页面切换动画(app.tsx:127-154)。
- 导航悬浮预加载:`web/src/lib/page-preload.ts:4-16` + `web/src/components/app-shell.tsx:77-83`。
- clear 入口在设置页 `web/src/components/modules/setting/Log.tsx:10-17`(非日志页内)。

**i18n**:`web/src/locales/zh_hans.json`(Vite 内联,非 public),provider 用 `use-intl`(`web/src/provider/locale.tsx:2,29`)。`log.*` 共 **24 keys**:log.card.* 20、log.list.* 2(empty/disconnected)、log.status.committed 1。

## B. fork dev 分支(git show dev: 读取)

### B4. log 模块结构:列表 + 筛选 + 分页 + 懒加载详情

目录 5 个文件(dev:`web/src/components/modules/log/`):

| 文件 | 行数 | 职责 |
|---|---|---|
| `index.tsx` | 225 | 页面壳:筛选工具栏 + 无限滚动列表 + 滚顶按钮 |
| `Item.tsx` | 880 | LogCard 概览卡(含 attempts tooltip)+ MorphingDialog 双栏详情 |
| `RequestDetailDialog.tsx` | 201 | 受控 radix Dialog 版单条请求详情(渠道页复用) |
| `ActiveRequests.tsx` | 143 | 活跃请求 Popover(依赖 fork `/active` 接口,**PRD 非目标,不移植**) |
| `ClientIcon.tsx` | — | UA→客户端图标徽章(next/image + 40 个 log.client.* i18n;**任务5 范围,不移植**) |

页面壳(dev:`web/src/components/modules/log/index.tsx`):
- 顶部工具栏(index.tsx:118-192):`MultiSelect` API Key 筛选(119-129)、`MultiSelect` 模型筛选(130-140)、`ActiveRequestsPopover`(141)、`has_error` 切换按钮(143-156)、SSE 断开/重连开关(157-177)、手动刷新(178-191)。
- **注意**:模型筛选项来自 `useGroupList()` 的 **group 名**而非模型列表(index.tsx:54-57 `(groups ?? []).map(g => g.name)`),因为 fork 的筛选参数叫 `model_names` 而其路由实体按模型名建组。移植到 dev-v2 时选项来源需重新裁决(dev-v2 有现成 `modelListQueryOptions`,`web/src/api/queries.ts:34-38`,返回 `LLMInfo[].name`,`web/src/api/model.ts:18-24`)。
- 列表 `VirtualizedGrid`(196-209)带 `footer`/`onReachEnd`/`reachEndEnabled`/`reachEndOffset`/`scrollContainerRef`,pageSize=10(45),滚顶按钮(212-221)。

`Item.tsx` 关键件(dev:):
- `RetryBadgeWithTooltip`(80-130):多 attempts 时渠道徽章 + tooltip 展示逐渠道成败/模型/耗时。
- `DeferredJsonContent`(132-223):`useMorphingDialog().isOpen` 门控,打开后 300ms 延迟渲染(146-156),关闭即 return null——**这是 morph 耦合点之一**。
- `ResponseLogContent`(388-449)+ `parseResponseLog`(349-386):把 SSE 文本/JSON 响应解析成摘要/思考/工具调用/meta(含 60000 字符、2000 事件截断保护,237-262)。
- `LogDetailPanels`(453-554):`useMorphingDialog().isOpen` 门控 + effect 内 `getLogDetail(log.id)` 懒加载(461-492,注意是 effect+setState 而非 react-query);双栏 request/response(498-523);`debug_content` 折叠(524-551)。文件内注释(451-452)明确「必须在 MorphingDialog 内部使用」。
- `LogCard`(556-880):卡片指标比上游多(ftut/TPS/API Key 名/缓存写,621-672)、诊断区(错误/attempts 链)可折叠(720-830)。

### B5. fork API 封装(dev:`web/src/api/endpoints/log.ts`,567 行)

| 导出 | 位置 | 端点 | 调用方式 |
|---|---|---|---|
| `useClearLogs` | 123-138 | `DELETE /api/v1/log/clear` | 成功后 `invalidateQueries(['logs'])`(**清库**语义) |
| `useLogs` | 166-528 | `/list` + `/stream-token` + `/stream` + `/active` | 见下 |
| `getLogDetail` | 533-535 | `GET /api/v1/log/{id}` | 裸 async 函数,详情含 request/response/debug_content/attempts |
| `useChannelAttempts` | 544-567 | `GET /api/v1/log/channel-attempts?channel_id&page&page_size` | useInfiniteQuery,`ChannelAttemptsResponse {list,total,truncated}`(49-53) |

`useLogs` 组合逻辑(核心差异):
- 历史列表:`useInfiniteQuery` GET `/api/v1/log/list?page&page_size&has_error&api_key_names=..&model_names=..`(192-217),`staleTime: Infinity`;pages 合并去重按 time 降序(219-234)。
- 行级 SSE:**先 `GET /api/v1/log/stream-token`**(291)拿一次性 token,再 `new EventSource(\`${API_BASE_URL}/api/v1/log/stream?token=${token}\`)`(295)——token 方案是因 Next.js 下 API 可能跨域(API_BASE_URL 为 env,dev:`web/src/api/client.ts:3`)且 `/stream` 路由不挂 Auth 中间件(dev:`internal/server/handlers/log.go:46-51`)。默认 `onmessage` 收完成日志并按筛选组合多写缓存(370-402,`writeLogToCache` 265-279);`addEventListener('active')` 维护活跃请求(405-436)。
- 竞态防护 `connectGenerationRef`(180、287)、sessionStorage 手动断开标志(147-160)、REST `/active` 兜底补拉快照(325-341、470-483)。
- fork 后端路由全集(dev:`internal/server/handlers/log.go:18-52`):`/list`、`/channel-attempts`、`/clear`、`/stream-token`、`/active`、`/:id` + 无鉴权 `/stream`。

类型 `RelayLog`(58-82):`time` unix 秒、`request_model_name`/`actual_model_name`、`channel_name`、`input/output/cached/cache_creation_tokens`、`ftut`、`use_time`、`cost`、`request_content/response_content/debug_content`、`error`、`attempts: ChannelAttempt[]`、`user_agent/client_name/reasoning_effort`(任务5 列)。

### B6. channel 模块 CallDetail(commit f551b95)

- **入口链**:channel 卡片「调用详情」按钮(dev:`web/src/components/modules/channel/Card.tsx:44` `setView({mode:'detail',channelID,channelName})`,按钮渲染 77-80,History 图标)→ `channel/index.tsx`(dev:62-65)`view.mode==='detail'` 时整页替换为 `<ChannelCallDetail/>`。
- **视图分态 store**:dev:`web/src/components/modules/channel/detail-store.ts:22-26` zustand `ChannelView = {mode:'list'} | {mode:'detail';channelID;channelName}` + `backToList`,模块内自路由、不动主导航(注释 3-9)。**纯 zustand,零依赖,可原样平移**。
- **明细行渲染**(dev:`CallDetail.tsx:113-150`):`#request_id` 按钮(`handleClickRequest` 64-67 → 开 RequestDetailDialog)+ 时间 + 状态 Badge(`STATUS_STYLE` 23-28:success/failed/circuit_break/skipped 四态样式+文案 key)+ `attempt_num/model_name/duration/sticky(Pin)/msg/request_error`。
- **概览统计**(82-92):total + 四状态计数(基于已加载页,54-59)+ `truncated` 提示。
- **分页**(154-164):`useChannelAttempts(channelID, 50)` + 手动「加载更多」按钮 + `noMore {loaded}` 文案。
- **RequestDetailDialog 复用**(166):`<RequestDetailDialog open={detailOpen} onOpenChange={setDetailOpen} requestId={detailRequestId} />`。

### B7. RequestDetailDialog(dev:`web/src/components/modules/log/RequestDetailDialog.tsx`,201 行)

- 受控 props `{open, onOpenChange, requestId}`(35-39);数据层 `useQuery(['log-detail', requestId], getLogDetail, {enabled: open && requestId != null, staleTime: 30s})`(47-52)。
- 布局:radix `ui/dialog`(13-19 导入)+ 顶部指标条(125-136)+ 错误块(138-145)+ 双栏 request/response JsonView(148-167,自含 `renderContent` 61-96,collapsed=2)+ `debug_content` 折叠(170-195)。

### B8. 两侧 i18n 差异量级

`log.*` 键数:**dev-v2 24 个 vs fork 96 个**(fork 独有 89 个)。分类:

| 分组 | fork 键数 | 说明 |
|---|---|---|
| log.client.* | 40 | ClientIcon 客户端名,**不移植**(任务5) |
| log.controls.* | 11 | 筛选/连接/刷新工具栏(历史区需要大部分) |
| log.card.* 新增 | ~14 | ftut/tps/attempts/cost 等历史卡片指标 |
| log.channelAttempts.* | 10 | CallDetail + RequestDetailDialog |
| log.activeRequest.* | 7 | 活跃请求 Popover,**不移植** |

另有 `channel.callDetail.*` 6 keys(fork 独有:back/empty/open/openRequestHint/stickyHint/title)。dev-v2 三语文件在 `web/src/locales/{zh_hans,zh_hant,en}.json`,fork 在 `web/public/locale/`。移植量级估算:**每语言约 +40~50 keys**(剔除 client/activeRequest 后)。

## C. 融合改动面评估

### C1. 布局选择:同页 tab/分区 vs 独立路由页

事实支撑:
- dev-v2 页面注册是 4 处联动:`stores/app.ts:7`(`Page` 联合类型)+ `:13-20`(`NAV_ITEMS`)+ `components/app.tsx:26,151`(lazy + 渲染分支)+ `lib/page-preload.ts:4-11`。新增独立日志历史页要动全部 4 处,且导航 Dock 变 7 项(悬浮偏移量按索引计算,`app-shell.tsx:38-40`,视觉回归面扩大)。
- 上游 Log 页壳极简(49 行),顶部无工具栏;`VirtualizedGrid` 已内置 `footer/onReachEnd/reachEnd*` props(`web/src/components/common/VirtualizedGrid.tsx:31-34,58-61`),可直接承载历史列表无限滚动;唯一缺 `scrollContainerRef`(fork 有,dev:`VirtualizedGrid.tsx:37`)——若历史区要「滚顶按钮」需补此 prop 或改用容器内滚动。
- dev-v2 已有 `ui/tabs.tsx`(日志详情左栏在用,`Item.tsx:13,204-214`),tab 方案零新组件。

**结论:同页 tab(实时流 / 历史)或同页上下分区改动面显著小于独立路由页**——实时区(白拿项)可整体原样保留(AC5 零回归),历史区作为新组件挂进 `log/index.tsx`;不动导航注册链。与 PRD R4「同页分区 vs tab 以设计文档为准、改动面最小化」一致,本调研为此提供依据。

### C2. 文件清单(前端)

**可近乎原样平移(仅机械替换)**:
| fork 文件 | 适配点 |
|---|---|
| `channel/detail-store.ts` | 零改动(zustand 5 两边同版本) |
| `channel/CallDetail.tsx` | 仅 `next-intl`→`use-intl` 导入替换(API 兼容);`useChannelAttempts` 改从 dev-v2 api 文件导入 |
| `log/RequestDetailDialog.tsx` | ① `next-intl`→`use-intl`;② `@/components/ui/dialog` 在 dev-v2 **不存在**(见 C4),需新建 dialog.tsx 或改用已有 morphing-dialog;③ `useTheme` 来自 `next-themes`→dev-v2 自有 `@/provider/theme`(上游 Item.tsx:7 同款用法可抄) |
| `common/MultiSelect.tsx`(dev:`web/src/components/common/MultiSelect.tsx`) | 依赖 Popover/Checkbox/Input,dev-v2 ui 目录均有;`next-intl`→`use-intl` |

**需按 dev-v2 组件体系重写/适配**:
| 内容 | 原因 |
|---|---|
| 历史 API hooks(fork `endpoints/log.ts` 的 /list、/:id、/channel-attempts 部分) | dev-v2 惯用法是 `api/*.ts`(hooks)+ `api/queries.ts`(`queryOptions` 集中,`web/src/api/queries.ts:16-38`);请求层 `apiClient.get`→`apiRequest`(`web/src/api/client.ts:38-57`,后者 fetch + credentials include + 统一 401 事件);**不移植** stream-token/stream/active 及 `useLogs` 的 SSE 整块(168-528 行大部分),只留 `getLogDetail`/`useChannelAttempts`/列表 infinite query |
| 历史列表卡片(fork `Item.tsx` 的 LogCard) | fork 卡片与上游卡片数据模型不同构(fork `RelayLog`:unix 秒/ftut/attempts;上游 `RelayLogOverview`:RFC3339/running/round)。历史区应有自己的卡片;移植 fork Item 时需:`getModelIcon` 签名差异(fork 返回 `{Avatar,color}`,dev:`model-icons.tsx:128`;dev-v2 返回 `{Icon,className,color}`,`web/src/lib/model-icons.tsx:68`)、去 ClientIcon、`next-intl`→`use-intl`、animate-ui Tooltip→`ui/tooltip.tsx` |
| `log/index.tsx` 页面壳 | 改造为 tab/分区容器,叠加筛选工具栏;模型筛选项来源重新裁决(见 B4 注意) |
| `channel/Card.tsx`、`channel/index.tsx` | dev-v2 的渠道卡/页结构已变(dev-v2 有 `channel/state.ts`、无 `CardContent.tsx`),入口按钮和 view 分态需按 dev-v2 现结构重做(模式照抄 fork:zustand 视图分态 + index.tsx 分支渲染) |
| `VirtualizedGrid`(可选) | 若历史区要 fork 的「滚顶按钮 + 外部滚动容器」,补 `scrollContainerRef` prop(dev:`VirtualizedGrid.tsx:37` 对比 dev-v2 缺失) |

**不移植**(PRD 非目标):`ActiveRequests.tsx`、`ClientIcon.tsx`、stream-token/stream/active 前端逻辑、`log.client.*`/`log.activeRequest.*` i18n、fork 的手动断开/重连/刷新按钮(上游 onerror 自动重连已覆盖)。

**修改清单汇总**:新建 `log/HistoryList.tsx`(命名随意)、`log/RequestDetailDialog.tsx`、`log/HistoryCard`(或并入)、`channel/CallDetail.tsx`、`channel/detail-store.ts`、`common/MultiSelect.tsx`、可选 `ui/dialog.tsx`;修改 `log/index.tsx`、`channel/index.tsx`、`channel/Card.tsx`、`api/log.ts`(或新建 `api/log-history.ts`)、`api/queries.ts`、三语 `locales/*.json`。

**clear 语义冲突提醒**(PRD R2):fork `useClearLogs` 成功后 `invalidateQueries(['logs'])`(dev:`endpoints/log.ts:130-133`)、`useLogs.clear` 是 `removeQueries`(466-468);移植时若历史区沿用 `['logs',...]` 前缀 queryKey 会与清内存/清库两种语义纠缠,建议历史区用独立 queryKey 前缀并在清库后精准 invalidate。

### C3. fork 的 morph 双栏渲染旁路是什么;上游有没有同样机制

**fork 双栏渲染链及其耦合**:fork log 页详情 = `LogCard` → `MorphingDialogContainer`(dev:`Item.tsx:682-876`)→ `LogDetailPanels`(453,懒加载 `getLogDetail`,**门控条件是 `useMorphingDialog().isOpen`**,463)→ `DeferredJsonContent`(133,同样吃 morph context 的 isOpen 做 300ms 延迟渲染,146-156)/`ResponseLogContent`(388)。三个渲染组件均为**文件内私有(未导出)且依赖 MorphingDialog context**。要在渠道页复用「单条请求双栏详情」,要么导出它们并让渠道页也包一层 MorphingDialog(morph layoutId 动画以触发卡片为锚点,跨页锚点不存在,动画会坏),要么解开 isOpen 门控改成受控 prop——两者都要动 880 行的 log `Item.tsx`,回归面大。所以 fork 选择**复刻旁路**:RequestDetailDialog 自含一套双栏渲染(getLogDetail 同源数据 + 同款 JsonView 主题),用普通 radix Dialog 完全绕开 morph,「morph 动画以外视觉与 log 页相近、零回归」——依据即其头注释 dev:`RequestDetailDialog.tsx:26-34` 原文。

**上游有同样的 morph 机制**:`web/src/components/ui/morphing-dialog.tsx`(464 行)与 fork 同源(layoutId 共享布局动画、Trigger/Container/Content/Title/Description/close、useClickOutside 等);上游 LogCard/LogDetail 同样构建其上(`Item.tsx:18-27,460-462,468-474`)。差异仅在上游详情数据层用 react-query hooks(`useLogRequestBody/ResponseBody`,Item.tsx:146-147)+ 600ms `detailReady` 延迟(Item.tsx:159-163)替代 fork 的 effect+setState/300ms 门控,受控性更好。

**对移植的含义**:RequestDetailDialog 平移到 dev-v2 后同样天然旁路 morph 耦合(它不依赖 morphing-dialog);而历史区详情若想与上游视觉完全一致,也可直接复用上游 `Item.tsx` 的 `JsonContent`(90-135,纯函数无 context 依赖)替代 RequestDetailDialog 内嵌的 `renderContent`——这是 fork 当年没有的便利(上游 JsonContent 无耦合,可直接导出复用)。

### C4. 组件体系/依赖差异对照(平移适配总表)

| 维度 | dev-v2(目标) | fork dev(来源) | 平移动作 |
|---|---|---|---|
| 框架 | Vite 8.2.2(`web/package.json:63`) | Next.js 16.0.7(dev:`package.json:36`) | 去 `'use client'`、`next/image`→`img`、`next-themes`→`@/provider/theme` |
| i18n | `use-intl` 4.14.1(`provider/locale.tsx:2`) | `next-intl` 4.5.8 | 仅换 import(use-intl 是 next-intl 核心,API 同) |
| i18n 文件 | `src/locales/*.json` | `public/locale/*.json` | 合并进三语文件 |
| 请求层 | `apiRequest`(fetch,cookie,统一 401 事件,`api/client.ts:38-57`) | `apiClient` + `API_BASE_URL` env(dev:`client.ts:3`) | 全部请求改 apiRequest |
| react-query 惯用法 | hooks + `queries.ts` queryOptions(`queries.ts:16-38`) | endpoints 文件内 hooks | 按目标风格落位 |
| motion | 13.1.1 | 12.23.25 | 兼容(morphing-dialog 两边同源) |
| `@uiw/react-json-view` | 已有 2.0.0-alpha.43(`package.json:26`) | alpha.39 | 直接用 |
| ui 独有件 | tabs、tooltip、popover、morphing-dialog、sonner | dialog、alert-dialog、card、dropdown-menu、table、animate-ui(tabs/tooltip) | **fork `ui/dialog` 在 dev-v2 不存在**;dev-v2 有 `radix-ui` meta 包 1.6.7(`package.json:34`),新建 shadcn dialog.tsx 零新增依赖;tooltip 用 dev-v2 `ui/tooltip.tsx` 替 animate-ui |
| getModelIcon | `{Icon, className, color}`(`lib/model-icons.tsx:68`) | `{Avatar, color}`(dev:`model-icons.tsx:128`) | 调用处改名 |
| VirtualizedGrid | 有 footer/onReachEnd/reachEnd*,无 scrollContainerRef(`VirtualizedGrid.tsx:31-34`) | 全有 + scrollContainerRef(dev:`VirtualizedGrid.tsx:37`) | 需要滚顶按钮则补 prop |
| zustand | 5.0.15 | 5.0.9 | 兼容,detail-store 原样平移 |

## Caveats / Not Found

- 本调研仅覆盖前端 + 后端路由清单;后端 handlers 内部逻辑(/list 筛选实现、channel-attempts LIKE 粗筛)按 PRD 归属其它 research 主题。
- fork `MultiSelect.tsx` 未逐行精读(仅确认依赖存在);平移时按实际 import 核对。
- 「同页 tab vs 分区」的最终裁决属设计文档,本文件仅提供改动面事实依据。
- dev-v2 渠道页结构(`channel/state.ts`、`Card.tsx` 现状)未逐行精读,C2 中「入口按 dev-v2 现结构重做」的具体行号引用以实现阶段现场核对为准。
