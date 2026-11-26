import { useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { usePlanQuotaList, useRefreshPlanQuotaList, type PlanQuotaListItem, type PlanQuotaWindow } from '@/api/channel';

// 窗口顺序固定 5 小时 → 周 → 月, 与渠道详情里的顺序一致。
const WINDOW_ORDER = ['5h', 'week', 'month'];

function sortWindows(windows: PlanQuotaWindow[]): PlanQuotaWindow[] {
    return [...windows].sort((left, right) => {
        const leftIndex = WINDOW_ORDER.indexOf(left.key);
        const rightIndex = WINDOW_ORDER.indexOf(right.key);
        return (leftIndex === -1 ? WINDOW_ORDER.length : leftIndex)
            - (rightIndex === -1 ? WINDOW_ORDER.length : rightIndex);
    });
}

// lowestRemaining 取所有窗口里最紧的那一档余量, 用于排序。
// 没有可用窗口(采集失败或账号无套餐)时给 -1, 排到最后, 不冒充"余量充足"。
function lowestRemaining(item: PlanQuotaListItem): number {
    if (item.windows.length === 0) return -1;
    return Math.min(...item.windows.map((window) => 100 - window.percent));
}

// useNow 每 30 秒推进一次, 让重置倒计时自己走动。
// 不做秒级: 展示粒度是分钟, 每秒重渲染整块不划算。
function useNow(intervalMs = 30000): number {
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
        return () => window.clearInterval(timer);
    }, [intervalMs]);
    return now;
}

// formatDuration 输出紧凑倒计时: 2h01m / 1d12h / 37m。
// 用紧凑写法而非"2 小时 1 分后重置", 是因为它要和百分比挤在同一行;
// 三列并排时, 长句会把标签顶出去。
function formatDuration(seconds: number): string {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    if (days > 0) return hours > 0 ? `${days}d${hours}h` : `${days}d`;
    if (hours > 0) return `${hours}h${String(minutes).padStart(2, '0')}m`;
    return `${Math.max(1, minutes)}m`;
}

const timeFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
const dayTimeFormat = new Intl.DateTimeFormat(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
});

// formatResetAt 把重置时刻写成具体钟点: 当天只给时分, 跨天补上日期。
//
// 光有"还有 3d11h"没法安排事情 —— 得知道那是 12-01 00:00 还是 12-01 09:00。
// 当天窗口不写日期: 5 小时窗口一律在当天, 补个 11-26 只会把这一行撑宽。
function formatResetAt(resetAt: number): string {
    const date = new Date(resetAt * 1000);
    const now = new Date();
    const sameDay = date.getFullYear() === now.getFullYear()
        && date.getMonth() === now.getMonth()
        && date.getDate() === now.getDate();
    return sameDay ? timeFormat.format(date) : dayTimeFormat.format(date);
}

// barClass 按剩余比例填充并上色: 条越长越安心, 与"剩余 80%"这个数字对得上。
// 剩余 30% 转琥珀、10% 转红。阈值不再往上收紧, 是因为套餐本来就会被用到接近满,
// 门槛太松会让面板长期挂着告警色。
function barClass(remaining: number): string {
    if (remaining <= 10) return 'bg-destructive';
    if (remaining <= 30) return 'bg-amber-500';
    return 'bg-primary';
}

function remainingClass(remaining: number): string {
    if (remaining <= 10) return 'text-destructive';
    if (remaining <= 30) return 'text-amber-600 dark:text-amber-500';
    return 'text-card-foreground';
}

// countdownClass 让倒计时自己报紧急度: 半小时内转琥珀, 十分钟内转红。
// 正常态用正文色而不是灰 —— 它是"现在还能不能安排一轮"的判断依据, 不该被压成脚注。
function countdownClass(seconds: number): string {
    if (seconds <= 600) return 'text-destructive';
    if (seconds <= 1800) return 'text-amber-600 dark:text-amber-500';
    return 'text-card-foreground';
}

const clockFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });

// WindowMeter 是一个窗口的一列: 标题行(名称 + 已用) / 进度条 / 脚注(重置时刻 + 倒计时)。
//
// 分三层而不是挤成一行: "已用 19%" 和 "15:00 重置" 字号相同、紧挨在一起时,
// 眼睛会把它们读成一句话, 分不清哪个是消耗、哪个是时间。
// 现在主值是 14px 加粗居中在条的右上方, 时刻退到条下方的小字灰字, 谁主谁次一眼能分开。
function WindowMeter({ window: item, label, now }: { window: PlanQuotaWindow; label: string; now: number }) {
    const t = useTranslations('home.planQuota');
    const percent = Math.min(100, Math.max(0, item.percent));
    // 一律按剩余说话: 进度条长度、百分比、颜色三者同口径, 不用在脑子里做 100 减。
    const remaining = Math.max(0, 100 - percent);
    const seconds = item.reset_at > 0 ? item.reset_at - Math.floor(now / 1000) : 0;

    return (
        <div className="min-w-0">
            <div className="mb-1 flex items-baseline justify-between gap-2">
                <span className="text-[11px] leading-none text-muted-foreground">{label}</span>
                <span className={`text-[13px] font-semibold leading-none tabular-nums ${remainingClass(remaining)}`}>
                    {t('remaining', { percent: Math.round(remaining) })}
                </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                    className={`h-full rounded-full transition-[width] duration-500 ${barClass(remaining)}`}
                    style={{ width: `${remaining}%` }}
                />
            </div>
            <div className="mt-1 flex items-baseline gap-2 text-[11px] leading-none tabular-nums">
                {item.reset_at <= 0 ? (
                    <span className="text-muted-foreground">{t('noReset')}</span>
                ) : seconds <= 0 ? (
                    <span className="text-muted-foreground">{t('expired')}</span>
                ) : (
                    <>
                        {/* 两个值说的事不同, 就必须长得不同: 时刻(何时回血)是排期, 退成灰字;
                            倒计时(还剩多久)是当下决策依据, 用正文色加粗, 告急再转琥珀/红。 */}
                        <span className="text-muted-foreground">{t('resetAt', { time: formatResetAt(item.reset_at) })}</span>
                        <span className={`font-medium ${countdownClass(seconds)}`}>{formatDuration(seconds)}</span>
                    </>
                )}
            </div>
        </div>
    );
}

// QuotaCard 是一个套餐的额度行: 左侧抬头, 右侧三个窗口横向平分。
//
// 单列铺满而不是并排卡片: 一个实例通常只挂一两个套餐, 两列网格会让半屏空着,
// 而每张卡为了撑住宽度又把内容拉开, 整块显得又高又散。横排三窗口把高度压到一行。
function QuotaCard({ item, now }: { item: PlanQuotaListItem; now: number }) {
    const t = useTranslations('home.planQuota');
    const labels: Record<string, string> = {
        '5h': t('window.fiveHour'),
        week: t('window.week'),
        month: t('window.month'),
    };

    const hasWindows = item.windows.length > 0;
    const lowest = lowestRemaining(item);
    // 徽章只在真该处理时出现: 常态挂一个"正常"标签会把整块压成状态噪音。
    const badge = lowest >= 0 && lowest <= 10
        ? { text: t('exhausted'), className: 'bg-destructive/10 text-destructive' }
        : lowest >= 0 && lowest <= 30
            ? { text: t('nearLimit'), className: 'bg-amber-500/10 text-amber-600 dark:text-amber-500' }
            : null;

    return (
        <div className="rounded-2xl border border-border bg-card px-4 py-2.5 text-card-foreground">
            <div className="flex items-baseline gap-2 leading-none">
                <span className={`truncate text-sm font-medium ${item.enabled ? '' : 'text-muted-foreground/50'}`}>
                    {item.channel_name}
                </span>
                {item.plan_name && (
                    <span className="shrink-0 text-[11px] capitalize text-muted-foreground/60">{item.plan_name}</span>
                )}
                {badge && (
                    <span className={`shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-medium ${badge.className}`}>
                        {badge.text}
                    </span>
                )}
                {/* 有数但这次没刷上: 必须单独标出来, 否则旧数字看着和实时数据一样新。 */}
                {hasWindows && item.error !== '' && (
                    <span className="shrink-0 text-[10px] text-amber-600 dark:text-amber-500" title={item.error}>
                        {t('stale')}
                    </span>
                )}
            </div>

            {hasWindows ? (
                <div className="mt-2 grid gap-x-6 gap-y-2.5 @lg/home:grid-cols-3">
                    {sortWindows(item.windows).map((window) => (
                        <WindowMeter key={window.key} window={window} label={labels[window.key] ?? window.key} now={now} />
                    ))}
                </div>
            ) : (
                <p
                    className={`mt-1.5 line-clamp-2 break-all text-[11px] leading-relaxed ${item.error ? 'text-destructive' : 'text-muted-foreground'}`}
                    title={item.error}
                >
                    {item.error ? `${t('errorTitle')}: ${item.error}` : t('neverCollected')}
                </p>
            )}
        </div>
    );
}

