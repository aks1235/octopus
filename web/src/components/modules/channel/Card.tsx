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
export function Card({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('channel.card');
    const tCallDetail = useTranslations('channel.callDetail');
    const setView = useChannelViewStore((s) => s.setView);
    // 布局是渠道页共享的视图选项, 直接取用而不由列表层层传入。
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const enableChannel = useEnableChannel();
    // 弹窗默认展示统计, 编辑入口在统计视图的头部; 关闭后下次打开仍回到统计, 故点卡片时复位。
    const [openInEditing, setOpenInEditing] = useState(false);
    const metrics = channel.formatted;

    // 成功率由成功与失败计数现算; 一次请求都没有时显示破折号, 而不是误导性的 0%。
    const successRate =
        metrics.request_count.raw > 0
            ? { value: ((metrics.request_success.raw / metrics.request_count.raw) * 100).toFixed(1), unit: '%' }
            : { value: '—', unit: '' };

    // 统计项统一成"数值 + 单位 + 小标签": 网格布局三项一行, 列表布局按列摊开。
    type MetricItem = { label: string; value: string; unit?: string };
    const gridMetrics: MetricItem[] = [
        { label: t('requestCount'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { label: t('successRate'), value: successRate.value, unit: successRate.unit },
        { label: t('totalCost'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
    ];
    const listMetrics: MetricItem[] = [
        { label: t('requestCount'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { label: t('model'), value: String(channel.models.length) },
        { label: t('successRequests'), value: metrics.request_success.formatted.value },
        { label: t('failedRequests'), value: metrics.request_failed.formatted.value },
        { label: t('totalCost'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
    ];
    const metricItems = layout === 'list' ? listMetrics : gridMetrics;

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

    // 状态收成一个色点: 文案徽标会把标题挤成省略号, 而卡片最窄时只有六列宽。
    // 四态优先级: 自动禁用 > 手动禁用 > 探测失败中 > 正常。详情进 tooltip, 不占版面。
    const statusTone = channel.auto_disabled
        ? 'bg-destructive'
        : !channel.enabled
            ? 'bg-muted-foreground/40'
            : channel.health_fail_count > 0
                ? 'bg-orange-500'
                : 'bg-emerald-500';
    const statusLabel = channel.auto_disabled
        ? t('health.autoDisabled')
        : !channel.enabled
            ? t('health.manualDisabled')
            : channel.health_fail_count > 0
                ? t('health.failing', { count: channel.health_fail_count })
                : t('health.ok');
    const statusDot = (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className={`mt-2 size-[7px] shrink-0 rounded-full ${statusTone}`} />
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

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className="flex h-full flex-col gap-4 rounded-3xl border border-border bg-card p-4 text-card-foreground"
                >
                    <header className="flex items-start gap-2">
                        {statusDot}
                        <h3
                            className="line-clamp-2 min-w-0 flex-1 break-words text-base font-bold leading-snug"
                            title={channel.channel_name}
                        >
                            {channel.channel_name}
                        </h3>
                        <div className="flex shrink-0 items-center gap-0.5">
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <Button
                                        variant="ghost"
                                        size="icon"
                                        className="size-7"
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
                    </header>

                    <dl
                        className={
                            layout === 'list'
                                ? 'mt-auto grid grid-cols-2 gap-3 border-t border-border/60 pt-3 sm:grid-cols-3 lg:grid-cols-5'
                                : 'mt-auto grid grid-cols-3 gap-3 border-t border-border/60 pt-3'
                        }
                    >
                        {metricItems.map(({ label, value, unit }) => (
                            <div key={label} className="flex min-w-0 flex-col-reverse">
                                <dt className="mt-0.5 truncate text-[11px] text-muted-foreground">{label}</dt>
                                <dd className="truncate text-lg font-bold tabular-nums leading-tight">
                                    {value}
                                    {unit && <span className="ml-0.5 text-xs font-normal text-muted-foreground">{unit}</span>}
                                </dd>
                            </div>
                        ))}
                    </dl>

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
