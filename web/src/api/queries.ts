import { queryOptions } from '@tanstack/react-query';
import dayjs from 'dayjs';
import type { APIKey, APIKeyStatsResponse } from './apikey';
import type { ChannelStats } from './channel';
import type { Group } from './group';
import type { LLMInfo } from './model';
import type { StatsDailyResponse, StatsHourly, StatsRankResponse, StatsTotal } from './stats';
import { apiRequest } from './client';

// apiKeyDashboardStatsQueryOptions 供页面查询和启动预取共享 API Key 统计定义。
export const apiKeyDashboardStatsQueryOptions = queryOptions({
    queryKey: ['apikey', 'dashboard', 'stats'],
    queryFn: () => apiRequest<APIKeyStatsResponse>('/api/v1/apikey/stats'),
});

// apiKeyListQueryOptions 供页面查询和启动预取共享 API Key 列表定义。
export const apiKeyListQueryOptions = queryOptions({
    queryKey: ['apikeys', 'list'],
    queryFn: () => apiRequest<APIKey[]>('/api/v1/apikey/list'),
});

// channelStatsQueryOptions 供页面查询和启动预取共享渠道统计定义。
// 渠道页与首页榜单共用这一条: 统计自带渠道名称, 启停与模型个数, 两处都无需再拉渠道配置。
export const channelStatsQueryOptions = queryOptions({
    queryKey: ['channels', 'stats'],
    queryFn: () => apiRequest<ChannelStats[]>('/api/v1/channel/stats'),
});

// groupListQueryOptions 供页面查询和启动预取共享分组列表定义。
export const groupListQueryOptions = queryOptions({
    queryKey: ['groups', 'list'],
    queryFn: () => apiRequest<Group[]>('/api/v1/group/list'),
});

// modelListQueryOptions 供页面查询和启动预取共享模型列表定义。
export const modelListQueryOptions = queryOptions({
    queryKey: ['models', 'list'],
    queryFn: () => apiRequest<LLMInfo[]>('/api/v1/model/list'),
});

// statsDailyQueryOptions 供页面查询和启动预取共享每日统计定义。
export const statsDailyQueryOptions = queryOptions({
    queryKey: ['stats', 'daily'],
    queryFn: () => apiRequest<StatsDailyResponse>('/api/v1/stats/daily'),
});

// todayDateStr 返回本地时区今天的 YYYYMMDD 字符串, 供默认查询与「回到今天」共用同一口径。
export function todayDateStr(): string {
    return dayjs().format('YYYYMMDD');
}

// statsHourlyQueryOptions 供页面查询和启动预取共享每小时统计定义, 按选中日期区分 queryKey;
// 缺省为今天, 与后端 date 缺省语义一致。
export function statsHourlyQueryOptions(date: string = todayDateStr()) {
    return queryOptions({
        queryKey: ['stats', 'hourly', date],
        queryFn: () => apiRequest<StatsHourly[]>(`/api/v1/stats/hourly?date=${date}`),
    });
}

// statsRankDailyQueryOptions 供首页排名「按天」视角查询, 后端从 relay_logs 按选中日聚合。
export function statsRankDailyQueryOptions(date: string) {
    return queryOptions({
        queryKey: ['stats', 'rank-daily', date],
        queryFn: () => apiRequest<StatsRankResponse>(`/api/v1/stats/rank?date=${date}`),
    });
}

// statsTotalQueryOptions 供页面查询和启动预取共享总计统计定义。
export const statsTotalQueryOptions = queryOptions({
    queryKey: ['stats', 'total'],
    queryFn: () => apiRequest<StatsTotal>('/api/v1/stats/total'),
});
