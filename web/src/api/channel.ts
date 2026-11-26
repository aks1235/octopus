import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import { channelStatsQueryOptions, groupListQueryOptions, modelListQueryOptions } from './queries';
import { formatStatsMetrics, type StatsMetrics, type StatsMetricsFormatted } from './stats';

// Protocol 是渠道支持的上游线协议位掩码，位值与后端 model.Protocol 一致，不可变更。
// 一条授权可同时支持多个协议，按位或组合；1 << 0 由后端保留待用。
export const Protocol = {
    OpenAIChatCompletion: 1 << 1,
    OpenAIResponse: 1 << 2,
    AnthropicMessage: 1 << 3,
} as const;

// Dialect 是上游在标准协议之上的方言，决定出站转换器的厂商特化配置。
// 地址与路径不属于方言范畴，由前端按服务商预填到渠道字段上。
export type Dialect = 'generic';

type CustomHeader = {
    header_key: string;
    header_value: string;
};

// ChannelKey 是渠道下的一份上游凭据；名称在渠道内唯一，读写都按它引用。
export type ChannelKey = {
    name: string;
    key: string;
    enabled: boolean;
};

// ChannelGrant 是渠道内的一条上游授权：指定模型使用指定凭据时支持的协议集合，也是转发的最小单位。
// 两侧按名称引用，读写同构：名称在渠道内唯一，新增的模型与凭据在后端同一事务内才分配主键，
// 故提交侧只能按名称引用，读侧也随之给名称，页面无需在主键与名称之间翻译。
// 授权本身没有启用状态: 不再授权即删掉该组合, 临时收回则停用凭据或摘掉协议位。
export type ChannelGrant = {
    model_name: string;
    key_name: string;
    protocols: number; // Protocol 位掩码。
};

// ChannelGrantCandidate 是分组页可选取的一条授权，字段与 GroupItem 的展示字段一一对应。
// 分组页由此不必拉整份渠道列表：那里带着统计、路径、代理与凭据明文，与选取成员无关。
// available 与分组成员同一口径，均由后端定稿，前后端不会各判一套。
export type ChannelGrantCandidate = {
    id: number; // 授权主键，分组成员按它引用。
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    protocols: number; // Protocol 位掩码。
    available: boolean;
};

/**
 * 渠道完整配置（与后端 model.ChannelDetail 对齐）
 * 读写同构：编辑表单读到什么形状就提交什么形状，故创建与更新共用此类型，无需另建提交类型。
 * 提交即全量，未列出的凭据与模型会被删除并级联删除其授权；创建时 id 取 0，由后端分配。
 * keys、models、grants 和 custom_header 恒为数组，后端读取侧承诺不为 null。
 */
export type ChannelDetail = {
    id: number;
    name: string;
    dialect: Dialect;
    enabled: boolean;
    base_url: string; // 上游默认地址，各协议共用。
    openai_base_url: string; // OpenAI 侧专用地址；留空沿用 base_url（上游按协议分入口时用）。
    anthropic_base_url: string; // Anthropic 侧专用地址；留空沿用 base_url。
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    keys: ChannelKey[];
    models: string[]; // 上游模型名称；模型除名称外没有界面用得上的字段。
    grants: ChannelGrant[];
    proxy: boolean;
    custom_header: CustomHeader[];
    param_override: string;
    channel_proxy: string;
    match_regex: string;
    health_check_skip: boolean; // 跳过健康检查；适用于无模型列表接口的渠道（探测必失败但转发可用）。
    official: boolean; // 官方/云厂商渠道：每次探测都消耗真实额度，故只参加每日夜间一次的那一轮。

    // 订阅套餐额度采集。套餐真实余量只在云厂商控制面上，推理 key 查不到，需另配账号级 AK/SK 签名请求。
    // plan_quota_vendor 留空表示该渠道不采集额度。
    plan_quota_vendor: string; // 采集厂商；目前只支持 volcengine。
    plan_quota_kind: string; // 套餐类型：agent_plan / coding_plan。
    plan_quota_access_key_id: string;
    plan_quota_secret_access_key: string; // 只提交不回传：读取时恒为空串。
    plan_secret_configured?: boolean; // 只读回执：该渠道是否已存过 SK。编辑时 SK 留空表示沿用原值。
};

// ChannelModelStats 是单个渠道模型的累计统计，自带名称。
export type ChannelModelStats = StatsMetrics & {
    model_id: number;
    model_name: string;
};

