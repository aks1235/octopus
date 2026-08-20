import { useMemo } from 'react';
import { ArrowLeft, Loader2, RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelAttempts } from '@/api/log';
import { useAppStore } from '@/stores/app';
import { useMorphingDialog } from '@/components/ui/morphing-dialog';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

const STATUS_ORDER = ['success', 'failed', 'skipped'] as const;

// statusStyle 按尝试状态返回徽标样式。
function statusStyle(status: string) {
    switch (status) {
        case 'success':
            return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400';
        case 'failed':
            return 'bg-destructive/10 text-destructive';
        case 'skipped':
            return 'bg-amber-500/10 text-amber-600 dark:text-amber-400';
        default:
            return 'bg-muted text-muted-foreground';
    }
}

// formatTime 将 ISO 字符串格式化为本地可读时间。
function formatTime(iso: string) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString();
}

// CallDetail 渲染指定渠道在内存日志窗口内的每次调用明细。
// 作为 CardContent 的子视图渲染（复用外层 MorphingDialog 上下文）。
export function CallDetail({ channelName, onBack }: { channelName: string; onBack: () => void }) {
    const t = useTranslations('channel.callDetail');
    const { setIsOpen } = useMorphingDialog();
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    const { data: attempts = [], isLoading, error, refetch, isFetching } = useChannelAttempts(channelName);

    const summary = useMemo(() => {
        const counts: Record<string, number> = { success: 0, failed: 0, skipped: 0 };
        for (const a of attempts) {
            counts[a.status] = (counts[a.status] ?? 0) + 1;
        }
        return counts;
    }, [attempts]);

    // goToLog 关闭渠道详情弹窗并切换到日志页（内存窗口内可继续定位该请求）。
    const goToLog = () => {
        setIsOpen(false);
        setCurrentPage('log');
    };

    return (
        <div className="flex h-[60vh] flex-col gap-3">
            <header className="flex shrink-0 items-center justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                    <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={onBack} aria-label={t('back')}>
                        <ArrowLeft className="size-4" />
                    </Button>
                    <h3 className="truncate text-base font-semibold">{channelName}</h3>
                </div>
                <Button
                    variant="ghost"
                    size="icon"
                    className="size-8 shrink-0"
                    onClick={() => refetch()}
                    disabled={isFetching}
                    aria-label={t('refresh')}
                >
                    <RefreshCw className={cn('size-4', isFetching && 'animate-spin')} />
                </Button>
            </header>

            <div className="flex shrink-0 flex-wrap items-center gap-2 text-xs">
                <span className="rounded-full bg-muted px-2.5 py-1 font-medium text-muted-foreground">
                    {t('total')}: {attempts.length}
                </span>
                {STATUS_ORDER.map((s) => (
                    <span key={s} className={cn('rounded-full px-2.5 py-1 font-medium', statusStyle(s))}>
                        {t(`status.${s}`)}: {summary[s] ?? 0}
                    </span>
                ))}
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto rounded-2xl border border-border/70">
                {isLoading ? (
                    <div className="flex h-full items-center justify-center">
                        <Loader2 className="size-5 animate-spin text-muted-foreground" />
                    </div>
                ) : error ? (
                    <div className="flex h-full items-center justify-center p-4 text-center text-xs text-destructive">
                        {t('error')}
                    </div>
                ) : attempts.length === 0 ? (
                    <div className="flex h-full items-center justify-center p-4 text-center text-xs text-muted-foreground">
                        {t('empty')}
                    </div>
                ) : (
                    <ul className="divide-y divide-border/70">
                        {attempts.map((a, i) => (
                            <li key={`${a.request_id}-${a.attempt_index}-${i}`} className="flex flex-col gap-1 px-3 py-2.5 text-xs">
                                <div className="flex items-center gap-2">
                                    <span className={cn('rounded px-1.5 py-0.5 font-medium', statusStyle(a.status))}>
                                        {t(`status.${a.status}`)}
                                    </span>
                                    <span className="text-muted-foreground">#{a.attempt_index}</span>
                                    <span className="truncate font-medium">{a.model_name}</span>
                                    <span className="ml-auto text-muted-foreground">{a.duration}ms</span>
                                </div>
                                <div className="flex items-center gap-2 text-muted-foreground">
                                    <button type="button" onClick={goToLog} className="font-mono text-primary hover:underline">
                                        #{a.request_id}
                                    </button>
                                    <span>·</span>
                                    <span>{formatTime(a.started_at)}</span>
                                    <span>·</span>
                                    <span className="truncate">{a.request_model}</span>
                                </div>
                                {a.error && <p className="break-all text-destructive">{a.error}</p>}
                                {a.final_channel_name && a.final_channel_name === channelName && (
                                    <p className="text-emerald-600 dark:text-emerald-400">{t('finalChannel')}</p>
                                )}
                            </li>
                        ))}
                    </ul>
                )}
            </div>
        </div>
    );
}
