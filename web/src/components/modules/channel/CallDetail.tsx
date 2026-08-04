'use client';

import { useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Loader2, ArrowLeft, Pin, ExternalLink, History } from 'lucide-react';
import {
    useChannelAttempts,
    type ChannelAttemptDetail,
    type AttemptStatus,
} from '@/api/endpoints/log';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useChannelViewStore } from './detail-store';
import { RequestDetailDialog } from '@/components/modules/log/RequestDetailDialog';

interface ChannelCallDetailProps {
    channelID: number;
    channelName: string;
}

/** 状态 -> 展示样式(variant + className)与文案 key */
const STATUS_STYLE: Record<AttemptStatus, { variant: 'default' | 'secondary' | 'destructive' | 'outline'; className: string; key: string }> = {
    success: { variant: 'secondary', className: 'bg-emerald-500/15 text-emerald-600 border-emerald-500/30', key: 'statusSuccess' },
    failed: { variant: 'destructive', className: '', key: 'statusFailed' },
    circuit_break: { variant: 'outline', className: 'text-amber-600 border-amber-500/40 bg-amber-500/10', key: 'statusCircuitBreak' },
    skipped: { variant: 'outline', className: 'text-muted-foreground', key: 'statusSkipped' },
};

function formatTime(timestamp: number): string {
    const d = new Date(timestamp * 1000);
    return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatDuration(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

export function ChannelCallDetail({ channelID, channelName }: ChannelCallDetailProps) {
    const t = useTranslations('log.channelAttempts');
    const tChannel = useTranslations('channel.callDetail');
    const backToList = useChannelViewStore((s) => s.backToList);

    const [detailRequestId, setDetailRequestId] = useState<number | null>(null);
    const [detailOpen, setDetailOpen] = useState(false);

    const query = useChannelAttempts(channelID, 50);
    const allItems = useMemo<ChannelAttemptDetail[]>(
        () => (query.data?.pages ?? []).flatMap((p) => p.list),
        [query.data]
    );

    // 已加载页的状态分布(全量统计需翻到底;此处基于已加载,配合 total 标注)
    const statusCounts = useMemo(() => {
        const c: Record<AttemptStatus, number> = { success: 0, failed: 0, circuit_break: 0, skipped: 0 };
        for (const it of allItems) c[it.status]++;
        return c;
    }, [allItems]);

    const total = query.data?.pages?.[0]?.total ?? 0;
    const truncated = query.data?.pages?.[0]?.truncated ?? false;

    const handleClickRequest = (id: number) => {
        setDetailRequestId(id);
        setDetailOpen(true);
    };

    return (
        <div className="flex flex-col gap-3 h-full min-h-0">
            {/* 顶部:返回 + 标题 */}
            <div className="flex items-center gap-2 shrink-0">
                <Button variant="ghost" size="sm" className="gap-1" onClick={backToList}>
                    <ArrowLeft className="size-4" /> {tChannel('back')}
                </Button>
                <History className="size-4 text-muted-foreground" />
                <span className="text-sm font-medium truncate">{channelName}</span>
                <span className="text-xs text-muted-foreground">{tChannel('title')}</span>
            </div>

            {/* 概览统计 */}
            <div className="flex flex-wrap items-center gap-2 text-xs shrink-0">
                <Badge variant="secondary">{t('total', { count: total })}</Badge>
                {(['success', 'failed', 'circuit_break', 'skipped'] as AttemptStatus[]).map((s) => (
                    <Badge key={s} variant={STATUS_STYLE[s].variant} className={cn('gap-1', STATUS_STYLE[s].className)}>
                        {t(STATUS_STYLE[s].key)} {statusCounts[s]}
                    </Badge>
                ))}
                {truncated && (
                    <span className="text-amber-600">{t('truncatedTip')}</span>
                )}
            </div>

            {/* 明细列表 */}
            <div className="flex-1 min-h-0 overflow-auto rounded-2xl border border-border bg-card">
                {query.isLoading && (
                    <div className="flex items-center justify-center h-full py-10">
                        <Loader2 className="size-5 text-muted-foreground animate-spin" />
                    </div>
                )}
                {query.isError && (
                    <div className="flex items-center justify-center h-full py-10 text-sm text-muted-foreground">
                        {t('loadFailed')}
                    </div>
                )}
                {!query.isLoading && !query.isError && allItems.length === 0 && (
                    <div className="flex items-center justify-center h-full py-10 text-sm text-muted-foreground">
                        {tChannel('empty')}
                    </div>
                )}

                {allItems.length > 0 && (
                    <ul className="divide-y divide-border">
                        {allItems.map((it) => {
                            const style = STATUS_STYLE[it.status];
                            return (
                                <li key={`${it.request_id}-${it.attempt_num}`} className="px-3 py-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs hover:bg-muted/40 transition-colors">
                                    <button
                                        type="button"
                                        onClick={() => handleClickRequest(it.request_id)}
                                        className="font-mono text-primary hover:underline inline-flex items-center gap-1"
                                        title={tChannel('openRequestHint')}
                                    >
                                        #{it.request_id} <ExternalLink className="size-3" />
                                    </button>
                                    <span className="text-muted-foreground">{formatTime(it.request_time)}</span>
                                    <Badge variant={style.variant} className={cn('gap-1', style.className)}>
                                        {t(style.key)}
                                    </Badge>
                                    <span className="text-muted-foreground">
                                        #{it.attempt_num} · {it.model_name || '-'}
                                    </span>
                                    <span className="font-mono">{formatDuration(it.duration)}</span>
                                    {it.sticky && (
                                        <span className="inline-flex items-center gap-0.5 text-amber-600" title={tChannel('stickyHint')}><Pin className="size-3" /></span>
                                    )}
                                    <span className="truncate text-muted-foreground min-w-0 flex-1">
                                        {it.request_model || ''}
                                        {it.request_error && <span className="text-destructive/80"> · {it.request_error}</span>}
                                    </span>
                                    {it.msg && (
                                        <span className="w-full text-muted-foreground/80 font-mono break-words pl-0">
                                            {it.msg}
                                        </span>
                                    )}
                                </li>
                            );
                        })}
                    </ul>
                )}
            </div>

            {/* 加载更多 */}
            <div className="shrink-0 flex items-center justify-center py-1">
                {query.hasNextPage && (
                    <Button variant="ghost" size="sm" disabled={query.isFetchingNextPage} onClick={() => query.fetchNextPage()}>
                        {query.isFetchingNextPage ? <Loader2 className="size-4 animate-spin" /> : null}
                        {t('loadMore')}
                    </Button>
                )}
                {!query.hasNextPage && allItems.length > 0 && (
                    <span className="text-xs text-muted-foreground">{t('noMore', { loaded: allItems.length })}</span>
                )}
            </div>

            <RequestDetailDialog open={detailOpen} onOpenChange={setDetailOpen} requestId={detailRequestId} />
        </div>
    );
}