// ChannelStats 是单个渠道及其模型的累计统计，自带名称与启停状态。
// 这一份同时充当渠道列表项：列表页要展示的名称、启停与模型个数（即 models.length）都在此，
// 故没有单独的渠道概览接口，整份配置在点开编辑时由 useChannelDetail 单独取。
// 健康状态是后端定时探测的只读结果，只随统计查询外出，不属于编辑形态 ChannelDetail。
export type ChannelStats = StatsMetrics & {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    official: boolean; // 官方直连或云厂商托管：每次探测消耗真实额度，定时探测只在夜间跑一次。
    health_fail_count: number; // 连续健康检查失败次数；探测成功后清零。
    last_health_error: string; // 最近一次健康检查的失败原因；探测成功后清空。
    last_health_at: number; // 最近一次健康检查完成的 unix 秒；0 表示尚未检查过。
    auto_disabled: boolean; // 是否因连续健康检查失败被自动禁用。
    models: ChannelModelStats[];
};

// ChannelModelStatsFormatted 是单个渠道模型的展示用统计。
export type ChannelModelStatsFormatted = {
    model_id: number;
    model_name: string;
    formatted: StatsMetricsFormatted;
};

// ChannelKeyStats 是单个渠道凭据的累计统计，与渠道/模型统计同一口径。
// 凭据禁用只影响选路，统计照常保留，enabled 供界面区分展示。
export type ChannelKeyStats = StatsMetrics & {
    key_id: number;
    key_name: string;
    enabled: boolean;
};

// ChannelKeyStatsFormatted 是单个渠道凭据的展示用统计。
export type ChannelKeyStatsFormatted = {
    key_id: number;
    key_name: string;
    enabled: boolean;
    formatted: StatsMetricsFormatted;
};

// ChannelStatsFormatted 是单个渠道及其模型的展示用统计，同时充当渠道列表项。
// 名称与启停随统计一并给出，列表页由此只消费这一条查询：模型个数即 models.length，
// 整份配置在点开编辑时由 useChannelDetail 单独取。
export type ChannelStatsFormatted = {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    official: boolean; // 官方直连或云厂商托管：列表上打"官方"标记，探测只在夜间跑一次。
    health_fail_count: number;
    last_health_error: string;
    last_health_at: number;
    auto_disabled: boolean;
    models: ChannelModelStatsFormatted[];
    formatted: StatsMetricsFormatted;
};

// FetchModelRequest 按指定凭据试拉上游模型列表。
// 渠道尚未保存时也可试拉，故随请求携带整份渠道配置：探测用的地址、协议路径、代理、Header 与过滤表达式
// 必须和保存后生效的完全一致，直接给编辑态即可，探测用不上的字段后端忽略。
// 名称可为空：探测常发生在渠道尚未命名时，后端只要求地址非空。
// 不带协议：后端一次同时探 OpenAI 与 Anthropic 两侧，协议支持由各侧响应决定。
type FetchModelRequest = {
    channel: Omit<ChannelDetail, 'id' | 'keys' | 'models' | 'grants'>;
    key: string;
};

// FetchModel 是探测到的单个上游模型及其可选协议集合。
// OpenAI 模型同时提供 Chat Completions 与 Responses 选项；Anthropic 模型标记 Messages。
export type FetchModel = {
    name: string;
    protocols: number; // Protocol 位掩码。
};

// TestKeyRequest 按指定凭据对模型列表逐个发起最小真实请求，测试凭据的模型连通性。
// 与 FetchModelRequest 同理：渠道尚未保存时也可测试，故携带整份编辑态配置，
// 测试用的地址、路径、代理与 Header 必须和保存后生效的完全一致。
// 模型由调用方给出：默认测活只给一个（该凭据有授权的第一个模型），测全部给渠道已配的全部模型，单模型重测只给一个。
// key_name 与 grants 供后端按「模型 x 凭据」取实际授权协议位精确试测，并把测试凭据名写进测试日志。
type TestKeyRequest = {
    channel: Omit<ChannelDetail, 'id' | 'keys' | 'models' | 'grants'>;
    channel_id: number; // 渠道主键；表单渠道尚未保存时为 0，测试日志照写。
    key: string;
    key_name: string;
    models: string[];
    grants: ChannelGrant[];
};

// KeyTestResult 是单个模型的凭据连通性测试结果。
// 后端按协议优先级逐个尝试出站端点，任一成功即成功并记入 protocol；失败时 error 带截断后的摘要。
export type KeyTestResult = {
    model_name: string;
    success: boolean;
    protocol: number; // 成功时使用的 Protocol 位；失败时为 0。
    error: string;
};

