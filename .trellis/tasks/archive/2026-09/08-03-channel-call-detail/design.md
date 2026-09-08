# 设计 — 渠道调用详情页(按渠道查日志)

> 任务: `08-03-channel-call-detail` · 依 `prd.md`

## 关键决策(已与用户确认)

1. **路由**:渠道页**内部自路由**,新增导航不变 `NavItem`。用 zustand 局部状态在 `channel` 模块内切 list ↔ detail。
2. **跳请求详情**:明细中"所在请求 id"点击 → **渠道详情页内就地弹出该请求详情**(复用 `getLogDetail`),不跨模块跳到日志页。
3. **数据范围**:复用保留期 7 天(`relay_log_keep_period`),本期不做 UI 时间 picker。
4. **入口**:渠道管理页卡片加「调用详情」按钮。

## 数据流与后端

### 后端查询函数 `RelayLogAttemptsByChannel`

新增于 `internal/op/log.go`:

- 签名:`RelayLogAttemptsByChannel(ctx, channelID int64, page, pageSize int) ([]ChannelAttemptDetail, int, error)` (返回明细切片 + total)。
- 实现策略:attempts 是 `relay_logs.attempts` JSON serializer 字段(`model/log.go:59`),**无法 SQL 展开**。采用「先限缩再展开」:
  1. 读 `relay_log_keep_period` 算 `cutoff = now - keep*24h`。
  2. **按 channel_id 粗筛**:relay_logs 表本身**没有 carries channel_id 索引能定位 attempts 内某 channel**。但 attempts JSON 里每条含 `channel_id`(`model/log.go:30`)。SQLite 可用 `attempts LIKE '%"channel_id":<id>%'` 做粗筛(MySQL/PG 用 `JSON_EXTRACT`/`@>`),再 Go 层精确展开过滤,大幅减少拉回量。
  3. Go 层 `json.Unmarshal` attempts → 过滤 `a.ChannelID == channelID` → 拼装 `ChannelAttemptDetail`(带 `request_id = relay_log.id`、`request_time`、`request_model`、req 级 `error`、attempt 字段)。
  4. 按 `request_time` 倒序;分页用「在过滤结果上的游标分页」——因粗筛后仍可能多,加分页上限 `pageSize`(默认 50, max 200) 与 total 估值。
- **上界保护**:粗筛后若仍超阈值(如 >5000 行待展开),截断并返回 `truncated=true` 标志,前端提示"仅展示最近 N 条"。避免 7 天大渠道打爆内存。
- **匹配口径**:用 `channel_id`(整数)匹配,不用 `channel_name`(变了名仍按 id 追溯,且避免重名歧义)。

### 后端路由与接口

新增于 `internal/server/handlers/log.go` 的 `init()`,挂在 `/api/v1/log` 组(已有 `middleware.Auth()`):

```
GET /api/v1/log/channel-attempts?channel_id=&page=&page_size=
→ { list: ChannelAttemptDetail[], total: int, truncated: bool }
```

- 与现有 `listLog`/`getLogDetail` 同组同鉴权。
- 复用 `RelayLogGet` 的 `getLogDetail` 接口处理「明细跳单条详情」时不需新接口(前端已有 `getLogDetail`)。

### 数据模型新增

`internal/model/log.go` 新增 `ChannelAttemptDetail`:

```go
type ChannelAttemptDetail struct {
    RequestID      int64           `json:"request_id"`
    RequestTime    int64           `json:"request_time"`     // relay_log.time(unix s)
    RequestModel   string          `json:"request_model"`    // request_model_name
    RequestError   string          `json:"request_error"`    // relay_log.error(请求级)
    AttemptNum     int             `json:"attempt_num"`
    Status         AttemptStatus   `json:"status"`
    ChannelID      int             `json:"channel_id"`
    ChannelName    string          `json:"channel_name"`
    ChannelKeyRem  string          `json:"channel_key_remark,omitempty"`
    ModelName      string          `json:"model_name"`
    Duration       int             `json:"duration"`         // ms
    Sticky         bool            `json:"sticky"`
    Msg            string          `json:"msg,omitempty"`
}
```

复用现有 `ChannelAttempt` 字段 + 增加请求级溯源字段。

## 前端

### 1. channel 模块内自路由(zustand)

`web/src/components/modules/channel/index.tsx`:
- 新增局部 zustand store(同目录新文件 `detail-store.ts`)或复用现有 toolbar store 模式:
  ```ts
  type ChannelView = { mode: 'list' } | { mode: 'detail'; channelID: number; channelName: string }
  ```
