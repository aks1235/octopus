import { useTranslations } from 'use-intl';
import { Ban, CircleCheck, Loader2 } from 'lucide-react';
import {
    DialogRoot,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
} from '@/components/ui/dialog';
import { useGrantProbeTimeline } from '@/api/group';

// ProbeHistoryDialog 是单个成员的探测历史（受控弹窗）。
// 与成员行上的徽标分工：徽标给"当前结论"，这里给"这段时间挂过几次、每次都错在哪"。
// 窗口固定 90 天（后端另有 grant_probe_keep_days 控制留存），一次探测一行，密度很低，列表比曲线诚实。
const probeHistoryDays = 90;

export function ProbeHistoryDialog({
    grantId,
    onOpenChange,
}: {
    grantId: number | null;
    onOpenChange: (open: boolean) => void;
}) {
    const t = useTranslations('group.probe');
    const query = useGrantProbeTimeline(grantId, probeHistoryDays);
    const data = query.data;

    return (
        <DialogRoot open={grantId != null} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-2xl rounded-2xl">
                <DialogHeader>
                    <DialogTitle>{data ? `${data.model_name} · ${data.key_name}` : t('title')}</DialogTitle>
                    <DialogDescription>
                        {data
                            ? `${data.channel_name} · ${t('window', { days: probeHistoryDays })}`
                            : t('window', { days: probeHistoryDays })}
                    </DialogDescription>
                </DialogHeader>

                {query.isLoading && (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}

                {data && (
                    <div className="space-y-3">
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
                    </div>
                )}
            </DialogContent>
        </DialogRoot>
    );
}
