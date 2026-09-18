import { TrendingUp } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useChannelStats } from '@/api/channel';
import { useStatsRankDaily } from '@/api/stats';
import { useHomeViewStore, type MetricKey, type RankViewMode } from './store';
import { MetricTabs } from './metric-tabs';
import type { StatsMetricsFormatted } from '@/api/stats';

// 榜单只展示这几项, 渠道可直接复用 api/channel 已算好的 formatted。
type RankMetrics = Pick<
    StatsMetricsFormatted,
    'total_cost' | 'total_token' | 'request_count' | 'request_success' | 'request_failed'
>;

// 榜单中的一个条目, 渠道和模型共用。
interface RankItem {
    id: string;
    name: string; // 渠道榜为渠道名, 模型榜为模型名。
    channelName?: string; // 仅模型榜有值; 有值则 name 是模型名, 模糊渠道名时只糊此项。
    formatted: RankMetrics;
}

// RankCard 渲染单个排行榜: 标题, 维度切换和榜单列表; hint 非空且无数据时替代「暂无数据」。
function RankCard({
    title,
    items,
    sortMode,
    onSortModeChange,
    hideChannelName,
    hint,
}: {
    title: string;
    items: RankItem[];
    sortMode: MetricKey;
    onSortModeChange: (value: MetricKey) => void;
    hideChannelName?: boolean;
    hint?: string;
}) {
    const t = useTranslations('home.rank');
    const sortField = sortMode === 'cost' ? 'total_cost' : sortMode === 'count' ? 'request_count' : 'total_token';
    const ranked = [...items].sort((a, b) => b.formatted[sortField].raw - a.formatted[sortField].raw);

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <div className="flex items-center justify-between">
                <h3 className="font-semibold text-base">{title}</h3>
                <MetricTabs value={sortMode} onChange={onSortModeChange} />
            </div>

            {ranked.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{hint ?? t('noData')}</p>
                </div>
            ) : (
                <div className="space-y-3 max-h-[300px] overflow-y-auto">
                    {ranked.map((item, index) => {
                        const successCount = item.formatted.request_success.raw;
                        const totalCount = successCount + item.formatted.request_failed.raw;

                        return (
                            <div key={item.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3">
                                <div className="flex items-center justify-center font-bold text-lg">{index + 1}</div>

                                <div className="min-w-0">
                                    <p className={`font-medium text-sm truncate ${hideChannelName && !item.channelName ? 'select-none blur-[3px]' : ''}`}>
                                        {item.name}
                                    </p>
                                    {item.channelName && (
                                        <p className={`mt-1 truncate text-xs text-muted-foreground ${hideChannelName ? 'select-none blur-[3px]' : ''}`}>
                                            {item.channelName}
                                        </p>
                                    )}
                                    {sortMode === 'count' && (
                                        <div className="flex items-center gap-1 text-xs text-muted-foreground mt-1">
                                            <span>{t('successRate')}:</span>
                                            <span>{(totalCount > 0 ? (successCount / totalCount) * 100 : 0).toFixed(1)}%</span>
                                        </div>
                                    )}
                                </div>

                                <div className="flex items-center gap-1 text-right">
                                    {sortMode === 'count' ? (
                                        <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
                                            <span className="text-accent">
                                                {item.formatted.request_success.formatted.value}
                                                <span className="text-xs text-muted-foreground">
                                                    {item.formatted.request_success.formatted.unit}
                                                </span>
                                            </span>
                                            <span className="text-muted-foreground/40 font-light">/</span>
                                            <span className="text-destructive">
                                                {item.formatted.request_failed.formatted.value}
                                                <span className="text-xs text-muted-foreground">
                                                    {item.formatted.request_failed.formatted.unit}
                                                </span>
                                            </span>
                                        </div>
                                    ) : (
                                        <span className="font-semibold text-base">
                                            {item.formatted[sortField].formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted[sortField].formatted.unit}
                                            </span>
                                        </span>
                                    )}
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}

// Rank 并列渠道榜和模型榜, 两榜各自独立排序; 时间视角(按天/累计)整区共用一个开关。
export function Rank() {
    const t = useTranslations('home.rank');
    const selectedDate = useHomeViewStore((state) => state.selectedDate);
    const rankViewMode = useHomeViewStore((state) => state.rankViewMode);
    const setRankViewMode = useHomeViewStore((state) => state.setRankViewMode);

    // 按天走 relay_logs 聚合端点; 累计走渠道统计接口。两份数据互斥拉取, 不看的那个不请求。
    const { data: dailyRank } = useStatsRankDaily(selectedDate, rankViewMode === 'daily');
    const { data: channelStats } = useChannelStats(rankViewMode === 'total');

    const channelSortMode = useHomeViewStore((state) => state.channelRankSortMode);
    const setChannelSortMode = useHomeViewStore((state) => state.setChannelRankSortMode);
    const modelSortMode = useHomeViewStore((state) => state.modelRankSortMode);
    const setModelSortMode = useHomeViewStore((state) => state.setModelRankSortMode);
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);

    // 按天数据超出日志保留期时给明确提示; 空列表(当天无流量)仍用常规「暂无数据」。
    const emptyHint = rankViewMode === 'daily' && dailyRank && !dailyRank.available
        ? t('beyondRetention')
        : undefined;
    // 模糊渠道名只作用于渠道榜与累计模型榜的渠道名; 按天模型条目不携带渠道名, 模型名本身不糊。
    const modelHideChannelName = isChannelNameHidden && rankViewMode === 'total';

    let channelItems: RankItem[];
    let modelItems: RankItem[];
    if (rankViewMode === 'daily') {
        channelItems = (dailyRank?.channels ?? []).map((entry) => ({
            id: `channel-${entry.channel_id}`,
            name: entry.name,
            formatted: entry,
        }));
        modelItems = (dailyRank?.models ?? []).map((entry) => ({
            id: `model-${entry.name}`,
            name: entry.name,
            formatted: entry,
        }));
    } else {
        channelItems = (channelStats ?? []).map((channel) => ({
            id: `channel-${channel.channel_id}`,
            name: channel.channel_name,
            formatted: channel.formatted,
        }));
        modelItems = (channelStats ?? []).flatMap((channel) =>
            channel.models.map((channelModel) => ({
                id: `model-${channelModel.model_id}`,
                name: channelModel.model_name,
                channelName: channel.channel_name,
                formatted: channelModel.formatted,
            }))
        );
    }

    return (
        <div className="space-y-4">
            <div className="flex items-center justify-end">
                <Tabs value={rankViewMode} onValueChange={(next) => setRankViewMode(next as RankViewMode)}>
                    <TabsList>
                        <TabsTrigger value="daily">{t('daily')}</TabsTrigger>
                        <TabsTrigger value="total">{t('total')}</TabsTrigger>
                    </TabsList>
                </Tabs>
            </div>
            <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
                <RankCard
                    title={t('channel')}
                    items={channelItems}
                    sortMode={channelSortMode}
                    onSortModeChange={setChannelSortMode}
                    hideChannelName={isChannelNameHidden}
                    hint={emptyHint}
                />
                <RankCard
                    title={t('model')}
                    items={modelItems}
                    sortMode={modelSortMode}
                    onSortModeChange={setModelSortMode}
                    hideChannelName={modelHideChannelName}
                    hint={emptyHint}
                />
            </div>
        </div>
    );
}
