# 日志页体验三连:token 速度 / 实时过滤 / debug 模式

## Goal

补齐 v2 日志页相对 v1 的三处体验缺口:输出速度不可见、进行中请求被冲走难找、长请求体详情卡顿。单个任务三组 AC,同一模块(日志页)一起交付。

## Background(2026-09-18 用户提出)

- **速度**:v1 有 tok/s 展示(`formatRate`);v2 `relay_logs` 已存 `output_tokens`/`use_time`(ms)/`ftut`(首字 ms),数据齐但没算没显示。
- **实时过滤**:实时面板(`web/src/components/modules/log/index.tsx` LivePanel)无过滤,长任务被新请求冲到下面,用户不知道有几个在跑。`RequestState` 已有 `running` 状态(`web/src/api/log.ts`)。
- **卡顿**:详情弹窗(`RequestDetailDialog.tsx`)无条件 `JSON.parse` + 渲染完整 `request_content`,几千条 messages 的请求打开即卡;用户要"只看这次流新增的"(即响应),请求体按需才看。

## Requirements

### A. Token/s 输出速度

- A1 历史日志卡片与详情页显示输出速度:`output_tokens / ((use_time - ftut) / 1000)` tok/s;`ftut` 为 0(无首字数据,如非流式)时回退 `use_time`。
- A2 仅当 `output_tokens > 0` 且分母 > 0 时显示;不满足不显示(不留 "0 tok/s" 噪音)。
- A3 千位/百万位缩写格式(K/M tok/s),口径对齐 v1 `formatRate`(`git show v1.5.3:web/src/lib/utils.ts`)。

### B. 实时日志过滤

- B1 实时面板加「只看进行中」开关(状态 = `running`);开启后列表只剩进行中的请求。
- B2 面板头部常显「进行中 N」计数徽标(N = 当前 running 数),一眼知道有几个在跑。
- B3 过滤为纯前端视图状态,不动 SSE 数据流;断线/空态文案逻辑保持。

### C. 详情弹窗简洁/调试两档

- C1 详情弹窗默认**简洁模式**:显示响应内容、token/速度/耗时等元信息;`request_content` 不渲染(收起),只显示大小摘要(如「请求体 1.2 MB,点开查看」)。
- C2 「调试模式」开关(记忆于会话内即可,不持久化):开启后渲染完整请求体(现状行为)。
- C3 请求体渲染后置到用户点开时,默认路径零 `JSON.parse` 大对象,长请求打开不再卡。
- C4 `debug_content` 既有折叠行为保持不变。

## Non-goals

- 不做日志服务端过滤 API(实时过滤纯前端)。
- 不动日志持久化 schema(速度由现有字段推导)。
- 不做跨页持久化的 UI 偏好(开关记忆仅限当前页面生命周期)。

## Acceptance Criteria

- [ ] AC-A 有输出 token 的日志显示 tok/s,K/M 缩写正确;非流式/无 token 日志不显示噪音值
- [ ] AC-B 「只看进行中」开关生效;计数徽标与实际 running 数一致;关闭开关恢复全量
- [ ] AC-C 默认打开长请求体日志详情不卡(请求体不渲染);调试模式点开后行为与现状一致
- [ ] AC-D i18n 三语(开关/徽标/模式文案)
- [ ] AC-E octopus-verify 自动关卡通过;UI 人审三项逐一看

## Notes

- 全部前端改动(`web/src/components/modules/log/` + i18n);无后端、无 schema、无 API 变更。
- 本地已攒 4 个未发提交(游标/轮询/中止记账/过滤黑名单),本任务发版时一并走 v2.2.0。
