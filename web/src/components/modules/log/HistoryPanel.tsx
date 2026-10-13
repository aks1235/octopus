import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, ScrollText, AlertCircle, ArrowRight, RotateCw } from 'lucide-react';
import { useLogHistory, useClearLogHistory, type RelayLog } from '@/api/log-history';
import { apiKeyListQueryOptions, groupListQueryOptions } from '@/api/queries';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { MultiSelect } from '@/components/common/MultiSelect';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { RequestDetailDialog } from './RequestDetailDialog';
import { ClientIconBadge, ReasoningEffortBadge } from './ClientIcon';
import { LogMetrics, type LogMetricsInput } from './LogMetrics';
import { ProtocolLabel, StreamingBadge } from './LogBadges';
import { getModelIcon } from '@/lib/model-icons';

const PAGE_SIZE = 50;

function HistoryCard({ log, onClick }: { log: RelayLog; onClick: () => void }) {
    const t = useTranslations('log.card');
    const failed = log.status === 'failed' || log.status === 'canceled' || (!!log.error && log.error.trim() !== '');
    const startedAt = new Date(log.time * 1000).toISOString();
    const { Icon: ModelIcon, className: modelIconClassName, color: brandColor } = getModelIcon(log.actual_model_name || log.request_model_name);
    const metricLog: LogMetricsInput = {
        started_at: startedAt,
        status: log.status || (failed ? 'failed' : 'success'),
        duration: log.use_time * 1_000_000,
        api_key_name: log.request_api_key_name ?? '',
        usage: {
            prompt_tokens: log.input_tokens,
            completion_tokens: log.output_tokens,
            prompt_tokens_details: { cached_tokens: log.cached_tokens, write_cached_tokens: log.cache_creation_tokens },
        },
        cost: log.cost,
        phase: undefined,
        phase_speed: undefined,
        first_token_ms: log.ftut,
    };

    return (
        <button
            type="button"
            onClick={onClick}
            className={cn(
                'w-full text-left rounded-3xl border bg-card transition-colors hover:bg-muted/40 cursor-pointer',
                failed ? 'border-destructive/40' : 'border-border',
            )}
        >
            <div className={cn('p-4 grid grid-cols-[auto_1fr] gap-4', failed ? 'items-start' : 'items-center')}>
                <span className="relative inline-flex shrink-0">
                    <ModelIcon aria-hidden="true" className={modelIconClassName} width={40} height={40} />
                    <ClientIconBadge clientName={log.client_name} className="absolute -bottom-1 -right-1.5 size-4 rounded-full bg-card" />
                </span>
                <div className="min-w-0 flex flex-col gap-3">
                    <div className="flex items-center gap-2 min-w-0 text-sm">
                        <ProtocolLabel protocol={log.protocol} />
                        <span className="font-semibold text-card-foreground truncate">{log.request_model_name}</span>
                        <StreamingBadge streaming={log.streaming} />
                        <ReasoningEffortBadge effort={log.reasoning_effort} />
                        {log.actual_model_name && (
                            <>
                                <ArrowRight className="size-3.5 shrink-0 text-muted-foreground/50" />
                                <ProtocolLabel protocol={log.target_protocol} />
                                <Badge
                                    variant="secondary"
                                    className="shrink-0 text-xs px-1.5 py-0"
                                    style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                >
                                    {log.channel_name || '-'}
                                </Badge>
                                <span className="text-muted-foreground truncate">{log.actual_model_name}</span>
                            </>
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
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs tabular-nums text-muted-foreground md:grid-cols-5">
                        <LogMetrics log={metricLog} now={0} brandColor={brandColor} variant="card" showDate />
                    </div>
                    {failed && (
                        <div className="p-2.5 rounded-xl bg-destructive/10 border border-destructive/20 overflow-hidden">
                            <p className="text-xs text-destructive line-clamp-2 whitespace-pre-line">
                            {log.error}
                            </p>
                        </div>
                    )}
                </div>
            </div>
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
