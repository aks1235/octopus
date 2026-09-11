# 执行计划:移植上游 v0.13.4

前置:trellis-before-dev 载入 backend spec;每项开工前 `git show <hash>` 读上游蓝本。

## 步骤

1. [x] client_header(relay/channel.go + 单测):applyChannelConfig 占位符替换
   - 验证:go vet + go test ./internal/relay/
2. [x] 全局模型过滤(setting.go 校验 + fetchModel 适配 + System.tsx + 三语):蓝本 d5a893f
   - 验证:vet + 新增单测;前端 lint+build
3. [x] 实时日志 APIKeyName + RoundStartedAt(state.go + handler 调用点 + api/log.ts + Item.tsx):蓝本 bab1a53/bf2027a
   - 验证:vet + 新增单测;前端 lint+build
4. [x] Item.tsx 样式优化融合:蓝本 9a80de3/edb4bfb,与 attempts 展示共存
   - 验证:前端 lint+build
5. [x] 全量回归(容器内,golang:1.26 -tags=jsoniter -buildvcs=false + 缓存卷):go test ./internal/... 含 route_disable_test
6. [x] octopus-verify 全流程(8081 smoke + data-verify,参照 group-manual-absorb 任务的隔离姿势,勿跑 deploy.sh):
   - 设置页配 model_filter → 渠道拉模型列表两级过滤生效
   - 人审:实时日志显示来源 Key;日志页 attempts/故障转移无回归;渠道自定义头占位符(可配一条观察转发日志或抓包确认)
7. [ ] spec 更新:relay-routing.md 或新增小节沉淀 client_header 占位符与 model_filter 方言口径(若值得);journal 记录
8. [ ] commit(建议按功能 2-3 个 commit)+ 任务归档

## 回滚点

- 每功能独立 commit;revert 即回滚,无 schema/数据变更。

## 风险

- Item.tsx 上游样式与我们 attempts 展示同区域,以前者功能优先、样式融合,人审把关。
- fetchModel 我们形状与基线不同,以蓝本「双正则 AND」语义为准重写,不硬套 diff。
