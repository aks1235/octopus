import {
    Activity,
    MessageSquare,
    TrendingUp,
    Bot,
    Database,
    DollarSign,
    Coins,
    CalendarDays,
    Wallet,
    type LucideIcon,
} from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useStatsDaily, useStatsTotal } from '@/api/stats';
import { todayDateStr } from '@/api/queries';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';

// 卡片里的一个指标: metric 缺省表示数据还没到, 显示为 0。
interface CardMetric {
    label: string;
    metric?: { formatted: { value: string; unit: string } };
    icon: LucideIcon;
    bgColor: string;
    digits?: number; // 小数位; 百分比给 1 位, 单价给 2 位, 计数用整数。
}

// 副行: 一组「名称 数值」, 用中点连接, 给主指标补充同一件事的另一个侧面。
interface CardHint {
    label: string;
    value: string;
}

const EMPTY_METRIC = { formatted: { value: '0', unit: '' } };

// percent 计算占比; 分母为 0 时给 0.0, 避免出现 NaN。
function percent(part: number, whole: number): string {
    return whole > 0 ? ((part / whole) * 100).toFixed(1) : '0.0';
}

// compact 让数值走与统计接口一致的量级缩写(K/M/B)。
function compact(value: number): string {
    const units: [number, string][] = [[1e9, 'B'], [1e6, 'M'], [1e3, 'K']];
    for (const [scale, unit] of units) {
        if (value >= scale) return `${(value / scale).toFixed(2)}${unit}`;
    }
    return value >= 1 ? value.toFixed(0) : value.toFixed(2);
}

// pair 拼出 "数值+单位" 的文本, 供副行使用。
function pair(metric?: { formatted: { value: string; unit: string } }): string {
    return `${metric?.formatted.value ?? '0'}${metric?.formatted.unit ?? ''}`;
}

// MetricValue 数字走滚动动画; 非数字(如空值占位)直接显示文本, 否则动画组件会把 NaN 吞掉。
// 单位紧跟数字不留缝: 成功率写成 95.9%, 而不是 95.9 %。
function MetricValue({ value, unit, digits }: { value: string; unit: string; digits?: number }) {
    const isNumeric = value !== '' && Number.isFinite(Number(value));

    return (
        <>
            <span className="text-xl">
                {isNumeric ? <AnimatedNumber value={value} digits={digits} /> : value}
            </span>
            {unit && <span className="text-sm text-muted-foreground">{unit}</span>}
        </>
    );
}

// Total 展示四组指标: 请求(量+成功率), 词元(量+缓存命中), 成本(总额+单价), 今日(时效视角)。
// 每个主指标配一行副行: 单看主数字判断不了好坏, 副行给的是同一件事的另一个侧面。
export function Total() {
    const { data: stats } = useStatsTotal();
    const { data: daily } = useStatsDaily();
    const t = useTranslations('home.total');
    const today = daily?.find((item) => item.date === todayDateStr());

    const successRate = stats ? percent(stats.request_success.raw, stats.request_count.raw) : '0.0';
    const cacheHitRate = stats ? percent(stats.cache_read_token.raw, stats.input_token.raw) : '0.0';
    // 每百万词元单价: 总花费 ÷ 总词元 × 1M; 保留两位小数, 便宜的模型也能看出差异。
    const perMillion = stats && stats.total_token.raw > 0
        ? (stats.total_cost.raw / stats.total_token.raw * 1e6).toFixed(2)
        : '0.00';
    const todaySuccessRate = today ? percent(today.request_success.raw, today.request_count.raw) : '0.0';

    const cards: { title: string; headerIcon: LucideIcon; items: CardMetric[]; hint: CardHint[] }[] = [
        {
            title: t('requestStats'),
            headerIcon: Activity,
            items: [
                { label: t('requestCount'), metric: stats?.request_count ?? EMPTY_METRIC, icon: MessageSquare, bgColor: 'bg-primary/10' },
                { label: t('successRate'), metric: { formatted: { value: successRate, unit: '%' } }, icon: TrendingUp, bgColor: 'bg-accent/10', digits: 1 },
            ],
            hint: [
                { label: t('success'), value: compact(stats?.request_success.raw ?? 0) },
                { label: t('failed'), value: compact(stats?.request_failed.raw ?? 0) },
            ],
        },
        {
            title: t('totalStats'),
            headerIcon: Bot,
            items: [
                { label: t('totalToken'), metric: stats?.total_token ?? EMPTY_METRIC, icon: Bot, bgColor: 'bg-chart-1/10' },
                { label: t('cacheHitRate'), metric: { formatted: { value: cacheHitRate, unit: '%' } }, icon: Database, bgColor: 'bg-chart-2/10', digits: 1 },
            ],
            hint: [
                { label: t('cacheRead'), value: compact(stats?.cache_read_token.raw ?? 0) },
                { label: t('newInput'), value: compact(stats?.new_input_token.raw ?? 0) },
            ],
        },
        {
            title: t('costStats'),
            headerIcon: DollarSign,
            items: [
                { label: t('totalCost'), metric: stats?.total_cost ?? EMPTY_METRIC, icon: DollarSign, bgColor: 'bg-chart-3/10' },
                { label: t('perMillionTokens'), metric: { formatted: { value: perMillion, unit: '$' } }, icon: Coins, bgColor: 'bg-chart-4/10', digits: 2 },
            ],
            hint: [
                { label: t('inputCost'), value: pair(stats?.input_cost) },
                { label: t('outputCost'), value: pair(stats?.output_cost) },
            ],
        },
        {
            title: t('todayStats'),
            headerIcon: CalendarDays,
            items: [
                // 今天的次数通常很小, 走原始整数, 避免出现 "2.00"。
                { label: t('todayRequests'), metric: { formatted: { value: String(today?.request_count.raw ?? 0), unit: '' } }, icon: MessageSquare, bgColor: 'bg-chart-5/10', digits: 0 },
                { label: t('todayCost'), metric: today?.total_cost ?? EMPTY_METRIC, icon: Wallet, bgColor: 'bg-chart-5/10', digits: 2 },
            ],
            hint: [
                { label: t('totalToken'), value: today ? compact(today.total_token.raw) : '0' },
                { label: t('successRate'), value: `${todaySuccessRate}%` },
            ],
        },
    ];

    return (
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
                                    <div className="flex items-baseline">
                                        <MetricValue
                                            value={item.metric?.formatted.value ?? '0'}
                                            unit={item.metric?.formatted.unit ?? ''}
                                            digits={item.digits}
                                        />
                                    </div>
                                </div>
                            </div>
                        ))}

                        {/* 副行: 同一件事的另一个侧面(成功/失败、命中/新增、输入/输出构成)。
                            竖排而不是用中点连成一行: 卡片窄, 横排换行后分隔符会掉到行首。 */}
                        <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                            {card.hint.map((hint) => (
                                <span key={hint.label} className="flex items-center justify-between gap-2">
                                    <span className="truncate">{hint.label}</span>
                                    <span className="shrink-0 text-foreground tabular-nums">{hint.value}</span>
                                </span>
                            ))}
                        </div>
                    </div>
                </section>
            ))}
        </div>
    );
}
