import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, ScrollText, AlertCircle, ArrowRight, RotateCw, Search } from 'lucide-react';
import { useLogHistory, useClearLogHistory, type RelayLog, type LogHistorySource } from '@/api/log-history';
import { apiKeyListQueryOptions, channelStatsQueryOptions, groupListQueryOptions } from '@/api/queries';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { MultiSelect } from '@/components/common/MultiSelect';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { RequestDetailDialog } from './RequestDetailDialog';
import { ClientIconBadge, ReasoningEffortBadge } from './ClientIcon';
import { LogMetrics, type LogMetricsInput } from './LogMetrics';
import { ProtocolLabel, StreamingBadge } from './LogBadges';
import { getModelIcon } from '@/lib/model-icons';

const PAGE_SIZE = 50;

// 来源分段: 探测(面板测试/分组测试/定时探测)默认不出现在排查视角里, 需要时显式切过去。
const SOURCE_TABS = [
    { value: 'relay', labelKey: 'sourceRelay' },
    { value: 'probe', labelKey: 'sourceProbe' },
    { value: 'all', labelKey: 'sourceAll' },
] as const;

// HighlightedSnippet 高亮关键词第一次出现的位置; 只高亮一处是有意的:
// 片段本来就只有关键词附近百来个字, 多处高亮反而看不清上下文。
function HighlightedSnippet({ text, keyword }: { text: string; keyword: string }) {
    if (keyword === '') return <>{text}</>;
    const index = text.toLowerCase().indexOf(keyword.toLowerCase());
    if (index < 0) return <>{text}</>;
    return (
        <>
            {text.slice(0, index)}
            <mark className="rounded bg-amber-400/30 px-0.5 text-foreground">{text.slice(index, index + keyword.length)}</mark>
            {text.slice(index + keyword.length)}
        </>
    );
}

