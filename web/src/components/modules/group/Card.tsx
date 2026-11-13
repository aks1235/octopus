import { memo, useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { Trash2, X, Pencil, FlaskConical, Loader2, RotateCcw, ChevronRight } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { type Group, type GroupUpdateRequest, useDeleteGroup, useGroupChannelOrder, useResetGroupCooldown, useUpdateGroup } from '@/api/group';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { CopyIconButton } from '@/components/common/CopyButton';
import { IconButton } from '@/components/common/IconButton';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { GroupEditor, type GroupEditorValues } from './Editor';
import { channelOrderOf } from './utils';
import { useGroupTest } from './useGroupTest';
import { ProbeHistoryDialog } from './ProbeHistoryDialog';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

interface EditDialogContentProps {
    group: Group;
    displayMembers: SelectedMember[];
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues, onDone?: () => void) => void;
}

function EditDialogContent({ group, displayMembers, isSubmitting, onSubmit }: EditDialogContentProps) {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('group');
    return (
        <MorphingDialogDescription className="flex-1 min-h-0 overflow-hidden">
            <GroupEditor
                key={`edit-group-${group.id}`}
                groupId={group.id}
                initial={{
                    name: group.name,
                    mode: group.mode,
                    relay_config: group.relay_config,
                    member_regex: group.member_regex,
                    members: displayMembers,
                }}
                submitText={t('detail.actions.save')}
                submittingText={t('create.submitting')}
                isSubmitting={isSubmitting}
                onCancel={() => setIsOpen(false)}
                onSubmit={(v) => onSubmit(v, () => setIsOpen(false))}
            />
        </MorphingDialogDescription>
    );
}

