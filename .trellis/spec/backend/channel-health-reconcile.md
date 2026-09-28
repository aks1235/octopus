# 健康检查模型对账契约

> 2026-09-24 由任务 09-24-health-model-reconcile 沉淀:健康检查探测时把已拉取的上游模型列表按对账语义落库,上游下线的模型最多存活一个健康检查周期(默认 30 分钟),不再依赖人工去渠道页点探测。

## 契约:探测返回清单与判活聚合分离

**What**:`probe.FetchModelsDetailed(ctx, config, keys)` 逐启用凭据、OpenAI/Anthropic 两侧**全部探测不短路**,返回逐凭据的两侧模型名列表与两侧错误(`KeyProbeResult`);`probe.FetchModels` 保留原签名,委托后聚合 bool——任一凭据任一侧 2xx 即健康,失败消息格式(`key %q openai: %v; ` 拼接、200 字符截断、`no enabled keys`)**冻结不变**,健康检查的判活/计数/自动禁用解禁语义零变化。

**Why**:对账需要每个凭据的完整结果(短路会漏测其余凭据);判活保持宽容语义,防误杀部分凭据失效或仅支持单侧协议的渠道。

## 契约:单凭据对账的替换语义(ChannelModelReconcile)

**What**:`op.ChannelModelReconcile(ctx, channelID, keyName, fetched)` 事务内把该凭据名下授权**整体替换**为 fetched:按"模型名+凭据名"匹配,已有的复用主键与统计,新的 Create,上游没有了的删除(级联删分组成员属预期);渠道模型集合按授权表实际存在重导出,无授权的孤立模型删除;`op.ModelNameKept` 两层过滤收缩(全局黑名单命中排除 + 渠道白名单命中保留)。

**Why**:与手动探测同语义(2026-09-18 用户定案:"上游都没有了这个模型,还一直请求,不符合逻辑");授权两侧按名匹配是保存路径既有约定,主键与统计由此保留。

**边界**:
- 空 fetched 直接报错拒绝(op 层防御;调用方另有三层护栏,见下)。
- 无实际变更**短路返回**(业务键→协议位的 maps.Equal 等价判定),不触发重算与缓存重载,避免每轮空转。
- 过滤收缩只作用本凭据的探测清单,不碰其他凭据名下存量——单凭据粒度,不确定的不动;全部启用凭据逐轮对账后收敛等价。
- `ModelNameKept` 已上移至 op 层作为**唯一判定口径**,handlers 委托调用——勿再各写一份过滤实现。

## 契约:对账的三层防误删护栏

| 条件 | 行为 |
|---|---|
| 凭据本轮探测失败(两侧都非 2xx) | 该凭据跳过对账,健康判定照旧 |
| 任一侧 2xx 但列表为空 | **整个凭据**本轮不对账(空列表更可能是上游故障而非全部下架,严格口径) |
| 渠道级整轮超时(`roundCtx.Err() != nil`) | 不落健康结论也不落任何对账(对账调用在超时检查之后) |

**Why**:对账最大风险是误删。上游 5xx/空列表/超时时用可疑的部分结果改写授权,破坏**不可自愈**——删掉的授权不会因下一轮探测回来,分组成员也随之消失。

## 契约:对账收尾与渠道保存路径同款

对账落库包 `runWithBusyRetry`(SQLite BUSY 退避 2s/5s,单凭据独立 30s 时限);有变更时收尾序列 `reloadChannelChildren → groupRefreshCache → GroupRegexSync → GroupManualAbsorb`,与 `ChannelUpdate` 收尾一致。

**已知边界**:渠道触发的分组成员变化(含既有保存路径)**本就不发分组 SSE 事件**——`publishGroupEvent` 仅由分组 HTTP 处理器调用,op→handlers 存在导入环;前端经拉取路径看到变化。勿在对账路径单独"补"事件,要事件化需先把事件机制下沉。

**不覆盖**:勾选"跳过健康检查"的渠道不探测不对账(它们靠人工探测维护模型列表);`health_check_interval=0` 时判活与对账全关。

## 测试锚点

- `internal/probe/models_test.go`:详细结果形状(含分页/禁用凭据)、bool 聚合与旧语义 err 逐字相等、不短路(请求计数)、200-空列表形态。
- `internal/op/channel_reconcile_test.go`:替换语义、主键/统计保留、分组级联、无变更短路、空 fetched 拒绝、两层过滤收缩。
- `internal/task/health_test.go`:两轮对账自动增删、失败凭据跳过、空列表跳过、轮超时不落。
- 回归红线:`syncChannelChildren` 及渠道保存路径零改动;`fetchModel` handler 行为不变。
