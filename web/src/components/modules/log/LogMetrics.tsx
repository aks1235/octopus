import type { CSSProperties } from 'react';
import { ArrowDownToLine, ArrowUpFromLine, Clock, Cpu, Database, DollarSign, Gauge, KeyRound, Timer } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { cn, formatRate, outputSpeed } from '@/lib/utils';

export type LogMetricsInput = {
    started_at: string;
    status: string;
    duration: number;
    api_key_name?: string;
    usage: {
        prompt_tokens: number;
        completion_tokens: number;
        prompt_tokens_details?: { cached_tokens: number; write_cached_tokens?: number } | null;
    };
    cost: number;
    phase?: string;
    phase_speed?: number;
    first_token_ms?: number;
};

function formatTime(value: string, showDate: boolean) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return '--';
    return date.toLocaleString(undefined, {
        ...(showDate ? { month: '2-digit', day: '2-digit' } as const : {}),
        hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    });
}

function formatMilliseconds(value: number) {
    const milliseconds = Math.max(0, value);
    if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`;
    return `${(milliseconds / 1000).toFixed(2)}s`;
}

export function LogMetrics({ log, now, brandColor, variant, showDate = false }: {
    log: LogMetricsInput;
    now: number;
    brandColor: string;
    variant: 'card' | 'footer';
    showDate?: boolean;
}) {
    const t = useTranslations('log.card');
    const cachedTokens = log.usage.prompt_tokens_details?.cached_tokens ?? 0;
    const cacheRate = log.usage.prompt_tokens > 0 ? Math.round((cachedTokens / log.usage.prompt_tokens) * 100) : null;
    const requestActive = log.status === 'running' || log.status === 'committed';
    const duration = requestActive
        ? formatMilliseconds(now - new Date(log.started_at).getTime())
        : formatMilliseconds(log.duration / 1_000_000);
    const liveSpeed = log.phase_speed ?? 0;
    const liveSpeedText = log.phase === 'thinking'
        ? (liveSpeed > 0 ? t('speedThinking', { value: liveSpeed.toLocaleString() }) : '-')
        : log.phase === 'answering'
            ? (liveSpeed > 0 ? t('speedAnswering', { value: liveSpeed.toLocaleString() }) : '-')
            : '-';
    const finalSpeed = outputSpeed(log.usage.completion_tokens, log.duration / 1_000_000, log.first_token_ms ?? 0);
    const speedText = requestActive
        ? liveSpeedText
        : (finalSpeed !== null ? `${formatRate(finalSpeed).formatted.value} ${formatRate(finalSpeed).formatted.unit}` : '-');
    const firstTokenText = (log.first_token_ms ?? 0) > 0 ? formatMilliseconds(log.first_token_ms ?? 0) : '-';
    const metrics = [
        { key: 'time', Icon: Clock, iconClassName: 'size-3.5 shrink-0', iconStyle: { color: brandColor } as CSSProperties, value: formatTime(log.started_at, showDate), valueClassName: 'tabular-nums', cellClassName: 'whitespace-nowrap', title: t('tipTime') },
        { key: 'apiKey', Icon: KeyRound, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: log.api_key_name || '-', valueClassName: 'truncate', title: log.api_key_name ? `${t('tipApiKey')}: ${log.api_key_name}` : t('tipApiKey') },
        { key: 'duration', Icon: Cpu, iconClassName: 'size-3.5 shrink-0 text-blue-500', value: duration, title: t('tipDuration') },
        { key: 'cost', Icon: DollarSign, iconClassName: 'size-3.5 shrink-0 text-emerald-500', value: log.cost.toFixed(6), valueClassName: 'font-medium text-emerald-600 dark:text-emerald-400', title: t('tipCost') },
        { key: 'prompt', Icon: ArrowDownToLine, iconClassName: 'size-3.5 shrink-0 text-green-500', value: (log.usage.prompt_tokens - cachedTokens).toLocaleString(), title: t('tipPrompt') },
        { key: 'cached', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-cyan-500', value: cacheRate === null ? cachedTokens.toLocaleString() : `${cachedTokens.toLocaleString()} (${cacheRate}%)`, title: t('tipCached') },
        { key: 'completion', Icon: ArrowUpFromLine, iconClassName: 'size-3.5 shrink-0 text-purple-500', value: log.usage.completion_tokens.toLocaleString(), title: t('tipCompletion') },
        { key: 'cacheWrite', Icon: Database, iconClassName: 'size-3.5 shrink-0 text-orange-500', value: (log.usage.prompt_tokens_details?.write_cached_tokens ?? 0).toLocaleString(), title: t('tipCacheWrite') },
        { key: 'speed', Icon: Gauge, iconClassName: 'size-3.5 shrink-0 text-sky-500', value: speedText, title: t('tipSpeed') },
        { key: 'firstToken', Icon: Timer, iconClassName: 'size-3.5 shrink-0 text-amber-500', value: firstTokenText, valueClassName: 'tabular-nums', cellClassName: 'whitespace-nowrap', title: t('tipFirstToken') },
    ];

    return metrics.map((metric) => (
        <div key={metric.key} title={metric.title} className={cn('flex min-w-0 items-center gap-1.5', variant === 'card' && metric.cellClassName)}>
            <metric.Icon className={metric.iconClassName} style={metric.iconStyle} />
            <span className={metric.valueClassName}>{metric.value}</span>
        </div>
    ));
}
