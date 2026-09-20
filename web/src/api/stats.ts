import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';
import { statsDailyQueryOptions, statsHourlyQueryOptions, statsRankDailyQueryOptions, statsTotalQueryOptions, todayDateStr } from './queries';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';

/**
 * 统计数据
 */
export interface StatsMetrics {
    input_token: number;
    output_token: number;
    input_cost: number;
    output_cost: number;
    wait_time: number;
    request_success: number;
    request_failed: number;
}

export interface StatsMetricsFormatted {
    input_token: ReturnType<typeof formatCount>;
    output_token: ReturnType<typeof formatCount>;
    input_cost: ReturnType<typeof formatMoney>;
    output_cost: ReturnType<typeof formatMoney>;
    wait_time: ReturnType<typeof formatTime>;
    request_success: ReturnType<typeof formatCount>;
    request_failed: ReturnType<typeof formatCount>;

    request_count: ReturnType<typeof formatCount>;
    total_token: ReturnType<typeof formatCount>;
    total_cost: ReturnType<typeof formatMoney>;
}

// formatStatsMetrics 把一组累计统计格式化为界面展示字段，并补齐合计项。
// 渠道、渠道模型和渠道凭据各自维护独立计数，均按同一口径展示。
export function formatStatsMetrics(metrics: StatsMetrics): StatsMetricsFormatted {
    return {
        input_token: formatCount(metrics.input_token),
        output_token: formatCount(metrics.output_token),
        total_token: formatCount(metrics.input_token + metrics.output_token),
        input_cost: formatMoney(metrics.input_cost),
        output_cost: formatMoney(metrics.output_cost),
        total_cost: formatMoney(metrics.input_cost + metrics.output_cost),
        wait_time: formatTime(metrics.wait_time),
        request_success: formatCount(metrics.request_success),
        request_failed: formatCount(metrics.request_failed),
        request_count: formatCount(metrics.request_success + metrics.request_failed),
    };
}

export interface StatsDaily extends StatsMetrics {
    date: string;
}
export interface StatsDailyResponse {
    max_request_count: number;
    items: StatsDaily[];
}
export interface StatsDailyFormatted extends StatsMetricsFormatted {
    date: string;
}
interface StatsDailyFormattedResponse {
    max_request_count: number;
    items: StatsDailyFormatted[];
}

export interface StatsTotal extends StatsMetrics {
    id: number;
}
type StatsTotalFormatted = StatsMetricsFormatted;

export interface StatsHourly extends StatsMetrics {
    hour: number;
    date: string;
}
interface StatsHourlyFormatted extends StatsMetricsFormatted {
    hour: number;
    date: string;
}

// StatsRankEntry 是按天排名的单条聚合(渠道榜带 channel_id, 模型榜 channel_id 恒为 0)。
export interface StatsRankEntry extends StatsMetrics {
    channel_id: number;
    name: string;
}
// available 语义为「是否有数据来源」; 统计永久化后数据来源恒存在, 恒为 true(字段保留仅为兼容响应结构)。
export interface StatsRankResponse {
    available: boolean;
    channels: StatsRankEntry[];
    models: StatsRankEntry[];
}
export interface StatsRankEntryFormatted extends StatsMetricsFormatted {
    channel_id: number;
    name: string;
}
export interface StatsRankResponseFormatted {
    available: boolean;
    channels: StatsRankEntryFormatted[];
    models: StatsRankEntryFormatted[];
}
/**
 * API Key 统计数据
 */
export interface StatsAPIKey extends StatsMetrics {
    api_key_id: number;
}

export interface StatsAPIKeyFormatted extends StatsMetricsFormatted {
    api_key_id: number;
}

// statsDailyFormattedQueryOptions 统一首页每日统计查询、格式化和刷新策略。
const statsDailyFormattedQueryOptions = queryOptions({
    ...statsDailyQueryOptions,
    select: (data): StatsDailyFormattedResponse => ({
        max_request_count: data.max_request_count,
        items: data.items.map((item): StatsDailyFormatted => ({
            ...formatStatsMetrics(item),
            date: item.date,
        })),
    }),
    refetchInterval: 3600000, // 1 小时
    refetchOnMount: 'always',
});

/**
 * 获取每日统计数据 Hook
 */
export function useStatsDaily() {
    const query = useQuery(statsDailyFormattedQueryOptions);
    return {
        ...query,
        data: query.data?.items,
        maxRequestCount: query.data?.max_request_count ?? 0,
    };
}

// statsHourlyFormattedQueryOptions 统一首页每小时统计查询、格式化和刷新策略。
// 刷新语义随选中日: 今天保持 10 秒实时积累, 历史日是静态快照, 关掉定时刷新。
const statsHourlyFormattedQueryOptions = (date: string) => queryOptions({
    ...statsHourlyQueryOptions(date),
    select: (data) => data.map((item): StatsHourlyFormatted => ({
        ...formatStatsMetrics(item),
        hour: item.hour,
        date: item.date,
    })),
    refetchInterval: date === todayDateStr() ? 10000 : false,// 10 秒
    refetchOnMount: 'always',
});

/**
 * 获取每小时统计数据 Hook; date 为 YYYYMMDD, 历史日返回静态快照。
 */
export function useStatsHourly(date: string) {
    return useQuery(statsHourlyFormattedQueryOptions(date));
}

// statsRankDailyFormattedQueryOptions 统一排名按天视角的查询、格式化和刷新策略, 与小时数据同一刷新语义。
const statsRankDailyFormattedQueryOptions = (date: string) => queryOptions({
    ...statsRankDailyQueryOptions(date),
    select: (data): StatsRankResponseFormatted => ({
        available: data.available,
        channels: data.channels.map((entry): StatsRankEntryFormatted => ({
            ...formatStatsMetrics(entry),
            channel_id: entry.channel_id,
            name: entry.name,
        })),
        models: data.models.map((entry): StatsRankEntryFormatted => ({
            ...formatStatsMetrics(entry),
            channel_id: entry.channel_id,
            name: entry.name,
        })),
    }),
    refetchInterval: date === todayDateStr() ? 30000 : false,// 30 秒
    refetchOnMount: 'always',
});

/**
 * 获取按天排名数据 Hook; date 为 YYYYMMDD, 数据永久留存(汇总表, 无汇总行时回退实时聚合)。
 */
export function useStatsRankDaily(date: string, enabled = true) {
    return useQuery({ ...statsRankDailyFormattedQueryOptions(date), enabled });
}

// statsTotalFormattedQueryOptions 统一首页总统计查询、格式化和刷新策略。
const statsTotalFormattedQueryOptions = queryOptions({
    ...statsTotalQueryOptions,
    select: (data): StatsTotalFormatted => formatStatsMetrics(data),
    refetchInterval: 10000,// 10 秒
    refetchOnMount: 'always',
});

/**
 * 获取总统计数据 Hook
 */
export function useStatsTotal() {
    return useQuery(statsTotalFormattedQueryOptions);
}



/**
 * 获取 API Key 统计数据列表 Hook
 */
export function useStatsAPIKey() {
    return useQuery({
        queryKey: ['stats', 'apikey'],
        queryFn: () => apiRequest<StatsAPIKey[]>('/api/v1/stats/apikey'),
        select: (data) => data.map((item): StatsAPIKeyFormatted => ({
            ...formatStatsMetrics(item),
            api_key_id: item.api_key_id,
        })),
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}
