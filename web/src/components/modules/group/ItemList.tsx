import { useEffect, useId, useRef, useState } from 'react';
import { Layers, GripVertical, X, Trash2, CircleCheck, CircleX, Ban, Clock3 } from 'lucide-react';
import {
    DragDropContext,
    Draggable,
    Droppable,
    type DraggableProvided,
    type DropResult,
} from '@hello-pangea/dnd';
import { motion, AnimatePresence } from 'motion/react';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useTranslations } from 'use-intl';
import type { Group, GroupMemberTestResult } from '@/api/group';
import { Protocol } from '@/api/channel';
import { MemberStatus } from './MemberStatus';

export interface SelectedMember {
    id: string;
    channel_grant_id: number;
    name: string;
    enabled: boolean;
    channel_id: number;
    channel_name: string;
    key_name: string;
    protocols: number;
    item_id?: number;
    // 探测结论由后端随授权下发: 空=未探测, ok=通过, dead=已判死(选路会跳过, 24 小时后自动失效)。
    probe_status?: string;
    probe_reason?: string;
    probe_at?: number;
}

// reorderVisible 在"渲染列表被过滤"的前提下重排完整成员数组。
//
// 拖拽下标来自渲染列表（已隐藏已禁用成员），而提交顺序必须是完整成员顺序——成员顺序即选路优先级。
// 直接对完整数组用渲染下标会错位（隐藏项把后面的成员整体前移），故按被拖动成员与目标的身份定位：
// 把 moved 插到目标成员之前（向下拖则插到其后），其余成员相对顺序保持不变。
function reorderVisible(
    full: SelectedMember[],
    visible: SelectedMember[],
    startIndex: number,
    endIndex: number
): SelectedMember[] {
    const moved = visible[startIndex];
    const target = visible[endIndex];
    if (!moved || !target) return full;
    const rest = full.filter((member) => member.id !== moved.id);
    const anchor = rest.findIndex((member) => member.id === target.id);
    if (anchor < 0) return full;
    const insertAt = endIndex > startIndex ? anchor + 1 : anchor;
    return [...rest.slice(0, insertAt), moved, ...rest.slice(insertAt)];
}

type MemberItemDnd = {
    innerRef: DraggableProvided['innerRef'];
    draggableProps: DraggableProvided['draggableProps'];
    dragHandleProps: DraggableProvided['dragHandleProps'];
    isDragging: boolean;
};

// staticDnd 给折叠区（不参与拖拽）的成员行用的空拖拽属性：
// 折叠区里的成员不可拖不可排序，但行本身的展示与移除按钮与主列表一致。
const staticDnd: MemberItemDnd = {
    innerRef: () => {},
    // 空对象成对断言：dnd 库的两个 data-* 属性只在其自身的拖拽上下文里有意义，折叠区不参与拖拽。
    draggableProps: {} as MemberItemDnd['draggableProps'],
    dragHandleProps: null,
    isDragging: false,
};

// protocolLabel 把成功的协议位显示成路径里讲的端点名, 与渠道 key 测试结果面板同款口径。
function protocolLabel(bit: number) {
    if (bit & Protocol.AnthropicMessage) return 'message';
    if (bit & Protocol.OpenAIResponse) return 'response';
    return 'chat';
}

