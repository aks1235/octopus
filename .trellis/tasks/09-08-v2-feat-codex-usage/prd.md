# v2 渠道 Key 运营包(原「Codex/Usage/渠道包」收缩版)

## Goal

在 dev-v2(上游 v0.13.2)上交付本包经生产实况核对后仅存的存量需求——渠道 Key 级可观测与按 Key 连通性测试;处置(关闭/推迟)其余登记项,完成 ADR-0002 第三次勘误与父任务地图同步。

## 范围裁定(2026-09-09 与用户两轮对齐)

依据:research/ 四份研究 + fork 生产库只读核对(usage_cards 0 行、o_auth_sessions 0 行、JSON 凭证 key 0 条——117 key 全为普通 API key;5 个名字带 codex 的渠道均为中转站渠道,非 ChatGPT 账号 OAuth)。

| ADR-0002 登记项 | 裁定 | 依据 |
|---|---|---|
| Codex OAuth 渠道 | **推迟** | 生产零 OAuth 账号、用户无官方 Codex;库白拿复活路径已存档 research/upstream-v2-reality.md §6(axonhub codex transformer+oauth 全套+DecodeAuthJSON),复活时授权模式预选设备码流 |
| Usage Card | **推迟** | 生产 0 张卡片;表不建,迁移条件携带(migrate_v1_to_v2.py:67)自然跳过,零影响 |
| auth 文件导入 | **推迟** | 依附 Codex 渠道,同命运 |
| 密钥行 Switch(bb33f79) | **白拿关闭** | 上游 FormKeys.tsx:113 每行凭据原生已有 enabled Switch |
| Key 轮询(ba5deca 轮询语义) | **不做** | 用户无多账号、无官方 Codex;生产 key:channel≈1:1 无分摊需求;上游 sticky+冷却原生兜底 |
| Key 级统计(ba5deca 读侧) | **移植(本包主件①)** | 写侧原生完整(ChannelKey 内嵌 StatsMetrics,relay 三处累加+定时落库);读侧端点/UI 全仓空白;117 key 真实运营需求 |
| 单 Key 测试(ba5deca) | **移植(本包主件②)** | 上游 probe 只 GET /models 拉列表,不发真实请求 |
| 模型连通性测试+选择对话框(f9534b5) | **不移植** | 挑选痛点已被探测结果自动合并消解(probe.ts:34-42);「发真实请求」的残余价值并入主件② |
| 9c06c03 渠道内穷举重试 | **不移植** | 上游 grant 级 failover 原生覆盖且更细(cooldown/恢复探测/亲和) |
| 4d569fc Provider 预设 | **排除** | v2 无 provider 概念,预设已前端化(channel-presets.tsx);OAuth 若复活走库 |

## Requirements

### R1 Key 级统计读侧

- 渠道维度新增按 key 粒度的统计读取端点(数据已在库:ChannelKey 内嵌 StatsMetrics 七列)
- 前端逐 key 可见:success/failed 计数、input/output token、cost、wait_time(与渠道/模型统计同口径,web/src/api/stats.ts:31 注释所称「凭据口径展示入口」)
- 入口形态不限(渠道统计页扩展 / 渠道卡展开),以最小改动贴合现有页面组织

### R2 单 Key 连通性测试(2026-09-10 用户 UI 验证后修订)

- 按渠道×key×模型发最小真实请求(非流式、max_tokens=1 量级、30s 超时),经 axonhub outbound 构请求——与转发链路同构,测到的才是真链路
- **默认单模型测活**:⚡ 只对该凭据有授权的第一个模型发 1 个请求(部分中转/公益站不允许测活,全量请求有风险);「测全部模型」为第二入口
- **协议位精确化**:按「模型×凭据」实际授权协议位构请求,查不到才回退全协议位(不再无差别全试)
- **测试请求写入 relay_logs**:每被测模型一条,ClientName 标记「面板测试」(与 claude-code 等客户端标识同位置),可审计可追溯;零表结构变更
- 结果逐模型可见(成功/失败+错误摘要);不引入模型选择对话框

### R3 白拿验证(Switch)

- 容器实测留档:凭据行 Switch 禁用后该 key 不参与选路、统计保留(对照 model/channel.go:64 注释语义)

### R4 文档与任务地图

- ADR-0002 追加第三次勘误(上表+理由;复活路径指向本任务 research/)
- 父任务 prd 任务地图第 6 项标注收缩结论;父任务 AC「5 个功能包全部移植」措辞相应更新
- research/ 追加「复活预选决策」备注:设备码流授权、(若届时有多账号)分组轮询策略——均为当时方案,未实施

## 约束

- 不建新表、不动迁移脚本:usage_cards/o_auth_sessions 不建,CONDITIONAL_TABLES 条件携带继续跳过
- 推迟项零半成品:不建死列、不建空包、不预留接口
- 遵循 v2 代码风格(中文注释、op/handler 分层、前端 use-intl 文案)
- R1/R2 相互独立,各自成 commit,每步可独立构建起容器(父任务 AC)
- 本地验证用 data-verify 隔离目录,勿动 ./data

## Acceptance Criteria

- [x] AC1 Key 统计端点对多 key 渠道返回全量 key 统计,数值与渠道统计同口径可对账(单测+UI)
- [x] AC2 前端逐 key 查看 success/failed/token/cost/wait_time(UI 人审 2026-09-10)
- [x] AC3 单 Key 测试:⚡ 默认仅测 1 个模型(该凭据首个授权模型);可用渠道返回成功;坏 base_url/坏 key 返回失败+错误摘要(单测+UI)
- [x] AC4 「测全部模型」入口在多模型渠道完成逐模型测试,结果逐行可见(UI 人审)
- [x] AC4b 测试请求在日志页可见,客户端标识为「面板测试」,与真实转发流量可区分(UI 人审)
- [x] AC5 Switch 白拿验证留档(research/verify-ui-2026-09-10.md)
- [x] AC6 ADR-0002 第三次勘误、父任务地图、复活决策备注均落盘
- [x] AC7 octopus-verify 闭环通过(容器编译+后端测试+前端 lint+build+起容器+健康检查+人看 UI;marker 已写)