// channelGrantListQueryOptions 供分组页查询可选授权。
// 与渠道列表分开: 选取成员只需名称与可用性, 拉整份渠道会连带路径, 代理与凭据明文。
export const channelGrantListQueryOptions = queryOptions({
    queryKey: ['channels', 'grants'],
    queryFn: () => apiRequest<ChannelGrantCandidate[]>('/api/v1/channel/grants'),
});

// useChannelGrantList 获取分组页可选的全部渠道授权。
export function useChannelGrantList(enabled = true) {
    return useQuery({ ...channelGrantListQueryOptions, enabled, refetchOnMount: 'always' });
}

// channelStatsFormattedQueryOptions 统一渠道统计查询, 格式化和刷新策略。
// 与配置分开刷新: 统计每次转发都在变, 配置只在人工改动后由 mutation 失效。
const channelStatsFormattedQueryOptions = queryOptions({
    ...channelStatsQueryOptions,
    select: (data) => data.map((item): ChannelStatsFormatted => ({
        channel_id: item.channel_id,
        channel_name: item.channel_name,
        enabled: item.enabled,
        official: item.official,
        health_fail_count: item.health_fail_count,
        last_health_error: item.last_health_error,
        last_health_at: item.last_health_at,
        auto_disabled: item.auto_disabled,
        models: item.models.map((channelModel) => ({
            model_id: channelModel.model_id,
            model_name: channelModel.model_name,
            formatted: formatStatsMetrics(channelModel),
        })),
        formatted: formatStatsMetrics(item),
    })),
    refetchInterval: 30000,
    refetchOnMount: 'always',
});

// useChannelStats 获取全部渠道及其模型的展示用统计, 也是渠道列表页的数据来源。
export function useChannelStats(enabled = true) {
    return useQuery({ ...channelStatsFormattedQueryOptions, enabled });
}

/**
 * 获取单个渠道各凭据的展示用统计 Hook, 供渠道统计页按凭据维度查看。
 * 与渠道统计同一刷新节奏: 凭据统计由转发链路累加, 每次转发都在变。
 *
 * @example
 * const { data: keys } = useChannelKeyStats(channel.channel_id);
 */
