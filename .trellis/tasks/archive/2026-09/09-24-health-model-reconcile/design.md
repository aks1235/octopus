# 技术设计:健康检查模型对账

## 总览

| 层 | 变更 |
|---|---|
| `internal/probe` | `FetchModels` 拆出返回逐凭据逐侧清单的入口;bool 判活包装保留 |
| `internal/op` | 新增 `ChannelModelReconcile`(单凭据粒度对账,事务内),复用 `syncChannelChildren` 的替换骨架 |
| `internal/task/health.go` | 探测循环改为逐凭据两侧全测(判活仍宽容),成功凭据逐个对账 |
| `web` | 无改动(对账结果经既有分组事件流自然到达前端) |

无 DB 迁移,无新端点,无新设置项。

## 1. probe 层:探测返回清单

现状:`FetchModels` 串行探测各凭据两侧,**任一成功立即 return true**(宽容判活),列表丢弃。

改造:
- 新增 `FetchModelsDetailed(ctx, config, keys) (healthy bool, results []KeyProbeResult, err error)`:
  - `KeyProbeResult{KeyName string; OpenAI []string; Anthropic []string; OpenAIErr, AnthropicErr error}`;
  - **不短路**:每个启用凭据两侧都探测(对账需要全部结果;判活由调用方按"任一侧成功"聚合,语义不变);
  - 复用 `FetchOpenAIModels` / `FetchAnthropicModels`(它们已返回模型名列表)。
- `FetchModels` 保留原签名,内部委托 `FetchModelsDetailed` 后聚合 bool——既有调用点(若有)与测试不破。

## 2. op 层:单凭据对账

新增 `op.ChannelModelReconcile(ctx, channelID int, keyName string, fetched []model.ChannelFetchModel) error`:

- **替换语义(43d6cae 同款)**:事务内删除该凭据名下全部授权 → 按 fetched 重建授权(模型不存在则建,存在则复用主键保统计)→ 模型集合按授权表重导出(无授权的孤立模型删除)→ 过滤收缩(`modelNameKept` 口径,主要清扫存量)。
- 骨架复用 `syncChannelChildren`(channel.go:277)的既有写法与缓存失效路径,提取可共享的部分为私有助手,**避免两套替换语义漂移**;渠道编辑保存路径行为不变(回归约束)。
- 对账无实际变更时短路返回(不触发重算与事件,避免每 30 分钟空转刷事件)。
- 空列表/空 fetched 由调用方拦截(R4),op 层再兜一道防御(空 fetched 直接报错拒绝)。

## 3. task 层:健康检查循环

`checkChannelHealth` 改造:
- 调 `FetchModelsDetailed`;判活聚合不变(任一凭据任一侧 2xx 即 healthy,失败计数/自动禁用/解禁逻辑照旧);
- **对账**:对每个探测成功(至少一侧 2xx 且该侧列表非空)的凭据,调 `ChannelModelReconcile`;任一凭据对账失败只记日志不中断其余凭据;
- 轮超时(`roundCtx.Err() != nil`)→ 不落健康结论也**不落任何对账**(现状语义自然覆盖,对账调用都在超时检查之后);
- 对账成功且实际有变更 → 走 `GroupRegexSync` + `GroupManualAbsorb`(与渠道保存同款,channel.go:200-204 同款调用),分组事件由 op 层既有发布路径带出。

## 4. 并发与一致性

- 对账写库与用户同时编辑同一渠道:与 `ChannelUpdate` 同款事务与缓存失效;最坏情况是后写者胜(用户保存覆盖对账结果或反之),下一轮对账自愈——可接受,不加锁升级。
- 健康检查本身每渠道一个 goroutine,渠道间天然隔离;单渠道内凭据串行(现状)。
- SQLite BUSY:对账落库走 `runWithBusyRetry` 同款退避(init.go:43 已有)。

## 5. 风险与权衡

- **误删风险**是本任务最大风险,R4 三层护栏(失败跳过/空列表跳过/轮超时不落)后,剩余场景是"上游正常返回了不含某模型的部分列表"——概率低且下一轮自愈,不再加熔断(Non-goal)。
- **探测成本上升**:不短路意味着死渠道要测完全部凭据两侧(现状短路后一个成功就停)。候选渠道通常不多、探测是 GET /models 轻请求,可接受;`healthCheckTimeout`(5min)总时限兜底。
- **skip 渠道**覆盖不到:文档与 PRD 已声明。

## 测试策略

- probe:`FetchModelsDetailed` 逐凭据两侧结果形状、bool 聚合与原 `FetchModels` 等价(假上游基建)。
- op:对账替换/保留主键统计/空 fetched 拒绝/无变更短路/过滤收缩/孤立模型清理。
- task:失败凭据跳过对账、空列表跳过、轮超时不落、对账变更触发重算(集成层,复用现有 health 测试模式)。
- 回归:渠道编辑保存路径既有测试全绿。

## 回滚

单 commit revert 即回滚;无迁移、无配置变更;最坏影响面 = 回到"只判活不对账"现状。