// MemberTestBadge 成员行的连通性测试结果徽标: 成功绿(带成功协议)、失败红(带错误摘要)、不可用灰。
// 不可用按成员行自身的 available 口径判定(渠道/凭据被禁用, 未发上游请求), 与列表的置灰展示同源。
function MemberTestBadge({ result, unavailable }: { result: GroupMemberTestResult; unavailable: boolean }) {
    const t = useTranslations('group.card');
    const iconClass = 'size-3.5 shrink-0';
    if (result.success) {
        const tip = result.cooldown_cleared
            ? t('testRecovered', { protocol: protocolLabel(result.protocol) })
            : t('testOk', { protocol: protocolLabel(result.protocol) });
        return (
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="inline-flex shrink-0 text-accent">
                        <CircleCheck className={iconClass} />
                    </span>
                </TooltipTrigger>
                <TooltipContent>{tip} · {t('testDuration', { ms: result.use_time })}</TooltipContent>
            </Tooltip>
        );
    }
    if (unavailable || result.status === 'unavailable') {
        return (
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="inline-flex shrink-0 text-muted-foreground">
                        <Ban className={iconClass} />
                    </span>
                </TooltipTrigger>
                <TooltipContent className="max-w-72 whitespace-normal break-all">
                    {result.error ? t('testUnavailableReason', { reason: result.error }) : t('testUnavailable')}
                </TooltipContent>
            </Tooltip>
        );
    }
    if (result.status === 'timeout') {
        return (
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="inline-flex shrink-0 text-amber-500"><Clock3 className={iconClass} /></span>
                </TooltipTrigger>
                <TooltipContent className="max-w-72 whitespace-normal break-all">
                    {t('testTimeout')}{result.error && `: ${result.error}`}
                </TooltipContent>
            </Tooltip>
        );
    }
    if (result.status === 'canceled') {
        return <Tooltip>
            <TooltipTrigger asChild><span className="inline-flex shrink-0 text-muted-foreground"><Ban className={iconClass} /></span></TooltipTrigger>
            <TooltipContent>{t('testCanceled')}</TooltipContent>
        </Tooltip>;
    }
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className="inline-flex shrink-0 text-destructive">
                    <CircleX className={iconClass} />
                </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-72 whitespace-normal break-all">
                {result.error || t('testFailed')}
            </TooltipContent>
        </Tooltip>
    );
}

// ProbeBadge 探测结论徽标。
// 只标"已判死", 不标"通过": 未探测过的成员占多数, 一旦用绿色表示通过, 界面上"没探测"与"已验证可用"就会被混为一谈。
function ProbeBadge({ member }: { member: SelectedMember }) {
    const t = useTranslations('group.card');
    if (member.probe_status !== 'dead') return null;
    const time = member.probe_at ? new Date(member.probe_at * 1000).toLocaleString() : '';
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className="inline-flex shrink-0 items-center gap-1 text-[10px] text-muted-foreground">
                    <Ban className="size-3.5 shrink-0" />
                    {t('probeDead')}
                </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-72 whitespace-normal break-all">
                {member.probe_reason || t('probeDead')}
                {time ? ` · ${t('probeDeadTime', { time })}` : ''}
            </TooltipContent>
        </Tooltip>
    );
}

