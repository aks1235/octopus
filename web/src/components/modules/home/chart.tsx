import { Fragment, useId, useMemo } from 'react';
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { ChartLine } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { todayDateStr } from '@/api/queries';
import { useStatsDaily, useStatsHourly, type StatsMetricsFormatted } from '@/api/stats';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount, formatMoney } from '@/lib/utils';
import { useTheme } from '@/provider/theme';
import { useHomeViewStore } from './store';
import { MetricTabs } from './metric-tabs';

// 趋势图上的一个点。小时与每日两种粒度只用到这三项, 且两者取值方式一致, 故统一成同一形状。
interface ChartPoint {
    label: string; // 横轴刻度, 小时粒度为 H:00, 每日粒度为 MM/DD。
    stat: Pick<StatsMetricsFormatted, 'request_count' | 'total_cost' | 'total_token'>;
}

// StatsChart 展示选定指标在选中日期(或其截止的周期)内的趋势, 并汇总该周期的请求, 金额和词元。
export function StatsChart() {
    const { data: statsDaily } = useStatsDaily();
    const selectedDate = useHomeViewStore((state) => state.selectedDate);
    const { data: statsHourly } = useStatsHourly(selectedDate);
    const t = useTranslations('home.chart');
    const tMetric = useTranslations('home.metric');
    // 订阅主题: 切换时 :root 上 --chart-* 的取值会变, 需重渲染以重新取色。
    useTheme();
    // 屏内图表与分享舞台副本同时存在, 渐变 id 需唯一, 避免 url(#id) 指向另一份 defs。
    const gradientId = `fillMetric-${useId().replace(/[^a-zA-Z0-9]/g, '')}`;

    const metricType = useHomeViewStore((state) => state.chartMetricType);
    const setMetricType = useHomeViewStore((state) => state.setChartMetricType);
    const period = useHomeViewStore((state) => state.chartPeriod);
    const setPeriod = useHomeViewStore((state) => state.setChartPeriod);
    const isToday = selectedDate === todayDateStr();

    // 单日取选中日的小时曲线, 多日取截止到选中日的最近 N 天; 两种粒度的统计字段同名, 后续处理不再分支。
    const source = useMemo<ChartPoint[]>(() => {
        if (period === '1') {
            return (statsHourly ?? []).map((stat) => ({ label: `${stat.hour}:00`, stat }));
        }
        return (statsDaily ?? [])
            .filter((stat) => stat.date <= selectedDate)
            .slice(-Number(period))
            .map((stat) => ({ label: `${stat.date.slice(4, 6)}/${stat.date.slice(6, 8)}`, stat }));
    }, [statsDaily, statsHourly, period, selectedDate]);

    // 图表从首个有请求的点前一个开始, 避免开头一长段零值; 汇总仍按整个周期统计。
    const firstUsage = source.findIndex((item) => item.stat.request_count.raw > 0);
    const metricField = metricType === 'cost' ? 'total_cost' : metricType === 'count' ? 'request_count' : 'total_token';
    const chartData = source
        .slice(firstUsage === -1 ? Math.max(source.length - 1, 0) : Math.max(firstUsage - 1, 0))
        .map((item) => ({ date: item.label, value: item.stat[metricField].raw }));

    const chartColor = getComputedStyle(document.documentElement)
        .getPropertyValue(metricType === 'cost' ? '--chart-1' : metricType === 'count' ? '--chart-2' : '--chart-3')
        .trim();
    // 单日周期下展示实际查看的日期, 今天才叫「今天」; 多日周期只与跨度有关, 不随选中日变。
    const periodLabel = period === '1' && !isToday
        ? `${selectedDate.slice(0, 4)}-${selectedDate.slice(4, 6)}-${selectedDate.slice(6, 8)}`
        : { '1': t('period.today'), '7': t('period.last7Days'), '30': t('period.last30Days') }[period];
    // 历史日整段无请求时给空态, 避免一条贴地的零值线被误读成"当天全天空转"。
    const hasData = source.some((item) => item.stat.request_count.raw > 0);
    const showEmpty = period === '1' && !isToday && !hasData;
    const summary = [
        { label: t('totalRequests'), metric: formatCount(source.reduce((sum, item) => sum + item.stat.request_count.raw, 0)) },
        { label: t('totalCost'), metric: formatMoney(source.reduce((sum, item) => sum + item.stat.total_cost.raw, 0)) },
        { label: t('totalTokens'), metric: formatCount(source.reduce((sum, item) => sum + item.stat.total_token.raw, 0)) },
    ];

    return (
        <div className="rounded-3xl bg-card border-border border pt-2 pb-0 text-card-foreground">
            <div className="px-4 pb-2 space-y-2">
                <div className="flex justify-between items-center">
                    <h3 className="font-semibold text-base">{t('title')}</h3>
                    <MetricTabs value={metricType} onChange={setMetricType} />
                </div>

                <div className="flex justify-between items-start">
                    <div className="flex gap-2 text-sm">
                        {summary.map(({ label, metric }, index) => (
                            <Fragment key={label}>
                                {index > 0 && <div className="w-px bg-border self-stretch" />}
                                <div>
                                    <div className="text-xs text-muted-foreground">{label}</div>
                                    <div className="text-xl font-semibold">
                                        <AnimatedNumber value={metric.formatted.value} />
                                        <span className="ml-0.5 text-sm text-muted-foreground">{metric.formatted.unit}</span>
                                    </div>
                                </div>
                            </Fragment>
                        ))}
                    </div>
                    <div
                        className="flex gap-2 text-sm cursor-pointer hover:opacity-80 transition-opacity"
                        onClick={() => setPeriod(period === '1' ? '7' : period === '7' ? '30' : '1')}
                    >
                        <div>
                            <div className="text-xs text-muted-foreground">{t('timePeriod')}</div>
                            <div className="text-base font-semibold">{periodLabel}</div>
                        </div>
                    </div>
                </div>
            </div>

            {showEmpty ? (
                <div className="h-40 flex flex-col items-center justify-center text-muted-foreground">
                    <ChartLine className="w-10 h-10 mb-2 opacity-30" />
                    <p className="text-sm">{t('empty')}</p>
                </div>
            ) : (
                <ChartContainer config={{ value: { label: tMetric(metricType) } }} className="h-40 w-full">
                    <AreaChart accessibilityLayer data={chartData}>
                        <defs>
                            {/* 停靠点取实际色值而非 var(): snapdom 不解析 defs 内的 var(), 截图会落回黑色。 */}
                            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                                <stop offset="5%" stopColor={chartColor} stopOpacity={1.0} />
                                <stop offset="95%" stopColor={chartColor} stopOpacity={0.1} />
                            </linearGradient>
                        </defs>
                        <CartesianGrid strokeDasharray="3 3" vertical={false} />
                        <XAxis dataKey="date" tickLine={false} axisLine={false} />
                        <YAxis
                            tickLine={false}
                            axisLine={false}
                            tickFormatter={(value) => {
                                const formatted = metricType === 'cost' ? formatMoney(value) : formatCount(value);
                                return `${formatted.formatted.value}${formatted.formatted.unit}`;
                            }}
                        />
                        <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="line" />} />
                        <Area type="monotone" dataKey="value" stroke={chartColor} fill={`url(#${gradientId})`} />
                    </AreaChart>
                </ChartContainer>
            )}
        </div>
    );
}
