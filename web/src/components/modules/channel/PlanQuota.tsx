import { useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelPlanQuota, useRefreshChannelPlanQuota, type ChannelPlanQuota, type PlanQuotaWindow } from '@/api/channel';

// 窗口顺序固定为 5 小时 → 周 → 月, 与火山控制台一致, 也符合"先看最近一档"的查看习惯。
const WINDOW_ORDER = ['5h', 'week', 'month'];

function windowIndex(key: string): number {
    const index = WINDOW_ORDER.indexOf(key);
    return index === -1 ? WINDOW_ORDER.length : index;
}

// 进度条按已用比例填充, 颜色随余量收紧。
// 90% 才转红而非 80%: 套餐额度本来就该用到接近满, 阈值太松会让面板长期挂着告警色。
function barClass(percent: number): string {
    if (percent >= 90) return 'bg-destructive';
    if (percent >= 70) return 'bg-amber-500';
    return 'bg-primary';
}

const numberFormat = new Intl.NumberFormat();

function formatCount(value: number): string {
    return numberFormat.format(Math.round(value));
}

// useNow 每 30 秒推进一次, 让倒计时与"多久前刷新"自己走动。
// 不做秒级: 展示粒度是分钟, 每秒重渲染整个面板不划算。
function useNow(intervalMs = 30000): number {
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
        return () => window.clearInterval(timer);
    }, [intervalMs]);
    return now;
}

// ResetLabel 把重置时刻说成人话: 天/小时/分钟三档, 已到期则提示即重置。
function ResetLabel({ resetAt, now }: { resetAt: number; now: number }) {
    const t = useTranslations('channel.planQuota');
    if (resetAt <= 0) return <span className="text-muted-foreground/50">{t('noReset')}</span>;

    const seconds = resetAt - Math.floor(now / 1000);
    if (seconds <= 0) return <span className="text-muted-foreground/50">{t('resetSoon')}</span>;
    if (seconds >= 86400) return <span>{t('resetInDays', { count: Math.floor(seconds / 86400) })}</span>;
    if (seconds >= 3600) return <span>{t('resetInHours', { count: Math.floor(seconds / 3600) })}</span>;
    return <span>{t('resetInMinutes', { count: Math.max(1, Math.floor(seconds / 60)) })}</span>;
}

// RefreshedAt 显示最近一次采集距今多久; 采集失败也更新该时刻, 故它表达"多久没刷了"而非"数据多新"。
function RefreshedAt({ quota, now }: { quota: ChannelPlanQuota; now: number }) {
    const t = useTranslations('channel.planQuota');
    if (!quota.fetched_at) return <>{t('neverRefreshed')}</>;

    const seconds = Math.floor(now / 1000) - quota.fetched_at;
    if (seconds < 60) return <>{t('refreshedJustNow')}</>;
    if (seconds < 3600) return <>{t('refreshedMinutesAgo', { count: Math.floor(seconds / 60) })}</>;
    return <>{t('refreshedHoursAgo', { count: Math.floor(seconds / 3600) })}</>;
}

function WindowRow({ window, now }: { window: PlanQuotaWindow; now: number }) {
    const t = useTranslations('channel.planQuota');
    const labels: Record<string, string> = {
        '5h': t('window.fiveHour'),
        week: t('window.week'),
        month: t('window.month'),
    };
    const percent = Math.min(100, Math.max(0, window.percent));
    const remaining = Math.max(0, 100 - percent);

    return (
        <div className="flex items-center gap-2">
            <span className="w-9 shrink-0 text-[11px] text-muted-foreground">{labels[window.key] ?? window.key}</span>
            <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-muted">
                <div
                    className={`h-full rounded-full transition-[width] duration-500 ${barClass(percent)}`}
                    style={{ width: `${percent}%` }}
                />
            </div>
            {/* 绝对用量在窄屏隐去: 百分比与进度条已表达同一件事, 弹窗收窄时优先保住可读性。 */}
            <span className="hidden w-[104px] shrink-0 text-right text-[11px] tabular-nums text-muted-foreground @md/stats:inline">
                {formatCount(window.used)} / {formatCount(window.total)}
            </span>
            <span className={`w-14 shrink-0 text-right text-[11px] tabular-nums ${remaining <= 10 ? 'text-destructive' : 'text-card-foreground'}`}>
                {t('remaining', { percent: Math.round(remaining) })}
            </span>
            <span className="w-[86px] shrink-0 text-right text-[11px] tabular-nums text-muted-foreground/70">
                <ResetLabel resetAt={window.reset_at} now={now} />
            </span>
        </div>
    );
}

/**
 * PlanQuota 展示渠道订阅套餐的额度窗口(火山方舟 Agent Plan 等)。
 *
 * 只在真有数据可展示时占位: 没配额度采集、或从未采到过, 整块不渲染,
 * 免得给绝大多数非订阅制渠道都挂一行空壳。
 * 采集失败但历史有数时保留旧值并标注"可能已过期", 而不是把面板清空。
 */
export function PlanQuota({ channelId }: { channelId: number }) {
    const t = useTranslations('channel.planQuota');
    const { data: quota } = useChannelPlanQuota(channelId);
    const refresh = useRefreshChannelPlanQuota();
    const now = useNow();

    if (!quota) return null;

    const windows = [...quota.windows].sort((left, right) => windowIndex(left.key) - windowIndex(right.key));
    const hasWindows = windows.length > 0;
    if (!hasWindows && !quota.error) return null;

    const stale = hasWindows && quota.error !== '';

    return (
        <section className="mb-3 flex flex-col gap-1.5 rounded-2xl border bg-card px-3 py-2.5">
            <div className="flex min-h-6 items-center gap-1.5">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground">{t('title')}</h3>
                {quota.plan_name && (
                    <span className="rounded-md border px-1.5 py-0 text-[10px] font-medium text-muted-foreground">
                        {quota.plan_name}
                    </span>
                )}
                <div className="flex-1" />
                <span className="text-[11px] tabular-nums text-muted-foreground/70">
                    <RefreshedAt quota={quota} now={now} />
                </span>
                <button
                    type="button"
                    data-share-exclude
                    onClick={() => refresh.mutate(channelId)}
                    disabled={refresh.isPending}
                    aria-label={t('refresh')}
                    className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground disabled:opacity-50"
                >
                    <RefreshCw className={`size-3.5 ${refresh.isPending ? 'animate-spin' : ''}`} />
                </button>
            </div>

            {hasWindows ? (
                <div className="flex flex-col gap-1.5">
                    {windows.map((window) => (
                        <WindowRow key={window.key} window={window} now={now} />
                    ))}
                </div>
            ) : (
                <p className="text-xs leading-relaxed text-destructive">
                    {t('errorTitle')}: {quota.error}
                </p>
            )}

            {stale && <p className="text-[11px] text-amber-600 dark:text-amber-500">{t('stale')}</p>}

            <p className="text-[11px] leading-relaxed text-muted-foreground/60">{t('footnote')}</p>
        </section>
    );
}