// MemberItem 渲染可拖拽成员及其删除确认状态。
// onRemove 缺省时不渲染删除按钮: 正则分组的成员集合由正则定稿, 界面不允许删除。
function MemberItem({
    member,
    onRemove,
    onActivate,
    isActive,
    group,
    now,
    testResult,
    isRemoving,
    showConfirmDelete = true,
    layoutScope,
    dnd,
}: {
    member: SelectedMember;
    onRemove?: (id: string) => void;
    onActivate?: (itemId: number) => void;
    isActive?: boolean;
    group?: Group; // group 提供成员当前的冷却和亲和时间。
    now: number; // now 是成员列表共享的当前 Unix 毫秒时间。
    testResult?: GroupMemberTestResult; // 最近一次整组测试中该成员的结果, 无则不渲染徽标。
    isRemoving?: boolean;
    showConfirmDelete?: boolean;
    layoutScope?: string;
    dnd: MemberItemDnd;
}) {
    const { Icon, className: iconClassName } = getModelIcon(member.name);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const isDisabled = member.enabled === false;

    return (
        <div
            // DnD libraries provide imperative refs/props; the hook lint rule (`react-hooks/refs`)
            // flags this pattern, but it's safe and required for correct drag behavior.
            // eslint-disable-next-line react-hooks/refs
            ref={dnd.innerRef}
            // eslint-disable-next-line react-hooks/refs
            {...dnd.draggableProps}
            className={cn('rounded-lg grid transition-[grid-template-rows] duration-200', isRemoving ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}
            // eslint-disable-next-line react-hooks/refs
            style={{
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.draggableProps?.style ?? {}),
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.isDragging ? { zIndex: 50, boxShadow: '0 8px 32px rgba(0,0,0,0.15)' } : null),
            }}
        >
            <div className={cn(
                'flex items-center gap-2 rounded-lg bg-background px-2.5 py-2 select-none transition-[background-color,opacity] duration-200 relative overflow-hidden',
                isRemoving && 'opacity-0',
                isDisabled && 'opacity-60 grayscale',
                onActivate && member.item_id !== undefined && 'cursor-pointer'
            )}
                onClick={() => member.item_id !== undefined && onActivate?.(member.item_id)}
                onKeyDown={(event) => {
                    if (event.target !== event.currentTarget || member.item_id === undefined || !onActivate || (event.key !== 'Enter' && event.key !== ' ')) return;
                    event.preventDefault();
                    onActivate(member.item_id);
                }}
                role={onActivate && member.item_id !== undefined ? 'button' : undefined}
                tabIndex={onActivate && member.item_id !== undefined ? 0 : undefined}
            >
                <div
                    className={cn(
                        'p-0.5 rounded touch-none transition-colors',
                        isDisabled
                            ? 'cursor-grab active:cursor-grabbing hover:bg-muted/60'
                            : 'cursor-grab active:cursor-grabbing hover:bg-muted'
                    )}
                    // eslint-disable-next-line react-hooks/refs
                    {...dnd.dragHandleProps}
                    onClick={(event) => event.stopPropagation()}
                >
                    <GripVertical className="size-3.5 text-muted-foreground" />
                </div>

                <span className={cn(isDisabled && 'opacity-70')}>
                    <Icon aria-hidden="true" className={iconClassName} width={18} height={18} />
                </span>

                <div className="flex flex-col min-w-0 flex-1">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className={cn(
                                'w-fit max-w-full text-sm font-medium truncate leading-tight',
                                isDisabled && 'text-muted-foreground'
                            )}>
                                {member.name}
                            </span>
                        </TooltipTrigger>
                        <TooltipContent key={member.name} side="top" sideOffset={10} align="center">
                            {member.name}
                        </TooltipContent>
                    </Tooltip>
                    <span className="text-[10px] text-muted-foreground truncate leading-tight">
                        {member.key_name ? `${member.channel_name} · ${member.key_name}` : member.channel_name}
                    </span>
                </div>

                {testResult && <MemberTestBadge result={testResult} unavailable={isDisabled} />}
                <ProbeBadge member={member} />
                {group && <MemberStatus group={group} itemId={member.item_id} now={now} active={isActive} activeClassName="p-1" />}

                {onRemove && (!showConfirmDelete || !confirmDelete) && (
                    <motion.button
                        layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                        type="button"
                        onClick={(event) => {
                            event.stopPropagation();
                            if (showConfirmDelete) setConfirmDelete(true);
                            else onRemove(member.id);
                        }}
                        className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                        transition={{ duration: 0.15 }}
                        style={{ pointerEvents: 'auto' }}
                    >
                        <X className="size-3" />
                    </motion.button>
                )}

                <AnimatePresence>
                    {onRemove && showConfirmDelete && confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-1.5 rounded-lg"
                            onClick={(event) => event.stopPropagation()}
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    setConfirmDelete(false);
                                }}
                                className="flex h-6 w-6 items-center justify-center rounded-md bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                            >
                                <X className="h-3 w-3" />
                            </button>
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    onRemove(member.id);
                                }}
                                className="flex-1 h-6 flex items-center justify-center gap-1.5 rounded-md bg-destructive-foreground text-destructive text-xs font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98]"
                            >
                                <Trash2 className="h-3 w-3" />
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </div>
        </div>
    );
}

