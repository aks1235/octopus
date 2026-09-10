import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, ScrollText, AlertCircle, Clock, Coins, Zap, KeyRound, RotateCw } from 'lucide-react';
import { useLogHistory, useClearLogHistory, type RelayLog } from '@/api/log-history';
import { apiKeyListQueryOptions, groupListQueryOptions } from '@/api/queries';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { MultiSelect } from '@/components/common/MultiSelect';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { RequestDetailDialog } from './RequestDetailDialog';
import { ClientIconBadge, ReasoningEffortBadge } from './ClientIcon';

const PAGE_SIZE = 20;

function formatTime(timestamp: number): string {
    const d = new Date(timestamp * 1000);
    return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatDuration(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

/**
 * 历史日志卡片:与实时流卡片不同构(落库行,unix 秒 + attempts 定稿),
 * 点击整卡弹出请求详情(按需加载内容字段)。
 */
function HistoryCard({ log, onClick }: { log: RelayLog; onClick: () => void }) {
    const t = useTranslations('log.card');
    const failed = !!log.error && log.error.trim() !== '';

    return (
        <button
            type="button"
            onClick={onClick}
            className="w-full text-left rounded-2xl border border-border bg-card p-4 transition-colors hover:bg-muted/40 cursor-pointer"
        >
            <div className="flex flex-wrap items-center gap-2">
                <ClientIconBadge clientName={log.client_name} className="size-4" />
                <span className="text-sm font-medium">{log.request_model_name}</span>
                <ReasoningEffortBadge effort={log.reasoning_effort} />
                {log.actual_model_name && log.actual_model_name !== log.request_model_name && (
                    <span className="text-xs text-muted-foreground">→ {log.actual_model_name}</span>
                )}
                {failed && (
                    <Badge variant="destructive" className="gap-1">
                        <AlertCircle className="size-3" /> {t('errorInfo')}
                    </Badge>
                )}
                {(log.total_attempts ?? 0) > 1 && (
                    <Badge variant="outline" className="gap-1 text-amber-600 dark:text-amber-400 border-amber-300/50 dark:border-amber-600/40">
                        <RotateCw className="size-3" /> {t('attempts', { count: log.total_attempts ?? 0 })}
                    </Badge>
                )}
                <span className="ml-auto text-xs text-muted-foreground font-mono">{formatTime(log.time)}</span>
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span className="inline-flex items-center gap-1"><Zap className="size-3" />{log.channel_name || '-'}</span>
                {log.request_api_key_name && (
                    <span className="inline-flex items-center gap-1"><KeyRound className="size-3" />{log.request_api_key_name}</span>
                )}
                <span>{log.input_tokens.toLocaleString()} → {log.output_tokens.toLocaleString()} {t('tokens')}</span>
                {log.cached_tokens > 0 && <span>{t('cached', { count: log.cached_tokens.toLocaleString() })}</span>}
                <span className="inline-flex items-center gap-1"><Clock className="size-3" />{formatDuration(log.use_time)}</span>
                {log.ftut > 0 && <span>{t('ftut')}: {formatDuration(log.ftut)}</span>}
                <span className="inline-flex items-center gap-1"><Coins className="size-3" />${log.cost.toFixed(4)}</span>
            </div>
            {failed && (
                <div className="mt-2 rounded-lg bg-destructive/10 px-2 py-1 text-xs text-destructive/90 font-mono truncate">
                    {log.error}
                </div>
            )}
        </button>
    );
}

/**
 * 日志页历史区:持久化日志的筛选 + 无限滚动列表。
 * 与实时区(SSE 概览)互补: 这里是落库行的回溯查询视角。
 */
export function HistoryPanel() {
    const t = useTranslations('log.history');
    const [apiKeyNames, setAPIKeyNames] = useState<string[]>([]);
    const [modelNames, setModelNames] = useState<string[]>([]);
    const [hasError, setHasError] = useState(false);
    const [detailRequestId, setDetailRequestId] = useState<number | null>(null);
    const [detailOpen, setDetailOpen] = useState(false);

    // 筛选选项: API Key 名取自身份凭据列表; 模型筛选项取分组名 —— request_model_name 的语义即分组名。
    const { data: apiKeys } = useQuery(apiKeyListQueryOptions);
    const { data: groups } = useQuery(groupListQueryOptions);
    const apiKeyOptions = useMemo(() => (apiKeys ?? []).map((k) => k.name), [apiKeys]);
    const modelOptions = useMemo(() => (groups ?? []).map((g) => g.name), [groups]);

    const query = useLogHistory(PAGE_SIZE, { hasError, apiKeyNames, modelNames });
    const logs = useMemo(() => {
        const pages = query.data?.pages ?? [];
        const seen = new Set<number>();
        const merged: RelayLog[] = [];
        for (const page of pages) {
            for (const log of page) {
                if (seen.has(log.id)) continue;
                seen.add(log.id);
                merged.push(log);
            }
        }
        merged.sort((a, b) => b.time - a.time);
        return merged;
    }, [query.data]);

    const clearHistory = useClearLogHistory();

    const handleOpenDetail = (id: number) => {
        setDetailRequestId(id);
        setDetailOpen(true);
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {/* 筛选工具栏 */}
            <div className="flex flex-wrap items-center gap-2 shrink-0">
                <MultiSelect
                    options={apiKeyOptions}
                    selected={apiKeyNames}
                    onSelectedChange={setAPIKeyNames}
                    placeholder={t('filterAPIKey')}
                    searchPlaceholder={t('searchPlaceholder')}
                    emptyText={t('filterEmpty')}
                    selectAllText={t('selectAll')}
                    deselectAllText={t('deselectAll')}
                />
                <MultiSelect
                    options={modelOptions}
                    selected={modelNames}
                    onSelectedChange={setModelNames}
                    placeholder={t('filterModel')}
                    searchPlaceholder={t('searchPlaceholder')}
                    emptyText={t('filterEmpty')}
                    selectAllText={t('selectAll')}
                    deselectAllText={t('deselectAll')}
                />
                <Button
                    variant="outline"
                    size="sm"
                    className={cn('h-9 rounded-xl px-3', hasError && 'border-destructive/40 text-destructive')}
                    onClick={() => setHasError((v) => !v)}
                >
                    <AlertCircle className="size-3.5" />
                    {t('onlyErrors')}
                </Button>
                <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto h-9 rounded-xl px-3 text-muted-foreground"
                    onClick={() => query.refetch()}
                    disabled={query.isRefetching}
                >
                    <Loader2 className={cn('size-3.5', query.isRefetching && 'animate-spin')} />
                    {t('refresh')}
                </Button>
            </div>

            {/* 列表 */}
            <div className="min-h-0 flex-1">
                {query.isLoading && (
                    <div className="flex h-full items-center justify-center">
                        <Loader2 className="size-6 animate-spin text-muted-foreground" />
                    </div>
                )}
                {!query.isLoading && query.isError && (
                    <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
                        <AlertCircle className="size-8" />
                        <span className="text-sm">{t('loadFailed')}</span>
                    </div>
                )}
                {!query.isLoading && !query.isError && logs.length === 0 && (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <ScrollText className="size-8" />
                        <span className="text-sm">{t('empty')}</span>
                        <Button
                            variant="destructive"
                            size="sm"
                            className="rounded-xl"
                            onClick={() => clearHistory.mutate()}
                            disabled={clearHistory.isPending}
                        >
                            {clearHistory.isPending ? t('clearing') : t('clear')}
                        </Button>
                    </div>
                )}
                {logs.length > 0 && (
                    <VirtualizedGrid
                        items={logs}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={110}
                        overscan={8}
                        getItemKey={(log) => `log-history-${log.id}`}
                        renderItem={(log) => (
                            <HistoryCard log={log} onClick={() => handleOpenDetail(log.id)} />
                        )}
                        onReachEnd={() => {
                            if (query.hasNextPage && !query.isFetchingNextPage) {
                                query.fetchNextPage();
                            }
                        }}
                        reachEndEnabled
                        footer={
                            <div className="flex items-center justify-center py-3">
                                {query.isFetchingNextPage && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
                                {!query.hasNextPage && (
                                    <span className="text-xs text-muted-foreground">{t('noMore', { loaded: logs.length })}</span>
                                )}
                            </div>
                        }
                    />
                )}
            </div>

            <RequestDetailDialog open={detailOpen} onOpenChange={setDetailOpen} requestId={detailRequestId} />
        </div>
    );
}
