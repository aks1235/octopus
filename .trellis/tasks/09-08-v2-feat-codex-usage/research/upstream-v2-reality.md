# Research: 上游 v0.13.2 实况(dev-v2 工作树)

- **Query**: 渠道/Key 数据模型、凭据探测、统计能力、OAuth 机制;对本包 6 子项哪些白拿/部分替代/空白
- **Scope**: internal(dev-v2 工作树 = 上游 v0.13.2 + 已移植包)
- **Date**: 2026-09-09

## 1. 渠道与 Key 数据模型

`internal/model/channel.go`:

- `Channel`(channel.go:44):嵌入 `ChannelConfig` 平铺成列——**单 BaseURL**(:32)+ 三协议路径(openai_chat_completion_path/openai_response_path/anthropic_message_path,:33-35)+ `Dialect`(仅 `generic`,:19-21)+ Proxy/ChannelProxy/CustomHeader/ParamOverride/MatchRegex;健康列(HealthFailCount/AutoDisabled 等,:53-56,ops-obs 包产物)
- `ChannelKey`(channel.go:68):`ChannelKeyConfig{Name(渠道内唯一), Key, Enabled}`(:61-65)+ **内嵌 `StatsMetrics`**(input/output token、cost、wait_time、request_success/failed)
- `ChannelModel`(channel.go:76):渠道的模型清单,也内嵌 StatsMetrics
- `ChannelGrant`(channel.go:85):**授权 = 模型×凭据×协议位掩码,转发最小单位**;唯一索引 (channel_model_id, channel_key_id)
- `ChannelDetail`(channel.go:98):编辑表单读写同构副本——keys 只给 `ChannelKeyConfig`(**不含统计**),grants 按名称引用
- 迁移:011(multi-KEY 重构,建 grants)、012;**无 usage_cards、无 o_auth_sessions**

结论:**多 Key 是一等公民**(比 fork 的"渠道下一堆同质 key"更进一步:每个 key 有名字、按 key 授权模型);fork 的 `ChannelKey.remark/status_code/last_use_time_stamp/total_*` 列全部不存在,统计换成统一的 StatsMetrics 七列。

## 2. 转发链路与 key 选择

- `internal/relay/channel.go:24 buildOutbound(channel, grant, channelKey, want)`:按 grant 协议位选出站协议,用 **axonhub/llm 库**构造 transformer(openai/responses/anthropic),凭据用 `auth.NewStaticKeyProvider(channelKey.Key)`(:41)——**静态 key 注入,无 OAuth 概念**
- `internal/relay/route.go:70 pickGroupItem`:**分组级选路**——sticky 优先级 + 冷却(Cooldowns)+ 恢复探测(ProbeItemID 单名额)+ 亲和(AffinityUntil)。**无轮询/负载分摊**:成员按 Priority 升序,首个可用者成为 CurrentItemID 并保持到失败
- `internal/relay/handler.go:100-380`:每轮重读分组 → pickGroupItem → ChannelGrantGet → buildOutbound → sendPassthrough/sendConverted;失败记 `ChannelAttempt{ChannelKeyID, ChannelKeyRemark(key名)}` 并 `op.ChannelKeyStatsUpdate(channelKey.ID, metrics)`(handler.go:242/295/377)
- **无熔断器**(fork 的 channel:key:model 熔断在 v2 不存在),替代物是冷却+恢复探测
- 渠道内同模型多 key 的上游原生表达:同模型多 key 各建一条 grant,再作为多个 group 成员;但选路是 sticky 优先级,不是 round-robin

## 3. Key 级统计(上游已有程度)

- 写侧完整:`ChannelKeyStatsUpdate`(`internal/op/stats.go:284`)累加 + 定时落库(StatsSaveDBTask);relay 三处调用
- 展示侧:`ChannelStats`(model/channel.go:119)只到渠道+模型粒度;`/channel/detail` 只给 ChannelKeyConfig;**全仓无 key 统计读取端点/UI**(grep handlers 与 web/src 均无)。`web/src/api/stats.ts:31` 注释"渠道、渠道模型和渠道凭据各自维护独立计数,均按同一口径展示"——凭据口径的展示入口尚未建设
- key 名出现在:group 编辑器(`group/Editor.tsx:211`)、日志项(`log/Item.tsx:289` `channel_name · key_name`)、CallDetail(attempts 带 key 名)

## 4. 凭据探测/模型刷新机制

- `internal/probe/models.go`:共用探测基建;`FetchModels(config, keys)`(:51)**串行试每个启用 key × OpenAI/Anthropic 两侧 /models 端点,任一 2xx 即健康**;宽容量化("key %q openai: …" 聚合错误,200 字符截断)
- 编辑器内:`web/src/components/modules/channel/probe.ts useModelProbe`——**凭据行内"探测"按钮**,POST /channel/fetch-model(带整份 ChannelConfig + key),返回 [{name, protocols}];结果**直接合并**进表单 models+grants(probe.ts:34-42),未探到的本地模型保留
- 健康任务:`internal/task/health.go`(ops-obs 包已移植)——定时探测、连续失败达阈值自动禁用、恢复自动解禁
- **空白**:无"发真实请求验证模型可用"的连通性测试;无模型选择对话框;探测仅 GET /models

