# Research: fork 实现盘点(Codex/Usage/渠道包)

- **Query**: 逐子项盘点 fork(`dev` 分支)的 commit、核心文件、机制、子项依赖关系
- **Scope**: internal(git show dev)
- **Date**: 2026-09-09

## 总览:commit 分组与依赖链

```
4d569fc(Provider预设+OAuth基建, 2026-04-20)
   └─> c9ba27f(Codex OAuth渠道+用量卡片+Auth导入, 05-04)
         ├─> ac09a62(Codex凭证生命周期/负载均衡, 05-04)
         ├─> 1a393d0 + 1e2486e(OAuth走系统代理+客户端初始化, 05-04)
         ├─> e873006(用量卡片代理开关, 05-04)
         ├─> ef1a854 + 6f5d8e9(导入按钮UX, 05-04)
         └─> 4237d00(卡片批量操作, 05-05) / bb33f79(密钥行Switch, 05-05)
73a13a2(Usage Card 基座, 05-03) ──先于 c9ba27f 一天,c9ba27f 在其上改模板/加 Codex
ba5deca(Key轮询+Key级统计+单Key测试, 04-19) ──被 9c06c03(06-21) 重构为穷举重试
f9534b5(模型连通性测试+选择对话框, 04-13) ──独立,与 ba5deca 共享 helper/test.go
```

依赖关系要点:OAuth 渠道的一切都挂在 4d569fc 的 provider/auth 基建上;Usage Card 的 Codex 模板依赖 oauth_codex.go 的凭证刷新;auth 文件导入依赖渠道已保存(要 channelId)。

## 子项 1:Usage Card(基座 73a13a2)

**表** `usage_cards`(model: `internal/model/usage_card.go`,AutoMigrate 注册 `internal/db/db.go:72`):

| 列 | 类型/默认 | 说明 |
|---|---|---|
| id | uint PK | |
| name | string not null | |
| template_id | string(64) not null | 内置模板 ID |
| account | string | 账号标识(去重键之一) |
| endpoint | string | 请求地址 |
| method | string(10) default GET | |
| auth_type | string(32) default none | none/bearer/x-api-key/custom-header/cookie |
| auth_header | string | custom-header 的 header 名 |
| encrypted_secret | string (json:"-") | `usagecard.EncryptSecret` 产物 |
| extra_headers | serializer:json | []UsageHeader |
| config | serializer:json | UsageCardConfig{Metrics[]}(字段路径提取规格) |
| enabled | bool default true | |
| use_proxy | bool default false | e873006 引入 |
| refresh_interval_sec | int default 300 | **字段存在但无后台调度,纯手动刷新** |
| last_result | serializer:json | UsageSnapshot(归一化指标) |
| last_error | string | |
| last_refresh_at | *time | |
| created_at/updated_at | | |

**核心包** `internal/usagecard/`:
- `client.go`(180 行):通用 HTTP 刷新执行器 + SSRF 防护(isBlockedIP/isBlockedHostname,`security.go` 119 行)+ EncryptSecret/decryptSecret(透传 `auth.Encrypt`)
- `extractor.go`(392 行 + 534 行测试):JSONPath 风格字段提取器,FieldSpec{Source: body/header/static/const, Path, Transform: epoch_to_iso/epoch_ms_to_iso/percent_to_float/to_float}
- `template.go`(282 行):内置模板注册表;**c9ba27f 后仅保留 `xfyun`(讯飞 Coding Plan,cookie)+ `codex-usage` 两个模板**,移除了 generic/github(见 c9ba27f commit message: "模板精简:仅保留讯飞和 Codex")
- `codex.go`(384 行):`RefreshCodex` 调 `https://chatgpt.com/backend-api/wham/usage`,解析 rate_limit + code_review_rate_limit + additional_rate_limits;凭证过期时用 CodexOAuthFlow.RefreshToken 刷新并回写 RefreshedCred

**op** `internal/op/usage_card.go`(258→后增):进程内 cache(4 容量)、CRUD、`UsageCardRefresh(id)` 单卡刷新、`UsageCardGetByAccount`、`AutoCreateCodexUsageCard`(channel.go 联动:导入 auth 文件后自动建卡,remark 存邮箱做关联)

**API** `internal/server/handlers/usage_card.go`:/api/v1/usage-card 下 templates/list/create/update/delete/:id/batch-delete/refresh/:id + import/codex-channel + batch-import/codex-channel(4237d00 加批量)

