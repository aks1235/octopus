package relay

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
)

// 客户端请求在转发过程中的当前状态。
type Status string

const (
	StatusRunning   Status = "running"   // 循环中: 正在选目标, 等待或请求上游。
	StatusCommitted Status = "committed" // 首字节已写出客户端, 此后不可再重试。
	StatusSuccess   Status = "success"   // 响应已完整交付客户端。
	StatusFailed    Status = "failed"    // 请求以错误结束。
	StatusCanceled  Status = "canceled"  // 客户端提前断开或取消。
)

// 流式请求在 committed 之后所处的输出相位, 与 Status 正交: Status 说明"响应已提交", 相位说明当前在思考还是在输出正文。
const (
	phaseThinking  = "thinking"  // 模型正在思考: 正在产出 reasoning 增量。
	phaseAnswering = "answering" // 模型正在输出正文: 已开始产出正文增量。
)

// 客户端请求的完整进程内状态, 同时作为状态流的消息形状; 上半部分在请求到达时写入并在结束时定稿, 下半部分每轮循环覆盖。
type RequestState struct {
	ID         uint64         `json:"id"`           // 请求在当前进程内的唯一标识。
	Status     Status         `json:"status"`       // 请求当前状态。
	StartedAt  time.Time      `json:"started_at"`   // 请求到达时间。
	Duration   time.Duration  `json:"duration"`     // 请求总耗时, 未结束时为零。
	Model      string         `json:"model"`        // 客户端请求的模型名称, 即分组名称。
	Protocol   model.Protocol `json:"protocol"`     // 客户端请求使用的协议, 由入站格式定出, 单个协议位而非掩码组合。
	GroupID    int            `json:"group_id"`     // 承载本请求的分组 ID, 供界面按主键直接定位分组而不必按名称回查。
	APIKeyName string         `json:"api_key_name"` // 发起请求的 API Key 名称, 登记时快照, 查询失败留空。
	Usage      llm.Usage      `json:"usage"`        // 请求结束时写入的展示用量。
	Cost       float64        `json:"cost"`         // 请求结束时写入的累计费用。

	ClientName      string `json:"client_name,omitempty"`      // 从 User-Agent 识别的客户端标识, 如 claude-code; 未知为空。
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // 客户端请求携带的思考等级; 非推理请求为空。
	Phase           string `json:"phase,omitempty"`            // 流式输出相位: "" 未定 / "thinking" 思考中 / "answering" 输出正文; 仅流式请求有值。
	FirstTokenMs    int    `json:"first_token_ms,omitempty"`   // 首字耗时(毫秒), 首个有效上游响应到达时相对请求到达时间计; 未取得响应前为空, 定稿后保留。
	PhaseChars      int64  `json:"phase_chars,omitempty"`      // 进行中的流式请求在当前相位已产出的字符数(思考相位计思考增量, 正文相位计正文增量); 相位未定或已定稿时为空。
	PhaseSpeed      int    `json:"phase_speed,omitempty"`      // 进行中的流式请求在当前相位的字符速度(字符/秒), 按相位起点到发布时点计算; 结束后由前端改用用量推导的精确速度。

	Round          int            `json:"round"`            // 最新一轮循环的递增序号, 人工中止按此匹配以免误杀下一轮。
	RoundStartedAt time.Time      `json:"round_started_at"` // 最新一轮上游请求的开始时间, 未开始过为零。
	TargetChannel  string         `json:"target_channel"`   // 最新一轮选中的渠道名称。
	TargetModel    string         `json:"target_model"`     // 最新一轮实际请求上游的模型名称。
	TargetProtocol model.Protocol `json:"target_protocol"`  // 最新一轮实际请求上游的协议, 与 Protocol 不同即本轮做了跨协议转换; 0 表示尚未选出。
	Sending        bool           `json:"sending"`          // 最新一轮是否仍在等待上游响应。
	Error          string         `json:"error,omitempty"`  // 最新一轮的失败原因, 请求结束后即为最终错误。

	body         string             // 客户端原始请求体, 体积大故不进状态流, 由独立接口按需拉取。
	responseBody string             // 聚合后的完整最终响应体, 同样按需拉取。
	apiKeyID     int                // 发起请求的 API Key ID, 用于请求完成后的归属统计。
	cancel       context.CancelFunc // 中止最新一轮上游请求, 仅在该轮等待响应期间非空。
	// outputPublishAt 是实时速度下一次允许发布的时点, 用于把每个增量事件的推送收敛到每秒一次。
	outputPublishAt time.Time
	// phaseStartedAt 是当前相位的计时起点: 相位切换(思考→正文)时重置, 使速度快照反映新相位自身的速度而非整轮均值。
	phaseStartedAt time.Time
}

