# 执行计划

## 步骤

- [x] **1. `protocol.go` 扩结构** — `streamEventParse` 增 `completed bool` 字段并补注释(与 `last` 的语义差别)
- [x] **2. `protocol.go` 增终态识别** — `parseStreamEvent` 的 `APIFormatOpenAIChatCompletion` 分支加 `finish_reason` 判定:
  - 位置**必须在** `len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil` 早返回**之前**
  - 判据 `FinishReason != nil && *FinishReason != ""`(空串防御)
  - 返回 `streamEventParse{completed: true}`,**不设** `last`
- [x] **3. `handler.go` 收尾判定** — 流式循环内维护 `completed`;`if !result.events.Next()` 分支中,`err != nil && completed` 时清空 `err` 再 `break`
- [x] **4. 单测补分支** — 实际落在**新文件 `internal/relay/stream_end_test.go`**(不动既有 `stream_parse_test.go`, 降低冲突面):
  - `TestParseStreamEventCompletedDetection`(表驱动 10 例):`finish_reason` → `completed && !last`;空串/null → 不误判;无 `delta` 字段的终态分片 → 识别;另两协议不产生 `completed`
  - `TestForward_upstreamFinishReasonWithoutDoneStillSucceeds`(handler 级):假上游发 `finish_reason` + usage、不发 `[DONE]`、以 `Content-Length` 不匹配制造不干净关闭
  - `TestForward_upstreamInterruptWithoutFinishReasonStillFails`(handler 级):守 R3 反侧
- [x] **5. 本地验证** — 见下(容器内, 宿主机无 Go)
- [ ] **6. octopus-verify 全流程** — 走 `octopus-verify` skill(容器内编译 → 后端测试 → 前端 build → 起容器 → 健康检查),产出 `.octopus-verified`

## 验证命令

```bash
# 定向单测(改动面)
go test ./internal/relay/... -run 'Stream|Parse' -v

# 包级回归
go test ./internal/relay/...

# 全量(容器内, 与 CI 一致) — 由 octopus-verify 负责
```

## Review gates

- **G1(改前)**:PRD/design 经用户确认后,才 `task.py start` 进入实现
- **G2(改后)**:单测全绿 + 包级回归无失败,方可进 octopus-verify
- **G3(验证后)**:octopus-verify 通过并写 marker,方可进入提交

## 回滚点

- 单一回滚点:`protocol.go` + `handler.go` 两处改动整体还原即可,无中间状态
- 无数据迁移、无配置项、无对外契约变更 → 回滚零副作用

## 待办的风险跟进(不在本次范围)

- 流中断时向客户端补发错误事件(当前静默断流)
- 上游不发 `[DONE]` 的渠道清单梳理(可据 `unexpected EOF` 日志反查)
