# 升级 axonhub/llm 依赖至 0909 快照

## Goal

`go.mod` 的 `github.com/looplj/axonhub/llm` 从 `v0.0.0-20260901162339-94e0d7c781e4` 升级到 `v0.0.0-20260909170523-3786f2c5c8de`(上游 1bd2ed8 中的依赖项),拿到 GLM-5.3 常开思考、DeepSeek 联网工具保留、Claude 缓存断点等修复,并以冒烟矩阵 A2(基线+定向)证明行为等价或差异可接受。

## Background

- 该库是全流量咽喉:所有进出消息的格式互转/流式解析/工具调用/思考等级传递。
- 两快照间 **25 个提交**触碰 llm/(已核实,GitHub API),关键项:
  - `834eea2b` GLM-5.2+ reasoning_effort 与 GLM-5.3 常开思考(用户有 glm-5.3 渠道)
  - `9f310561` Anthropic 侧保留 DeepSeek 原生 web search 工具
  - `7444f537` 保留客户端 cache_control 断点(Claude prompt cache 命中)
  - `fd4158ef`/`a0850956`/`e2b726eb` OpenAI 跨格式转换补全/多条 system 合并/请求契约保留
  - `0e4ac151` 零状态错误不再误报 200;`3786f2c5` 断连后终态保留
  - `35434951` 入站请求体大小上限;`dfbe2259` 跨协议统一 reasoning effort
  - 新渠道支持:gpt-6-astra/ZenMux/Command Code(顺带)
- 上游 1bd2ed8 同时升了 x/crypto 0.54→0.55、x/net 0.57→0.58(例行安全更新,一并带上)。

## Requirements

- R1 升级 llm 依赖(容器内 `go get` + `go mod tidy`),连同 x/crypto、x/net 一并跟进 1bd2ed8 的版本;不改其他依赖。
- R2 编译 + 全量单测全绿;现有 relay 转换测试不退化。
- R3 冒烟验证(8081 隔离环境),分两层:
  - **基线回归**(原 A2 项):DeepSeek thinking/json_schema/tool_call、SSE `[DONE]` 与流式截断、空响应、failed 重试、Anthropic 兼容路径
  - **定向用例**(按 25 提交):GLM-5.3 常开思考标记透传、DeepSeek web_search 工具保留、Claude cache_control 断点保留、跨格式(OpenAI↔Anthropic)消息转换、多条 system 合并
  - 优先用**假上游模拟器自动化**(扩展 09-11-port-upstream-0134 的 sim 思路,模拟各家响应形状断言转换输出),无法自动化的用真渠道实测(8081 指真上游,用户配合发请求)
- R4 octopus-verify 全流程通过,随 v2.0.4 发布。
- R5 差异处理:发现行为变化时,能接受的记录进任务与 spec,不能接受的评估回退该行为(库层一般无法 cherry-pick,如实上报差异让用户决策)。

## Non-goals

- 不动 llm 库本身源码(依赖升级,不是 fork 该库)。
- 不引入上游 1bd2ed8 除依赖外的任何代码(那是已排除的 0b919da 之外无其他内容,1bd2ed8 纯依赖)。
- 不做 A1/A3/A4 冒烟(依赖升级不触及那些面)。

## Acceptance Criteria

- [x] AC1 go.mod/go.sum 升级到位,llm=3786f2c5c8de,x/crypto=0.55.0,x/net=0.58.0
- [x] AC2 容器内编译 + `go test ./...` 全绿(含既有 relay/op 测试)
- [x] AC3 基线回归项全部通过(模拟器自动化或真渠道)
- [x] AC4 定向用例通过:GLM-5.3 思考/DeepSeek 工具/Claude 缓存断点/跨格式转换(允许以模拟器形状断言,真渠道抽查至少 GLM-5.3)
- [x] AC5 octopus-verify 全流程 + 用户 UI 人审(日志页/转发正常)
- [x] AC6 行为差异清单(如有)已记录并有用户决策

## Notes

- 时间预估:顺利 1.5~2.5 小时墙钟。
- 发布:并入本任务尾部的 v2.0.4(octopus-publish 流程,用户确认后执行)。
- 模拟器资产参考:`/tmp/octopus-sim/sim.py`(09-11-port 任务,可扩展);注意 /tmp 可能被清,需要时从 journal 描述重建。
