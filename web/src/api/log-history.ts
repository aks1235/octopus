import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * 尝试状态。
 * v2 转发链只产生 success/failed; circuit_break/skipped 仅为迁移历史数据保留展示。
 */
export type AttemptStatus = 'success' | 'failed' | 'circuit_break' | 'skipped';

/**
 * 单次渠道尝试信息
 */
export interface ChannelAttempt {
    channel_id: number;
    channel_key_id?: number;
    channel_name: string;
    channel_key_remark?: string;
    model_name: string;
    attempt_num: number;
    status: AttemptStatus;
    duration: number;
    sticky?: boolean;
    msg?: string;
}

/**
 * 按渠道查调用明细的单条记录。
 * 复用 ChannelAttempt 的尝试字段,并增加请求级溯源字段(指向该 attempt 所属的 relay_log)。
 */
export interface ChannelAttemptDetail {
    request_id: number;       // 所属 relay_log.id
    request_time: number;     // relay_log.time(unix 秒)
    request_model: string;    // request_model_name
    request_error?: string;   // relay_log.error(请求级)
    attempt_num: number;      // 本次 attempt 在请求中的序号
    status: AttemptStatus;    // success/failed/circuit_break/skipped
    channel_id: number;       // 本次尝试渠道(非最终成功渠道)
    channel_name?: string;
    channel_key_remark?: string;
    model_name?: string;      // 被试模型
    duration: number;         // 耗时(ms)
    sticky?: boolean;
    msg?: string;
}

/** /api/v1/log/channel-attempts 响应体 */
export interface ChannelAttemptsResponse {
    list: ChannelAttemptDetail[];
    total: number;
    truncated: boolean; // 粗筛行数触顶时为 true,前端提示"仅展示最近 N 条"
}

/**
 * 历史转发日志(落库行)。与实时流的 RelayLogOverview 不同构:
 * time 为 unix 秒, 内容字段仅详情接口返回。
 */
export interface RelayLog {
    id: number;
    time: number;
    request_model_name: string;
    request_api_key_name?: string;
    channel: number;
    channel_name: string;
    actual_model_name: string;
    reasoning_effort?: string;
    input_tokens: number;
    output_tokens: number;
    cached_tokens: number;
    cache_creation_tokens: number;
    ftut: number;
    use_time: number;
    cost: number;
    request_content?: string;
    response_content?: string;
    debug_content?: string;
    error: string;
    attempts?: ChannelAttempt[];
    total_attempts?: number;
    user_agent?: string;
    client_name?: string;
}

/**
 * 获取单条日志详情（含完整的 request_content 和 response_content）
 */
export async function getLogDetail(id: number): Promise<RelayLog> {
    return apiRequest<RelayLog>(`/api/v1/log/${id}`);
}

/** 历史列表筛选条件 */
export interface LogHistoryFilter {
    hasError: boolean;
    apiKeyNames: string[];
    modelNames: string[];
}

/**
 * 历史日志分页查询(无限滚动)。queryKey 独立 ['log-history'] 前缀,
 * 与实时流的内存清空(invalidate)互不误伤。
 */
export function useLogHistory(pageSize: number, filter: LogHistoryFilter) {
    return useInfiniteQuery({
        queryKey: ['log-history', 'list', pageSize, filter.hasError, filter.apiKeyNames, filter.modelNames],
        initialPageParam: 1,
        queryFn: async ({ pageParam }: { pageParam: number }) => {
            const params = new URLSearchParams();
            params.set('page', String(pageParam));
            params.set('page_size', String(pageSize));
            if (filter.hasError) {
                params.set('has_error', 'true');
            }
            if (filter.apiKeyNames.length > 0) {
                params.set('api_key_names', filter.apiKeyNames.join(','));
            }
            if (filter.modelNames.length > 0) {
                params.set('model_names', filter.modelNames.join(','));
            }
            const result = await apiRequest<RelayLog[] | null>(`/api/v1/log/list?${params.toString()}`);
            return result ?? [];
        },
        getNextPageParam: (lastPage, allPages) => {
            if (!lastPage || lastPage.length < pageSize) return undefined;
            return allPages.length + 1;
        },
        staleTime: 30_000,
    });
}

/**
 * 按渠道查调用明细(infinite query)。
 * channelID 为 null 时禁用查询(供渠道详情视图未选定渠道时使用)。
 */
export function useChannelAttempts(channelID: number | null, pageSize = 50) {
    return useInfiniteQuery({
        queryKey: ['log-history', 'channel-attempts', channelID, pageSize],
        enabled: channelID != null,
        initialPageParam: 1,
        queryFn: async ({ pageParam }: { pageParam: number }) => {
            const params = new URLSearchParams();
            params.set('channel_id', String(channelID));
            params.set('page', String(pageParam));
            params.set('page_size', String(pageSize));
            return apiRequest<ChannelAttemptsResponse>(
                `/api/v1/log/channel-attempts?${params.toString()}`
            );
        },
        getNextPageParam: (lastPage, allPages) => {
            // 一页不满 pageSize 即到底; total 也兜底
            if (!lastPage.list || lastPage.list.length < pageSize) return undefined;
            const fetched = allPages.reduce((n, p) => n + (p.list?.length ?? 0), 0);
            if (fetched >= lastPage.total) return undefined;
            return allPages.length + 1;
        },
        staleTime: 30_000,
    });
}

/**
 * 清空持久化历史日志(内存缓冲与数据库一并清空)。
 * 与实时流的 useClearLogs(清进程内状态)是两个入口。
 */
export function useClearLogHistory() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/history/clear', { method: 'DELETE' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['log-history'] });
        },
    });
}
