import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, Send, MessageSquare, AlertCircle, ChevronDown, Clock, Coins, RotateCw, CheckCircle2, XCircle } from 'lucide-react';
import JsonView from '@uiw/react-json-view';
import { githubDarkTheme } from '@uiw/react-json-view/githubDark';
import { githubLightTheme } from '@uiw/react-json-view/githubLight';
import { motion, AnimatePresence } from 'motion/react';
import { getLogDetail } from '@/api/log-history';
import {
    DialogRoot,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
} from '@/components/ui/dialog';
import { Badge } from '@/components/ui/badge';
import { useTheme } from '@/provider/theme';
import { cn } from '@/lib/utils';

/**
 * 可复用的「单条请求详情」受控弹窗。
 *
 * 设计取舍:log 页的详情渲染构建在 MorphingDialog 之上(context 门控 + 布局动画),
 * 跨页复用需解开 morph 耦合,回归面大。本组件自含渲染:用 getLogDetail(requestId)
 * 拿同一份数据,以同款 JsonView 主题渲染,不改上游 Item.tsx,零回归。
 *
 * 数据层用 react-query(项目惯用法),避免 effect 内同步 setState。
 */
export interface RequestDetailDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    requestId: number | null;
}

export function RequestDetailDialog({ open, onOpenChange, requestId }: RequestDetailDialogProps) {
    const t = useTranslations('log.card');
    const tLog = useTranslations('log.channelAttempts');
    const [debugExpanded, setDebugExpanded] = useState(false);

    // 弹窗打开且有 requestId 时下拉单条详情;关闭则禁用查询(不占请求)。
    // queryKey 挂在 ['log-history'] 前缀下: 清空历史(invalidate ['log-history'])时缓存详情一并失效。
    const detailQuery = useQuery({
        queryKey: ['log-history', 'detail', requestId],
        enabled: open && requestId != null,
        queryFn: () => getLogDetail(requestId as number),
        staleTime: 30_000,
    });

    const detail = detailQuery.data ?? null;
    const loading = detailQuery.isLoading;
    const error = detailQuery.isError;

    const { resolvedTheme } = useTheme();
    const isDark = resolvedTheme === 'dark';

    const renderContent = (content: string | undefined, loadingState: boolean, fallbackText: string) => {
        if (loadingState) {
            return (
                <div className="p-4 flex items-center justify-center h-full">
                    <Loader2 className="h-5 w-5 text-muted-foreground animate-spin" />
                </div>
            );
        }
        if (!content) {
            return <div className="p-4 text-sm text-muted-foreground">{fallbackText}</div>;
        }
        let parsed: unknown;
        let isJson = true;
        try {
            parsed = JSON.parse(content);
        } catch {
            // 非 JSON 内容按纯文本渲染
            parsed = content;
            isJson = false;
        }
        if (isJson) {
            return (
                <div className="p-3 overflow-auto h-full">
                    <JsonView
                        value={parsed as object}
                        style={isDark ? githubDarkTheme : githubLightTheme}
                        collapsed={2}
                    />
                </div>
            );
        }
        return (
            <pre className="p-4 whitespace-pre-wrap break-words leading-relaxed text-muted-foreground font-mono text-xs overflow-auto h-full">
                {content}
            </pre>
        );
    };

    return (
        <DialogRoot open={open} onOpenChange={onOpenChange}>
            <DialogContent className="!max-w-[calc(100vw-2rem)] md:!max-w-[80vw] !w-full h-[calc(100vh-2rem)] !p-0 !gap-0 flex flex-col overflow-hidden">
                <DialogHeader className="px-6 py-4 border-b border-border shrink-0">
                    <DialogTitle className="text-sm flex items-center gap-2">
                        <span>{tLog('requestTitle')}</span>
                        {requestId != null && (
                            <Badge variant="secondary" className="text-xs font-mono">#{requestId}</Badge>
                        )}
                    </DialogTitle>
                    <DialogDescription className="sr-only">{tLog('requestTitle')}</DialogDescription>
                </DialogHeader>

                {loading && (
                    <div className="flex-1 flex items-center justify-center">
                        <Loader2 className="h-6 w-6 text-muted-foreground animate-spin" />
                    </div>
                )}
                {error && !loading && (
                    <div className="flex-1 flex items-center justify-center text-sm text-muted-foreground">
                        {tLog('loadFailed')}
                    </div>
                )}

                {!loading && !error && detail && (
                    <div className="flex-1 min-h-0 flex flex-col gap-3 p-6 overflow-hidden">
                        {/* 顶部指标条 */}
                        <div className="flex flex-wrap items-center gap-2 text-xs shrink-0">
                            <Badge variant="secondary" className="gap-1">
                                <Clock className="size-3" /> {detail.use_time}ms
                            </Badge>
                            <Badge variant="secondary">
                                {detail.input_tokens.toLocaleString()} → {detail.output_tokens.toLocaleString()} {t('tokens')}
                            </Badge>
                            <Badge variant="secondary" className="gap-1">
                                <Coins className="size-3" /> ${detail.cost.toFixed(4)}
                            </Badge>
                            <span className="text-muted-foreground ml-auto truncate">{detail.request_model_name}</span>
                        </div>

                        {detail.error && (
                            <div className="shrink-0 rounded-xl border border-destructive/40 bg-destructive/10 p-3 text-xs">
                                <div className="flex items-center gap-1.5 font-medium text-destructive mb-1">
                                    <AlertCircle className="size-3.5" /> {t('errorInfo')}
                                </div>
                                <pre className="whitespace-pre-wrap break-words text-destructive/90 font-mono">{detail.error}</pre>
                            </div>
                        )}

                        {/* 双栏请求/响应 */}
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-3 min-h-0 flex-1">
                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                                <div className="flex items-center gap-2 px-3 py-2.5 border-b border-border bg-muted/50 shrink-0">
                                    <Send className="size-4 text-green-500" />
                                    <span className="text-sm font-medium">{t('requestContent')}</span>
                                </div>
                                <div className="flex-1 overflow-auto min-h-0">
                                    {renderContent(detail.request_content, false, t('noRequestContent'))}
                                </div>
                            </div>
                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden min-h-0">
                                <div className="flex items-center gap-2 px-3 py-2.5 border-b border-border bg-muted/50 shrink-0">
                                    <MessageSquare className="size-4 text-purple-500" />
                                    <span className="text-sm font-medium">{t('responseContent')}</span>
                                </div>
                                <div className="flex-1 overflow-auto min-h-0">
                                    {renderContent(detail.response_content, false, t('noResponseContent'))}
                                </div>
                            </div>
                        </div>

                        {/* 故障转移链路: 逐次尝试的渠道/协议/状态/耗时/失败原因, 请求经历了几轮一目了然。 */}
                        {detail.attempts && detail.attempts.length > 1 && (
                            <div className="shrink-0 rounded-2xl border border-border bg-muted/30 overflow-hidden">
                                <div className="flex items-center gap-2 px-4 py-2.5 border-b border-border bg-muted/50">
                                    <RotateCw className="size-4 text-amber-500" />
                                    <span className="text-sm font-medium">{tLog('retryDetails')}</span>
                                    <Badge variant="outline" className="text-xs">{detail.attempts.length} {tLog('attemptCount')}</Badge>
                                </div>
                                <div className="divide-y divide-border max-h-[200px] overflow-auto">
                                    {detail.attempts.map((a, i) => (
                                        <div key={i} className="flex items-center gap-2 px-4 py-2 text-xs">
                                            {a.status === 'success' ? (
                                                <CheckCircle2 className="size-3.5 shrink-0 text-emerald-500" />
                                            ) : (
                                                <XCircle className="size-3.5 shrink-0 text-destructive" />
                                            )}
                                            <span className="font-medium shrink-0">{a.channel_name}</span>
                                            {a.channel_key_remark && a.channel_key_remark !== 'default' && (
                                                <span className="text-muted-foreground shrink-0">· {a.channel_key_remark}</span>
                                            )}
                                            <span className="text-muted-foreground shrink-0">{a.duration}ms</span>
                                            {a.msg && (
                                                <span className="text-destructive/80 truncate min-w-0 flex-1" title={a.msg}>{a.msg}</span>
                                            )}
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}

                        {/* 调试信息折叠 */}
                        {detail.debug_content && (
                            <div className="flex flex-col shrink-0 rounded-2xl border border-border bg-muted/30 overflow-hidden max-h-[200px]">
                                <button
                                    type="button"
                                    onClick={() => setDebugExpanded((v) => !v)}
                                    className="flex items-center gap-2 px-4 py-2.5 border-b border-border bg-muted/50 w-full text-left cursor-pointer select-none hover:bg-muted/70 transition-colors shrink-0"
                                >
                                    <AlertCircle className="size-4 text-amber-500" />
                                    <span className="text-sm font-medium">{tLog('debugInfo')}</span>
                                    <ChevronDown className={cn('size-4 ml-auto text-muted-foreground transition-transform duration-200', debugExpanded && 'rotate-180')} />
                                </button>
                                <AnimatePresence initial={false}>
                                    {debugExpanded && (
                                        <motion.div
                                            initial={{ height: 0, opacity: 0 }}
                                            animate={{ height: 'auto', opacity: 1 }}
                                            exit={{ height: 0, opacity: 0 }}
                                            transition={{ duration: 0.2, ease: 'easeInOut' }}
                                            className="overflow-auto"
                                        >
                                            {renderContent(detail.debug_content, false, '')}
                                        </motion.div>
                                    )}
                                </AnimatePresence>
                            </div>
                        )}
                    </div>
                )}
            </DialogContent>
        </DialogRoot>
    );
}