**加密实况(重要)**:`auth.SetEncryptionKey` 在 fork 全库**从未被调用**(`git grep SetEncryptionKey` 仅定义处);`auth.Encrypt` 无密钥时原样返回明文(`internal/provider/auth/crypto.go:41-44` 注释"无密钥时不加密(开发模式)")。即生产库 `encrypted_secret`/`o_auth_sessions.session_data` 实际都是明文。

**刷新调度**:无后台任务。`internal/task/init.go` 注册的任务里没有 usage card;前端 index.tsx 仅手动单卡刷新 + 串行批量刷新(带 abort)。

## 子项 2:Codex OAuth 渠道(c9ba27f + 基建 4d569fc + ac09a62 + 1a393d0/1e2486e)

**基建(4d569fc, 2026-04-20, chenjh)**:
- `internal/provider/`:registry(88 行)、presets.json(74 行,7 个 provider 预设)、builtin/{anthropic,gemini,openai_chat,openai_embedding,openai_response,volcengine}.go(每个 45 行,声明式 CredentialSchema+模型拉取)、credential.go/normalize.go/model_fetch.go/helpers.go
- `internal/provider/auth/`:oauth_web.go(PKCE auth-code 流)、oauth_device.go(设备码流)、crypto.go(AES-256-GCM)、session.go(`OAuthSession` 持久化模型)、manual.go(手动回调)
- `internal/op/oauth_session.go`(103 行):o_auth_sessions 表 CRUD + `CleanupExpiredOAuthSessions`(fork task/init.go 注册 TaskOAuthCleanup)
- `internal/server/handlers/provider.go`(246 行):/api/v1/provider 的 auth start/poll/callback 接口
- 前端:OAuthPanel.tsx(101 行)、ProviderPresetSelect.tsx、DynamicCredentialField.tsx、api/endpoints/provider.ts

**Codex 特化(c9ba27f)**:
- `internal/provider/auth/oauth_codex.go`(398 行):`CodexOAuthFlow` PKCE(code_verifier 96 字节)+ 常量 `CodexAuthURL=https://auth.openai.com/oauth/authorize`、`CodexTokenURL=https://auth.openai.com/oauth/token`、`CodexClientID=app_EMoamEEZ73f0CkXaXp7hrann`;`ParseIDTokenPayload` 从 id_token JWT 提 email/chatgpt_account_id;`CodexCredential{access_token,refresh_token,id_token,expires_at,account_id,email,token_type}` JSON 即 key 存储格式
- `internal/provider/builtin/codex.go`(58 行):AuthProvider;**RedirectURL=`http://localhost:1455/auth/callback`(Codex CLI 标准回调),手动回调模式**——用户浏览器登录后跳 localhost 报错页,复制完整 URL 粘回管理界面换 token(见 OAuthPanel.tsx waiting_callback 分支)
- `internal/transformer/outbound/codex/codex.go`(318 行):fork 自写出站转换器(chatgpt.com/backend-api/codex/responses, SSE)
- 前端:OAuthPanel.tsx 扩展 submitCallback、Form.tsx +285 行、CardContent.tsx、utils.ts(parseOAuthLabel)
- 渠道形状:挂在 `BaseUrl.ProviderID="codex"` 上(fork 009 迁移后 base_urls[] 每条带 provider_id)

**凭证生命周期(ac09a62, 695 行含 444 行测试)**:
- `internal/op/codex_auth.go`:`IsCodexAuthKey(key)` = `key[0]=='{'`;`EnsureCodexKeyReady`(请求前检查:过期无 refresh_token → `DisableCodexKey(401)` 自动禁用;过期有 → 加锁刷新);`RefreshCodexKey`(per-key sync.Map 锁,成功回写缓存+DB)
- relay 集成:`internal/relay/relay.go` 每轮 attempt 前对 Codex key 调 EnsureCodexKeyReady

**代理修复(1a393d0 + 1e2486e)**:OAuth 授权/刷新走 GetProxyHTTPClient(true)(函数变量注入避免循环依赖);1e2486e 修初始化时序 + 加 290 行测试。

## 子项 3:auth 文件导入(c9ba27f + ef1a854 + 6f5d8e9)

