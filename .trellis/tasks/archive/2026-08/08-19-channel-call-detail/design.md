# Design — ⑥ 渠道调用详情页

> 基于 `prd.md`。本设计针对上游内存日志架构重写,不照搬 dev `f551b95`。

## 1. 总体方案

上游日志纯内存(`logRecords map[uint64]LogRecord`,trim 50,无 gorm 表)。dev 从 DB 展开 `relay_logs.attempts` JSON 的路子不可用。⑥ 分三层:

1. **存储层(log.go)**:给 `LogRecord` 补 attempts 历史切片;给 `LogAttempt` 补 `Status`/`Duration`;在 `applyLog` 终态事件时累积。
2. **查询层(log.go + handlers/log.go)**:`GetLogAttemptsByChannel(name)` 遍历内存 map 匹配渠道名;`GET /api/v1/log/channel-attempts` 暴露。
3. **前端(channel/ + api/log.ts)**:Card 加按钮 → MorphingDialog 渲染明细;`useChannelAttempts` hook 拉取。

## 2. 存储层设计(log.go)

### 2.1 现状(verified)
- `LogAttempt{Type, Index, ChannelName, ModelName, Error}`。`Type` 是 `json:"-"` 的 LogEventType(作 SSE 事件名),其余有 json tag。
- `LogRecord{LogOverview; RequestBody; ResponseBody; currentAttempt *LogAttempt}`。`currentAttempt` 是**执行中游标**(未导出),完成即置 nil。**无历史切片。**
- `applyLog`:AttemptStarted 置 currentAttempt;AttemptFinished/ResponseCommitted 清 currentAttempt;RequestFinished 也清。每次 `logRecords[id] = record`(覆盖快照)。
- `logRecords` 不被序列化到概览流(概览流只取 `LogOverview`),所以给 LogRecord 加字段是纯内部变更,不破坏现有 JSON 契约。

### 2.2 扩展
```go
// AttemptStatus 尝试结果状态(⑦ 移植熔断器后追加 circuit_break)。
type AttemptStatus string
const (
    AttemptSuccess AttemptStatus = "success" // 上游返回可用响应并提交
    AttemptFailed  AttemptStatus = "failed"  // 上游请求失败
    AttemptSkipped AttemptStatus = "skipped" // 目标不可用,未发上游请求
)

// LogAttempt 增加:
type LogAttempt struct {
    Type        LogEventType  `json:"-"`
    Index       int           `json:"attempt_index"`
    ChannelName string        `json:"channel_name"`
    ModelName   string        `json:"model_name"`
    Status      AttemptStatus `json:"status,omitempty"` // ⑥ 新增
    Duration    int64         `json:"duration,omitempty"` // ⑥ 新增,毫秒
    Error       string        `json:"error"`
}

// LogRecord 增加 attempts 历史切片:
type LogRecord struct {
    LogOverview
    RequestBody    string
    ResponseBody   string
    attempts       []LogAttempt // ⑥ 新增:已完成尝试历史(未导出)
    currentAttempt *LogAttempt
}
```

**字段名冲突检查**:④ 加的 `UserAgent`/`ClientName` 在 `LogOverview`(嵌入 LogRecord),不在 `LogAttempt`。⑥ 加的 `Status`/`Duration` 在 `LogAttempt`,`attempts` 在 `LogRecord`。结构体不同,名字不撞。✓

### 2.3 applyLog 累积逻辑(改动点)
在现有 currentAttempt 游标逻辑之外,加历史累积。`record` 是 `e.log` 的值拷贝传入,`e.log.attempts` 始终空(applyLog 不改 e.log),历史只活在 `logRecords[id].attempts`:

