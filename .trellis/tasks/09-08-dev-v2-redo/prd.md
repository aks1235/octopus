# dev-v2 路线A重做:基于上游v0.13.2

## Goal

以 `upstream/master@27aa40d`(v0.13.2)为基线新建 `dev-v2` 分支,按既定决策移植 fork 自有功能,完成数据迁移与生产切换,首发 **v2.0.0**。

**所有决策已达成共识并落盘 `docs/adr/0001-0008`,术语见 `docs/glossary.md`。本 PRD 只承载任务地图与跨子任务验收,技术方案细节以 ADR 为准。**

## Requirements(决策索引)

| ADR | 决策 |
|---|---|
| 0001 | dev-v2 钉 27aa40d;dev 冻结留 v1.0.x 热修;feat/my-features 仅参考 |
| 0002 | 移植范围:5 个功能包;明确排除项(一键刷新/统计增强/updatePrice 去重/多URL/uTLS) |
| 0003 | 多 URL 不移植,迁移取唯一 URL(生产 0 使用) |
| 0004 | uTLS 不带入(CF 1010 根因是客户端缺 UA) |
| 0005 | 转换脚本 fork库→新库;停机一次切;统计不带;relay_logs/usage_cards/o_auth_sessions 随包保留 |
| 0006 | 首发 v2.0.0 |
| 0007 | 基线先行分批;axonhub/llm 冒烟矩阵为最大风险区 |
| 0008 | deploy.sh/compose/release.yaml 平移适配 vite;仓库指向 4 处改 aks1235 |

## 任务地图(子任务,按依赖序)

1. `09-08-v2-baseline-assets` — ✅ 完成(2026-09-08,commit `e86d8d9`:基线+资产+指向+lint 修复;smoke 8081 验证过,marker 已写;生产 compose 事故已复盘——同目录 compose 项目名冲突,已用独立 name 修复)
2. `09-08-v2-db-migration` — ✅ 完成(2026-09-08:`scripts/migrate_v1_to_v2.py` 落地;副本演练对账全绿 117/117/2236/2236/10/233;容器加载+真实上游选路 200;幂等+新快照重跑通过;发现并修复 base_url `/v1` 双叠坑(echo 实验实证);条件携带路径已用合成模板实测 1285 条;用户页面检查通过)
3. `09-08-v2-feat-ops-obs` — ✅ 完成(2026-09-09:渠道健康检查+失败自动禁用/解禁、分组正则成员落地;②⑤验证上游原生覆盖关闭;范围裁定详见 ADR-0002 勘误;AC1-7 全实测过,人审 UI 确认)
4. `09-08-v2-feat-log-persistence` — ✅ 完成(2026-09-09:009 删表拦截+RelayLog 全列(fork 形状)建表;relay 终态 defer finalize 落库+attempts 插桩;历史查询四接口+同页 tab(实时零改动)+渠道调用详情页;fork 行级 SSE 裁剪不移植;单测 10 个;迁移演练 1285 条全量「转换」零丢列、设置交集补带 relay_log_keep_*;cleanup/开关容器实测;UI 人审通过;规划期发现渠道禁用不生效缺陷,另立 `09-09-v2-fix-channel-disable-routing`)
5. `09-08-v2-feat-client-theme` — 客户端识别+主题+思考等级
6. `09-08-v2-feat-codex-usage` — 渠道 Key 运营包(收缩:Key 级统计读侧+单 Key 测试;Codex OAuth 渠道/Usage Card/auth 导入推迟——生产零使用,复活路径存档其 research/;Switch 白拿、Key 轮询不做;ADR-0002 第三次勘误)
7. `09-08-v2-smoke-switch` — 冒烟矩阵+停机切换发 v2.0.0(最终前置:1-6 全部完成)

3-6 之间无硬依赖,按 ADR-0007 建议顺序执行;每个功能包子任务须自带对 migrate 演练的影响说明(表形状是否兼容数据带走)。

## Acceptance Criteria(父任务级)

- [ ] dev-v2 分支存在且基线为 27aa40d,首个 commit 起每步可独立构建起容器
- [ ] 上游白拿项验证可用:多KEY、failover、gzip、日志页 SSE 实时流
- [ ] 5 个功能包按 ADR-0002 勘误后范围交付,各自通过 octopus-verify 闭环(0002 三次勘误:ops-obs 重想象、client-theme 收缩、codex-usage 收缩)
- [ ] 转换脚本在 data 副本上演练通过:113 渠道、channel_keys、分组、API Key、用户、设置、1284 条日志全量转换无丢失(usage_cards/o_auth_sessions 随 0002 第三次勘误不再建表,条件携带跳过;2026-09-08 演练时曾验证携带无丢失)
- [ ] axonhub/llm 冒烟矩阵全项通过(ADR-0007 清单),不等价项已在新层重修
- [ ] 生产切换完成,线上跑 v2.0.0;回滚预案(镜像+备份)演练过
- [ ] 遗留③关闭:Info 页仓库指向 aks1235/octopus

## Notes

- 重做期间不追上游新提交(ADR-0001),切换后再评估
- 「管理面板立即更新」按钮继续禁用原则(glossary)
- 每个子任务完成时更新本文件的任务地图勾选状态