export function useChannelKeyStats(id?: number) {
    return useQuery({
        queryKey: ['channels', 'key-stats', id],
        queryFn: () => apiRequest<ChannelKeyStats[]>(`/api/v1/channel/key-stats/${id}`),
        select: (data) => data.map((item): ChannelKeyStatsFormatted => ({
            key_id: item.key_id,
            key_name: item.key_name,
            enabled: item.enabled,
            formatted: formatStatsMetrics(item),
        })),
        enabled: id !== undefined,
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

// PlanQuotaWindow 是套餐的一个计量窗口。key 决定界面文案，数值单位随厂商（火山为 AFP）。
export type PlanQuotaWindow = {
    key: string; // 5h / week / month。
    used: number;
    total: number;
    percent: number; // 已用百分比 0-100，后端已封顶。
    reset_at: number; // 下次重置的 unix 秒；0 表示该窗口未排定重置。
};

/**
 * ChannelPlanQuota 是渠道订阅套餐最近一次的采集结果。
 * 与网关按 token 累计的统计是两套账，不可互相扣减。
 * windows 为空且 error 也为空表示账号下没有生效中的套餐，不是失败。
 */
export type ChannelPlanQuota = {
    channel_id: number;
    vendor: string;
    kind: string;
    plan_name: string; // 套餐档位，如 Medium；厂商不返回时为空。
    fetched_at: number; // 最近一次采集时刻，成功失败都更新。
    synced_at: number; // 最近一次采集成功的时刻；采集失败时保留旧值。
    error: string; // 最近一次采集的失败原因；成功时为空。
    windows: PlanQuotaWindow[];
};

// 后端对"从未采集过"回空对象、对"采集失败且无历史"回 null 窗口，统一收敛成 null 与空数组，
// 免得消费方每处都写一遍形状判断。
function normalizePlanQuota(row: ChannelPlanQuota | Record<string, never> | null): ChannelPlanQuota | null {
    if (!row || !('channel_id' in row)) return null;
    const quota = row as ChannelPlanQuota;
    return { ...quota, windows: Array.isArray(quota.windows) ? quota.windows : [] };
}

/**
 * 获取渠道套餐额度 Hook。id 为空时不发请求。
 *
 * @example
 * const { data: quota } = useChannelPlanQuota(channelId);
 */
export function useChannelPlanQuota(id?: number) {
    return useQuery({
        queryKey: ['channels', 'plan-quota', id],
        queryFn: async () => normalizePlanQuota(
            await apiRequest<ChannelPlanQuota | Record<string, never> | null>(`/api/v1/channel/plan-quota/${id}`),
        ),
        enabled: id !== undefined,
        refetchOnMount: 'always',
    });
}

/**
 * 立即采集一次渠道套餐额度 Hook，返回采集后的记录。
 * 采集失败不抛错：失败原因在返回记录的 error 里，界面按常规状态展示。
 *
 * @example
 * const refresh = useRefreshChannelPlanQuota();
 * refresh.mutate(channelId);
 */
export function useRefreshChannelPlanQuota() {
    const queryClient = useQueryClient();

    return useMutation({
        // 必须带 body：后端按 Content-Type 拦非 GET 请求，空 body 会因缺少 JSON 头被拒成 415。
        mutationFn: (id: number) =>
            apiRequest<ChannelPlanQuota | Record<string, never> | null>(
                `/api/v1/channel/plan-quota-refresh/${id}`,
                { method: 'POST', body: {} },
            ),
        onSuccess: (data, id) => {
            queryClient.setQueryData(['channels', 'plan-quota', id], normalizePlanQuota(data));
            // 首页聚合用的是另一条查询, 手动刷新后同步失效, 免得两处数字不一致。
            queryClient.invalidateQueries({ queryKey: ['channels', 'plan-quota', 'list'] });
        },
    });
}

/**
 * 立即采集全部套餐额度 Hook，供首页那块的一键刷新。
 * 后端同步采完才返回，回来直接无效化列表，不必轮询。
 *
 * @example
 * const refresh = useRefreshPlanQuotaList();
 * refresh.mutate();
 */
export function useRefreshPlanQuotaList() {
    const queryClient = useQueryClient();

    return useMutation({
        // 必须带 body：后端按 Content-Type 拦非 GET 请求，空 body 会因缺少 JSON 头被拒成 415。
        mutationFn: () =>
            apiRequest<{ channels: number }>('/api/v1/stats/plan-quota-refresh', {
                method: 'POST',
                body: {},
            }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels', 'plan-quota'] });
        },
    });
}

// PlanQuotaListItem 是首页聚合视图的一条: 额度快照外加渠道名与启停。
export type PlanQuotaListItem = ChannelPlanQuota & {
    channel_name: string;
    enabled: boolean;
};

/**
 * 获取全部已采集的套餐额度 Hook，供首页一屏展示。
 * 后端给空数组而不是 null，此处仍兜一层，避免旧版本后端返回 null 时上层崩在 .map 上。
 *
 * 每分钟重取: 采集 15 分钟一轮, 不重取就只能靠用户手动点刷新, 页面会一直停在旧数字上。
 * 返回空数组表示一条额度都没有。
 *
 * @example
 * const { data: items } = usePlanQuotaList();
 */
export function usePlanQuotaList() {
    return useQuery({
        queryKey: ['channels', 'plan-quota', 'list'],
        queryFn: async () => {
            const rows = await apiRequest<PlanQuotaListItem[] | null>('/api/v1/stats/plan-quota');
            return (rows ?? []).map((row) => ({
                ...row,
                windows: Array.isArray(row.windows) ? row.windows : [],
            }));
        },
        refetchInterval: 60_000,
    });
}

// ChannelBalance 是 DeepSeek 官方账户余额的一次实时快照, 不入库也不定时。
export type ChannelBalance = {
    available: boolean; // 账户当前能否调用 API。
    entries: {
        currency: string;
        total: string;
        granted: string;
        topped_up: string;
    }[];
};

/**
 * 实时查询官方 DeepSeek 渠道的账户余额, 供渠道详情抽屉展示。
 * 只对官方域名开放, 非官方渠道后端返回 400 —— 组件据此不渲染, 不重试。
 *
 * @example
 * const { data } = useChannelBalance(channelId);
 */
export function useChannelBalance(id: number) {
    return useQuery({
        queryKey: ['channels', 'balance', id],
        queryFn: () => apiRequest<ChannelBalance>(`/api/v1/channel/balance/${id}`),
        refetchOnMount: 'always',
        retry: false,
    });
}

/**
 * 获取单个渠道完整配置 Hook, 供编辑表单打开时读取; id 为空时不发请求。
 * 不随统计一并取回: 整份配置带着路径、代理与凭据明文, 只有正在编辑的那一个渠道用得上。
 *
 * @example
 * const { data: detail } = useChannelDetail(channelId);
 */
export function useChannelDetail(id?: number) {
    return useQuery({
        queryKey: ['channels', 'detail', id],
        queryFn: () => apiRequest<ChannelDetail>(`/api/v1/channel/detail/${id}`),
        enabled: id !== undefined,
        refetchOnMount: 'always',
    });
}

/**
 * 创建渠道 Hook；提交整份配置，id 取 0 由后端分配。
 * 授权与凭据、模型在同一请求提交：授权按名称引用两侧，后端在同一事务内解析为主键。
 *
 * @example
 * const createChannel = useCreateChannel();
 *
 * createChannel.mutate({
 *   id: 0,
 *   name: 'OpenAI',
 *   base_url: 'https://api.openai.com',
 *   keys: [{ name: 'default', key: 'sk-xxx', enabled: true }],
 *   models: ['gpt-4o'],
 *   grants: [{ model_name: 'gpt-4o', key_name: 'default', protocols: Protocol.OpenAIResponse }],
 *   // ...其余配置字段
 * });
 */
export function useCreateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/create', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
        },
    });
}