interface MemberListProps {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove?: (id: string) => void; // 缺省时成员行不渲染删除按钮（正则分组成员集合只读）。
    onActivate?: (itemId: number) => void;
    activeItemId?: number;
    group?: Group; // group 提供当前模式和成员运行状态。
    now?: number; // now 是页面共享的当前 Unix 毫秒时间，仅展示运行态时需要。
    testResults?: Record<number, GroupMemberTestResult>; // 整组测试结果按 item_id 索引, 供成员行渲染结果徽标。
    /**
     * When true, auto-scroll the list to bottom when a *new visible* member appears
     * (i.e. a new member id is added). Useful in "editor" flows. Defaults to true.
     */
    autoScrollOnAdd?: boolean;
    onDragStart?: () => void;
    /**
     * Called only when a drop results in a different order (i.e. commit reorder).
     * Useful for persisting the new order.
     */
    onDrop?: (members: SelectedMember[]) => void;
    /**
     * Called whenever a drag ends (including cancel / same-index drop).
     * Useful for lifecycle cleanup (e.g. clearing "isDragging" flags).
     */
    onDragFinish?: () => void;
    removingIds?: Set<string>;
    /**
     * When true, show a confirmation overlay before removing an item.
     * When false, clicking the delete button removes the item immediately.
     * Defaults to true.
     */
    showConfirmDelete?: boolean;
    layoutScope?: string;
}

