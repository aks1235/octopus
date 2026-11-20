import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * 后端 /api/v1/update 返回的最新发布信息
 */
interface LatestInfo {
    tag_name: string;
    published_at: string;
    body: string;
    message: string;
}

/**
 * 获取最新发布信息 Hook
 * 
 * @example
 * const { data: latestInfo, isLoading, error } = useLatestInfo();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * console.log('Latest tag:', latestInfo?.tag_name);
 */
export function useLatestInfo() {
    return useQuery({
        queryKey: ['update', 'latest'],
        queryFn: () => apiRequest<LatestInfo>('/api/v1/update'),
        refetchInterval: 3600000, // 1 小时
        // 与后端缓存同一新鲜期: 反复进出设置页不再回源。GitHub 未认证接口按出口 IP 限流,
        // 每次挂载都请求会把 60 次/小时的额度耗光, 表现为「最新版本 未知」。
        // 更新完成时 useUpdateCore 会 invalidate 该 query, 不影响升级后立即看到新版本。
        staleTime: 10 * 60 * 1000,
    });
}

/**
 * 获取后端当前版本 Hook
 *
 * 后端: GET /api/v1/update/now-version -> string
 */
export function useNowVersion() {
    return useQuery({
        queryKey: ['update', 'now-version'],
        queryFn: () => apiRequest<string>('/api/v1/update/now-version'),
        refetchInterval: 3600000, // 1 小时
        refetchOnMount: 'always',
    });
}

/**
 * 执行更新 Hook
 * 
 * @example
 * const updateCore = useUpdateCore();
 * 
 * updateCore.mutate(undefined, {
 *   onSuccess: () => {
 *     console.log('Update started successfully');
 *   },
 * });
 */
export function useUpdateCore() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: () => apiRequest<string>('/api/v1/update', { method: 'POST' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['update', 'latest'] });
            queryClient.invalidateQueries({ queryKey: ['update', 'now-version'] });
        },
    });
}