```go
func applyLog(eventType LogEventType, record LogRecord, attempt *LogAttempt) {
    logMu.Lock(); defer logMu.Unlock()
    // ⑥ 保留已累积历史(跨非终态事件不丢失)
    if prev, ok := logRecords[record.ID]; ok {
        record.attempts = prev.attempts
    }
    if attempt != nil {
        attempt.Type = eventType
        switch eventType {
        case LogEventAttemptStarted:
            record.currentAttempt = attempt
        case LogEventAttemptFinished, LogEventResponseCommitted:
            record.currentAttempt = nil
            record.attempts = append(record.attempts, *attempt) // ⑥ 追加终态
        }
    }
    if eventType == LogEventRequestFinished {
        record.currentAttempt = nil
    }
    logRecords[record.ID] = record
    // ...sendLogOverviewLocked / sendLogDetailLocked 不变
}
```

**切片别名安全**:`record.attempts = prev.attempts`(共享底层),append 若就地写只在 slot≥len,prev 的 len 不变不读;若扩容则新数组,prev 冻结。读侧(GetLogAttemptsByChannel)只读 [0:len),终态后 logRecords[id].attempts 只读不写。安全。✓

### 2.4 GetLogAttemptsByChannel 查询
```go
// ChannelAttemptDetail 按渠道查调用明细的单条记录(请求级 + 尝试级)。
type ChannelAttemptDetail struct {
    RequestID        uint64        `json:"request_id"`
    RequestState     RequestState  `json:"request_state"`
    StartedAt        time.Time     `json:"started_at"`
    RequestModel     string        `json:"request_model"`
    FinalChannelName string        `json:"final_channel_name"`
    AttemptIndex     int           `json:"attempt_index"`
    ChannelName      string        `json:"channel_name"`
    ModelName        string        `json:"model_name"`
    Status           AttemptStatus `json:"status"`
    Duration         int64         `json:"duration"`
    Error            string        `json:"error,omitempty"`
}

// GetLogAttemptsByChannel 返回内存窗口内指定渠道被尝试的每次调用明细,按请求时间倒序。
func GetLogAttemptsByChannel(channelName string) []ChannelAttemptDetail {
    logMu.Lock(); defer logMu.Unlock()
    rows := make([]ChannelAttemptDetail, 0)
    for _, record := range logRecords {
        for i := range record.attempts {
            a := record.attempts[i]
            if a.ChannelName != channelName { continue }
            rows = append(rows, ChannelAttemptDetail{
                RequestID: record.ID, RequestState: record.State,
                StartedAt: record.StartedAt, RequestModel: record.RequestModel,
                FinalChannelName: record.FinalChannelName,
                AttemptIndex: a.Index, ChannelName: a.ChannelName, ModelName: a.ModelName,
                Status: a.Status, Duration: a.Duration, Error: a.Error,
            })
        }
    }
    sort.Slice(rows, func(i, j int) bool { return rows[i].StartedAt.After(rows[j].StartedAt) })
    return rows
}
```
内存上限天然收敛于 trim 50 请求,无需额外分页/上限(每请求 attempts 数有限)。

## 3. execution.go 改动(设置 Status/Duration)

各终态调用点在 `e.emit(...)` 前设 `attempt.Status`/`attempt.Duration`,applyLog 累积时即带上:

- `recordUnavailableTarget`(L267-280):未发上游。`attempt.Status = AttemptSkipped; attempt.Duration = 0`(startedAt 未记,瞬时失败)。已设 `attempt.Error`。在 `e.emit(LogEventAttemptFinished, attempt)` 前。
- `handleAttemptFailure`(L197-228):上游失败。`duration` 入参已有(`time.Since(startedAt)`,L189)。三处 `e.emit(LogEventAttemptFinished, attempt)`(L207/212/226)前设 `attempt.Status = AttemptFailed; attempt.Duration = duration.Milliseconds()`。已设 `attempt.Error`。
- `commitAttempt`(L231-254):上游成功提交。`metricDuration` 已算(L241-244)。`e.emit(LogEventResponseCommitted, attempt)`(L233)前设 `attempt.Status = AttemptSuccess; attempt.Duration = metricDuration.Milliseconds()`。commit.err 是客户端写失败(请求级),不影响 attempt 级 success 判定。

**不改 `e.log.attempts`**——累积由 applyLog 负责,execution.go 只设字段 + emit。

## 4. HTTP 接口(handlers/log.go)