const streamBuffer = 16 // 单个状态流连接的非阻塞消息缓冲容量。
const maxFinished = 50  // 进程内最多保留的已结束请求数量。

// outputPublishInterval 是流式实时速度的发布节流间隔: 增量事件每秒可达数十上百个, 每事件推送会淹没状态流,
// 故最快每秒发布一次速度快照, 期间只累加字符数。
const outputPublishInterval = time.Second

var (
	idSeq    atomic.Uint64                          // 进程内严格递增的请求 ID。
	mu       sync.Mutex                             // 全部共享状态的互斥锁。
	requests = make(map[uint64]*RequestState)       // 按请求 ID 保存的全部请求状态。
	watchers = make(map[chan RequestState]struct{}) // 全部状态流 SSE 连接。
)

// newRequestState 分配请求 ID 并登记初始运行状态; 返回的记录是本请求后续全部状态写入的入口。
func newRequestState(ctx context.Context, modelName string, groupID int, protocol model.Protocol, body string, apiKeyID int, userAgent, reasoningEffort string) *RequestState {
	mu.Lock()
	defer mu.Unlock()

	request := &RequestState{
		ID:              idSeq.Add(1),
		Status:          StatusRunning,
		StartedAt:       time.Now(),
		Model:           modelName,
		Protocol:        protocol,
		GroupID:         groupID,
		ClientName:      detectClient(userAgent),
		ReasoningEffort: reasoningEffort,
		body:            body,
		apiKeyID:        apiKeyID,
	}
	// 登记时保存 API Key 名称快照, 查询失败留空; op.APIKeyGet 只读内存缓存, 持锁无 I/O。
	// 快照须在首次发布前写入, 界面首条消息即带名称。
	if apiKey, err := op.APIKeyGet(apiKeyID, ctx); err == nil {
		request.APIKeyName = apiKey.Name
	}
	requests[request.ID] = request
	publishRequestLocked(request)
	return request
}

// startRound 记录本轮选中的目标并进入上游请求, cancel 供人工中止本轮, 返回递增的轮次序号。
func (r *RequestState) startRound(cancel context.CancelFunc, channel, modelName string, protocol model.Protocol) int {
	mu.Lock()
	defer mu.Unlock()

	r.Round++
	r.RoundStartedAt = time.Now()
	r.TargetChannel = channel
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.Sending = true
	r.Error = ""
	r.cancel = cancel
	publishRequestLocked(r)
	return r.Round
}

// finishRound 记录本轮上游结果, errText 为空表示已取得可提交响应。
func (r *RequestState) finishRound(errText string) {
	mu.Lock()
	defer mu.Unlock()

	r.Sending = false
	r.Error = errText
	r.cancel = nil
	publishRequestLocked(r)
}

// Interrupt 中止指定请求仍在等待响应且轮次匹配的上游请求; 轮次不匹配说明该轮已结束, 不影响后续轮次。
func Interrupt(id uint64, round int) {
	mu.Lock()
	request := requests[id]
	if request == nil || request.Round != round || request.cancel == nil {
		mu.Unlock()
		return
	}
	cancel := request.cancel
	request.cancel = nil
	mu.Unlock()

	cancel()
}

// wait 在重新选择目标之前退避 seconds 秒; 客户端在退避期间断开时以取消终态定稿并返回 false。
func (r *RequestState) wait(ctx context.Context, seconds int) bool {
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-time.After(time.Duration(seconds) * time.Second):
		return true
	}
}

// markCommitted 标记响应已提交; 流式响应在此之后仍会持续转发, 故必须先于提交动作调用。
func (r *RequestState) markCommitted() {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCommitted
	publishRequestLocked(r)
}

// markPhase 更新流式请求的输出相位; 相位未变化时不赋值也不推送, 因而每个请求至多推送两次(进 thinking, 进 answering)。
// 相位切换时一并重置相位字符量与计时起点(思考→正文重新计时), 使速度反映新相位自身的速度; 仅流式循环在提交后调用,
// 非流式与未识别出相位的事件保持留空。
func (r *RequestState) markPhase(phase string) {
	mu.Lock()
	defer mu.Unlock()

	r.markPhaseLocked(phase)
}

