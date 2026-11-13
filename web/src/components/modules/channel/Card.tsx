import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';
import { AnimatePresence, motion } from 'motion/react';
import { History } from 'lucide-react';
import dayjs from 'dayjs';
import { type ChannelStatsFormatted, useEnableChannel } from '@/api/channel';
import { usePageActionsStore } from '@/components/common/PageActions';
import { ChannelStats } from './Stats';
import { ChannelForm } from './Form';
import { useChannelViewStore } from './detail-store';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';
import { toast } from 'sonner';
import { useState } from 'react';

// Card 展示单个渠道的概览; 名称, 启停与统计都在同一条统计查询里, 整份配置只在点开编辑时另取。
//
// 两种布局的形态差别不小, 按布局分别排:
//   网格 —— 竖向紧凑卡片, 名称两行 + 三项统计;
//   列表 —— 单行条目, 名称一行 + 统计内联, 一屏能扫过几十个渠道。
export function Card({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('channel.card');
    const tCallDetail = useTranslations('channel.callDetail');
    const setView = useChannelViewStore((s) => s.setView);
    // 布局是渠道页共享的视图选项, 直接取用而不由列表层层传入。
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const isList = layout === 'list';
    const enableChannel = useEnableChannel();
    // 弹窗默认展示统计, 编辑入口在统计视图的头部; 关闭后下次打开仍回到统计, 故点卡片时复位。
    const [openInEditing, setOpenInEditing] = useState(false);
    const metrics = channel.formatted;

    // 状态四态: 自动禁用 > 手动禁用 > 探测失败中 > 正常。色点标状态, 异常态只换边框色不铺底色:
    // 整页几十张卡片时, 大面积浅红会把页面压得很脏, 真正不能用的渠道反而被淹没。
    const statusTone = channel.auto_disabled
        ? 'bg-destructive'
        : !channel.enabled
            ? 'bg-muted-foreground/40'
            : channel.health_fail_count > 0
                ? 'bg-orange-500'
                : 'bg-emerald-500';
    // 列表布局用左侧一道红色竖条标自动禁用, 不用整圈红边: 几十行连续红框会让整页像报错列表。
    const cardTone = isList
        ? channel.auto_disabled
            ? 'border-border/60 border-l-2 border-l-destructive'
            : !channel.enabled
                ? 'border-border/60 bg-muted/40'
                : 'border-border/60'
        : channel.auto_disabled
            ? 'border-destructive/50'
            : !channel.enabled
                ? 'border-border/60 bg-muted/40'
                : 'border-border';
    const statusLabel = channel.auto_disabled
        ? t('health.autoDisabled')
        : !channel.enabled
            ? t('health.manualDisabled')
            : channel.health_fail_count > 0
                ? t('health.failing', { count: channel.health_fail_count })
                : t('health.ok');

    const successRate =
        metrics.request_count.raw > 0
            ? { value: ((metrics.request_success.raw / metrics.request_count.raw) * 100).toFixed(1), unit: '%' }
            : null;
    const hasTraffic = metrics.request_count.raw > 0;

    type StatItem = { label: string; value: string; unit?: string };
    const stats: StatItem[] = [
        { label: t('requestShort'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { label: t('successRate'), value: successRate?.value ?? '—', unit: successRate ? '%' : '' },
        { label: t('costShort'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
    ];

    const handleEnableChange = (checked: boolean) => {
        enableChannel.mutate(
            { id: channel.channel_id, enabled: checked },
            {
                onSuccess: () => {
                    toast.success(checked ? t('toast.enabled') : t('toast.disabled'));
                },
                onError: (error) => {
                    toast.error(error.message);
                },
            }
        );
    };

    const statusDot = (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className={`${isList ? '' : 'mt-1.5'} size-2 shrink-0 rounded-full ${statusTone}`} />
            </TooltipTrigger>
            <TooltipContent side="top" sideOffset={8} align="start">
                <div className="max-w-64 space-y-1 text-xs">
                    <p className="font-medium">{statusLabel}</p>
                    {channel.last_health_error && <p className="break-all text-muted-foreground">{channel.last_health_error}</p>}
                    <p className="text-muted-foreground">
                        {channel.last_health_at > 0
                            ? t('health.lastCheck', { time: dayjs.unix(channel.last_health_at).format('YYYY-MM-DD HH:mm:ss') })
                            : t('health.neverChecked')}
                    </p>
                </div>
            </TooltipContent>
        </Tooltip>
    );

    const actions = (
        <div className="-mr-1 flex shrink-0 items-center gap-0.5">
            <Tooltip>
                <TooltipTrigger asChild>
                    <Button
                        variant="ghost"
                        size="icon"
                        className="size-6 text-muted-foreground"
                        aria-label={tCallDetail('open')}
                        onClick={(e) => {
                            // 阻止冒泡到 MorphingDialogTrigger, 避免同时打开渠道编辑弹窗。
                            e.stopPropagation();
                            e.preventDefault();
                            setView({
                                mode: 'detail',
                                channelID: channel.channel_id,
                                channelName: channel.channel_name,
                            });
                        }}
                    >
                        <History className="size-3.5" />
                    </Button>
                </TooltipTrigger>
                <TooltipContent side="top" sideOffset={10} align="center">
                    {tCallDetail('open')}
                </TooltipContent>
            </Tooltip>
            <Switch
                checked={channel.enabled}
                onCheckedChange={handleEnableChange}
                disabled={enableChannel.isPending}
                onClick={(e) => e.stopPropagation()}
            />
        </div>
    );

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className={
                        isList
                            ? `flex h-full items-center gap-3 rounded-xl border px-3 py-2 transition-colors hover:border-muted-foreground/30 ${cardTone}`
                            : `flex h-full flex-col justify-between gap-2.5 rounded-2xl border p-3 transition-colors hover:border-muted-foreground/30 ${cardTone}`
                    }
                >
                    <header className={isList ? 'flex min-w-0 flex-1 items-center gap-2' : 'flex items-start gap-2'}>
                        {statusDot}
                        <h3
                            className={
                                isList
                                    ? 'min-w-0 flex-1 truncate text-[13px] font-semibold'
                                    : 'line-clamp-2 min-w-0 flex-1 break-words text-[13px] font-semibold leading-[1.45]'
                            }
                            title={channel.channel_name}
                        >
                            {channel.channel_name}
                        </h3>
                        {!isList && actions}
                    </header>

                    {isList ? (
                        <>
                            <div className="flex shrink-0 items-baseline gap-4 text-[11px] leading-5 text-muted-foreground">
                                {hasTraffic ? (
                                    stats.map(({ label, value, unit }) => (
                                        <span key={label} className="w-[76px] whitespace-nowrap text-right">
                                            <span className="text-xs font-medium tabular-nums text-foreground">
                                                {value}
                                                {unit}
                                            </span>
                                            <span className="ml-1">{label}</span>
                                        </span>
                                    ))
                                ) : (
                                    <span className="text-muted-foreground/70">{t('noTraffic')}</span>
                                )}
                            </div>
                            {actions}
                        </>
                    ) : hasTraffic ? (
                        <dl className="grid grid-cols-3 gap-2">
                            {stats.map(({ label, value, unit }) => (
                                <div key={label} className="flex min-w-0 flex-col-reverse">
                                    <dt className="truncate text-[10px] leading-4 text-muted-foreground">{label}</dt>
                                    <dd className="truncate text-[13px] font-semibold leading-5 tabular-nums">
                                        {value}
                                        {unit && <span className="text-[10px] font-normal text-muted-foreground">{unit}</span>}
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    ) : (
                        // 没有调用记录时不摆一排 0.00 / — / 0.00$, 只说一句没跑过。
                        <p className="text-[11px] leading-5 text-muted-foreground/70">{t('noTraffic')}</p>
                    )}
                </article>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={!openInEditing}
                    className="relative w-full md:max-w-3xl h-fit bg-card text-card-foreground p-4 rounded-3xl overflow-hidden"
                >
                    {/* 高度固定在外层, 与表单自带的高度取同一值: 切换时两者同高, 弹窗才不会随内容缩放。
                        两个视图绝对定位重叠, 退场与入场同时进行: 串行会在两段动画之间留出谁都不在的空档。
                        重叠期间旧视图 pointer-events-none, 否则正在淡出的那份还能挡住点击。
                        位移方向表达前进与后退: 进编辑时表单自右侧推入, 返回统计时反向。 */}
                    <MorphingDialogDescription className="relative h-[min(29rem,calc(100vh-10rem))]">
                        <AnimatePresence initial={false}>
                            {openInEditing ? (
                                <motion.div
                                    key="form"
                                    initial={{ opacity: 0, x: 20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: 20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0"
                                >
                                    <ChannelForm
                                        channelId={channel.channel_id}
                                        onBack={() => setOpenInEditing(false)}
                                    />
                                </motion.div>
                            ) : (
                                <motion.div
                                    key="stats"
                                    initial={{ opacity: 0, x: -20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: -20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0 overflow-y-auto overscroll-contain"
                                >
                                    <ChannelStats
                                        channel={channel}
                                        onEdit={() => setOpenInEditing(true)}
                                    />
                                </motion.div>
                            )}
                        </AnimatePresence>
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