- `Channel` 组件依据 `view.mode` 渲染:`list` → 现有 `VirtualizedGrid`;`detail` → 新 `<ChannelCallDetail channelID=.../>`。
- 顶部标题/返回:`detail` 模式渲染「← 返回渠道列表 · <渠道名>调用详情」,`setView({mode:'list'})` 返回。返回走 store,不动主导航 activeItem。

### 2. 渠道卡片入口

`web/src/components/modules/channel/Card.tsx` / `CardContent.tsx` 卡片操作区加「调用详情」按钮(图标 `History`/`ScrollText`),`onClick` 调 `setView({mode:'detail', channelID, channelName})`。

### 3. 详情视图组件 `ChannelCallDetail`

新文件 `web/src/components/modules/channel/CallDetail.tsx`:
- 顶部概览统计条:总尝试数 + 各状态计数 badges(success/failed/circuit_break/skipped,复用现有 Badge 颜色语义;circuit_break / skipped 视觉区分,弥补 log 页那个不区分的遗憾)。
- 主体:虚拟列表 / 分页 list(复用 `VirtualizedGrid` 或简单分页),逐条行:
  `#请求id 时间 请求模型 · attempt#N [状态] 被试模型 耗时ms (sticky?)` + 错误 msg 折叠。
- 翻页:`useInfiniteQuery` 调新接口 `/log/channel-attempts`。
- **请求 id 点击** → 就地弹该请求详情(见下)。

### 4. 抽出可复用的请求详情弹窗

现 `web/src/components/modules/log/Item.tsx` 的 `LogDetailPanels`(未导出,依赖 `useMorphingDialog`)需可复用:
- **方案**:将「请求详情弹窗」抽为新组件 `web/src/components/modules/log/RequestDetailDialog.tsx`,导出 `<RequestDetailDialog open requestId onClose />`。内部:
  - 自带 `MorphingDialog` 包裹(不依赖卡片 trigger 的 context,用受控 open)。
  - 复用现有 `getLogDetail(requestId)` + 现有 `LogDetailPanels` 的渲染逻辑(双栏 + 调试面板)。把 `LogDetailPanels` 从 Item.tsx 提取/复制到新组件,改为受 `requestId` 驱动(忽略原 `log` 入参,统一从 `getLogDetail` 拿),消除对原 `useMorphingDialog` 的隐式依赖。
- channel 详情页引用该组件,点 request_id 即打开,不跳页、不依赖 log 模块路由状态。
- log 页原详情保持不动(可后续重构复用同一组件,本期不强制,避免回归风险)。

### 5. API 端点

`web/src/api/endpoints/log.ts` 新增:
```ts
useChannelAttempts(channelID, page) -> GET /api/v1/log/channel-attempts
```
带 `ChannelAttemptDetail` 类型 + `total`/`truncated`。

## 文件清单(预计改动)

**后端**:
- `internal/model/log.go` — 新增 `ChannelAttemptDetail` 结构。
- `internal/op/log.go` — 新增 `RelayLogAttemptsByChannel`。
- `internal/server/handlers/log.go` — 新增 `channelAttempts` handler + 路由注册。

**前端**:
- `web/src/api/endpoints/log.ts` — 新增类型 + query hook。
- `web/src/components/modules/channel/index.tsx` — 接入 view 状态切换。
- `web/src/components/modules/channel/detail-store.ts` — 新增 view zustand。
- `web/src/components/modules/channel/Card.tsx`(或 CardContent)— 加入口按钮。
- `web/src/components/modules/channel/CallDetail.tsx` — 新增详情视图。
- `web/src/components/modules/log/RequestDetailDialog.tsx` — 新增可复用弹窗(提自 Item.tsx 逻辑)。
- i18n 文案messages 相应 key(中英)。

## 兼容性 / 风险

- 后端粗筛 `attempts LIKE '%channel_id":N%'` 在 SQLite 等通配,JSON_EXTRACT 兼容 MySQL/PG 三库需分别适配(项目支持 SQLite/MySQL/PG)。`ChannelAttempt` 在 PG 用 `@>` 需 attempts 列为 jsonb,当前是 `serializer:json`(text-like),三库统一走 LIKE 粗筛 + Go 展开,避免 dialect 分叉。
- 7 天大渠道展开上界保护:truncated 提示,不留隐性错截。
- 抽出 `RequestDetailDialog` 若选择复制而非重构(log 页保持原样),会产生少量重复;权衡后优先不回归原 log 详情。可另起重构任务收。
- 客户端状态路由:刷新页面若无持久会丢失"当前在看哪个渠道详情"——可接受(本期不持久化 query param)。

## 回退

- 后端新接口独立、前端默认不进入 detail 视图,功能按 store 开关,可整体回退不影响主链路。
- `RequestDetailDialog` 为新文件,不影响 log 页。
