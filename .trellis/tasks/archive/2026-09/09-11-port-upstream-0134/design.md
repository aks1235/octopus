# 技术设计:移植上游 v0.13.4 四项功能

## 总策略:手工移植,不 merge

dev-v2 与 upstream/master 在 `internal/server/handlers/channel.go`(重写)、`internal/relay/handler.go`(+112,日志终态挂钩)、`internal/relay/state.go`(attempts/故障转移标记)、`web/src/components/modules/log/Item.tsx`(attempts 展示)分叉大;且必须排除 0b919da/1bd2ed8。故以 `git show <hash>` 为蓝本逐项重写,落到我们的形状与契约上。

## 逐项设计

### 1. 全局模型过滤(蓝本 d5a893f)

- `internal/model/setting.go`:新增 `SettingKeyModelFilter = "model_filter"`,默认值空串;`Validate` 分支:空串放行,否则 `regexp2.Compile(value, regexp2.ECMAScript)` 失败报错(与上游一致,方言同 group-member-regex.md)。
- `internal/server/handlers/channel.go` `fetchModel`(现位于 L214 起,形状与上游基线不同,**按我们形状适配**):在现有渠道级 MatchRegex 编译处并行取 `op.SettingGetString(model.SettingKeyModelFilter)` 并编译;把「单正则过滤」改写为 matches(name) 双正则 AND 闭包(蓝本结构);忽略设置读取错误(缺行按不过滤,上游同款)。
- 前端 `web/src/components/modules/setting/System.tsx`:新增设置行(与既有 relay_log_keep_* 等共存);输入预检复用 `compileMemberRegex`(group 域已有该 util,如跨模块引用不便则就近复用同逻辑,保持单一口径);文案三语。
- 边界:仅 fetchModel 路径;手工模型不受影响。

### 2. client_header 占位符(蓝本 1c48ee5)

- `internal/relay/channel.go` `applyChannelConfig`:包级预编译 `\{client_header:[^}]+\}`,Header 值做 `ReplaceAllStringFunc`,取 `request.Headers.Get(NAME)`(取不到替换为空串);敏感 Header 跳过逻辑不动。
- 我们的 `applyChannelConfig` 自基线未改(buildOutbound 是另加函数),预期近原样落;若上下文有偏移以我们的代码为准。
- 已知边界(文档化,不处理):探测/keytest 走合成请求无客户端头,占位符替换为空——上游同款行为。

### 3. 实时日志来源 API Key(蓝本 bab1a53)

- `internal/relay/state.go` `RequestState` 增加 `APIKeyName string`(json `api_key_name`),放在 GroupID 之后(字段对齐位置仅为 diff 可读,无序列化兼容问题——实时流形状,前端同步改)。
- `newRequestState` 增加 ctx 入参并快照 `op.APIKeyGet(apiKeyID, ctx)`(成功才写名称,失败留空);调用点 `internal/relay/handler.go` Forward 两处调用之一(登记处)同步改。
- 注意与 fork 已有字段(attempts/故障转移标记)共存;`op.APIKeyGet(id, ctx)` 签名两侧一致,直接用。
- 前端 `web/src/api/log.ts` 类型 + `Item.tsx` 实时条目展示名称(历史侧已有,不动)。

### 4. RoundStartedAt + 日志前端优化(蓝本 bf2027a/9a80de3/edb4bfb)

- `RequestState` 增加 `RoundStartedAt time.Time`(json `round_started_at`);`startRound` 里 `time.Now()`。
- 前端 Item.tsx:按上游三笔的展示意图(轮次开始时间、spinner 样式、布局微调)适配到我们的 Item.tsx;**与我们 attempts 链路展示共存**,冲突处以我们已有功能优先、上游样式融合。
- edb4bfb/9a80de3 纯样式,顺手带上,不逐像素对齐。

## 明确不做

- 不动 GroupGetByName / 选路禁用逻辑(0b919da 排除);不动 go.mod/go.sum(1bd2ed8 排除)。
- 不给历史日志补 APIKeyName(已有 RequestAPIKeyName)。
- 不做依赖升级、不做冒烟矩阵。

## 测试与验证

- 单测:`applyChannelConfig` 占位符替换(relay 包,参照既有测试基建);`model_filter` 设置校验(op 或 model 层小测);`newRequestState` 名称快照(成功/失败两态)。
- 其余走 octopus-verify:设置页配置过滤 → 渠道拉模型列表两级过滤;实时日志显示 Key;日志页无回归。
- 回归确认:route_disable_test 全绿(证明未误伤禁用契约)。

## 回滚

四个功能相互独立,可分 commit(建议 2 个:后端+前端按功能 1 个/项,或合 1 个);回滚 revert 对应 commit 即可,无 schema/数据变更。