/**
 * 更新渠道 Hook；提交整份配置，整体替换。
 * keys、models 和 grants 提交即覆盖：未列出的凭据和模型会被删除并级联删除其授权。
 * grants 的 protocols 不能为 0 或含未定义位，名称也必须属于同一渠道，否则后端整单拒绝。
 *
 * @example
 * const updateChannel = useUpdateChannel();
 *
 * updateChannel.mutate({ ...detail, enabled: false });
 */
export function useUpdateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/update', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 删除渠道 Hook
 * 
 * @example
 * const deleteChannel = useDeleteChannel();
 * 
 * deleteChannel.mutate(1); // 删除 ID 为 1 的渠道
 */
export function useDeleteChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/channel/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 启用/禁用渠道 Hook
 * 
 * @example
 * const enableChannel = useEnableChannel();
 * 
 * enableChannel.mutate({ id: 1, enabled: true }); // 启用 ID 为 1 的渠道
 * enableChannel.mutate({ id: 1, enabled: false }); // 禁用 ID 为 1 的渠道
 */
export function useEnableChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: { id: number; enabled: boolean }) =>
            apiRequest<null>('/api/v1/channel/enable', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['channels'] });
            // 渠道启停会改变其授权在分组内的可用性，成员列表要跟着刷新。
            queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 获取渠道模型列表 Hook
 *
 * @example
 * const fetchModel = useFetchModel();
 *
 * fetchModel.mutate({
 *   channel: { base_url: 'https://api.openai.com', openai_response_path: '/v1/responses', ... },
 *   key: 'sk-xxx',
 * });
 *
 * // 在 onSuccess 中获取模型列表
 * fetchModel.data // [{ name: 'gpt-4o', protocols: 4 }, ...]
 */
export function useFetchModel() {
    return useMutation({
        mutationFn: (data: FetchModelRequest) =>
            apiRequest<FetchModel[]>('/api/v1/channel/fetch-model', { method: 'POST', body: data }),
    });
}

/**
 * 按凭据测试模型连通性 Hook。
 * 每个模型发一次最小真实请求（生成上限 1 token），产生真实计费消耗，仅由人工触发，无定时调用。
 * 结果逐模型返回：任一协议成功即成功；失败时 error 带上游状态与原文摘要。
 * 后端逐模型各落一条测试日志（客户端标识「面板测试」），并按 grants 里「模型 x 凭据」的实际协议位精确试测。
 *
 * @example
 * const testKey = useTestChannelKey();
 *
 * testKey.mutate({
 *   channel: toChannelConfig(state),
 *   channel_id: channel?.id ?? 0,
 *   key: 'sk-xxx',
 *   key_name: 'default',
 *   models: ['gpt-4o'], // 默认测活只给一个，整测给 state.models，重测只给一个
 *   grants: toGrantConfigs(state),
 * });
 *
 * // onSuccess 中获取逐模型结果
 * testKey.data // [{ model_name: 'gpt-4o', success: true, protocol: 4, error: '' }]
 */
export function useTestChannelKey() {
    return useMutation({
        mutationFn: (data: TestKeyRequest) =>
            apiRequest<KeyTestResult[]>('/api/v1/channel/test-key', { method: 'POST', body: data }),
    });
}
