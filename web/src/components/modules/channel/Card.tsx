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
import { IconButton } from '@/components/common/IconButton';
import { ChannelStats } from './Stats';
import { ChannelForm } from './Form';
import { useChannelViewStore } from './detail-store';
import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { toast } from 'sonner';
import { useState } from 'react';

// Card 展示单个渠道的概览; 名称, 启停与统计都在同一条统计查询里, 整份配置只在点开编辑时另取。
//
// 尺寸与分组页的 GroupCard、日志页的日志条目取同一套语言: 外层 rounded-3xl + bg-card + 竖向排布,
// 内部统计区是 rounded-xl + border-border/50 + bg-muted/30 —— 与分组卡片里那块成员区同构。
// 页面之间来回切换时"厚度"与圆角档位一致, 才不会看起来像一个面板里嵌了另一个产品。
//
// 两种布局只差排布方向, 语言仍是同一套:
//   网格 —— 名称一行 + 三格统计;
//   列表 —— 单行条目, 统计内联右对齐, 一屏能扫过几十个渠道。
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

    // 状态四态: 自动禁用 > 手动禁用 > 探测失败中 > 正常。
    // 正常渠道不挂任何标记, 只有异常才出一枚 Badge —— 配方与分组页成员的冷却徽标一致
    // (outline + 10px 字 + 同色系浅底细边), 全站只此一种语气。
    // 不给卡片描红边: 一页几十张卡同时泛红会把整页压成报错列表, 真正不能用的渠道反而被淹没。
    const statusNote = channel.auto_disabled
        ? t('health.autoDisabled')
        : !channel.enabled
            ? t('health.manualDisabled')
            : channel.health_fail_count > 0
                ? t('health.failing', { count: channel.health_fail_count })
                : '';
    const statusTone = channel.auto_disabled
        ? 'border-destructive/30 bg-destructive/10 text-destructive'
        : !channel.enabled
            ? 'border-border bg-muted text-muted-foreground'
            : 'border-orange-500/30 bg-orange-500/10 text-orange-600 dark:text-orange-400';

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

    // 徽标只在异常时出现, 悬停补上"为什么"和"最近一次检查": 正常渠道这些信息没人看,
    // 常驻成第二行文字会变成一页几十条的噪音。
    const statusBadge = statusNote ? (
        <Tooltip>
            <TooltipTrigger asChild>
                <Badge
                    variant="outline"
                    className={cn('shrink-0 px-1.5 py-0 text-[10px] font-medium', !isList && 'mt-0.5', statusTone)}
                >
                    {statusNote}
                </Badge>
            </TooltipTrigger>
            <TooltipContent side="top" sideOffset={8} align="start">
                <div className="max-w-64 space-y-1 text-xs">
                    {channel.last_health_error && <p className="break-all">{channel.last_health_error}</p>}
                    <p className="text-muted-foreground">
                        {channel.last_health_at > 0
                            ? t('health.lastCheck', { time: dayjs.unix(channel.last_health_at).format('YYYY-MM-DD HH:mm:ss') })
                            : t('health.neverChecked')}
                    </p>
                </div>
            </TooltipContent>
        </Tooltip>
    ) : null;

    const actions = (
        <div className="-mr-1 flex shrink-0 items-center gap-0.5">
            <IconButton
                className="size-7"
                tip={tCallDetail('open')}
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
                <History className="size-4" />
            </IconButton>
            <Switch
                checked={channel.enabled}
                onCheckedChange={handleEnableChange}
                disabled={enableChannel.isPending}
                onClick={(e) => e.stopPropagation()}
            />
        </div>
    );

    // 无调用记录时不摆一排 0.00 / — / 0.00, 只说一句没跑过。
    // 这句话不套统计区的灰底框: 渠道页里大半渠道都是 "暂无调用", 几十个灰框会把页面压成一片空地。
    // 但高度仍按统计区 52px 占位, 否则虚拟列表按估算高度切行会把矮卡切掉。
    const statsBlock = hasTraffic ? (
        isList ? (
            <div className="flex shrink-0 items-center gap-4">
                {stats.map(({ label, value, unit }) => (
                    <span key={label} className="w-[74px] whitespace-nowrap text-right text-[11px] text-muted-foreground">
                        <span className="text-sm font-bold tabular-nums text-card-foreground">{value}</span>
                        {unit && <span className="text-[10px] text-muted-foreground">{unit}</span>}
                        <span className="ml-1">{label}</span>
                    </span>
                ))}
            </div>
        ) : (
            <dl className="grid grid-cols-3 gap-2 rounded-xl border border-border/50 bg-muted/30 px-2.5 py-2">
                {stats.map(({ label, value, unit }) => (
                    <div key={label} className="flex min-w-0 flex-col">
                        <dt className="truncate text-[10px] leading-4 text-muted-foreground">{label}</dt>
                        <dd className="truncate text-sm font-bold leading-5 tabular-nums text-card-foreground">
                            {value}
                            {unit && <span className="ml-0.5 text-[10px] font-normal text-muted-foreground">{unit}</span>}
                        </dd>
                    </div>
                ))}
            </dl>
        )
    ) : isList ? (
        // 列表里固定同宽右对齐(3×74 + 2×16 gap), 有数据与没数据的行统计列才不会错位。
        <div className="flex w-[254px] shrink-0 justify-end">
            <span className="text-[11px] text-muted-foreground/50">{t('noTraffic')}</span>
        </div>
    ) : (
        <div className="flex h-[52px] items-center px-2.5">
            <span className="text-[11px] text-muted-foreground/50">{t('noTraffic')}</span>
        </div>
    );

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className={cn(
                        'transition-colors',
                        // 列表是密集形态, 圆角降一档: 40px 高的行套 24px 圆角会变成胶囊, 反而不像可点的行。
                        isList
                            ? 'flex h-full items-center gap-3 rounded-2xl border px-3 py-2'
                            : 'flex h-full flex-col gap-2 rounded-3xl border p-3',
                        // 三种底色把状态分到一眼可辨的三档, 但都压到"看得见"而不"扎眼"的程度:
                        //   正常/失败中 —— 保持白卡, 数量最多的一档必须是最安静的;
                        //   自动禁用   —— 极淡红底 + 淡红边(系统判定不可用);
                        //   手动禁用   —— 灰底 + 灰字(用户自己关掉的, 用"褪色"与红色的故障区分开)。
                        // 底色再重一点, 这个页面就会变回一堆报警卡片, 名字又该看不清了。
                        channel.auto_disabled
                            ? 'border-destructive/25 bg-destructive/5 hover:border-destructive/40 dark:bg-destructive/10'
                            : channel.enabled
                                ? 'border-border bg-card hover:border-muted-foreground/30'
                                : 'border-border bg-muted/60 hover:border-muted-foreground/30',
                    )}
                >
                    <header className={cn('flex min-w-0 items-start gap-1.5', isList && 'flex-1 items-center')}>
                        {/* 名称不设 flex-1: 徽标紧跟名称末尾, 否则它会浮在行中间, 与谁都不挨着。
                            网格里给两行: 渠道名普遍长于分组名, 一行装不下时就只剩 "42 API | compl…",
                            而名称是这页最先要看的东西, 宁可让卡片高一点。
                            末尾补一个占位把开关推到右边; 名称过长时 h3 收缩让位, 占位归零。 */}
                        <h3
                            className={cn(
                                'min-w-0 shrink text-[15px] font-bold',
                                // 手动禁用才褪色: 自动禁用仍要读得清名字, 它靠底色表达。
                                !channel.enabled && !channel.auto_disabled
                                    ? 'text-muted-foreground'
                                    : 'text-card-foreground',
                                isList ? 'truncate' : 'line-clamp-2 leading-[1.4]',
                            )}
                            title={channel.channel_name}
                        >
                            {channel.channel_name}
                        </h3>
                        {statusBadge}
                        {!isList && (
                            <>
                                <span className="flex-1" />
                                {actions}
                            </>
                        )}
                    </header>

                    {statsBlock}
                    {isList && actions}
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
