import { useTranslations } from 'use-intl';
import { usePlanQuotaList, type PlanQuotaListItem, type PlanQuotaWindow } from '@/api/channel';

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

// chipClass 与渠道详情里同一套阈值: 90% 已用才转红, 70% 转琥珀。
function chipClass(remaining: number): string {
    if (remaining <= 10) return 'border-destructive/30 bg-destructive/10 text-destructive';
    if (remaining <= 30) return 'border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-500';
    return 'border-border bg-muted text-muted-foreground';
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

    if (!data || data.length === 0) return null;

    const rows = [...data].sort((left, right) => {
        const leftLowest = lowestRemaining(left);
        const rightLowest = lowestRemaining(right);
        if (leftLowest < 0 && rightLowest < 0) return left.channel_name.localeCompare(right.channel_name);
        if (leftLowest < 0) return 1;
        if (rightLowest < 0) return -1;
        return leftLowest - rightLowest;
    });

    const labels: Record<string, string> = {
        '5h': t('window.fiveHour'),
        week: t('window.week'),
        month: t('window.month'),
    };

    return (
        <section className="rounded-2xl border bg-card px-3 py-2.5">
            <div className="mb-1 flex items-center gap-2">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground">{t('title')}</h3>
                <span className="text-[11px] text-muted-foreground/60">{t('hint')}</span>
            </div>
            <div className="flex flex-col">
                {rows.map((item) => (
                    <div
                        key={item.channel_id}
                        className="flex items-center gap-2 border-t border-border/50 py-1.5 first:border-t-0"
                    >
                        <span className={`min-w-0 flex-1 truncate text-sm ${item.enabled ? 'text-card-foreground' : 'text-muted-foreground/50'}`}>
                            {item.channel_name}
                        </span>
                        {item.plan_name && (
                            <span className="shrink-0 rounded border px-1 text-[10px] text-muted-foreground">
                                {item.plan_name}
                            </span>
                        )}
                        {item.windows.length > 0 ? (
                            <div className="flex shrink-0 items-center gap-1">
                                {sortWindows(item.windows).map((window) => {
                                    const remaining = Math.max(0, 100 - window.percent);
                                    return (
                                        <span
                                            key={window.key}
                                            className={`rounded border px-1.5 text-[11px] tabular-nums ${chipClass(remaining)}`}
                                            title={t('chipTitle', {
                                                window: labels[window.key] ?? window.key,
                                                used: Math.round(window.percent),
                                                left: Math.round(remaining),
                                            })}
                                        >
                                            {labels[window.key] ?? window.key} {Math.round(remaining)}%
                                        </span>
                                    );
                                })}
                            </div>
                        ) : (
                            <span className="shrink-0 text-[11px] text-destructive">{t('failed')}</span>
                        )}
                    </div>
                ))}
            </div>
        </section>
    );
}
