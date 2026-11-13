import { memo, useEffect, useState } from 'react';
import { AlertCircle, ArrowRight, CircleCheck, Loader2, SkipForward, Square } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { type RelayLogOverview, useLogRequestBody, useLogResponseBody, useStopRound } from '@/api/log';
import { useGroup, useUpdateGroup } from '@/api/group';
import { getModelIcon } from '@/lib/model-icons';
import { ClientIconBadge, ReasoningEffortBadge } from '@/components/modules/log/ClientIcon';
import { FullContentBody, SimpleContentBody } from '@/components/modules/log/ContentBody';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import { LogMetrics } from './LogMetrics';
import { ProtocolLabel, StreamingBadge } from './LogBadges';
import { CopyIconButton } from '@/components/common/CopyButton';
import { toast } from 'sonner';
import { MemberStatus } from '@/components/modules/group/MemberStatus';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

// LogDetail 渲染日志详情弹窗内容, 仅在弹窗打开期间挂载, 由此避免列表中的卡片持有详情查询和状态。
function LogDetail({ log, now }: { log: RelayLogOverview; now: number }) {
    const t = useTranslations('log.card');
    const statusT = useTranslations('log.status');
    const [leftTab, setLeftTab] = useState<'request' | 'group'>('group');
    const [detailReady, setDetailReady] = useState(false); // 展开动画结束后才允许加载详情数据。
    // 请求体默认简洁档(只画最后一条用户输入), 「查看请求体」才渲染全量; 收起即卸载全量视图。
    const [showRequestBody, setShowRequestBody] = useState(false);
    // 响应体同理默认简洁档: 响应体可能很大, 未展开时不解析、不渲染。
    const [showResponseBody, setShowResponseBody] = useState(false);
    const [switchingItemId, setSwitchingItemId] = useState<number | null>(null);
    const requestBody = useLogRequestBody(log.id, log.started_at, detailReady && leftTab === 'request');
    const responseBody = useLogResponseBody(log.id, log.started_at, detailReady && log.status === 'success');
    const { data: activeGroup } = useGroup(log.group_id, detailReady, detailReady);
    const updateActiveItem = useUpdateGroup();
    const stopRound = useStopRound();
    const actualModel = log.target_model || log.model;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const errorText = log.error ?? '';
    const requestFailed = log.status === 'failed' || log.status === 'canceled';
    const responseCommitted = log.status === 'committed';
    // 流式请求提交后按相位显示思考中/输出中; 无相位(非流式或尚未识别)回退既有文案。
    const phaseText = log.phase === 'thinking' ? t('phaseThinking') : log.phase === 'answering' ? t('phaseAnswering') : undefined;
    // 尝试记录直接来自状态流(后端每次尝试结束推一条), 不再由前端观察轮次号推断 ——
    // 那套推断要求"弹窗开着且恰好观察到该轮中间快照", 漏一帧就永久丢一轮。
    const attempts = log.attempts ?? [];
    const showRounds = log.status === 'running' || (requestFailed && attempts.length > 0);
    const waitingRound = log.status === 'running' && log.sending;
    const isWaitingForSelection = log.status === 'running' && !log.sending && activeGroup?.mode === 'manual' && activeGroup.runtime.current_item_id === 0; // isWaitingForSelection 表示手动模式请求正等待选择渠道。

    // 让弹窗先完成展开动画, 避免详情请求及其状态更新占用动画起步帧。
    useEffect(() => {
        const timer = window.setTimeout(() => setDetailReady(true), 600);
        return () => window.clearTimeout(timer);
    }, []);

    return (
        <MorphingDialogContent className="relative w-[calc(100vw-2rem)] md:w-[80vw] bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden">
            <MorphingDialogClose className="top-4 right-5 text-muted-foreground hover:text-foreground transition-colors" />
            <MorphingDialogTitle className="flex items-center gap-2 mb-3 text-sm">
                <span className="relative inline-flex shrink-0">
                    <Icon aria-hidden="true" className={iconClassName} width={28} height={28} />
                    <ClientIconBadge clientName={log.client_name} className="absolute -bottom-1 -right-1.5 size-3.5 rounded-full bg-card" />
                </span>
                <ProtocolLabel protocol={log.protocol} />
                <span className="font-semibold text-card-foreground">{log.model || t('unknownModel')}</span>
                <ReasoningEffortBadge effort={log.reasoning_effort} />
                {log.status === 'running' || responseCommitted
                    ? <Loader2 className={cn('size-3.5 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                    : <ArrowRight className="size-3.5 text-muted-foreground/50" />}
                <ProtocolLabel protocol={log.target_protocol} />
                <Badge
                    variant="secondary"
                    className="text-xs px-1.5 py-0"
                    style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                >
                    {log.target_channel || '-'}
                </Badge>
                <span className="text-muted-foreground">{actualModel}</span>
            </MorphingDialogTitle>

            <MorphingDialogDescription className="flex-1 min-h-0">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 h-full min-h-0">
                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 pl-1 pr-3 md:pr-4">
                            <Tabs value={leftTab} onValueChange={(value) => setLeftTab(value as 'request' | 'group')}>
                                <TabsList variant="text" className="p-0">
                                    <TabsTrigger value="group" className="pr-0">
                                        {t('group')}
                                    </TabsTrigger>
                                    <span aria-hidden="true" className="mx-1 inline-flex h-full -translate-y-px items-center text-sm font-medium leading-none text-muted-foreground/50">/</span>
                                    <TabsTrigger value="request" className="pl-0">
                                        {t('requestContent')}
                                    </TabsTrigger>
                                </TabsList>
                            </Tabs>
                            {leftTab === 'request' && (
                                <>
                                    <Badge variant="secondary" className="ml-auto text-xs">
                                        {(log.usage.prompt_tokens - (log.usage.prompt_tokens_details?.cached_tokens ?? 0)).toLocaleString()} {t('tokens')}
                                    </Badge>
                                    {/* 展开后提供收起入口; 收起即卸载全量视图, 不保留 DOM(与历史弹窗一致)。 */}
                                    {showRequestBody && (
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            className="h-6 rounded-lg px-2 text-xs text-muted-foreground"
                                            onClick={() => setShowRequestBody(false)}
                                        >
                                            {t('hideRequestBody')}
                                        </Button>
                                    )}
                                </>
                            )}
                        </div>
                        <div className="flex-1 overflow-auto min-h-0">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : leftTab === 'request' ? (
                                requestBody.isLoading ? (
                                    <div className="flex h-full items-center justify-center">
                                        <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                    </div>
                                ) : requestBody.error ? (
                                    <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                        <AlertCircle className="size-5" />
                                        <span>{t('detailUnavailable')}</span>
                                    </div>
                                ) : requestBody.data ? (
                                    showRequestBody ? (
                                        // 全量档: 与改前一致的 JsonView 全展开视图, 按内容串 memo, 心跳不重建。
                                        <FullContentBody content={requestBody.data} collapsed={false} dense className="p-4" />
                                    ) : (
                                        // 简洁档(默认): 只画最后一条用户输入 + 消息总数 + 大小摘要, 未展开时零解析。
                                        <SimpleContentBody content={requestBody.data} onViewFull={() => setShowRequestBody(true)} />
                                    )
                                ) : (
                                    <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word leading-relaxed">
                                        {t('noRequestContent')}
                                    </pre>
                                )
                            ) : !activeGroup ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('groupUnavailable')}
                                </div>
                            ) : !activeGroup.items.length ? (
                                <div className="flex h-full items-center justify-center px-4 text-xs text-muted-foreground">
                                    {t('noGroupItems')}
                                </div>
                            ) : (
                                <div className="divide-y divide-border">
                                    {activeGroup.items.map((item) => {
                                        const { Icon: ItemIcon, className: itemIconClassName } = getModelIcon(item.model_name);
                                        const itemCurrent = switchingItemId !== null
                                            ? item.id === switchingItemId
                                            : activeGroup.runtime.current_item_id === item.id;
                                        // 人工切换只对手动模式有意义: 轮询与故障转移都由路由自动选成员。
                                        return (
                                            <button
                                                key={item.id}
                                                type="button"
                                                aria-pressed={itemCurrent}
                                                disabled={activeGroup.mode !== 'manual' || switchingItemId !== null || stopRound.isPending}
                                                onClick={async () => {
                                                    if (activeGroup.mode !== 'manual') return;
                                                    setSwitchingItemId(item.id);
                                                    const isCurrent = activeGroup.runtime.current_item_id === item.id;
                                                    try {
                                                        await updateActiveItem.mutateAsync({ id: activeGroup.id, active_item_id: isCurrent ? 0 : item.id });
                                                        if (log.sending) {
                                                            await stopRound.mutateAsync({ requestId: log.id, round: log.round });
                                                        }
                                                        toast.success(isCurrent ? t('channelCleared') : t('channelChanged'));
                                                    } catch (cause) {
                                                        toast.error(t('channelChangeFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                                    } finally {
                                                        setSwitchingItemId(null);
                                                    }
                                                }}
                                                className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2.5 text-left text-xs transition-colors hover:bg-muted/50 disabled:cursor-default disabled:hover:bg-transparent"
                                            >
                                                <ItemIcon aria-hidden="true" className={itemIconClassName} width={20} height={20} />
                                                <span className="min-w-0 flex-1">
                                                    <span className="block truncate font-semibold text-foreground">
                                                        {item.model_name}
                                                    </span>
                                                    <span className="block truncate text-[11px] text-muted-foreground">
                                                        {item.key_name ? `${item.channel_name} · ${item.key_name}` : item.channel_name}
                                                    </span>
                                                </span>
                                                <MemberStatus group={activeGroup} itemId={item.id} now={now} active={itemCurrent} />
                                            </button>
                                        );
                                    })}
                                </div>
                            )}
                        </div>
                    </div>

                    <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                        <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border bg-muted/50 px-3 md:px-4">
                            <span className="text-sm font-medium text-card-foreground">
                                {isWaitingForSelection ? t('waitingChannelSelection') : showRounds ? t('retryDetails') : requestFailed ? t('errorInfo') : t('responseContent')}
                            </span>
                            {log.status === 'running' && log.sending && activeGroup?.mode === 'manual' ? (
                                <button
                                    type="button"
                                    disabled={stopRound.isPending}
                                    onClick={async () => {
                                        try {
                                            await stopRound.mutateAsync({ requestId: log.id, round: log.round });
                                        } catch (cause) {
                                            toast.error(t('stopFailed'), { description: cause instanceof Error ? cause.message : undefined });
                                        }
                                    }}
                                    className="ml-auto flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
                                >
                                    {stopRound.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Square className="size-3.5" />}
                                    {t('stopRound')}
                                </button>
                            ) : !requestFailed && (
                                <Badge variant="secondary" className="ml-auto text-xs">
                                    {responseCommitted
                                        ? phaseText ?? statusT('committed')
                                        : `${log.usage.completion_tokens.toLocaleString()} ${t('tokens')}`}
                                </Badge>
                            )}
                            {/* 响应体展开后提供收起入口; 收起即卸载全量视图, 与历史弹窗一致。 */}
                            {showResponseBody && responseBody.data && (
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    className="h-6 rounded-lg px-2 text-xs text-muted-foreground"
                                    onClick={() => setShowResponseBody(false)}
                                >
                                    {t('hideResponseBody')}
                                </Button>
                            )}
                        </div>
                        <div className="min-h-0 flex-1 overflow-auto">
                            {!detailReady ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : isWaitingForSelection ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {t('waitingChannelSelection')}
                                </div>
                            ) : showRounds ? (
                                <div className="divide-y divide-border">
                                    {/* 退避等待与成员冷却期间没有上游在飞, 界面若不动会像卡住: 补一行说明正在等下一轮。
                                        不给倒计时: 剩余秒数取决于冷却与退避两套计时, 说了反而容易对不上。 */}
                                    {log.status === 'running' && !log.sending && attempts.length > 0 && (
                                        <div className="flex items-center gap-2 px-3 py-2.5 text-xs text-muted-foreground">
                                            <Loader2 className="size-4 shrink-0 animate-spin" />
                                            <span>{t('waitingRetry')}</span>
                                        </div>
                                    )}
                                    {/* 正在等待上游的这一轮还没结束, 单独占一行; 已完成的一次一条, 最新在最上。 */}
                                    {waitingRound && (
                                        <div className="flex items-center gap-2 px-3 py-2.5 text-xs text-muted-foreground">
                                            <Loader2 className="size-4 shrink-0 animate-spin" />
                                            <span className="shrink-0">{t('retryIndex', { index: attempts.length + 1 })}</span>
                                            <span className="shrink-0 font-semibold text-foreground">{log.target_channel || '-'}</span>
                                            <span className="truncate">{t('waitingResponse')}</span>
                                        </div>
                                    )}
                                    {attempts.length === 0 && !waitingRound && (
                                        <div className="flex h-full items-center justify-center gap-2 px-3 py-6 text-xs text-muted-foreground">
                                            <Loader2 className="size-4 animate-spin" />
                                            {t('waitingChannelSelection')}
                                        </div>
                                    )}
                                    {attempts.slice().reverse().map((attempt) => (
                                        <div key={`${attempt.attempt_num}-${attempt.channel_id}-${attempt.channel_key_id ?? 0}`} className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
                                            <div className="flex items-center gap-2">
                                                <span className="shrink-0 text-muted-foreground">{t('retryIndex', { index: attempt.attempt_num })}</span>
                                                <span className="shrink-0 font-semibold text-foreground">{attempt.channel_name || '-'}</span>
                                                {attempt.channel_key_remark && (
                                                    <span className="shrink-0 truncate text-muted-foreground">{attempt.channel_key_remark}</span>
                                                )}
                                                <span className="shrink-0 tabular-nums text-muted-foreground">{attempt.duration} ms</span>
                                                {attempt.status === 'success' ? (
                                                    <CircleCheck className="ml-auto size-3.5 shrink-0 text-emerald-600 dark:text-emerald-500" />
                                                ) : attempt.status === 'failed' ? (
                                                    <CopyIconButton
                                                        text={attempt.msg ?? ''}
                                                        className="ml-auto p-1 rounded-md text-destructive/60 hover:text-destructive hover:bg-destructive/10 transition-colors"
                                                        copyIconClassName="size-3.5"
                                                        checkIconClassName="size-3.5"
                                                    />
                                                ) : (
                                                    <SkipForward className="ml-auto size-3.5 shrink-0 text-muted-foreground" />
                                                )}
                                            </div>
                                            {attempt.msg && (
                                                <div className="text-[11px] leading-relaxed text-destructive/90 whitespace-pre-wrap wrap-break-word">
                                                    {attempt.msg}
                                                </div>
                                            )}
                                        </div>
                                    ))}
                                </div>
                            ) : responseCommitted ? (
                                <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
                                    <Loader2 className="size-4 animate-spin" />
                                    {phaseText ?? t('responseStreaming')}
                                </div>
                            ) : requestFailed ? (
                                errorText ? (
                                    // 失败请求展示错误正文(本身就是完整信息), 走全量档按内容串 memo。
                                    <FullContentBody content={errorText} collapsed={false} dense className="p-4" />
                                ) : (
                                    <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word leading-relaxed">
                                        {t('noResponseContent')}
                                    </pre>
                                )
                            ) : responseBody.isLoading ? (
                                <div className="flex h-full items-center justify-center">
                                    <Loader2 className="size-5 animate-spin text-muted-foreground" />
                                </div>
                            ) : responseBody.error ? (
                                <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-xs text-destructive">
                                    <AlertCircle className="size-5" />
                                    <span>{t('detailUnavailable')}</span>
                                </div>
                            ) : responseBody.data ? (
                                showResponseBody ? (
                                    // 全量档: 响应体只在 status=success 时拉取一次, 内容串稳定, 心跳不重建。
                                    <FullContentBody content={responseBody.data} collapsed={false} dense className="p-4" />
                                ) : (
                                    // 简洁档(默认): 响应体不是消息数组, 只给大小摘要与「查看响应体」入口, 零解析。
                                    <SimpleContentBody content={responseBody.data} mode="response" onViewFull={() => setShowResponseBody(true)} />
                                )
                            ) : (
                                <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap wrap-break-word leading-relaxed">
                                    {t('noResponseContent')}
                                </pre>
                            )}
                        </div>
                    </div>
                </div>
            </MorphingDialogDescription>

            <div className="flex w-full shrink-0 flex-wrap items-center gap-3 pt-4 mt-auto text-xs text-muted-foreground md:gap-4">
                <LogMetrics log={log} now={now} brandColor={brandColor} variant="footer" />
            </div>
        </MorphingDialogContent>
    );
}

// LogCardBody 渲染日志概览卡片, 并在弹窗打开时挂载详情面板。
function LogCardBody({ log }: { log: RelayLogOverview }) {
    const t = useTranslations('log.card');
    const { isOpen } = useMorphingDialog();
    const [now, setNow] = useState(() => Date.now());
    const actualModel = log.target_model || log.model;
    const { Icon, className: iconClassName, color: brandColor } = getModelIcon(actualModel);
    const requestRunning = log.status === 'running' || log.status === 'committed';
    const requestFailed = log.status === 'failed' || log.status === 'canceled';
    const errorText = log.error ?? '';

    // 仅在请求进行中或弹窗打开时走秒级刷新, 避免已完成日志持续触发重渲染。
    useEffect(() => {
        if (!requestRunning && !isOpen) return;
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [isOpen, requestRunning]);

    return (
        <>
            <MorphingDialogTrigger
                className={cn(
                    "rounded-3xl border bg-card w-full text-left",
                    requestFailed ? "border-destructive/40" : "border-border",
                )}
            >
                <div className={cn("p-4 grid grid-cols-[auto_1fr] gap-4", requestFailed ? "items-start" : "items-center")}>
                    <span className="relative inline-flex shrink-0">
                        <Icon aria-hidden="true" className={iconClassName} width={40} height={40} />
                        <ClientIconBadge clientName={log.client_name} className="absolute -bottom-1 -right-1.5 size-4 rounded-full bg-card" />
                    </span>
                    <div className="min-w-0 flex flex-col gap-3">
                        <div className="flex items-center gap-2 min-w-0 text-sm">
                            <ProtocolLabel protocol={log.protocol} />
                            <span className="font-semibold text-card-foreground truncate">
                                {log.model || t('unknownModel')}
                            </span>
                            <StreamingBadge streaming={log.streaming} />
                            <ReasoningEffortBadge effort={log.reasoning_effort} />
                            {requestRunning
                                ? <Loader2 className={cn('size-3.5 shrink-0 animate-spin', log.status === 'committed' ? 'text-green-500' : log.round > 1 ? 'text-red-500' : 'text-muted-foreground/50')} />
                                : <ArrowRight className="size-3.5 shrink-0 text-muted-foreground/50" />}
                            <ProtocolLabel protocol={log.target_protocol} />
                            <Badge
                                variant="secondary"
                                className="shrink-0 text-xs px-1.5 py-0"
                                style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                            >
                                {log.target_channel || '-'}
                            </Badge>
                            {/* 故障转移标记: 按已完成尝试数算, 同一轮内重试也能反映出来(round 只在换轮时递增)。 */}
                            {(log.attempts?.length ?? 0) > 1 && (
                                <Badge variant="outline" className="shrink-0 text-xs px-1.5 py-0 gap-1 text-amber-600 dark:text-amber-400 border-amber-300/50 dark:border-amber-600/40">
                                    <AlertCircle className="size-3" />{t('attemptCount', { count: log.attempts?.length ?? 0 })}
                                </Badge>
                            )}
                            <span className="text-muted-foreground truncate">
                                {actualModel}
                            </span>
                        </div>
                        {/* 十项指标挤成一行会溢出(缓存率与相位速度为长文本, 单元格仅剩约 50-75px):
                            移动端每行两项(宽度充足), md 起每行五项共两行; 十项要单行需每格约 98px, 容不下「1,234 (87%)」
                            与「输出 1,234 字符/秒」这类最长值, 故不再按 xl 并成一行。 */}
                        <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs tabular-nums text-muted-foreground md:grid-cols-5">
                            <LogMetrics log={log} now={now} brandColor={brandColor} variant="card" />
                        </div>
                        {requestFailed && errorText && (
                            <div className="p-2.5 rounded-xl bg-destructive/10 border border-destructive/20 overflow-hidden">
                                <p className="text-xs text-destructive line-clamp-2 whitespace-pre-line">{errorText}</p>
                            </div>
                        )}
                    </div>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <LogDetail log={log} now={now} />
            </MorphingDialogContainer>
        </>
    );
}

// LogCard 展示一条日志概览, 并在弹窗打开时加载详情。
export const LogCard = memo(function LogCard({ log }: { log: RelayLogOverview }) {
    return (
        <MorphingDialog>
            <LogCardBody log={log} />
        </MorphingDialog>
    );
});
