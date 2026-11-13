import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Ban, CircleCheck, Clock, Loader2, SkipForward } from 'lucide-react';
import {
    DialogRoot,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
} from '@/components/ui/dialog';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useGrantCalls, useGrantProbeTimeline } from '@/api/group';
import { RequestDetailDialog } from '@/components/modules/log/RequestDetailDialog';
import { cn } from '@/lib/utils';

// ProbeHistoryDialog 是单个分组成员的"调用情况"弹窗。
//
// 两个视角:
//   - 历史调用(默认): 这个成员被真实调用/尝试过的每一次, 数据来自 relay_log_attempts,
//     点任一行可回放当时的请求与响应。打开就要能看见刚发生的调用, 所以查询不缓存(staleTime 0)。
//   - 探测日志: 定时任务与手动测试的结论时间线, 数据来自 grant_probe_records。
// 之前只给探测日志, 会把"这个成员最近有没有被调用"和"探测结论"混为一谈。
const probeHistoryDays = 90;
const callsPageSize = 50;

export function ProbeHistoryDialog({
    grantId,
    onOpenChange,
}: {
    grantId: number | null;
    onOpenChange: (open: boolean) => void;
}) {
    const t = useTranslations('group.probe');
    const tCard = useTranslations('log.card');
    // 默认停在"历史调用"; 调用方以 key={grantId} 挂载组件, 换成员即重建, tab 自然回到默认,
    // 不在这里用 effect 重置(那会多一次渲染, 也被 lint 拦下)。
    const [tab, setTab] = useState<'calls' | 'probe'>('calls');
    const [detailRequestId, setDetailRequestId] = useState<number | null>(null);
    const [detailOpen, setDetailOpen] = useState(false);

    const probeQuery = useGrantProbeTimeline(grantId, probeHistoryDays);
    const callsQuery = useGrantCalls(grantId, callsPageSize);
    const data = probeQuery.data;
    const calls = callsQuery.data?.list ?? [];

    const handleOpenCall = (requestId: number) => {
        setDetailRequestId(requestId);
        setDetailOpen(true);
    };

    return (
        <>
            <DialogRoot open={grantId != null} onOpenChange={onOpenChange}>
                <DialogContent className="max-w-2xl rounded-2xl">
                    <DialogHeader>
                        <DialogTitle>{data ? `${data.model_name} · ${data.key_name}` : t('title')}</DialogTitle>
                        <DialogDescription>
                            {data ? data.channel_name : t('callsHint', { days: probeHistoryDays })}
                        </DialogDescription>
                    </DialogHeader>

                    <Tabs value={tab} onValueChange={(value) => setTab(value as 'calls' | 'probe')} className="flex flex-col gap-3">
                        <TabsList variant="text" className="self-start p-0">
                            <TabsTrigger value="calls">{t('callsTab')}</TabsTrigger>
                            <TabsTrigger value="probe">{t('probeTab')}</TabsTrigger>
                        </TabsList>

                        {tab === 'calls' && (
                            <div className="flex flex-col gap-2">
                                {callsQuery.isLoading && (
                                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                        <Loader2 className="size-4 animate-spin" />
                                        {t('callsLoading')}
                                    </div>
                                )}
                                {!callsQuery.isLoading && calls.length === 0 && (
                                    <p className="py-4 text-center text-sm text-muted-foreground">{t('callsEmpty')}</p>
                                )}
                                {calls.length > 0 && (
                                    <>
                                        <span className="text-xs text-muted-foreground">
                                            {t('callsTotal', { count: callsQuery.data?.total ?? calls.length })}
                                        </span>
                                        <div className="max-h-80 divide-y overflow-y-auto rounded-xl border border-border/60">
                                            {calls.map((call) => {
                                                const failed = call.status === 'failed';
                                                const skipped = call.status === 'skipped' || call.status === 'circuit_break';
                                                return (
                                                    <button
                                                        key={`${call.request_id}-${call.attempt_num}`}
                                                        type="button"
                                                        onClick={() => handleOpenCall(call.request_id)}
                                                        className="flex w-full items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-muted/60"
                                                    >
                                                        {failed ? (
                                                            <Ban className="mt-0.5 size-4 shrink-0 text-destructive" />
                                                        ) : skipped ? (
                                                            <SkipForward className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                                                        ) : (
                                                            <CircleCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
                                                        )}
                                                        <div className="min-w-0 flex-1">
                                                            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                                                                <span>{new Date(call.request_time * 1000).toLocaleString()}</span>
                                                                <span>{call.model_name || call.request_model}</span>
                                                                {call.duration > 0 && (
                                                                    <span className="inline-flex items-center gap-1">
                                                                        <Clock className="size-3" />
                                                                        {call.duration} ms
                                                                    </span>
                                                                )}
                                                            </div>
                                                            {call.msg && (
                                                                <p className={cn('mt-0.5 break-all text-xs', failed ? 'text-destructive/90' : 'text-muted-foreground')}>
                                                                    {call.msg}
                                                                </p>
                                                            )}
                                                        </div>
                                                    </button>
                                                );
                                            })}
                                        </div>
                                        <span className="text-xs text-muted-foreground">{tCard('viewDetailHint')}</span>
                                    </>
                                )}
                            </div>
                        )}

                        {tab === 'probe' && (
                            <div className="space-y-3">
                                {probeQuery.isLoading && (
                                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                        <Loader2 className="size-4 animate-spin" />
                                        {t('loading')}
                                    </div>
                                )}
                                {data && (
                                    <>
                                        <div className="flex flex-wrap items-center gap-3 text-sm">
                                            <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-500">
                                                <CircleCheck className="size-4" />
                                                {t('okCount', { count: data.ok })}
                                            </span>
                                            <span className="inline-flex items-center gap-1 text-destructive">
                                                <Ban className="size-4" />
                                                {t('failedCount', { count: data.failed })}
                                            </span>
                                            {data.last_at > 0 && (
                                                <span className="text-xs text-muted-foreground">
                                                    {t('lastAt', { time: new Date(data.last_at * 1000).toLocaleString() })}
                                                </span>
                                            )}
                                        </div>

                                        {data.records.length === 0 ? (
                                            <p className="text-sm text-muted-foreground">{t('empty')}</p>
                                        ) : (
                                            <div className="max-h-80 divide-y overflow-y-auto rounded-xl border border-border/60">
                                                {data.records.map((record) => (
                                                    <div key={record.id} className="flex items-start gap-2 px-3 py-2">
                                                        {record.success ? (
                                                            <CircleCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
                                                        ) : (
                                                            <Ban className="mt-0.5 size-4 shrink-0 text-destructive" />
                                                        )}
                                                        <div className="min-w-0 flex-1">
                                                            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                                                                <span>{new Date(record.at * 1000).toLocaleString()}</span>
                                                                <span>
                                                                    {record.source === 'scheduled'
                                                                        ? t('sourceScheduled')
                                                                        : t('sourceManual')}
                                                                </span>
                                                                {record.use_time > 0 && <span>{record.use_time} ms</span>}
                                                            </div>
                                                            {!record.success && record.reason && (
                                                                <p className="mt-0.5 break-all text-xs text-destructive/90">
                                                                    {record.reason}
                                                                </p>
                                                            )}
                                                        </div>
                                                    </div>
                                                ))}
                                            </div>
                                        )}
                                    </>
                                )}
                            </div>
                        )}
                    </Tabs>
                </DialogContent>
            </DialogRoot>

            <RequestDetailDialog open={detailOpen} onOpenChange={setDetailOpen} requestId={detailRequestId} />
        </>
    );
}