```go
// init() 内 AddRoute:
router.NewRoute("/channel-attempts", http.MethodGet).Handle(getChannelAttempts)

func getChannelAttempts(c *gin.Context) {
    name := strings.TrimSpace(c.Query("channel_name"))
    if name == "" {
        resp.Error(c, http.StatusBadRequest, "missing channel_name")
        return
    }
    resp.Success(c, relay.GetLogAttemptsByChannel(name))
}
```
- 路由安全:`/clear`(depth-1 静态)已与 `/:id/...`(depth-1 param :id)共存注册成功,故 `/channel-attempts`(depth-1 静态)无冲突。✓
- Auth 已由 `/api/v1/log` 组 `Use(middleware.Auth())` 覆盖。只读不写库。✓

## 5. 前端设计

### 5.1 接入方式:MorphingDialog(非路由)
上游日志详情是 `MorphingDialog`(`useMorphingDialog`,`Item.tsx`),非 NavItem 路由。⑥ 渠道详情同款:从 `channel/Card.tsx` 按钮打开 MorphingDialog,内部渲染明细。不加 NavItem、不改 `app.ts` 的 `Page` 联合类型(YAGNI,与上游模式一致)。

### 5.2 useChannelAttempts hook(api/log.ts)
```ts
export interface ChannelAttemptDetail { /* 镜像后端结构 */ }
export function useChannelAttempts(channelName: string, enabled = true) {
    return useQuery({
        queryKey: ['channel-attempts', channelName],
        queryFn: () => apiRequest<ChannelAttemptDetail[]>(
            `/api/v1/log/channel-attempts?channel_name=${encodeURIComponent(channelName)}`,
        ),
        enabled: enabled && !!channelName,
        staleTime: 0, // 实时刷新(内存窗口随时变)
    });
}
```

### 5.3 渠道详情视图组件
- 位置:`web/src/components/modules/channel/CallDetail.tsx`(新文件)。
- 结构:顶部概览(总/各状态计数,用 useMemo 聚合)+ VirtualizedGrid 或列表渲染逐条明细。
- 字段映射见 prd In Scope。request_id 行可点击 → `useAppStore.getState().setCurrentPage('log')`(跳日志页)。
- 空态:内存窗口无该渠道尝试时提示(复用上游空态样式)。

### 5.4 Card.tsx 入口
在 `channel/Card.tsx` 卡片操作区加「调用详情」按钮,`onClick` 打开 CallDetail MorphingDialog,传 `channel.name`。

### 5.5 stretch:自动打开特定请求详情
行内 request_id 点击 → setCurrentPage('log') 后,若要在日志页自动打开该请求 MorphingDialog,需经 app store 传 `pendingLogRequestId`(日志页 LogCard 消费后自启)。**本期列为 stretch**,基线只跳页;若 implement 阶段评估低成本(<20 行)则一并做,否则记 TODO。

### 5.6 i18n
三语 `web/src/locales/{en,zh_hans,zh_hant}.json` 同步加 `channel.callDetail.*`(title/total/trigger/empty + 状态名)。

## 6. 与 ④ 的协作契约
- ④ 改 `LogOverview`(user_agent/client_name);⑥ 改 `LogAttempt`/`LogRecord`/`applyLog`。同文件 `internal/relay/log.go`,不同结构体,字段不冲突。④ 已归档(`f3052c4`),⑥ 在其之上增量改,无合并冲突。
- 前端 ④ 的 `RelayLogOverview` 类型已有 user_agent/client_name;⑥ 的 `RelayAttempt` 类型加 status/duration(可选),与 ④ 无交集。

## 7. 兼容性与回滚
- 后端:LogRecord/LogAttempt 加字段纯增量,applyLog 累积逻辑向后兼容(无历史时 attempts 为空,nil 安全)。`streamDetail` SSE 负载多出 status/duration 字段,前端 `RelayAttempt` 可选字段兼容(旧 Item.tsx 忽略新字段,不报错)。
- 前端:新组件 + Card 按钮,不改既有页面结构。
- 回滚:revert 本子任务 commit 即可,无 DB 迁移、无配置项。
