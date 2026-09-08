# fork→v2 schema 映射研究笔记(2026-09-08)

数据源:`data/data.db`(fork 生产副本,只读)、`data-v2/data.db`(dev-v2 迁移链 3-12 跑完的真实模板)、fork 代码 `git show dev:`、v2 代码当前 worktree。

## 库概览

| | fork(data/data.db) | v2(data-v2/data.db) |
|---|---|---|
| 表数 | 21(含 sqlite_sequence) | 15(含 sqlite_sequence) |
| 关键差异 | base_urls JSON 多URL、mode 整数、8 张 stats 表、relay_logs/usage_cards/o_auth_sessions 存在 | channel_models/channel_grants 拆表、mode 文本、relay_config JSON、4 张 stats 表、无 relay_logs(009 已 DROP) |

## fork 库数据事实

- channels 117(93 启用/24 禁用),**全部单 URL**(base_urls JSON 数组长度均为 1),`base_url`/`key`/`type` 列全空或 NULL
- base_urls[0].provider_id 分布:`openai-chat` 115、`openai-response` 2;无 anthropic
- 每渠道恰好 1 把 channel_key(117 把,全部启用),remark 全空
- channels.model 为逗号分隔清单,合计 2236 个模型名,31 渠道模型列表为空;custom_model 全空
- custom_header 16 渠道为字面 `'[]'`(两版 JSON 数组形状兼容);param_override/channel_proxy/match_regex 全空
- groups 10:mode 3(故障转移)×7、mode 1(轮询)×3;first_token_time_out 30/60、retry_interval 1、session_keep_time 0、active_item_id 全 0;reasoning_effort_override 有 'high'/'max' 各一
- group_items 234,含权重列;**1 条孤儿**:gi 8155 引用 channel 90 的 'deepseek-v4-pro' 不在该渠道 model 清单里
- api_keys 1、users 1(bcrypt)、llm_infos 1003(fork 多 context_length/max_output_tokens 两列)
- settings 12 键 ∩ v2 4 键 = `proxy_url`、`stats_save_interval`、`cors_allow_origins`、`model_info_update_interval`
- relay_logs 1285、usage_cards 0、o_auth_sessions 0;8 张 stats 表有零星历史

## v2 侧语义(代码确认)

- `Protocol` 位掩码(internal/model/channel.go:8-10):OpenAIChatCompletion=2、OpenAIResponse=4、AnthropicMessage=8,1<<0 保留;归属"模型×凭据"组合(channel_grants.protocols)
- `Dialect` 当前仅 `"generic"`
- channel_keys.name 渠道内唯一(NOT NULL);channel_models (channel_id,name) 唯一;channel_grants (model_id,key_id) 唯一
- groups.mode ∈ {manual, failover}(internal/model/group.go:6-9);relay_config JSON 字段:member_max_attempts(默认2)/member_retry_interval_seconds(3)/member_non_stream_response_timeout_seconds(120)/member_stream_first_event_timeout_seconds(30)/member_cooldown_seconds(60)/member_affinity_seconds(300)
- 请求路由:`op.GroupGetByName(metadata.Model)` **精确名匹配**(internal/relay/handler.go:70)
- **fork 请求路由同样是精确名匹配**(`internal/op/group.go` 的 `GroupGetEnabledMap` → `groupMap.Get(name)`,2026-09-08 实现代理读 dev 代码确认并经主会话复核)。~~fork 是正则路由~~——此结论错误,已证伪:match_regex 仅用于自动分组同步与模型抓取。两版路由语义等价,不存在「正则→精确名」退化
- migration_records:fork 1-10,v2 链 3-12;目标库保留自身记录即不再重跑迁移

## fork 侧语义(dev 分支代码确认)

- GroupMode:1=轮询、2=随机、3=故障转移(按优先级)、4=加权(internal/model/group.go)
- base_urls 元素形状:`{"url","delay","type","provider_id"}`

## 行为退化清单(转换后不可避免/需决策)

1. ~~正则路由→精确名~~ **已证伪**:fork 请求路由本就是精确名匹配,两版等价。原分析把 match_regex 的同步用途误当路由用途;`deepseek-ai/deepseek-v4-flash-0731` 那条日志实为 `model not found` 失败请求(channel_id=0)。D2 别名组的定性由「保等价」改为「新增路由能力」,需用户重新裁决。
2. **轮询组(3 个)无 v2 等价**:failover 保持全员可用但无负载分摊;manual 钉死单成员。(已决策 D1→failover)
3. 丢弃字段:group weight/retry_interval(语义并入 relay_config)/session_keep_time/reasoning_effort_override/match_regex(仅同步用途,无路由损失);channel sync_fail_count/last_sync_error/last_sync_at/auto_disabled/auto_sync/auto_group/utls;channel_key status_code/last_use_time_stamp/total_* 统计;llm_infos 两列。
4. fork settings 其余 8 键为功能包设置,随包迁移时由各自任务处理。

## base_url `/v1` 后缀归一化(2026-09-08 冒烟发现,关键)

- **axonhub 出站转换器对 BaseURL+EndpointPath 朴素拼接**;v2 各协议路径字段自带 `/v1` 前缀(如 `/v1/chat/completions`),而 fork 的 base_urls 全部带 `/v1` 后缀(117/117)→ 直接平移会拼出 `/v1/v1/chat/completions`,上游 404
- 症状极具迷惑性:全成员失败后**应用按设计无限等待重试(不向客户端返回错误)**(internal/relay/handler.go 的 for 循环无总失败出口),客户端表现为挂死而非报错;统计列可见 request_failed 在涨
- 判决性实验:本地 echo 服务器承接渠道出站,直接看到应用请求 `POST /v1/v1/chat/completions`
- 修复:转换脚本归一化 base_url(去结尾 `/v1`);修复后组 9 真实上游 3.4s 返回 200
- 遗留认知:切换后若某渠道上游 404/挂死,客户端会**挂起不报错**——监控渠道 request_failed 增长是主要信号(与 task 7 冒烟矩阵相关)

## 质量检查阶段结论(2026-09-08,trellis-check)

- 映射 #1-#11 与两库真实 schema 逐列比对一致;退出码/回滚/只读保证实测通过(4 类错误路径均 exit 2 且 dst 不残留)
- **条件携带路径已实测**:合成含空 relay_logs 表的模板跑真实转换,1285/1285 条转入、15 个列丢弃正确、FK 0 违规——任务 4(日志持久化)落地后直接可用
- 修复:copy_template_to_dst 的 ro 打开不在重试窗口内(smoke 容器活写 data-v2/data.db 时实测间歇 disk I/O error),已纳入重试(3→5 次)
- 模板干净度检查扩展到条件携带三表(防模板残留行与 fork 行混装)
- 残余注意:正式切换的模板应从静止容器重新生成;报告阶段 ro 连接会在 dst 旁建空 -wal/-shm,切换拷贝时删掉伴随文件即可

## 活库与快照(2026-09-08 演练发现)

- `data/data.db` 是生产容器 octopus-new 正在写的**活库**(WAL 2.9MB)。演练快照必须用 SQLite backup API(ro 连接)而非 cp——活写下 cp 可能拿到不一致副本。正式切换时应用已停,无此问题
- 源数据随时间漂移:研究时点的孤儿 gi 8155 已被 fork group_reconcile(60s)自动清除,演练快照为 233 group_items / 2236 models / 2236 grants。**验收数字以切换窗口实际快照为准**,脚本对账逻辑已覆盖孤儿补入路径(构造用例复验 234/2237/2237 通过)
