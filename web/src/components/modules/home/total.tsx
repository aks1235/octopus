import {
    Activity,
    MessageSquare,
    Clock,
    ArrowDownToLine,
    ChartColumnBig,
    Bot,
    ArrowUpFromLine,
    Rewind,
    DollarSign,
    FastForward
} from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useStatsTotal, type StatsMetricsFormatted } from '@/api/stats';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount } from '@/lib/utils';

// TokenComposition 展示词元总量里 缓存命中 / 新增输入 / 输出 / 缓存写入 的占比。
// 不套卡片: 单独一张宽卡里只有一条细线, 视觉上是一块空白色块; 这里做成统计区上方的一行说明,
// 四列数值把宽度占满, 也不与下方四张指标卡抢层级。
// 上游没返回缓存明细时只给一句说明, 不画一条"全是新增输入"的假分布。
function TokenComposition({ stats }: { stats: StatsMetricsFormatted }) {
    const t = useTranslations('home.total');
    const rows = [
        { key: 'cacheRead', label: t('cacheRead'), value: stats.cache_read_token.raw, color: 'bg-chart-1' },
        { key: 'newInput', label: t('newInput'), value: stats.new_input_token.raw, color: 'bg-chart-2' },
        { key: 'output', label: t('outputTokens'), value: stats.output_token.raw, color: 'bg-chart-4' },
        { key: 'cacheWrite', label: t('cacheWrite'), value: stats.cache_write_token.raw, color: 'bg-chart-5' },
    ];
    const total = rows.reduce((sum, row) => sum + row.value, 0);
    const hasCache = stats.cache_read_token.raw + stats.cache_write_token.raw > 0;
    const inputTotal = stats.input_token.raw;
    const hitRate = inputTotal > 0 ? (stats.cache_read_token.raw / inputTotal) * 100 : 0;
    const share = (value: number) => (total > 0 ? ((value / total) * 100).toFixed(1) : '0.0');
    const amount = (value: number) => {
        const formatted = formatCount(value).formatted;
        return `${formatted.value}${formatted.unit}`;
    };

    return (
        <section className="px-1">
            <div className="mb-2 flex items-center justify-between gap-3 text-xs">
                <h3 className="font-medium text-foreground">{t('composition')}</h3>
                <span className="text-muted-foreground">
                    {hasCache ? `${t('cacheHitRate')} ${hitRate.toFixed(1)}%` : t('cacheUnavailable')}
                </span>
            </div>

            <div
                className="flex h-3 w-full overflow-hidden rounded-full bg-border/60"
                role="img"
                aria-label={rows.map((row) => `${row.label} ${amount(row.value)}`).join('，')}
            >
                {rows.map((row) => (
                    <span
                        key={row.key}
                        className={`h-full ${row.color}`}
                        style={{ width: `${share(row.value)}%` }}
                        title={`${row.label} ${amount(row.value)} · ${share(row.value)}%`}
                    />
                ))}
            </div>

            {/* 四列铺满整行: 色块 - 名称 - 绝对值 - 占比, 不再是一条悬在空处的图例。 */}
            <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 @xl/home:grid-cols-4">
                {rows.map((row) => (
                    <div key={row.key} className="flex min-w-0 items-baseline gap-1.5 text-xs">
                        <span className={`mt-1 size-2 shrink-0 self-start rounded-[2px] ${row.color}`} aria-hidden="true" />
                        <span className="truncate text-muted-foreground">{row.label}</span>
                        <span className="ml-auto shrink-0 font-medium text-foreground tabular-nums">{amount(row.value)}</span>
                        <span className="shrink-0 text-muted-foreground tabular-nums">{share(row.value)}%</span>
                    </div>
                ))}
            </div>
        </section>
    );
}

// Total 展示累计的请求, 总量, 输入和输出四组指标卡片, 顶部附一条词元构成占比。
export function Total() {
    const { data: stats } = useStatsTotal();
    const t = useTranslations('home.total');

    const cards = [
        {
            title: t('requestStats'),
            headerIcon: Activity,
            items: [
                { label: t('requestCount'), metric: stats?.request_count, icon: MessageSquare, bgColor: 'bg-primary/10' },
                { label: t('timeConsumed'), metric: stats?.wait_time, icon: Clock, bgColor: 'bg-accent/10' },
            ],
        },
        {
            title: t('totalStats'),
            headerIcon: ChartColumnBig,
            items: [
                { label: t('totalToken'), metric: stats?.total_token, icon: Bot, bgColor: 'bg-chart-1/10' },
                { label: t('totalCost'), metric: stats?.total_cost, icon: DollarSign, bgColor: 'bg-chart-2/10' },
            ],
        },
        {
            title: t('inputStats'),
            headerIcon: ArrowDownToLine,
            items: [
                { label: t('inputTokens'), metric: stats?.input_token, icon: Rewind, bgColor: 'bg-chart-3/10' },
                { label: t('inputCost'), metric: stats?.input_cost, icon: DollarSign, bgColor: 'bg-chart-3/10' },
            ],
        },
        {
            title: t('outputStats'),
            headerIcon: ArrowUpFromLine,
            items: [
                { label: t('outputTokens'), metric: stats?.output_token, icon: FastForward, bgColor: 'bg-chart-4/10' },
                { label: t('outputCost'), metric: stats?.output_cost, icon: DollarSign, bgColor: 'bg-chart-4/10' },
            ],
        },
    ];

    return (
        <div className="space-y-4">
            {stats && <TokenComposition stats={stats} />}
            <div className="grid grid-cols-1 @xl/home:grid-cols-2 @3xl/home:grid-cols-4 gap-4">
                {cards.map((card) => (
                    <section
                        key={card.title}
                        className="rounded-3xl bg-card border-border border p-5 text-card-foreground flex flex-row items-center gap-4"
                    >
                        <div className="flex flex-col items-center justify-center gap-3 border-r border-border/50 pr-4 py-1 self-stretch">
                            <card.headerIcon className="w-4 h-4" />
                            <h3 className="font-medium text-sm [writing-mode:vertical-lr]">{card.title}</h3>
                        </div>

                        <div className="flex flex-col gap-4 flex-1 min-w-0">
                            {card.items.map((item) => (
                                <div key={item.label} className="flex items-center gap-3">
                                    <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${item.bgColor}`}>
                                        <item.icon className="w-5 h-5" />
                                    </div>
                                    <div className="flex flex-col min-w-0">
                                        <span className="text-xs text-muted-foreground">{item.label}</span>
                                        <div className="flex items-baseline gap-1">
                                            <span className="text-xl">
                                                <AnimatedNumber value={item.metric?.formatted.value} />
                                            </span>
                                            {/* 计数在千位以下 unit 为空串, 空 span 仍是 flex 项, 会多出 gap-1 的间距。 */}
                                            {item.metric?.formatted.unit && (
                                                <span className="text-sm text-muted-foreground">{item.metric.formatted.unit}</span>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            ))}
                        </div>
                    </section>
                ))}
            </div>
        </div>
    );
}
