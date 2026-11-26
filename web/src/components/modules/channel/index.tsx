import { useMemo } from 'react';
import {
    ArrowDownAZ, ArrowUpAZ, ArrowDownWideNarrow, ArrowUpNarrowWide,
    ClockArrowDown, ClockArrowUp, type LucideIcon,
} from 'lucide-react';
import { useTranslations } from 'use-intl';
import { type ChannelStatsFormatted, useChannelStats } from '@/api/channel';
import { PageActions, usePageActionsStore, type PageActionSortOrder } from '@/components/common/PageActions';
import { MorphingDialogDescription } from '@/components/ui/morphing-dialog';
import { Card } from './Card';
import { ChannelForm } from './Form';
import { ChannelCallDetail } from './CallDetail';
import { useChannelViewStore } from './detail-store';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// normalizeChannelSort 把持久化的排序值归一到已知集合: 旧数据只存过 asc/desc, 未知值回落名称升序。
function normalizeChannelSort(order: string | undefined) {
    return order === 'desc' ||
        order === 'timeAsc' ||
        order === 'timeDesc' ||
        order === 'rateDesc' ||
        order === 'rateAsc' ||
        order === 'requestDesc' ||
        order === 'requestAsc'
        ? order
        : 'asc';
}

// SortGroup 把 8 个排序值归成 4 类: 点一次进该类默认方向, 再点一次切反向。
// 默认方向按"这类通常想先看什么"定 —— 成功率低在前(排障), 请求次数多在前(看主力),
// 名称与时间维持正序。8 个值一个不删, 旧 localStorage 记录照常生效。
type SortGroup = {
    base: PageActionSortOrder; // 该类默认方向的排序值。
    reverse: PageActionSortOrder;
    labelKey: string;
    icon: LucideIcon; // 默认方向的类别图标。
    reverseIcon: LucideIcon; // 反方向的类别图标, 做 3D 翻牌切换用。
};

const SORT_GROUPS: SortGroup[] = [
    { base: 'asc', reverse: 'desc', labelKey: 'sortName', icon: ArrowUpAZ, reverseIcon: ArrowDownAZ },
    { base: 'timeAsc', reverse: 'timeDesc', labelKey: 'sortTime', icon: ClockArrowUp, reverseIcon: ClockArrowDown },
    { base: 'rateAsc', reverse: 'rateDesc', labelKey: 'sortRate', icon: ArrowUpNarrowWide, reverseIcon: ArrowDownWideNarrow },
    { base: 'requestDesc', reverse: 'requestAsc', labelKey: 'sortRequest', icon: ArrowDownWideNarrow, reverseIcon: ArrowUpNarrowWide },
];

// successRate 取渠道成功率(0-1); 一次都没调用过时返回 null —— 这类渠道不该混进成功率排序里冒充 0%。
function successRate(channel: ChannelStatsFormatted): number | null {
    const total = channel.formatted.request_count.raw;
    return total > 0 ? channel.formatted.request_success.raw / total : null;
}

// ChannelActions 向稳定顶栏提供渠道页面的搜索、视图选项和创建入口。
export function ChannelActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.channel || '');
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const sortOrder = usePageActionsStore((state) => normalizeChannelSort(state.sortOrders.channel));
    const filter = usePageActionsStore((state) => state.channelFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setLayout = usePageActionsStore((state) => state.setLayout);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setChannelFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('channel', value)}
            layout={layout}
            onLayoutChange={(value) => setLayout('channel', value)}
            sortOptions={SORT_GROUPS.map((group) => {
                // 当前那一类交出实际方向值, 高亮自然落在它上面; 其他类交默认值, 点过去就是默认方向。
                // 这样不必改通用 PageActions 的匹配逻辑。
                const isCurrent = group.base === sortOrder || group.reverse === sortOrder;
                const value = isCurrent ? sortOrder : group.base;
                return {
                    // key 固定为类别名, 不随方向变 —— 否则切方向时按钮被重建, 图标过渡不会触发。
                    key: group.labelKey,
                    value,
                    label: t(`popover.${group.labelKey}`),
                    icon: group.icon,
                    iconAlt: group.reverseIcon,
                    // 以该类自身的 base/reverse 判断, 不用全局升降序:
                    // "请求次数"默认是降序(多在前), 用全局判断会把图标装反。
                    iconReversed: value === group.reverse,
                };
            })}
            sortValue={sortOrder}
            onSortChange={(value) => {
                const clicked = value as PageActionSortOrder;
                const group = SORT_GROUPS.find((item) => item.base === clicked || item.reverse === clicked);
                if (!group) return;
                // 再点一次当前高亮的按钮 → 同类切反向; 点别的类 → 用那一类的默认方向。
                const next = clicked === sortOrder
                    ? (clicked === group.base ? group.reverse : group.base)
                    : clicked;
                setSort('channel', next);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.channel.all') },
                { value: 'enabled', label: t('popover.filter.channel.enabled') },
                { value: 'disabled', label: t('popover.filter.channel.disabled') },
                { value: 'official', label: t('popover.filter.channel.official') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'enabled' || value === 'disabled' || value === 'official') setFilter(value);
            }}
        >
            <div className="w-screen max-w-full md:max-w-3xl flex flex-col">
                <MorphingDialogDescription disableLayoutAnimation>
                    <ChannelForm />
                </MorphingDialogDescription>
            </div>
        </PageActions>
    );
}