function HistoryCard({ log, onClick, keyword }: { log: RelayLog; onClick: () => void; keyword: string }) {
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
                    {log.match_snippet && (
                        <div className="flex flex-col gap-1 rounded-xl border border-border/50 bg-muted/40 px-2.5 py-2">
                            {log.match_scope && (
                                <Badge
                                    variant="outline"
                                    className={cn(
                                        'self-start text-[10px] px-1.5 py-0',
                                        log.match_scope === 'history'
                                            ? 'text-muted-foreground'
                                            : 'border-emerald-500/40 text-emerald-700 dark:text-emerald-400',
                                    )}
                                >
                                    {log.match_scope === 'history' ? t('scopeHistory') : t('scopeNew')}
                                </Badge>
                            )}
                            <p className="text-xs leading-relaxed text-muted-foreground break-all">
                                <HighlightedSnippet text={log.match_snippet} keyword={keyword} />
                            </p>
                        </div>
                    )}
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
    const [source, setSource] = useState<LogHistorySource>('relay');
    const [channelNames, setChannelNames] = useState<string[]>([]);
    // keywordInput 是输入框的即时值, keyword 是防抖后真正参与查询的值:
    // 每敲一个字就查一次会把库打满, 也会让列表在输入过程中反复跳动。
    const [keywordInput, setKeywordInput] = useState('');
    const [keyword, setKeyword] = useState('');
    // 只看本次新增: agent loop 会把历史反复带给上游, 同一句话因此重复命中很多次,
    // 打开后只留下"这一轮真正说出来"的那几条。
    const [onlyNewMatch, setOnlyNewMatch] = useState(false);
    useEffect(() => {
        const timer = setTimeout(() => setKeyword(keywordInput.trim()), 300);
        return () => clearTimeout(timer);
    }, [keywordInput]);
    const [detailRequestId, setDetailRequestId] = useState<number | null>(null);
    const [detailOpen, setDetailOpen] = useState(false);

    // 筛选选项: API Key 名取自身份凭据列表; 模型筛选项取分组名 —— request_model_name 的语义即分组名。
    const { data: apiKeys } = useQuery(apiKeyListQueryOptions);
    const { data: groups } = useQuery(groupListQueryOptions);
    const { data: channelStats } = useQuery(channelStatsQueryOptions);
    const apiKeyOptions = useMemo(() => (apiKeys ?? []).map((k) => k.name), [apiKeys]);
    const modelOptions = useMemo(() => (groups ?? []).map((g) => g.name), [groups]);
    const channelOptions = useMemo(() => (channelStats ?? []).map((c) => c.channel_name), [channelStats]);

    const query = useLogHistory(PAGE_SIZE, { hasError, apiKeyNames, modelNames, source, channelNames, keyword });
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

    // 只在有关键词时过滤: 没搜内容时 match_scope 根本不存在, 开关也不该起作用。
    const visibleLogs = useMemo(
        () => (onlyNewMatch && keyword !== '' ? logs.filter((log) => log.match_scope !== 'history') : logs),
        [logs, onlyNewMatch, keyword],
    );
    const filteredHistoryCount = keyword !== '' ? logs.length - visibleLogs.length : 0;

    const clearHistory = useClearLogHistory();

    const handleOpenDetail = (id: number) => {
        setDetailRequestId(id);
        setDetailOpen(true);
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {/* 筛选工具栏 */}
            <div className="flex flex-wrap items-center gap-2 shrink-0">
                {/* 来源切换放在最左: 它决定这一屏看到的是真实流量还是探测记录。 */}
                <div className="flex items-center gap-1 rounded-xl border border-border p-0.5">
                    {SOURCE_TABS.map((tab) => (
                        <Button
                            key={tab.value}
                            variant="ghost"
                            size="sm"
                            aria-pressed={source === tab.value}
                            className={cn(
                                'h-8 rounded-lg px-2.5 text-xs font-medium',
                                source === tab.value ? 'bg-muted text-foreground' : 'text-muted-foreground',
                            )}
                            onClick={() => setSource(tab.value)}
                        >
                            {t(tab.labelKey)}
                        </Button>
                    ))}
                </div>
                <div className="relative">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        value={keywordInput}
                        onChange={(event) => setKeywordInput(event.target.value)}
                        placeholder={t('searchContent')}
                        className="h-9 w-56 rounded-xl pl-8 text-xs"
                    />
                </div>
                <MultiSelect
                    options={channelOptions}
                    selected={channelNames}
                    onSelectedChange={setChannelNames}
                    placeholder={t('filterChannel')}
                    searchPlaceholder={t('searchPlaceholder')}
                    emptyText={t('filterEmpty')}
                    selectAllText={t('selectAll')}
                    deselectAllText={t('deselectAll')}
                />
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
                {/* 开关只在搜内容时有意义, 没关键词时置灰, 免得点了没反应像坏了。 */}
                <label
                    className={cn(
                        'flex select-none items-center gap-2 text-xs',
                        keyword === '' ? 'text-muted-foreground/50' : 'cursor-pointer text-muted-foreground',
                    )}
                >
                    <span>{t('onlyNewMatch')}</span>
                    <Switch
                        checked={onlyNewMatch}
                        onCheckedChange={setOnlyNewMatch}
                        disabled={keyword === ''}
                        aria-label={t('onlyNewMatch')}
                    />
                </label>
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
                {!query.isLoading && !query.isError && filteredHistoryCount > 0 && (
                    <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                        <span>{t('filteredHistory', { count: filteredHistoryCount })}</span>
                        {query.hasNextPage && (
                            <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 rounded-lg px-2 text-xs"
                                onClick={() => query.fetchNextPage()}
                                disabled={query.isFetchingNextPage}
                            >
                                {query.isFetchingNextPage && <Loader2 className="mr-1 size-3 animate-spin" />}
                                {t('loadMore')}
                            </Button>
                        )}
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
                {visibleLogs.length === 0 && logs.length > 0 && (
                    <div className="flex flex-col items-center justify-center gap-3 py-10 text-sm text-muted-foreground">
                        <ScrollText className="size-8" />
                        <span>{t('allHistoryFiltered')}</span>
                        {query.hasNextPage && (
                            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => query.fetchNextPage()}>
                                {t('loadMore')}
                            </Button>
                        )}
                    </div>
                )}
                {visibleLogs.length > 0 && (
                    <VirtualizedGrid
                        items={visibleLogs}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={110}
                        overscan={8}
                        getItemKey={(log) => `log-history-${log.id}`}
                        renderItem={(log) => (
                            <HistoryCard log={log} onClick={() => handleOpenDetail(log.id)} keyword={keyword} />
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
                                    <span className="text-xs text-muted-foreground">{t('noMore', { loaded: visibleLogs.length })}</span>
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