// markPhaseLocked 是 markPhase 的持锁版本, 供相位与字符量在同一次加锁内一并入账 (见 addPhaseChars) 时复用。
func (r *RequestState) markPhaseLocked(phase string) {
	if r.Phase == phase {
		return
	}
	// 已进入正文后不再回退到思考: 少数上游会在收尾分片里混入 reasoning 增量, 不应把相位与计时倒退
	// (与合并前「进入 answering 即不再分类」的既有语义一致)。
	if r.Phase == phaseAnswering && phase == phaseThinking {
		return
	}
	r.Phase = phase
	// 新相位从零开始: 思考期与正文期的字符量各归各, 互不掺入对方的字符与时间。
	r.PhaseChars = 0
	r.PhaseSpeed = 0
	r.phaseStartedAt = time.Now()
	// 节流基线一并重置, 使新相位的首个速度快照在完整间隔后发布, 避免除以极短窗口得出虚高速度。
	r.outputPublishAt = time.Time{}
	publishRequestLocked(r)
}

// markFirstToken 记录首个有效上游响应到达时的首字耗时, 已记录过则不再覆盖(多轮重试取首次成功的时刻)。
// 起点与转发日志落库的 ftut 同源同算法(见 firstTokenElapsedMs), 界面据此用「总耗时 − 首字」推导输出速度,
// 使实时日志卡片与历史面板对同一请求给出相同数值。
func (r *RequestState) markFirstToken(at time.Time) {
	mu.Lock()
	defer mu.Unlock()

	if r.FirstTokenMs > 0 {
		return
	}
	r.FirstTokenMs = firstTokenElapsedMs(r.StartedAt, at)
}

// firstTokenElapsedMs 计算首字耗时(毫秒): 首个有效上游响应到达时刻相对请求到达时间的毫秒数。
// 实时状态的首字字段与转发日志的 ftut 共用此函数, 两处数值不会因算法分歧而不同。
func firstTokenElapsedMs(startedAt, firstValidAt time.Time) int {
	return int(firstValidAt.Sub(startedAt).Milliseconds())
}

// addPhaseChars 把一次流事件的相位与增量字符数入账, 并按节流间隔发布当前相位的速度快照 (R1/R2)。
// phase 非空时按事件相位切换(见 markPhaseLocked), 切换即重置字符量与计时起点, 使思考→正文重新计时;
// count 为该事件携带的增量字符数, 由事件解析搭车得出(见 parseStreamEvent): 思考增量计思考字符, 正文增量计正文
// 字符, 一次事件至多落入其中一类, 故两者可相加传入。
// 相位未定(未识别出相位)时不累计也不发布, 与界面「相位未定显示 -」的既有表现一致。
// 首帧只立节流基线而不发布: 此刻距相位起点不足一个间隔, 直接发布会把刚产出的字符数除以极短耗时得出虚高速度。
func (r *RequestState) addPhaseChars(phase string, count int) {
	mu.Lock()
	defer mu.Unlock()

	if phase != "" {
		r.markPhaseLocked(phase)
	}
	// 入账相位必须与当前相位一致: 被阻断的相位回退(进入正文后收尾分片混入的思考增量)不并入当前相位,
	// 否则思考字符会抬高正文相位的「输出 c/s」, 与界面「输出速度只反映正文增量」的语义不符 (R1)。
	if count <= 0 || r.Phase == "" || (phase != "" && phase != r.Phase) {
		return
	}
	r.PhaseChars += int64(count)
	now := time.Now()
	if r.outputPublishAt.IsZero() {
		r.outputPublishAt = now.Add(outputPublishInterval)
		return
	}
	if now.Before(r.outputPublishAt) {
		return
	}
	r.outputPublishAt = now.Add(outputPublishInterval)
	r.PhaseSpeed = outputCharSpeed(r.PhaseChars, r.phaseWindowStart(), now)
	publishRequestLocked(r)
}

// phaseWindowStart 返回当前相位速度的耗时窗口起点: 正常路径下相位切换时已写入相位起点;
// 尚未识别出相位(例如测试直接构造的状态)时回退到本轮或请求起点, 保证窗口非零。
func (r *RequestState) phaseWindowStart() time.Time {
	if !r.phaseStartedAt.IsZero() {
		return r.phaseStartedAt
	}
	return r.outputWindowStart()
}