export function MemberList({
    members,
    onReorder,
    onRemove,
    onActivate,
    activeItemId,
    group,
    now = 0,
    testResults,
    autoScrollOnAdd = true,
    onDragStart,
    onDrop,
    onDragFinish,
    removingIds = new Set(),
    showConfirmDelete = true,
    layoutScope: externalLayoutScope,
}: MemberListProps) {
    const internalLayoutScope = useId();
    const layoutScope = externalLayoutScope ?? internalLayoutScope;
    const scrollContainerRef = useRef<HTMLDivElement | null>(null);
    const prevMemberCountRef = useRef<number>(0);
    const hasMountedRef = useRef(false);

    const visibleCount = members.filter((m) => !removingIds.has(m.id)).length;
    const isEmpty = visibleCount === 0;
    const t = useTranslations('group');

    // 已禁用成员默认收在底部的只读折叠区：它们的数量常远超可用成员，平铺会把可用项挤到屏幕外
    //（用户原话：要往下翻才能找到）。顺序即选路优先级，故收起只影响渲染，提交始终是全量成员。
    const [showDisabledMembers, setShowDisabledMembers] = useState(false);
    const draggableMembers = members.filter((member) => member.enabled !== false || removingIds.has(member.id));
    const disabledMembers = members.filter((member) => member.enabled === false && !removingIds.has(member.id));

    useEffect(() => {
        // Skip the initial mount so we don't auto-scroll on first render / initial data load.
        if (!hasMountedRef.current) {
            hasMountedRef.current = true;
            prevMemberCountRef.current = members.length;
            return;
        }

        if (!autoScrollOnAdd) {
            prevMemberCountRef.current = members.length;
            return;
        }

        const hasNewMember = members.length > prevMemberCountRef.current;

        // Auto-scroll only when member count increases (i.e. added; not reorder / not "unhide").
        if (hasNewMember) {
            // Wait a tick for DOM/placeholder/layout to settle.
            requestAnimationFrame(() => {
                const el = scrollContainerRef.current;
                if (!el) return;
                el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
            });
        }

        prevMemberCountRef.current = members.length;
    }, [members.length, autoScrollOnAdd]);

    const handleDragEnd = (result: DropResult) => {
        try {
            const { destination, source } = result;
            if (!destination) return;
            if (destination.index === source.index) return;

            const next = reorderVisible(members, draggableMembers, source.index, destination.index);
            onReorder(next);
            onDrop?.(next);
        } finally {
            // Ensure drag lifecycle always finishes, even when drop is canceled.
            onDragFinish?.();
        }
    };

    return (
        <div className="relative h-full min-h-0">
            <div
                className={cn(
                    'absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground',
                    'transition-opacity duration-200 ease-out',
                    isEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none'
                )}
            >
                <Layers className="size-10 opacity-40" />
                <span className="text-sm">{t('card.empty')}</span>
            </div>

            <div
                className={cn(
                    'h-full overflow-y-auto transition-opacity duration-200',
                    isEmpty ? 'opacity-0' : 'opacity-100'
                )}
                ref={scrollContainerRef}
            >
                <DragDropContext
                    onDragStart={() => onDragStart?.()}
                    onDragEnd={handleDragEnd}
                >
                    <Droppable
                        droppableId={`members-${layoutScope}`}
                        renderClone={(draggableProvided, snapshot, rubric) => {
                            const member = draggableMembers[rubric.source.index];
                            return (
                                <MemberItem
                                    member={member}
                                    onRemove={onRemove}
                                    onActivate={onActivate}
                                    isActive={member.item_id === activeItemId}
                                    group={group}
                                    now={now}
                                    testResult={member.item_id !== undefined ? testResults?.[member.item_id] : undefined}
                                    isRemoving={false}
                                    showConfirmDelete={showConfirmDelete}
                                    layoutScope={layoutScope}
                                    dnd={{
                                        innerRef: draggableProvided.innerRef,
                                        draggableProps: draggableProvided.draggableProps,
                                        dragHandleProps: draggableProvided.dragHandleProps,
                                        isDragging: snapshot.isDragging,
                                    }}
                                />
                            );
                        }}
                    >
                        {(droppableProvided) => (
                            <div
                                ref={droppableProvided.innerRef}
                                {...droppableProvided.droppableProps}
                                className="p-2 flex flex-col space-y-1.5"
                            >
                                {draggableMembers.map((member, index) => (
                                    <Draggable
                                        key={member.id}
                                        draggableId={member.id}
                                        index={index}
                                        isDragDisabled={removingIds.has(member.id)}
                                    >
                                        {(draggableProvided, snapshot) => (
                                            <MemberItem
                                                member={member}
                                                onRemove={onRemove}
                                                onActivate={onActivate}
                                                isActive={member.item_id === activeItemId}
                                                group={group}
                                                now={now}
                                                testResult={member.item_id !== undefined ? testResults?.[member.item_id] : undefined}
                                                isRemoving={removingIds.has(member.id)}
                                                showConfirmDelete={showConfirmDelete}
                                                layoutScope={layoutScope}
                                                dnd={{
                                                    innerRef: draggableProvided.innerRef,
                                                    draggableProps: draggableProvided.draggableProps,
                                                    dragHandleProps: draggableProvided.dragHandleProps,
                                                    isDragging: snapshot.isDragging,
                                                }}
                                            />
                                        )}
                                    </Draggable>
                                ))}
                                {droppableProvided.placeholder}
                            </div>
                        )}
                    </Droppable>
                </DragDropContext>

                {disabledMembers.length > 0 && (
                    <div className="px-2 pb-2">
                        <button
                            type="button"
                            onClick={() => setShowDisabledMembers((value) => !value)}
                            className="w-full rounded-lg border border-dashed border-border/60 px-2.5 py-1.5 text-[11px] text-muted-foreground transition-colors hover:bg-muted/60"
                        >
                            {showDisabledMembers
                                ? t('card.collapseDisabled')
                                : t('card.disabledMembers', { count: disabledMembers.length })}
                        </button>
                        {showDisabledMembers && (
                            <div className="mt-1.5 flex flex-col space-y-1.5">
                                {disabledMembers.map((member) => (
                                    <MemberItem
                                        key={member.id}
                                        member={member}
                                        onRemove={onRemove}
                                        onActivate={onActivate}
                                        isActive={member.item_id === activeItemId}
                                        group={group}
                                        now={now}
                                        testResult={member.item_id !== undefined ? testResults?.[member.item_id] : undefined}
                                        isRemoving={false}
                                        showConfirmDelete={showConfirmDelete}
                                        layoutScope={layoutScope}
                                        dnd={staticDnd}
                                    />
                                ))}
                            </div>
                        )}
                    </div>
                )}
            </div>
        </div>
    );
}