## 5. 统计页现有能力

- 后端 `handlers/stats.go`:daily/hourly/total/apikey 四端点
- 渠道维度:`/channel/stats`(ChannelStats:渠道+各模型)、`channel/Stats.tsx`(389 行,模型排序 cost/count/tokens)
- 无 TPS/RPM、无趋势图 key 筛选、无模型排行榜(那是被排除的统计增强包)

## 6. OAuth:上游为零,但 axonhub/llm 库有全套

**仓内零 OAuth**:`grep -ri oauth internal/ web/src` 无命中;无 provider 概念、无 presets 后端(前端 `web/src/lib/channel-presets.tsx` 13 个服务商纯预填)。

**`github.com/looplj/axonhub/llm` 库**(go.mod 钉 v0.0.0-20260901162339-94e0d7c781e4):

- `oauth/` 包:`TokenProvider`(Exchange/Get/EnsureFresh/StartAutoRefresh,单飞去重)、`OAuthCredentials{client_id,access_token,refresh_token,id_token,expires_at,…}`、`ParseCredentialsJSON`、`DeviceFlowProvider`(设备码流)、Form/JSON 两种 `ExchangeStrategy`
- `transformer/openai/codex/`:**完整 Codex 出站转换器**——`OutboundTransformer`(Params{TokenProvider, BaseURL, Transport}),复用 responses 出站构请求,SSE only,支持 WebSocket;常量与 fork 完全一致:`AuthorizeURL=auth.openai.com`、`ClientID=app_EMoamEEZ73f0CkXaXp7hrann`、`RedirectURI=http://localhost:1455/auth/callback`、codexBaseURL=`https://chatgpt.com/backend-api/codex#`;`DefaultModels()` 静态模型表(gpt-5 系 17 个,**注释明说 Codex 后端无稳定公开 /models 端点**);`DecodeAuthJSON(raw)` **直接解析 Codex CLI auth.json**({last_refresh, tokens{access_token,refresh_token,id_token}});TurnMetadata/Session 头透传
- `transformer/anthropic/claudecode/`:Claude Code OAuth 出站(claude.ai 授权常量),佐证该库的 OAuth 渠道设计范式
- `auth/` 包:APIKeyProvider(Static/Random/Func 适配)

**版本坑(重要)**:go.mod 要求 0901 版,**本地模块缓存只有 0810 版**(`/home/yuan/go/pkg/mod/.../llm@v0.0.0-20260810024229-4495aa3cda46`;无 vendor 目录)。上述 API 面按 0810 版核对;0901 版的 codex/oauth 包签名是否变动**待人工确认**(容器内 `go doc` 或查 proxy)。

## 7. 逐子项对照结论

| 本包子项 | 上游 v0.13.2 实况 | 判定 |
|---|---|---|
| Codex OAuth 渠道 | 仓内零 OAuth;但库白拿 codex outbound+oauth token provider+DecodeAuthJSON | 重想象移植:dialect 扩展 + buildOutbound 分支 + key 存凭证 JSON |
| Usage Card | 完全空白(home 无卡片模块) | 移植,后端 usagecard 包近乎原样,前端适配新结构 |
| auth 文件导入 | 空白;库 DecodeAuthJSON 可替代 fork 解析 | 移植(依赖渠道形状落地) |
| 密钥行 Switch | **原生已有**:FormKeys.tsx:113 每行 enabled Switch | 白拿,不移植 |
| Key 轮询 | 无轮询(选路 sticky 优先级);多 key 靠多 grant 表达 | 语义缺口,需设计决策 |
| Key 级统计 | 写侧原生完整(七列+落库),**读侧无端点/UI** | 大半白拿,展示小增量 |
| 单 Key 测试 | 无(probe 只拉列表) | 移植(适配 v2 协议) |
| 模型连通性测试+对话框 | 编辑器按凭据探测+自动合并已有;无真实请求测试;Codex 渠道无 /models 端点,静态表也无法探测 | 部分被原生替代,收缩评估 |

## Caveats / 待人工确认

- axonhub/llm 0901 版(实际编译版本)的 oauth/codex 包 API 与 0810 版是否一致——本地无缓存,需容器内验证
- 库 codex outbound 的 WebSocket executor、Transport 参数在 octopus 侧如何接线(上游 octopus 未用过该 transformer)需试验
- Codex key(JSON 凭证)经迁移脚本会原样带入 v2 `channel_keys.key`(脚本对 channel_keys 丢的是统计/remark 列,保留 key 本身,`scripts/migrate_v1_to_v2.py:93-94`)——v2 落地 codex 支持后存量 key 即可复活,前提是 JSON 形状兼容库的解析(fork `CodexCredential` JSON vs 库 `OAuthCredentials` JSON:字段名 access_token/refresh_token/id_token 一致;expires_at fork 是 RFC3339 字符串、库是 time.Time JSON——形状兼容性待实测,**待人工确认**)