// outputWindowStart 返回实时速度的耗时窗口起点: 已提交的流式请求不会再换轮, 故取本轮上游请求的开始时间;
// 尚未开始过轮次(例如测试直接构造的状态)时回退到请求到达时间。
func (r *RequestState) outputWindowStart() time.Time {
	if !r.RoundStartedAt.IsZero() {
		return r.RoundStartedAt
	}
	return r.StartedAt
}

// outputCharSpeed 由已产出字符数与耗时窗口算出字符速度(字符/秒); 窗口非正时返回零, 避免除零或负速度。
func outputCharSpeed(chars int64, start, now time.Time) int {
	elapsed := now.Sub(start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return int(float64(chars) / elapsed)
}

// markSucceeded 以成功终态定稿请求。
func (r *RequestState) markSucceeded(responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusSuccess
	r.Error = ""
	r.responseBody = responseBody
	r.finishLocked(usage)
}

// markFailed 以失败终态定稿请求, 最终错误取自本次失败原因。
func (r *RequestState) markFailed(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusFailed
	r.Error = err.Error()
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// markCanceled 以取消终态定稿请求, 用于客户端提前断开或主动取消。
func (r *RequestState) markCanceled(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCanceled
	r.Error = err.Error()
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// finishLocked 写入用量和费用, 发布终态, 更新请求级统计并裁剪历史; 调用方必须持有锁。
func (r *RequestState) finishLocked(usage *llm.Usage) {
	r.Sending = false
	r.cancel = nil
	// 实时速度只在流式进行中有值: 定稿后归零, 界面改用 usage 与耗时推导的精确速度 (R2/R5)。
	r.PhaseChars = 0
	r.PhaseSpeed = 0
	if usage != nil {
		r.Usage = *usage
	}
	metrics := usageMetrics(r.TargetModel, usage)
	r.Cost = metrics.InputCost + metrics.OutputCost
	r.Duration = time.Since(r.StartedAt)
	metrics.WaitTime = r.Duration.Milliseconds()
	if r.Status == StatusSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	_ = op.StatsTotalUpdate(metrics)
	_ = op.StatsHourlyUpdate(metrics)
	_ = op.StatsDailyUpdate(context.Background(), metrics)
	if r.apiKeyID > 0 {
		_ = op.StatsAPIKeyUpdate(r.apiKeyID, metrics)
	}
	publishRequestLocked(r)

	finished := 0
	oldest := uint64(0)
	for id, request := range requests {
		if request.Status == StatusRunning || request.Status == StatusCommitted {
			continue
		}
		finished++
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	if finished > maxFinished {
		delete(requests, oldest)
	}
}

// usageMetrics 将统一用量按模型单价转换为 Token 与费用统计; 无用量或价格时对应费用为零。
func usageMetrics(modelName string, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	price, err := op.LLMGet(modelName)
	if err != nil {
		return metrics
	}
	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// publishRequestLocked 非阻塞发布最新请求状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 调用方必须持有锁。
func publishRequestLocked(request *RequestState) {
	for stream := range watchers {
		select {
		case stream <- *request:
		default:
			delete(watchers, stream)
			close(stream)
		}
	}
}

// OpenRequestStream 注册请求状态流连接, 返回按请求 ID 倒序的全部快照和后续增量通道。
// 日志页不提供排序开关, 而 requests 是 map, 遍历顺序随机, 故顺序须由此处定稿。
func OpenRequestStream() ([]RequestState, chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan RequestState, streamBuffer)
	watchers[stream] = struct{}{}

	snapshot := make([]RequestState, 0, len(requests))
	for _, request := range requests {
		snapshot = append(snapshot, *request)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ID > snapshot[j].ID })
	return snapshot, stream
}

// CloseRequestStream 注销并关闭指定请求状态流连接。
func CloseRequestStream(stream chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := watchers[stream]; exists {
		delete(watchers, stream)
		close(stream)
	}
}

// RequestBody 返回指定请求保存的原始请求体, 记录不存在时返回空串。
func RequestBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.body
	}
	return ""
}

// ResponseBody 返回指定请求当前保存的响应体, 记录不存在或响应未完成时返回空串。
func ResponseBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.responseBody
	}
	return ""
}

// Clear 删除全部已结束的请求记录。
func Clear() {
	mu.Lock()
	defer mu.Unlock()

	for id, request := range requests {
		if request.Status != StatusRunning && request.Status != StatusCommitted {
			delete(requests, id)
		}
	}
}