// Channel 渲染渠道列表正文; 调用详情视图(detail)时整页替换, 模块内自路由不动主导航。
export function Channel() {
    const view = useChannelViewStore((s) => s.view);
    const { data: statsData } = useChannelStats();
    const searchTerm = usePageActionsStore((state) => state.searchTerms.channel || '');
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const sortOrder = usePageActionsStore((state) => normalizeChannelSort(state.sortOrders.channel));
    const filter = usePageActionsStore((state) => state.channelFilter);

    // 先按搜索词和启用状态过滤, 再按名称或添加时间排序; 添加时间以自增主键近似(迁移保留 v1 主键, 历史顺序正确)。
    const visibleChannels = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const matched = (statsData ?? []).filter((channel) => {
            if (term && !channel.channel_name.toLowerCase().includes(term)) return false;
            if (filter === 'enabled') return channel.enabled;
            if (filter === 'disabled') return !channel.enabled;
            // 官方标记随统计一并下发, 无需再取整份配置。
            if (filter === 'official') return channel.official;
            return true;
        });

        // 时间分支先于名称分支判断, 名称两向收在 default 与 desc。
        return matched.sort((a, b) => {
            switch (sortOrder) {
                case 'timeAsc':
                    return a.channel_id - b.channel_id;
                case 'timeDesc':
                    return b.channel_id - a.channel_id;
                // 成功率/请求次数: 哨兵取值让"没有调用记录"的渠道在两种方向下都沉底,
                // 它们不该混进 0% 那一档冒充"最差渠道"。(成功率取值区间是 0-1, 故用 -1 与 2)
                case 'rateDesc':
                    return (successRate(b) ?? -1) - (successRate(a) ?? -1);
                case 'rateAsc':
                    return (successRate(a) ?? 2) - (successRate(b) ?? 2);
                case 'requestDesc':
                    return b.formatted.request_count.raw - a.formatted.request_count.raw;
                case 'requestAsc':
                    return a.formatted.request_count.raw - b.formatted.request_count.raw;
                case 'desc':
                    return b.channel_name.localeCompare(a.channel_name);
                default:
                    return a.channel_name.localeCompare(b.channel_name);
            }
        });
    }, [statsData, searchTerm, filter, sortOrder]);

    return (
        view.mode === 'detail' ? (
            <ChannelCallDetail channelID={view.channelID} channelName={view.channelName} />
        ) : (
        <VirtualizedGrid
            items={visibleChannels}
            layout={layout}
            // 列数与分组页取同一档(1440 宽下 3 列): 渠道名普遍比分组名长,
            // 再挤到 4-5 列只剩 "42 API | compl…" 这种残句, 名称是这页最先要看的东西。
            columns={{ default: 1, md: 2, lg: 3, xl: 3, '2xl': 4 }}
            // 高度按卡片实际内容算: 网格 = p-3*2 + 名称两行 42 + gap 8 + 统计区 52 ≈ 126; 列表 = py-2*2 + 名称 24 ≈ 40。
            // 留一点余量, 否则虚拟列表按估算高度切行会把卡片切掉一条边。
            estimateItemHeight={layout === 'list' ? 44 : 130}
            gap={layout === 'list' ? 6 : 16}
            getItemKey={(channel) => `channel-${channel.channel_id}`}
            renderItem={(channel) => (
                <Card channel={channel} />
            )}
        />
        )
    );
}