/**
 * PlanQuotaList 是首页的套餐额度聚合区块: 一屏列出所有配了额度的渠道。
 *
 * 与渠道详情里的面板互补: 那里回答"这个渠道还能不能跑", 这里回答"今天哪个套餐快见底了"。
 * 排序按最紧的那一档余量升序, 最该关注的排最前; 采集失败的排最后。
 * 一条都没有时整块不渲染 —— 绝大多数实例不配套餐额度, 不该常年挂一块空壳。
 */
export function PlanQuotaList() {
    const t = useTranslations('home.planQuota');
    const { data } = usePlanQuotaList();
    const refresh = useRefreshPlanQuotaList();
    const now = useNow();

    if (!data || data.length === 0) return null;

    const rows = [...data].sort((left, right) => {
        const leftLowest = lowestRemaining(left);
        const rightLowest = lowestRemaining(right);
        if (leftLowest < 0 && rightLowest < 0) return left.channel_name.localeCompare(right.channel_name);
        if (leftLowest < 0) return 1;
        if (rightLowest < 0) return -1;
        return leftLowest - rightLowest;
    });

    // 顶部统一显示最晚的一次采集时刻: 各渠道是同一轮任务采的, 逐卡片再写一遍会啰嗦。
    const latest = Math.max(...data.map((item) => item.fetched_at));

    return (
        <section className="space-y-2">
            <div className="flex items-center gap-2">
                <h3 className="text-sm font-medium">{t('title')}</h3>
                <span className="text-[11px] text-muted-foreground/60">{t('hint')}</span>
                <div className="flex-1" />
                <span className="text-[11px] tabular-nums text-muted-foreground/60">
                    {latest > 0
                        ? t('updatedAt', { time: clockFormat.format(new Date(latest * 1000)) })
                        : t('neverCollected')}
                </span>
                <button
                    type="button"
                    data-share-exclude
                    onClick={() =>
                        refresh.mutate(undefined, {
                            // 单个渠道采集失败不走这里(后端已把原因落库显示在卡片上);
                            // 能到这儿的只有整批请求本身失败, 比如网络断开或后端超时。
                            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
                        })
                    }
                    disabled={refresh.isPending}
                    aria-label={t('refresh')}
                    title={t('refresh')}
                    className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground/60 transition-colors hover:text-foreground disabled:opacity-50"
                >
                    <RefreshCw className={`size-3.5 ${refresh.isPending ? 'animate-spin' : ''}`} />
                </button>
            </div>
            <div className="flex flex-col gap-2">
                {rows.map((item) => (
                    <QuotaCard key={item.channel_id} item={item} now={now} />
                ))}
            </div>
        </section>
    );
}
