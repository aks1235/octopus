import { useMemo } from 'react';
import { ArrowUpAZ } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { GroupCard } from './Card';
import { CreateDialogContent } from './Create';
import { useRuntimeClock } from './MemberStatus';
import { useGroupList } from '@/api/group';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

// GroupActions 向稳定顶栏提供分组页面的搜索、视图选项和创建入口。
export function GroupActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const layout = usePageActionsStore((state) => state.layouts.group || 'list');
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setLayout = usePageActionsStore((state) => state.setLayout);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setGroupFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('group', value)}
            layout={layout}
            onLayoutChange={(value) => setLayout('group', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('group', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.group.all') },
                { value: 'with-members', label: t('popover.filter.group.withMembers') },
                { value: 'empty', label: t('popover.filter.group.empty') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'with-members' || value === 'empty') setFilter(value);
            }}
        >
            <CreateDialogContent />
        </PageActions>
    );
}

// Group 渲染分组列表正文。
export function Group() {
    const { data: groups } = useGroupList(true, true);
    const runtimeNow = useRuntimeClock(groups);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    // 默认列表: 16 个分组平均 20 个成员, 网格一屏只装得下 6 个。要平铺看成员时再切网格。
    const layout = usePageActionsStore((state) => state.layouts.group || 'list');
    const isList = layout === 'list';

    const sortedGroups = useMemo(() => {
        if (!groups) return [];
        return [...groups].sort((a, b) =>
            sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)
        );
    }, [groups, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const byName = !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));

        if (filter === 'with-members') return byName.filter((g) => (g.items?.length || 0) > 0);
        if (filter === 'empty') return byName.filter((g) => (g.items?.length || 0) === 0);

        return byName;
    }, [sortedGroups, searchTerm, filter]);

    return (
        <VirtualizedGrid
            items={visibleGroups}
            layout={layout}
            columns={isList ? { default: 1 } : { default: 1, md: 2, lg: 3 }}
            // 列表每行折叠态约 44px, 展开后的实际高度由 VirtualizedGrid 的 measureElement 量出来,
            // 这里的值只影响首帧估算, 给足余量以免展开瞬间抖动。
            estimateItemHeight={isList ? 48 : 520}
            gap={isList ? 6 : 16}
            getItemKey={(group) => group.id}
            renderItem={(group) => {
                let deadline = group.runtime.affinity_until;
                for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
                    deadline = Math.max(deadline, cooldownUntil);
                }
                return <GroupCard group={group} now={deadline > runtimeNow ? runtimeNow : deadline} isList={isList} />;
            }}
        />
    );
}
