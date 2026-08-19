# Design — ④ 客户端 UA 识别

## 1. 结构事实(决策依据,已核实)

- **无持久化 log 模型**:`internal/model/` 无 `log.go`(仅 apikey/backup/channel/group/llm/setting/stats/user);`grep RelayLog internal/` 全空;`internal/op/` 无 `active_request.go`。上游日志=纯内存 SSE(`internal/relay/log.go` 的 `LogRecord` map,trim 50)。
- **SSE 下发路径**:`internal/server/handlers/log.go:111,132` `sse.Encode(c.Writer, sse.Event{Event:"log", Data: overview})` 直接 marshal `LogOverview` 结构(按 json tag)。→ **加字段到 `LogOverview` 自动流到前端,无需改 handler**。
- **execution.go**:`execution.ctx *gin.Context`(line 27);`LogRecord` 初始化于 line 52;三入口 `HandleChatCompletions`/`HandleResponses`/`HandleMessages`(line 36-48)。UA = `e.ctx.GetHeader("User-Agent")`,在 `execute()` 内即可读。
- **前端 model-icons.tsx**:`@thesvg/react` 品牌 SVG(subpath 导入)+ 前缀数组 + `getModelIcon(modelName)→{Icon,className,color}`。**无 i18n**,模型名作为 raw 文本展示。
- **前端 log/Item.tsx**:line 203 卡片模型图标 `<Icon width={40} height={40}/>`;line 266 弹窗标题图标;`log/index.tsx` 虚拟列表渲染 `LogCard`。
- **前端可复用**:`web/src/components/ui/tooltip.tsx`、`badge.tsx` 存在;`lucide-react@^1.31.0`、`@thesvg/react@3.2.21` 均为依赖。

## 2. 数据流

```
HTTP 入口(gin.Context)
  → execute() line52 初始化 LogRecord
  → 只读读 UA header
  → DetectClient(ua) 解析客户端名
  → 写 e.log.UserAgent / e.log.ClientName
  → emit/applyLog → LogOverview snapshot
  → SSE channel → handler sse.Encode(JSON)
  → 前端 EventSource → RelayLogOverview
  → getClientIcon(client_name) → ClientIcon 徽标 overlay 到模型图标
```

## 3. 改动清单

### 后端

| 文件 | 动作 | 说明 |
|---|---|---|
| `internal/relay/client_detect.go` | 新建 | 移植 dev `a6c2e4f:internal/relay/client_detect.go` 原文(`DetectClient` + `clientRules` + `wordBoundaryContains`)。包名 `relay`,纯 `strings` 包无外部依赖,可直接搬。 |
| `internal/relay/client_detect_test.go` | 新建 | 移植 dev 测试用例。 |
| `internal/relay/log.go` | 改 | `LogOverview` 加 `UserAgent string \`json:"user_agent"\`` + `ClientName string \`json:"client_name"\``。位置放 `ClientProtocol` 附近。 |
| `internal/relay/execution.go` | 改 | `execute()` line 52 后只读捕获:`ua:=e.ctx.GetHeader("User-Agent"); e.log.UserAgent=ua; e.log.ClientName=DetectClient(ua)`。不动请求体/流。 |

### 前端

| 文件 | 动作 | 说明 |
|---|---|---|
| `web/src/api/log.ts` | 改 | `RelayLogOverview` 加 `user_agent: string; client_name: string;`。 |
| `web/src/lib/client-icons.tsx` | 新建 | 复用 `model-icons.tsx` 机制:`getClientIcon(clientName)→{Icon,className,color,label}`。优先 `@thesvg/react` 品牌图标(实现时列 subpath 导出确定可用项),缺则 `lucide-react`。`label` 为静态显示名。 |
| `web/src/components/modules/log/ClientIcon.tsx` | 新建 | 客户端图标徽标:`getClientIcon` + `components/ui/tooltip.tsx`。无匹配返回 `null`。 |
| `web/src/components/modules/log/Item.tsx` | 改 | line 203 模型图标包 `relative` 容器,右下角 `absolute` 叠加 `<ClientIcon clientName={log.client_name}/>`;line 266 弹窗标题图标同样叠加。 |

### i18n

- 不新增 locale key(见 §5 D2)。

## 4. 字段规划(④⑥ 不冲突)

- ④ 加:`LogOverview.UserAgent` / `.ClientName`(json: `user_agent` / `client_name`)。
- ⑥ 后续:基于 `logRecords` 加 channel-attempts 查询函数(如 `RelayLogAttemptsByChannel`),**不改 ④ 两字段**。⑥ 若需在 `LogOverview` 加字段,命名避开 `user_agent`/`client_name`。无交集。

## 5. 决策(已 review 确认 ✓)

- **D1 不涉及 migrate** ✓:上游无 log 表,UA 字段加在内存 `LogOverview`,无 DB 加列。与原「加列走 migrate」约束冲突——因上游结构使然,migrate 无表可加。用户已认可按此实现。
- **D2 i18n 用静态 label** ✓:`client-icons.tsx` 内置 `label`,不走 locales。理由:`model-icons.tsx` 先例 + 客户端产品名多为专有名词三语相同 + 避免 100+ locale 条目。用户已选静态 label。

## 6. 验证关卡

- `go build -tags=jsoniter ./internal/...`
- `go test -tags=jsoniter ./internal/... ./cmd/...`(含 `client_detect_test`)
- `pnpm build`(web/)+ 自改文件 lint 干净
- 不跑容器 smoke(父任务 Step 8 统一)

## 7. 回滚

- 单 commit,失败 `git reset --hard HEAD^`。④ 改动隔离在 4 后端 + 4 前端文件,无侵入其他子功能。
