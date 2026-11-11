import { Fragment, useMemo } from 'react';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';
import { ChartLine } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { todayDateStr } from '@/api/queries';
import { useStatsDaily, useStatsHourly, type StatsMetricsFormatted } from '@/api/stats';
import { ChartContainer, ChartTooltip } from '@/components/ui/chart';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount, formatMoney } from '@/lib/utils';
import { useTheme } from '@/provider/theme';
import { useHomeViewStore } from './store';
import { MetricTabs } from './metric-tabs';

// 趋势图上的一个点。小时与每日两种粒度口径一致, 只是横轴刻度不同。
interface ChartPoint {
    label: string; // 横轴刻度, 小时粒度为 H:00, 每日粒度为 MM/DD。
    stat: StatsMetricsFormatted;
}

// 柱子的取值维度。字段名固定为这四个, 由指标类型决定填入哪一套口径。
type SeriesKey = 'input' | 'output' | 'success' | 'failed';

// 图表数据点: 柱子按指标填充, calls 恒为请求次数, 供右轴折线使用。
interface TrendDatum {
    date: string;
    input: number;
    output: number;
    success: number;
    failed: number;
    calls: number;
}

// 一根柱子/一段堆叠的展示信息。
interface TrendSeries {
    key: SeriesKey;
    label: string;
    color: string; // 已解析的实际色值: snapdom 截图不解析 CSS 变量, 需在渲染期取实值。
}

// cssVar 读取 :root 上的主题色, 图表用实值而非 var()。
function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// formatMetric 按指标口径格式化: 金额走 formatMoney, 词元与次数走 formatCount。
function formatMetric(value: number, isCost: boolean) {
    return (isCost ? formatMoney(value) : formatCount(value)).formatted;
}

// TrendTooltip 悬浮卡: 柱子各段数值 + 合计, 有折线时再补一行请求次数。
function TrendTooltip({
    active,
    payload,
    series,
    showCalls,
    isCost,
    callsLabel,
    totalLabel,
}: {
    active?: boolean;
    payload?: Array<{ payload?: TrendDatum }>;
    series: TrendSeries[];
    showCalls: boolean;
    isCost: boolean;
    callsLabel: string;
    totalLabel: string;
}) {
    const datum = payload?.[0]?.payload;
    if (!active || !datum) return null;

    const total = series.reduce((sum, item) => sum + datum[item.key], 0);
    const totalMetric = formatMetric(total, isCost);

    return (
        <div className="grid min-w-[12rem] gap-2 rounded-lg border border-border/50 bg-background px-3 py-2.5 text-xs shadow-xl">
            <div className="font-medium text-foreground">{datum.date}</div>
            <div className="grid gap-1.5">
                {series.map((item) => {
                    const metric = formatMetric(datum[item.key], isCost);

                    return (
                        <div key={item.key} className="flex items-center gap-2">
                            <span
                                className="size-2.5 shrink-0 rounded-[2px]"
                                style={{ backgroundColor: item.color }}
                                aria-hidden="true"
                            />
                            <span className="flex-1 text-muted-foreground">{item.label}</span>
                            <span className="font-mono font-medium tabular-nums text-foreground">
                                {metric.value}{metric.unit}
                            </span>
                        </div>
                    );
                })}
            </div>
            <div className="flex items-center justify-between border-t border-border/60 pt-1.5">
                <span className="text-muted-foreground">{totalLabel}</span>
                <span className="font-mono font-semibold tabular-nums text-foreground">
                    {totalMetric.value}{totalMetric.unit}
                </span>
            </div>
            {showCalls && (
                <div className="flex items-center gap-2 border-t border-border/60 pt-1.5">
                    <span className="h-0 w-3 shrink-0 border-t-2 border-dashed border-foreground/60" aria-hidden="true" />
                    <span className="flex-1 text-muted-foreground">{callsLabel}</span>
                    <span className="font-mono font-medium tabular-nums text-foreground">
                        {datum.calls.toLocaleString()}
                    </span>
                </div>
            )}
        </div>
    );
}