export const GroupCard = memo(function GroupCard({ group, now, isList = false }: { group: Group; now: number; isList?: boolean }) {
    const t = useTranslations('group');
    const tProbe = useTranslations('group.probe');
    const updateGroup = useUpdateGroup();
    const activateItem = useUpdateGroup(); // 与配置提交分开持有: 共用一个实例会让点选成员点亮编辑弹窗的提交态。
    const saveChannelOrder = useGroupChannelOrder(); // 正则分组的拖拽排序走独立顺序端点, 不经成员整体提交。
    const deleteGroup = useDeleteGroup();
    const resetCooldown = useResetGroupCooldown();

    const [confirmDelete, setConfirmDelete] = useState(false);
    // 列表形态下每个分组默认只占一行, 展开才渲染成员列表: 16 个分组 × 平均 20 个成员,
    // 网格里无论如何都看不全, 而这页最常做的事是"扫一眼哪个分组现在不对"。
    const [expanded, setExpanded] = useState(false);
    const [members, setMembers] = useState<SelectedMember[]>([]);
    // 成员探测历史的弹窗状态：null 表示关闭，数字表示看哪个授权（grant）。
    const [historyGrantId, setHistoryGrantId] = useState<number | null>(null);
    const { runningMode, runTest, progress } = useGroupTest(group);
    const isDragging = useRef(false);

    // 成员的名称, 所属渠道与可用性由后端随分组给出, 此处只做展示形状的转换。
    // 不可用的成员同样列出: 否则用户看不到它的存在也就无法移除。
    const displayMembers = useMemo((): SelectedMember[] =>
        (group.items || []).map((item) => ({
            id: String(item.channel_grant_id),
            channel_grant_id: item.channel_grant_id,
            name: item.model_name,
            enabled: item.available,
            channel_id: item.channel_id,
            channel_name: item.channel_name,
            key_name: item.key_name,
            protocols: item.protocols,
            item_id: item.id,
            probe_status: item.probe_status,
            probe_reason: item.probe_reason,
            probe_at: item.probe_at,
            probe_blocking: item.probe_blocking,
        })),
        [group.items]
    );

    useEffect(() => {
        if (!isDragging.current) setMembers([...displayMembers]);
    }, [displayMembers]);

    const onSuccess = useCallback(() => toast.success(t('toast.updated')), [t]);
    const onError = useCallback((error: Error) => toast.error(t('toast.updateFailed'), { description: error.message }), [t]);

    const handleDragStart = useCallback(() => { isDragging.current = true; }, []);
    const handleDragFinish = useCallback(() => { isDragging.current = false; }, []);

    // 最近一次探测时间挂在成员上, 分组层面只有"最晚那次"这一个值得看的汇总。
    // 不做成常驻文字行——一页几十张卡片, 那就是新的噪音; 需要时悬停在测试按钮上看即可。
    const lastProbeAt = useMemo(
        () => group.items.reduce((latest, item) => (item.probe_at > latest ? item.probe_at : latest), 0),
        [group.items]
    );

    const isRegexGroup = group.member_regex !== '';
    const submitOrderPending = updateGroup.isPending || saveChannelOrder.isPending;

    // 重置冷却入口只在存在未到期冷却时渲染: 手动模式冷却本就不适用, 无冷却时按钮无意义(不渲染比禁用更干净)。
    const hasActiveCooldown = group.mode !== 'manual' &&
        Object.values(group.runtime.cooldowns ?? {}).some((until) => until > now);

    const handleResetCooldown = useCallback(() => {
        resetCooldown.mutate(group.id, {
            onSuccess: () => toast.success(t('card.cooldownResetDone')),
            onError: (error: Error) => toast.error(t('card.cooldownResetFailed'), { description: error.message }),
        });
    }, [group.id, resetCooldown, t]);

    // 正则分组的拖拽只提交渠道顺序: 成员集合由正则定稿(整体替换会被重算覆盖, 旧路径形同虚设),
    // 拖拽折算为去重渠道 ID(按首次出现序), 渠道内顺序由后端按 (模型, 凭据) 自然序保持;
    // 手动分组成员为整体替换, 拖拽与移除都直接提交当前排列, 优先级由提交顺序决定。
    const submitMembers = useCallback((next: SelectedMember[]) => {
        if (isRegexGroup) {
            saveChannelOrder.mutate(
                { id: group.id, channel_ids: channelOrderOf(next) },
                { onSuccess, onError },
            );
            return;
        }
        updateGroup.mutate(
            { id: group.id, items: next.map((m) => ({ channel_grant_id: m.channel_grant_id })) },
            { onSuccess, onError },
        );
    }, [group.id, isRegexGroup, saveChannelOrder, updateGroup, onSuccess, onError]);

    // 移除成员只对手动分组有意义: 正则分组的成员集合只读, MemberList 不挂 onRemove。
    const handleRemoveMember = useCallback((id: string) => {
        submitMembers(members.filter((m) => m.id !== id));
    }, [members, submitMembers]);

    // 点击当前成员即取消选择, 提交 0。
    const handleActivate = useCallback((itemId: number) => {
        if (group.mode !== 'manual' || activateItem.isPending) return;
        activateItem.mutate(
            { id: group.id, active_item_id: itemId === group.runtime.current_item_id ? 0 : itemId },
            { onSuccess, onError },
        );
    }, [activateItem, group.id, group.mode, group.runtime.current_item_id, onError, onSuccess]);

    const handleSubmitEdit = useCallback((values: GroupEditorValues, onDone?: () => void) => {
        const payload: GroupUpdateRequest & { id: number } = { id: group.id };

        if (values.name !== group.name) payload.name = values.name;
        if (values.mode !== group.mode) payload.mode = values.mode;
        // 空串是有意义的值(改回手动分组), 与其余可选字段一样只在变化时提交。
        if (values.member_regex !== group.member_regex) payload.member_regex = values.member_regex;
        if (values.context_length !== (group.context_length ?? 0)) payload.context_length = values.context_length;
        if (
            values.relay_config.member_max_attempts !== group.relay_config.member_max_attempts ||
            values.relay_config.member_retry_interval_seconds !== group.relay_config.member_retry_interval_seconds ||
            values.relay_config.member_non_stream_response_timeout_seconds !== group.relay_config.member_non_stream_response_timeout_seconds ||
            values.relay_config.member_stream_first_event_timeout_seconds !== group.relay_config.member_stream_first_event_timeout_seconds ||
            values.relay_config.member_cooldown_seconds !== group.relay_config.member_cooldown_seconds ||
            values.relay_config.member_max_cooldown_seconds !== group.relay_config.member_max_cooldown_seconds ||
            values.relay_config.member_affinity_seconds !== group.relay_config.member_affinity_seconds
        ) payload.relay_config = values.relay_config;
        // 提交后的分组类型: member_regex 为空即手动分组。分流与端点先后都按终态判定, 不按当前类型:
        // 本次改成正则分组时顺序端点必须排在分组更新之后(在那之前后端仍按手动分组拒绝),
        // 反过来改回手动时编辑器的拖拽就该按手动语义提交 items, 而不是被当前类型吞掉。
        const finalRegexGroup = values.member_regex !== '';
        // 手动分组成员集合与顺序有任一处不同就整体提交; 后端按授权主键匹配, 已有成员保留其主键与统计。
        // 终态是正则分组时成员集合只读, 不提交 items(整体替换会被重算覆盖), 顺序走下方顺序端点。
        if (!finalRegexGroup) {
            const nextGrantIDs = values.members.map((m) => m.channel_grant_id);
            const currentGrantIDs = (group.items || []).map((item) => item.channel_grant_id);
            if (nextGrantIDs.length !== currentGrantIDs.length || nextGrantIDs.some((id, i) => id !== currentGrantIDs[i])) {
                payload.items = nextGrantIDs.map((channel_grant_id) => ({ channel_grant_id }));
            }
        }

        // 只有顺序变化(配置无变化)时直接保存顺序; 否则先走分组更新。
        if (values.channelOrder === undefined && Object.keys(payload).length === 1) {
            onDone?.();
            return;
        }

        // 顺序端点只对已是正则分组的分组开放, 故配置更新必须先行: 可能刚把手动分组改成正则分组, 也可能不改类型。
        // 无配置变更时跳过更新, 有则等它落地再保存顺序并收尾; 任一步失败即停, 不写下一步。
        // (顺序保存失败时配置已落地, 弹窗保持打开, 用户重试保存即可补上顺序。)
        const saveOrderThenFinish = () => {
            if (!values.channelOrder) {
                onSuccess();
                onDone?.();
                return;
            }
            saveChannelOrder.mutate(
                { id: group.id, channel_ids: values.channelOrder },
                {
                    onSuccess: () => {
                        onSuccess();
                        onDone?.();
                    },
                    onError,
                },
            );
        };

        if (Object.keys(payload).length === 1) {
            saveOrderThenFinish();
            return;
        }

        updateGroup.mutate(payload, { onSuccess: saveOrderThenFinish, onError });
    }, [group.id, group.items, group.member_regex, group.context_length, group.mode, group.name, group.relay_config, onSuccess, onError, saveChannelOrder, updateGroup]);

    // 两种形态共用同一套操作区、删除确认与成员列表, 只有外壳不同:
    // 网格是整卡平铺(成员区固定高), 列表是一行摘要 + 按需展开。
    const modeLabel =
        group.mode === 'manual'
            ? t('form.manual')
            : group.mode === 'roundrobin'
                ? t('form.roundrobin')
                : t('form.failover');
    const memberCount = group.items?.length ?? 0;
    // 分子只算"健康且真的会被选到"的成员。单看 item.available 不够——它只表示凭据/渠道启用,
    // 三类成员会被误算成可用:
    //   ① probe_status === 'dead'(上次探测判死): 选路可能仍会尝试(结论过期后), 但它不是"能用";
    //   ② probe_blocking(判死且结论未过期): 选路直接跳过;
    //   ③ 已禁用凭据。
    // 三类都留在 tooltip 里逐条说清, 不混进这一个数字——"这组现在有几个真能顶"才是这列要回答的。
    const items = group.items || [];
    const healthyItems = items.filter(
        (item) => item.available && !item.probe_blocking && item.probe_status !== 'dead',
    );
    const availableCount = healthyItems.length;
    const deadTriedCount = items.filter(
        (item) => item.available && !item.probe_blocking && item.probe_status === 'dead',
    ).length;
    const blockedCount = items.filter((item) => item.available && item.probe_blocking).length;
    const disabledCount = items.filter((item) => !item.available).length;

    const actionButtons = (
        // 阻止冒泡: 列表形态下整行是展开热区, 点测试/编辑/复制/删除不该顺带把行展开或收起。
        <div className="flex items-center gap-1 shrink-0" onClick={(event) => event.stopPropagation()}>
            <IconButton
                onClick={() => runTest('full')}
                disabled={runningMode !== null || group.items.length === 0}
                className="size-7"
                tip={
                    runningMode === 'full'
                        ? t('card.testRunning')
                        : lastProbeAt > 0
                            ? `${t('card.fullTest')} · ${tProbe('lastRun', { time: new Date(lastProbeAt * 1000).toLocaleString() })}`
                            : t('card.fullTest')
                }
            >
                {runningMode === 'full' ? <Loader2 className="size-4 animate-spin" /> : <FlaskConical className="size-4" />}
            </IconButton>
            {/* 清空全部成员冷却: 只恢复候选资格, 当前承载成员与亲和不变; 徽标消失由 SSE runtime 增量驱动。 */}
            {hasActiveCooldown && (
                <IconButton
                    onClick={handleResetCooldown}
                    disabled={resetCooldown.isPending}
                    className="size-7"
                    tip={t('card.cooldownReset')}
                >
                    <RotateCcw className={`size-4 ${resetCooldown.isPending ? 'animate-spin' : ''}`} />
                </IconButton>
            )}
            <MorphingDialog>
                {/* trigger 自身渲染 motion.div 承担弹窗形变, 故由它出元素, IconButton 只补样式。 */}
                <IconButton asChild className="size-7">
                    <MorphingDialogTrigger>
                        <Pencil className="size-4" />
                    </MorphingDialogTrigger>
                </IconButton>

                <MorphingDialogContainer>
                    <MorphingDialogContent
                        dismissOnClickOutside={false}
                        className="relative w-screen max-w-full md:max-w-4xl bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden"
                    >
                        <EditDialogContent
                            group={group}
                            displayMembers={displayMembers}
                            isSubmitting={submitOrderPending}
                            onSubmit={handleSubmitEdit}
                        />
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>

            {/* CopyIconButton 自带按钮元素与复制成功的图标切换, 故以 asChild 交给它渲染。 */}
            <IconButton asChild className="size-7">
                <CopyIconButton
                    text={group.name}
                    copyIconClassName="size-4"
                    checkIconClassName="size-4 text-primary"
                />
            </IconButton>
            {/* asChild 保留 motion.button: 它与确认态共享 layoutId, 换成普通按钮会丢掉形变动画。 */}
            {!confirmDelete && (
                <IconButton asChild className="size-7 hover:text-destructive">
                    <motion.button layoutId={`delete-btn-group-${group.id}`} type="button" onClick={() => setConfirmDelete(true)}>
                        <Trash2 className="size-4" />
                    </motion.button>
                </IconButton>
            )}
        </div>
    );

    const deleteOverlay = (
        <AnimatePresence>
            {confirmDelete && (
                <motion.div layoutId={`delete-btn-group-${group.id}`} onClick={(event) => event.stopPropagation()} className="absolute inset-0 z-10 flex items-center justify-center gap-2 bg-destructive p-2 rounded-xl" transition={{ type: 'spring', stiffness: 400, damping: 30 }}>
                    <button type="button" onClick={() => setConfirmDelete(false)} className="flex h-7 w-7 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95">
                        <X className="size-4" />
                    </button>
                    <button type="button" onClick={() => deleteGroup.mutate(group.id, { onSuccess: () => toast.success(t('toast.deleted')) })} disabled={deleteGroup.isPending} className="flex-1 h-7 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed">
                        <Trash2 className="size-3.5" />
                        {t('detail.actions.confirmDelete')}
                    </button>
                </motion.div>
            )}
        </AnimatePresence>
    );

    const memberListBody = (
        <MemberList
            members={members}
            onReorder={setMembers}
            onRemove={isRegexGroup ? undefined : handleRemoveMember}
            onActivate={group.mode === 'manual' ? handleActivate : undefined}
            onShowProbeHistory={setHistoryGrantId}
            activeItemId={group.runtime.current_item_id}
            group={group}
            now={now}
            onDragStart={handleDragStart}
            onDrop={submitMembers}
            onDragFinish={handleDragFinish}
            autoScrollOnAdd={false}
            layoutScope={`card-${group.id}`}
        />
    );

    const historyDialog = (
        <ProbeHistoryDialog
            grantId={historyGrantId}
            onOpenChange={(open) => {
                if (!open) setHistoryGrantId(null);
            }}
        />
    );

    // 列表形态: 一行装下"叫什么、怎么路由、可用几个", 展开才铺开成员。
    // 成员区按成员数算高而不是写死: 2 个成员的组展开出 420px 的空白, 跟网格里那个毛病是同一个。
    // 超过上限才内部滚动——47 个成员的组一展开就是两千多像素, 反而失去概览的意义。
    if (isList) {
        const expandHeight = Math.min(420, Math.max(140, memberCount * 50 + 20));
        return (
            <article className="rounded-2xl border border-border bg-card text-card-foreground">
                {/* 名称吃满剩余宽度, 模式与可用数用固定宽度贴右: 一列扫下去, 徽标和数字各自成列,
                    不会因为分组名长短不同而左右漂移。 */}
                {/* 整行都是展开热区, 箭头只是指示器: 一行 40px 高、目标是"看这个分组的成员",
                    只在 24px 的箭头上才能点中太小了。行内的操作按钮各自阻止冒泡, 不会误触展开。 */}
                <header
                    onClick={() => setExpanded((value) => !value)}
                    onKeyDown={(event) => {
                        // 只在行自身获得焦点时响应, 内部按钮的 Enter 交给按钮自己处理。
                        if (event.target !== event.currentTarget || (event.key !== 'Enter' && event.key !== ' ')) return;
                        event.preventDefault();
                        setExpanded((value) => !value);
                    }}
                    role="button"
                    tabIndex={0}
                    aria-expanded={expanded}
                    aria-label={expanded ? t('card.collapse') : t('card.expand')}
                    className="relative flex cursor-pointer items-center gap-2 px-3 py-2 transition-colors hover:bg-muted/40"
                >
                    <ChevronRight
                        className={cn(
                            'size-4 shrink-0 text-muted-foreground transition-transform duration-200',
                            expanded && 'rotate-90',
                        )}
                    />
                    <h3 className="min-w-0 flex-1 truncate text-[15px] font-bold" title={group.name}>
                        {group.name}
                    </h3>
                    {/* 固定的是外层容器宽度, 不是徽标本身: 徽标按文字自然宽(手动/轮询/故障转移字数不同),
                        强行等宽会把"故障转移"压扁。可用数紧跟其后, 位置仍然是固定的。 */}
                    <span className="w-[62px] shrink-0">
                        <Badge
                            variant="outline"
                            className="px-1.5 py-0 text-[11px] font-medium text-muted-foreground"
                        >
                            {modeLabel}
                        </Badge>
                    </span>
                    {/* 分子只算真正参与选路的成员, 明细放 tooltip: 一个数字说不清"是没人了还是全在探测失败"。 */}
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span
                                className={cn(
                                    'w-[46px] shrink-0 text-right text-xs font-medium tabular-nums',
                                    // 0 个健康还要分两种: 完全没得试(红) / 还有判死的会被尝试(橙)。
                                    // 后者不是"这组废了", 只是没有一个是确认健康的。
                                    availableCount > 0
                                        ? 'text-muted-foreground'
                                        : deadTriedCount > 0
                                            ? 'text-orange-600 dark:text-orange-400'
                                            : 'text-destructive',
                                )}
                            >
                                {availableCount}/{memberCount}
                            </span>
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={8} align="end">
                            <div className="space-y-0.5 text-xs">
                                <p className="font-medium">
                                    {t('card.availableOf', { available: availableCount, total: memberCount })}
                                </p>
                                {disabledCount > 0 && (
                                    <p className="text-muted-foreground">
                                        {t('card.disabledMembers', { count: disabledCount })}
                                    </p>
                                )}
                                {blockedCount > 0 && (
                                    <p className="text-muted-foreground">
                                        {t('card.probeBlocked', { count: blockedCount })}
                                    </p>
                                )}
                                {deadTriedCount > 0 && (
                                    <p className="text-orange-600 dark:text-orange-400">
                                        {t('card.probeDeadTried', { count: deadTriedCount })}
                                    </p>
                                )}
                            </div>
                        </TooltipContent>
                    </Tooltip>
                    {runningMode && (
                        <span className="shrink-0 text-[11px] text-muted-foreground" aria-live="polite">
                            {tProbe('running', progress)}
                        </span>
                    )}
                    {actionButtons}
                    {deleteOverlay}
                </header>
                {expanded && (
                    <div
                        style={{ height: expandHeight }}
                        className="relative mx-2 mb-2 overflow-hidden rounded-xl border border-border/50 bg-muted/30"
                    >
                        {memberListBody}
                    </div>
                )}
                {historyDialog}
            </article>
        );
    }

    return (
        <article className="flex flex-col rounded-3xl border border-border bg-card text-card-foreground p-4">
            <header className="flex items-start justify-between mb-3 relative overflow-visible rounded-xl -mx-1 px-1 -my-1 py-1">
                <div className="relative flex-1 mr-2 min-w-0 group/title">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <h3 className="text-lg font-bold truncate">{group.name}</h3>
                        </TooltipTrigger>
                        <TooltipContent key={group.name} side="top" sideOffset={10} align="center">
                            {group.name}
                        </TooltipContent>
                    </Tooltip>
                </div>

                {actionButtons}
                {deleteOverlay}
            </header>

            {/* 测试/探测共用一份结论, 这里只报进度: 结果去哪了看成员行的徽标。
                "上次检测 N/M 已确认可用"那行已删除——它和成员行徽标说的是同一件事。 */}
            {runningMode && (
                <div className="text-xs text-muted-foreground px-1 mb-2" aria-live="polite">
                    {tProbe('running', progress)}
                </div>
            )}

            <section className="rounded-xl border border-border/50 bg-muted/30 overflow-hidden relative h-101">
                {memberListBody}
            </section>

            {historyDialog}
        </article >
    );
});
