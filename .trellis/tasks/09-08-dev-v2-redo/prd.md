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
3. `09-08-v2-feat-ops-obs` — 运维刚需组+可观测组
4. `09-08-v2-feat-log-persistence` — 日志持久化包(须拦截 migrate/009 删表)
5. `09-08-v2-feat-client-theme` — 客户端识别+主题+思考等级
6. `09-08-v2-feat-codex-usage` — Codex OAuth+Usage Card+渠道包
7. `09-08-v2-smoke-switch` — 冒烟矩阵+停机切换发 v2.0.0(最终前置:1-6 全部完成)

3-6 之间无硬依赖,按 ADR-0007 建议顺序执行;每个功能包子任务须自带对 migrate 演练的影响说明(表形状是否兼容数据带走)。

## Acceptance Criteria(父任务级)

- [ ] dev-v2 分支存在且基线为 27aa40d,首个 commit 起每步可独立构建起容器
- [ ] 上游白拿项验证可用:多KEY、failover、gzip、日志页 SSE 实时流
- [ ] 5 个功能包全部移植并各自通过 octopus-verify 闭环
- [ ] 转换脚本在 data 副本上演练通过:113 渠道、channel_keys、分组、API Key、用户、设置、1284 条日志、usage_cards、o_auth_sessions 全量转换无丢失
- [ ] axonhub/llm 冒烟矩阵全项通过(ADR-0007 清单),不等价项已在新层重修
- [ ] 生产切换完成,线上跑 v2.0.0;回滚预案(镜像+备份)演练过
- [ ] 遗留③关闭:Info 页仓库指向 aks1235/octopus

## Notes

- 重做期间不追上游新提交(ADR-0001),切换后再评估
- 「管理面板立即更新」按钮继续禁用原则(glossary)
- 每个子任务完成时更新本文件的任务地图勾选状态