// StatsChart 展示选定指标的构成与调用次数, 并汇总该周期的请求, 金额和词元。
// 柱子是构成(词元/金额的输入输出, 或次数维度的成功失败), 折线是请求次数(右轴)。
export function StatsChart() {
    const { data: statsDaily } = useStatsDaily();
    const selectedDate = useHomeViewStore((state) => state.selectedDate);
    const { data: statsHourly } = useStatsHourly(selectedDate);
    const t = useTranslations('home.chart');
    // 订阅主题: 切换时 :root 上 --chart-* 的取值会变, 需重渲染以重新取色。
    useTheme();

    const metricType = useHomeViewStore((state) => state.chartMetricType);
    const setMetricType = useHomeViewStore((state) => state.setChartMetricType);
    const period = useHomeViewStore((state) => state.chartPeriod);
    const setPeriod = useHomeViewStore((state) => state.setChartPeriod);
    const isToday = selectedDate === todayDateStr();
    const isCost = metricType === 'cost';

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
    const visible = source.slice(firstUsage === -1 ? Math.max(source.length - 1, 0) : Math.max(firstUsage - 1, 0));

    // 次数维度下柱子换成成功/失败构成, 折线(总次数)与柱高重复, 故不再画。
    const showCalls = metricType !== 'count';
    const series = useMemo<TrendSeries[]>(() => {
        if (metricType === 'count') {
            return [
                { key: 'success', label: t('success'), color: cssVar('--primary') },
                { key: 'failed', label: t('failed'), color: cssVar('--destructive') },
            ];
        }
        const labels = isCost
            ? { input: t('inputCost'), output: t('outputCost') }
            : { input: t('inputToken'), output: t('outputToken') };

        // 同一色相的两个明度档: 相邻堆叠段靠明度区分, 比换色相更容易读成"同一根柱子"。
        return [
            { key: 'input', label: labels.input, color: cssVar('--chart-1') },
            { key: 'output', label: labels.output, color: cssVar('--chart-3') },
        ];
    }, [metricType, isCost, t]);

    const chartData: TrendDatum[] = visible.map((item) => ({
        date: item.label,
        input: (isCost ? item.stat.input_cost : item.stat.input_token).raw,
        output: (isCost ? item.stat.output_cost : item.stat.output_token).raw,
        success: item.stat.request_success.raw,
        failed: item.stat.request_failed.raw,
        calls: item.stat.request_count.raw,
    }));

    // 单日周期下展示实际查看的日期, 今天才叫「今天」; 多日周期只与跨度有关, 不随选中日变。
    const periodLabel = period === '1' && !isToday
        ? `${selectedDate.slice(0, 4)}-${selectedDate.slice(4, 6)}-${selectedDate.slice(6, 8)}`
        : { '1': t('period.today'), '7': t('period.last7Days'), '30': t('period.last30Days') }[period];
    // 历史日整段无请求时给空态, 避免一排贴地的零值柱被误读成"当天全天空转"。
    const hasData = source.some((item) => item.stat.request_count.raw > 0);
    const showEmpty = period === '1' && !isToday && !hasData;
    const summary = [
        { label: t('totalRequests'), metric: formatCount(source.reduce((sum, item) => sum + item.stat.request_count.raw, 0)) },
        { label: t('totalCost'), metric: formatMoney(source.reduce((sum, item) => sum + item.stat.total_cost.raw, 0)) },
        { label: t('totalTokens'), metric: formatCount(source.reduce((sum, item) => sum + item.stat.total_token.raw, 0)) },
    ];
    const callsColor = cssVar('--foreground');

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
                <>
                    {/* 图例: 堆叠段与折线各一条, 让"柱子=构成, 折线=次数"不必看轴也能读出来。 */}
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 pb-1 text-xs text-muted-foreground">
                        {series.map((item) => (
                            <span key={item.key} className="inline-flex items-center gap-1.5">
                                <span
                                    className="size-2.5 rounded-[2px]"
                                    style={{ backgroundColor: item.color }}
                                    aria-hidden="true"
                                />
                                {item.label}
                            </span>
                        ))}
                        {showCalls && (
                            <span className="inline-flex items-center gap-1.5">
                                <span
                                    className="h-0 w-4 border-t-2 border-dashed opacity-60"
                                    style={{ borderColor: callsColor }}
                                    aria-hidden="true"
                                />
                                {t('totalRequests')}
                            </span>
                        )}
                    </div>

                    <ChartContainer
                        config={Object.fromEntries(series.map((item) => [item.key, { label: item.label, color: item.color }]))}
                        className="h-60 w-full"
                    >
                        <ComposedChart data={chartData} margin={{ top: 8, right: 4, left: 0, bottom: 0 }} barCategoryGap="18%">
                            <CartesianGrid strokeDasharray="3 3" vertical={false} />
                            {/* 点位少时(今天 24 格 / 7 天)全部标出, 30 天交给 recharts 抽稀, 否则日期会缺格。 */}
                            <XAxis
                                dataKey="date"
                                tickLine={false}
                                axisLine={false}
                                minTickGap={8}
                                interval={chartData.length <= 12 ? 0 : 'preserveStartEnd'}
                            />
                            <YAxis
                                yAxisId="value"
                                tickLine={false}
                                axisLine={false}
                                width={58}
                                tickFormatter={(value: number) => {
                                    const formatted = formatMetric(value, isCost);
                                    return `${formatted.value}${formatted.unit}`;
                                }}
                            />
                            {showCalls && (
                                <YAxis
                                    yAxisId="calls"
                                    orientation="right"
                                    tickLine={false}
                                    axisLine={false}
                                    width={40}
                                    allowDecimals={false}
                                    tickFormatter={(value: number) => String(Math.round(value))}
                                />
                            )}
                            <ChartTooltip
                                cursor={{ fill: 'var(--muted)', opacity: 0.5 }}
                                content={
                                    <TrendTooltip
                                        series={series}
                                        showCalls={showCalls}
                                        isCost={isCost}
                                        callsLabel={t('totalRequests')}
                                        totalLabel={t('total')}
                                    />
                                }
                            />
                            {series.map((item) => (
                                <Bar
                                    key={item.key}
                                    yAxisId="value"
                                    dataKey={item.key}
                                    stackId="composition"
                                    fill={item.color}
                                    // 段间用卡片底色描边, 堆叠层次不糊成一块。
                                    stroke="var(--card)"
                                    strokeWidth={1}
                                    maxBarSize={36}
                                />
                            ))}
                            {showCalls && (
                                <Line
                                    yAxisId="calls"
                                    type="monotone"
                                    dataKey="calls"
                                    stroke={callsColor}
                                    strokeOpacity={0.6}
                                    strokeWidth={1.5}
                                    strokeDasharray="6 4"
                                    dot={false}
                                    activeDot={{ r: 3, strokeWidth: 0 }}
                                />
                            )}
                        </ComposedChart>
                    </ChartContainer>
                </>
            )}
        </div>
    );
}
