# ADR-0002: 功能移植范围

- 状态: 已接受(2026-09-08)
- 关联: ADR-0003、ADR-0004

## 背景

dev(fork)自 2026-04 基点后累计 180+ 自有/chenjh commit。经逐项与上游 v0.13.2 对照:用户 7-31 后的 bug 修复(streaming 误报族、SSE 族)上游已修且更彻底(`10f0204`、`1ab744f`);chenjh 的 139 个 commit 在上游全部不存在。上游白拿项:单渠道多KEY(单URL)、failover、gzip、分享功能、渠道统计重设计、vite、移动端、信任设备登录。

## 决策

移植进 dev-v2(上游均无):

| 包 | 内容 | dev 出处 |
|---|---|---|
| 运维刚需组 | 同步失败标记+自动禁用/解禁、GroupItem 孤儿对账、自动分组默认正则匹配 | `55ffcbf` `6f8e156` `cf39ae2` |
| 可观测组 | 渠道调用详情页(attempts 明细)、分组列表渠道名 DTO | `f551b95` `8185f12` |
| 日志持久化包 | 日志入库、API Key/模型多选筛选、详情按需加载 | 见 ADR-0005 |
| 识别+主题+思考等级 | 客户端 UA 识别+图标、Claude 主题+选择器、分组覆盖思考等级(xhigh/max) | `a6c2e4f`系 `e0270ff` `c912f5e` `7a37d98` |
| Codex/Usage/渠道包 | Codex OAuth 渠道、Usage Card、auth 文件导入、密钥行 Switch、Key 轮询+Key 级统计、模型连通性测试+选择对话框 | `c9ba27f` `73a13a2` `bb33f79` `ba5deca` `f9534b5` 等 |

不移植:

- 日志一键刷新(`856432d`):上游日志页已是 SSE 全生命周期实时流,无此痛点
- 统计增强包(TPS/RPM 实时监控、模型排行榜、趋势图密钥筛选):上游统计已够用
- updatePrice.py 去重(`0fafab7`):需要时随时可补
- 多 URL、uTLS:见 ADR-0003/0004

## 后果

- 移植量集中在 5 个功能包,每包独立可验证(配合 ADR-0007 分批)
- chenjh 的老 transformer 层修复(DeepSeek thinking 族、Anthropic 透传族等)不可移植,转为行为冒烟验证项(见 ADR-0007 ④)
- 统计历史表(fork 8 张)不迁移,相关 UI 无数据支撑自然作废
