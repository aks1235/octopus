# fork→v2 数据迁移转换脚本

## Goal

交付一个 DB→DB 转换脚本(Python 标准库),把 fork 生产库(fork 线 schema,`data/data.db` 为演练副本)转换为 dev-v2(v0.13.2)schema 的全新库,并在副本上完成可重复演练,为 `09-08-v2-smoke-switch` 的停机切换提供工具与时长基准。

依据:ADR-0005(转换脚本/停机一次切/统计不带)、ADR-0003(取唯一 URL)。上游迁移链 003-012 与 fork 库分叉,原地升级不可行。

## Requirements

- 脚本 `scripts/migrate_v1_to_v2.py`,参数 `--src/--dst/--template`,仅用 Python 标准库
- 目标库 = 复制 v2 模板库(迁移链已跑完的空库,当前取 `data-v2/data.db`)后写入;不依赖运行中的应用建 schema;不得 hardcode 迁移链版本号
- 必带(ADR-0005):channels(取唯一 URL)、channel_keys、groups+group_items、api_keys、users、settings(交集 4 键)、llm_infos
- 派生:channel_models(channels.model 拆分 + 分组孤儿模型补全)、channel_grants(模型×凭据,protocols 由 base_urls[0].provider_id 映射:openai-chat→2、openai-response→4、anthropic→8)
- relay_logs/usage_cards/o_auth_sessions:**条件携带**——目标库存在该表才转换,否则跳过并在报告注明(功能包任务 4/6 落地后自动生效)
- 不带:8 张 stats 历史表;migration_records 保留目标模板自身
- 保留 fork 主键,转换后修复 sqlite_sequence;输出逐表对账报告(源行数/目标行数/跳过原因/丢弃字段)

## Constraints

- 源库只读打开(mode=ro);目标库先删后建,演练可重复(幂等:同输入同输出)
- 上游 schema 以当前 dev-v2 代码为准;后续功能包新增迁移后模板库需重新生成(记录再生成方法)
- 已知行为退化必须在报告中列明:正则路由→精确名路由、轮询组降级、丢弃字段清单(详见 research/schema-mapping.md)

## Acceptance Criteria

- [x] data/ 副本演练通过并数字对账(以演练时快照为准,生产为活库会漂移;2026-09-08 终态:117 channels / 117 channel_keys / 2236 channel_models / 2236 channel_grants / 10 groups / 233 group_items / 1 api_key / 1 user / 1003 llm_infos,外键 0 违规;孤儿补入路径已用构造用例验证 2237/2237/234)
- [x] 转换出的库被 v2 容器(smoke)正常加载:migration_records 不重跑、无启动报错;数据经 API 与库双确认(渠道 117/分组 10)
- [x] 至少一个分组发一次请求选路成功:组 9 真实上游 3.4s/2.4s 两次返回 200,统计落盘正确(完整冒烟矩阵留给 task 7)
- [x] 报告列出:请求模型名扫描及 v2 精确名命中情况(D2 终裁:不建别名组)、丢弃字段清单、relay_logs 等 3 表跳过状态
- [x] 脚本重复执行结果一致(两轮 md5 相同);换 backup API 新快照重跑同样通过
- [x] 过程中发现的 base_url /v1 双叠问题已修复并入报告(归一化 117 渠道,echo 实验实证,见 research)

## Notes

- 生产停机切换动作属于 `09-08-v2-smoke-switch`,本任务只交付脚本+演练
- 两个开放决策(轮询组映射、正则别名组)见 design.md,演练前须定案
