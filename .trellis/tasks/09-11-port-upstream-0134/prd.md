# 移植上游 v0.13.4:全局模型过滤 + client_header + 实时日志 Key

## Goal

把上游 v0.13.2→v0.13.4 中评估为「白拿」的四项功能移植到 dev-v2,并适配 fork 形状。全程**手工移植**(参照上游 diff 重写),不做 git merge/cherry-pick——分叉大且需排除两项。

## 移植清单与排除项(2026-09-11 评估已定)

| 上游提交 | 内容 | 处置 |
|---|---|---|
| `d5a893f` | 全局模型过滤:设置 `model_filter`,拉取模型列表时与渠道级正则 AND | ✅ 移植 |
| `1c48ee5` | client_header:自定义 Header 值 `{client_header:xxx}` 占位符引用客户端请求头 | ✅ 移植 |
| `bab1a53` | 实时日志显示来源 API Key(state 加名称快照 + 前端展示) | ✅ 移植 |
| `bf2027a`/`9a80de3`/`edb4bfb` | RoundStartedAt 字段 + 前端日志样式优化 | ✅ 移植 |
| `0b919da` | 渠道禁用 fix #379 | ❌ 排除:已有自有实现(relay-routing.md 契约,单测锁定),语义更细;采纳会破坏「ChannelGrantGet 唯一裁决点」 |
| `1bd2ed8` | 依赖升级(含 axonhub/llm 0901→0909 快照) | ❌ 排除:ADR-0007 最大风险区,须单独任务重跑冒烟矩阵后再升 |

## Requirements

- R1 全局模型过滤:设置页新增 `model_filter` 正则;渠道「拉取模型列表」时与渠道级 MatchRegex 取 AND(两侧留空各自不生效);正则方言与校验对齐 group-member-regex.md 契约(regexp2 ECMAScript;前端输入预检复用 compileMemberRegex);设置校验失败返回明确错误。仅作用拉取路径,不影响手工填写的模型。
- R2 client_header:渠道自定义 Header 值支持 `{client_header:NAME}` 占位符,转发时替换为客户端同名请求头的实际值(取不到替换为空串);其余 Header 行为不变;敏感 Header 保护逻辑不变。
- R3 实时日志来源 Key:请求登记时快照 API Key 名称(查询失败留空),实时日志条目与前端展示该名称;历史日志侧已有 RequestAPIKeyName,不动。
- R4 日志实时优化:state 增加 RoundStartedAt(每轮上游请求开始时间),前端按上游 bf2027a/9a80de3/edb4bfb 的展示改善适配,与 fork 已有的 attempts 展示/故障转移标记共存不冲突。
- R5 三语 i18n(en/zh_hans/zh_hant)全部补齐;不引入上游的渠道禁用与依赖升级改动。

## Acceptance Criteria

- [x] AC1 设置页可配置 model_filter;渠道拉取模型列表时全局+渠道两级过滤同时生效(空的一侧不生效);非法正则被前后端双重拦截
- [x] AC2 自定义 Header 带 `{client_header:User-Agent}` 的渠道,转发请求头被替换为客户端真实 UA(单测锁定);不带占位符的 Header 行为零变化
- [x] AC3 实时日志页每条请求显示来源 API Key 名称;用某 API Key 发请求 → 日志页立刻可见(UI 人审)
- [x] AC4 日志页展示优化生效且 attempts/故障转移标记等 fork 功能无回归(UI 人审)
- [x] AC5 后端单测全绿 + 前端 lint/build 过 + octopus-verify 全流程(起容器 + 人审)
- [x] AC6 `git diff` 确认不含渠道禁用(0b919da)与依赖升级(1bd2ed8)的内容

## Notes

- 参考材料:上游各 commit 可 `git show <hash>` 直接读;本任务不 merge 上游分支。
- 版本随下个 release 出(与手动分组吸纳同批,预计 v2.1.0,发版时定)。
- axonhub/llm 升级另立任务(需冒烟矩阵 A2 重跑)。
