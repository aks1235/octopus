import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiRequest } from './client';

/**
 * Setting 数据
 */
export interface Setting {
    key: string;
    value: string;
}

export const SettingKey = {
    ProxyURL: 'proxy_url',
    StatsSaveInterval: 'stats_save_interval',
    ModelInfoUpdateInterval: 'model_info_update_interval',
    HealthCheckInterval: 'health_check_interval',
    HealthFailThreshold: 'health_fail_threshold',
    CORSAllowOrigins: 'cors_allow_origins',
    ModelFilter: 'model_filter',
    RelayLogKeepEnabled: 'relay_log_keep_enabled',
    RelayLogKeepPeriod: 'relay_log_keep_period',
    GroupProbeEnabled: 'group_probe_enabled',
    GrantProbeKeepDays: 'grant_probe_keep_days',
} as const;

/**
 * 获取 Setting 列表 Hook
 * 
 * @example
 * const { data: settings, isLoading, error } = useSettingList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * settings?.forEach(setting => console.log(setting.key, setting.value));
 */
export function useSettingList() {
    return useQuery({
        queryKey: ['settings', 'list'],
        queryFn: () => apiRequest<Setting[]>('/api/v1/setting/list'),
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

/**
 * 按全局模型过滤表达式清理已入库的模型 Hook
 *
 * 过滤表达式平时只在拉取与对账时生效, 清不到此前入库的存量; 这里触发一次全量清理。
 * 返回删除的模型数量。
 */
export function usePruneModels() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: () => apiRequest<number>('/api/v1/setting/prune-models', { method: 'POST' }),
        onSuccess: () => {
            // 模型被删会连带消失授权与分组成员, 渠道与分组两边都要重新拉。
            queryClient.invalidateQueries({ queryKey: ['channel'] });
            queryClient.invalidateQueries({ queryKey: ['group'] });
        },
    });
}

/**
 * 设置 Setting Hook
 * 
 * @example
 * const setSetting = useSetSetting();
 * 
 * setSetting.mutate({
 *   key: 'theme',
 *   value: 'dark',
 * });
 */
export function useSetSetting() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: Setting) =>
            apiRequest<Setting>('/api/v1/setting/set', { method: 'POST', body: data }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['settings', 'list'] }),
    });
}

/**
 * 数据库导入/导出
 */

/** 导入时遇到的同名对象处理策略: 跳过保留目标 / 覆盖为目标 / 另起名称并存。 */
export type DBImportConflict = 'skip' | 'overwrite' | 'rename';

interface DBImportResult {
    rows_affected: Record<string, number>;
    /** 因冲突策略被跳过、未写入的行数, 按表分组。 */
    skipped?: Record<string, number>;
    /** 本次导入实际采用的策略, 便于界面回显。 */
    conflict?: DBImportConflict;
}

function parseFilename(contentDisposition: string | null): string | null {
    if (!contentDisposition) return null;
    // e.g. attachment; filename="octopus-export-20250101120000.json"
    const match = contentDisposition.match(/filename="([^"]+)"/i);
    return match?.[1] ?? null;
}

function exportFallbackFilename() {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
    return `octopus-export-${ts}.json`;
}

async function downloadBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

/**
 * 导出数据库（下载 JSON 文件）
 */
export function useExportDB() {
    return useMutation({
        mutationFn: async () => {
            const res = await fetch('/api/v1/setting/export', {
                method: 'GET',
                credentials: 'include',
            });

            if (!res.ok) {
                const data = await res.json().catch(() => null) as { message?: string } | null;
                throw new ApiError(res.status, data?.message || `Request failed: ${res.status}`);
            }

            const blob = await res.blob();
            const filename = parseFilename(res.headers.get('content-disposition')) || exportFallbackFilename();
            await downloadBlob(blob, filename);
            return { filename };
        },
    });
}

/**
 * 导入数据库（上传 JSON 文件）
 *
 * 冲突策略随请求传给后端: 后端按「天然键」(渠道名 / 渠道+凭据名 / 分组名) 判断同名对象,
 * 而不是按主键 —— 两个实例的主键空间各自独立, 按主键合并会把配置写到不相关的对象上。
 */
export function useImportDB() {
    return useMutation({
        mutationFn: async (input: { file: File; conflict: DBImportConflict }) => {
            const form = new FormData();
            form.append('file', input.file);
            form.append('conflict', input.conflict);

            const res = await fetch('/api/v1/setting/import', {
                method: 'POST',
                body: form,
                credentials: 'include',
            });

            const contentType = res.headers.get('content-type') || '';
            const isJson = contentType.includes('application/json');
            const data = isJson
                ? await res.json() as { message?: string; data?: DBImportResult }
                : await res.text();

            if (!res.ok) {
                const message = typeof data === 'string' ? data : data.message;
                throw new ApiError(res.status, message || `Request failed: ${res.status}`);
            }

            // 支持后端标准 ApiResponse：{code,message,data:{...}}
            return typeof data === 'string' ? data as unknown as DBImportResult : data.data as DBImportResult;
        },
    });
}