- 后端 `internal/server/handlers/codex_auth_import.go`(598 行):`POST /api/v1/channel/:id/codex/auth-files/import`;支持单/多 JSON 文件与 ZIP(200 文件上限);`processJSONData` 解析 Codex CLI auth.json 格式 → `CodexCredential` JSON 存入 channel_keys.channel_key;批量预览结果(importBatchResult);`buildKeyLookup` 同批次按账号去重;remark 只存邮箱;导入后联动 `AutoCreateCodexUsageCard`
- 前端 `AuthFileImportPanel.tsx`(406 行):拖拽/选择、虚拟滚动预览、全选(仅有效文件)
- ef1a854/6f5d8e9:创建流程 UX——无 channelId 时提示先保存渠道

## 子项 4:密钥行 Switch(bb33f79, 26 行)

- `CardContent.tsx`:channel 卡片展开的 key 列表,圆点指示灯 → Switch;`toggleKeyEnabled` 发 `UpdateChannelRequest{keys_to_update:[{id,enabled}]}`
- `Form.tsx` +11 行:表单侧同步

## 子项 5:Key 轮询 + Key 级统计 + 单 Key 测试(ba5deca, 后被 9c06c03 重构)

- **Key 轮询** `op.ChannelGetKey`(ba5deca)/`op.ChannelGetKeys`(9c06c03):过滤 enabled=false 与 429 冷却(5 分钟);**最低 TotalCost 优先 + 同成本原子计数器轮询**(sync.Map per channel)
- **Key 级统计**:ChannelKey 加 `TotalRequests/TotalInputToken/TotalOutputToken`(TotalCost/StatusCode/LastUseTimeStamp 原有);修复仅 shutdown 刷盘丢数据问题
- **单 Key 测试**:`POST /api/v1/channel/test-models-by-key` → `helper.TestModelsWithKey(ctx, channel, key, models)`;前端 CardContent key 行显示调用次数/Token,单 Key 测试 + 一键测试全部(全失败自动禁用)
- **渠道内穷举(9c06c03, 2026-06-21)**:relay Handler 渠道内逐 key 尝试,全失败才跳下一渠道;熔断按 channel:key:model 粒度(`iter.SkipCircuitBreak`);附带修复 remark 被 tx.Save 覆盖的回归

## 子项 6:模型连通性测试 + 选择对话框(f9534b5, 2026-04-13, chenjh)

- 后端:`POST /api/v1/channel/test-models`(按已存渠道)与 `/test-models-by-config`(按表单配置);`helper.testSingleModel` 发最小非流式请求 `"1+1=?"` max_tokens=1(embedding 渠道发 Single:"test"),30s 超时,带 CustomHeader
- 前端 `Form.tsx` +235 行:刷新上游模型后弹**模型选择对话框**(搜索/全选/勾选确认替换);auto_sync 开启时显示覆盖风险提示;引入一个 npm 依赖(pnpm-lock +851 行,对话框组件库)

## 前端结构差异要点(fork vs dev-v2,规划移植时必读)

| 维度 | fork | dev-v2(上游 v0.13.2) |
|---|---|---|
| 构建/i18n | next-intl,`web/public/locale/*.json` | vite + use-intl,同目录 locale(已含 client-theme 包改动) |
| API 层 | `web/src/api/endpoints/<mod>.ts` 每模块一个文件 | `web/src/api/<mod>.ts` 平铺(channel.ts/stats.ts/…) |
| 渠道编辑 | 单页大表单 Form.tsx(600+ 行)+ CardContent 展开 | 分步表单:`Form.tsx`(306 行,step: preset/connection/keys/grants/advanced)+ `FormKeys.tsx`(148)+ `FormGrants.tsx`(261)+ `state.ts`(单一 ChannelFormState,读写同构) |
| 渠道列表 | Card.tsx + CardContent.tsx(展开含 key 列表/测试) | Card.tsx(228 行,无 key 展开)+ Stats.tsx(389 行,渠道+模型统计) |
| 首页 | usage-cards 子模块(form/index/utils) | activity/chart/metric-tabs/rank/total,**无卡片概念** |
| 预设 | 后端 presets.json + ProviderPresetSelect | 纯前端 `web/src/lib/channel-presets.tsx`(13 服务商,仅预填 URL/路径/方言) |

## Caveats / 待人工确认

- fork 生产库里 usage_cards 实际有哪些 template_id 存量(决定 v2 模板集是否要含 xfyun):`data/` 副本可查,本研究未查库
- codex 模板 `PrimaryMetricIDs` 等展示口径以 `web/src/components/modules/home/usage-cards/utils.ts` 为准,未逐行展开
