# 技术设计:fork→v2 转换脚本

## 总体形状

```
python3 scripts/migrate_v1_to_v2.py --src data/data.db --dst out/data.db --template data-v2/data.db
```

- 单文件 Python 脚本,仅标准库(sqlite3/json/argparse/shutil)
- 流程:校验源库与模板库 → 复制 template→dst(先删旧 dst)→ 预清理(dst 的 users/settings 交集键)→ 逐表转换(显式主键插入)→ 修复 sqlite_sequence → 条件表处理 → 打印对账报告
- 源库全程 `file:...?mode=ro`;目标库事务包裹,任一步失败整体回滚并删除 dst

## 模板库策略

- 模板 = 迁移链跑完的 v2 空库(当前 `data-v2/data.db`:migration_records 3-12、settings 4 默认键、stats 零值行)
- 再生成方法(功能包加迁移后):用当前 dev-v2 镜像对空 data 目录起一次容器,取生成的 data.db
- 预清理:DELETE FROM users(模板含演练期用户);settings 只覆盖交集 4 键;stats/migration_records 不动

## 逐表映射

| # | fork → v2 | 规则 |
|---|---|---|
| 1 | users(1) → users | 原样:id/username/password |
| 2 | api_keys(1) → api_keys | 原样全列(schema 相同) |
| 3 | llm_infos(1003) → llm_infos | 取 name/input/output/cache_read/cache_write,丢 context_length/max_output_tokens |
| 4 | channels(117) → channels | base_urls[0].url→base_url 并**归一化:去掉结尾 `/v1`(或 `/v1/`)**——axonhub 出站转换器对 BaseURL+EndpointPath 朴素拼接,v2 路径自带 `/v1` 前缀,fork URL 的 `/v1` 后缀会造成 `/v1/v1/...` 双叠(2026-09-08 echo 实验实证,上游 404→全成员失败→应用按设计无限等待重试→客户端挂死);dialect='generic';三个协议路径留默认;保留 name/enabled/proxy/channel_proxy/custom_header/param_override/match_regex;stats 列置 0;丢弃 model/custom_model/auto_sync/auto_group/sync_fail_count/last_sync_error/last_sync_at/auto_disabled/type/base_urls/base_url旧列/key/utls。渠道协议位(供 #7):base_urls[0].provider_id ∈ {openai-chat→2, openai-response→4, anthropic→8},未知值报错中止 |
| 5 | channels.model → channel_models(2236+1) | 按逗号拆分去空白去重;再补入 group_items 引用但渠道清单缺失的模型(生产仅 channel 90 + 'deepseek-v4-pro' 一例) |
| 6 | channel_keys(117) → channel_keys | id/channel_id 保留;channel_key→key;name='default'(每渠道恰好 1 把,渠道内唯一约束满足);enabled 保留;stats 置 0;丢 status_code/last_use_time_stamp/total_*/remark |
| 7 | (#5 × #6) → channel_grants(2237) | 每渠道 每模型×每凭据 一条,protocols=渠道协议位 |
| 8 | groups(10) → groups | id/name 保留;mode:3→'failover',1→'failover'(轮询降级,报告标注;见开放决策 D1);active_item_id 保留(生产全 0);relay_config 显式六字段 JSON:first_token_time_out→member_stream_first_event_timeout_seconds(0 取默认 30)、retry_interval→member_retry_interval_seconds(0 取默认 3)、session_keep_time→member_affinity_seconds,其余取默认(2/120/60);丢 match_regex/reasoning_effort_override |
| 9 | group_items(234) → group_items | group_id 保留;(channel_id,model_name)→channel_model→其唯一 grant(每渠道 1 key 故唯一)→channel_grant_id;priority 保留;weight 丢;解析失败的条目报错中止(而非静默丢) |
| 10 | settings → settings | 仅覆盖交集 4 键(proxy_url/stats_save_interval/cors_allow_origins/model_info_update_interval)为 fork 值;其余 8 个 fork 键属功能包,随各自任务处理 |
| 11 | relay_logs(1285)/usage_cards(0)/o_auth_sessions(0) | 条件携带:查 dst 的 sqlite_master,表存在→按届时确认的列映射转换;不存在→跳过并报告行数。当前模板必然跳过 relay_logs(009 已 DROP) |

不迁:stats 8 张历史表(ADR-0005)、migration_records(目标自带)、sqlite_sequence(重建)。

## 主键与序列

- 全部显式保留 fork 主键插入;完成后对每张有 AUTOINCREMENT 的表 `UPDATE sqlite_sequence SET seq=max(id)`,避免新插入撞键
- relay_logs 条件携带时同样保留 id

## 对账报告(脚本 stdout)

- 每表一行:表名/源行数/目标行数/状态(转换|跳过|不带),数量不一致→exit 1
- 附加:渠道协议位分布、孤儿模型补入清单、轮询降级分组清单、仅正则命中的请求模型名(查 relay_logs distinct request_model_name 对 v2 组名精确匹配),丢弃字段汇总

## 演练与验证流程

1. 源快照:活库必须用 SQLite backup API(ro 连接)生成副本,`cp` 活库不安全(见 research);正式切换时应用已停,无此约束
2. 跑脚本 → dst 库 + 对账报告全绿
3. 用 smoke compose 起容器挂 dst 库:启动无迁移重跑/无报错,渠道页 117 条、分组页 10 条、编辑页字段正常
4. 页面选一个分组手动发一次请求,验证选路与转发
5. 重跑一次脚本,diff 两轮 dst 库(除可能的随机因素外应一致)

## 已定决策(2026-09-08 用户确认)

- **D1 轮询组映射**(3 个 mode=1 组):→ **failover**,全员保持可路由,成员顺序=原优先级;报告标注丢负载分摊。
- **D2 正则别名**:~~建别名组~~ **终裁改为不建**(前提更正后用户重新裁决:fork 路由本就精确名匹配,该名字在 fork 上一直 404,别名组属新增能力而非保等价;切换日零意外原则)。脚本保留 relay_logs 请求模型名扫描作纯报告,精确名未命中的名字迁移后仍 404,与 fork 行为严格一致。

## 风险与回退

- 脚本只读源库、目标先删后建,误操作半径为零;生产切换前另有全量备份(ADR-0005)
- 模板库过期风险:功能包落地新增迁移后必须重新生成模板,脚本校验 dst 的 migration_records 与模板一致即免
- v2 某列出现 NOT NULL 而映射未覆盖 → 插入报错事务回滚,报告指出表名列名(宁可失败不可半成品)
