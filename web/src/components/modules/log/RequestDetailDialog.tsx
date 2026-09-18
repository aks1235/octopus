import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, Send, MessageSquare, AlertCircle, ChevronDown, Clock, Coins, Gauge, RotateCw, CheckCircle2, XCircle } from 'lucide-react';
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
import { Button } from '@/components/ui/button';
import { useTheme } from '@/provider/theme';
import { cn, formatRate, outputSpeed } from '@/lib/utils';

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

// formatSizeBytes 将字节数格式化为 KB/MB 摘要文本。
function formatSizeBytes(bytes: number): string {
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
}

// lastMessageOf 从请求体提取最近一条**用户输入**: 用户看日志要的是自己发了什么,
// 不是工具往返(末条的 tool_result 是模型发起的, agentic 循环里占大多数)。
// 只认 user 角色消息里的真实文本(字符串与 text 段), tool_result 块跳过, 向前回溯;
// 全程没有用户文本或结构不符返回 null, 调用方退回大小摘要。
// 解析与提取在此一次完成, 渲染只落最后一行(超长截断), DOM 恒为小体量。
function lastMessageOf(content: string): { role: string; text: string; count: number } | null {
    let data: unknown;
    try {
        data = JSON.parse(content);
    } catch {
        return null;
    }
    if (typeof data !== 'object' || data === null) return null;
    const messages = (data as { messages?: unknown }).messages;
    if (!Array.isArray(messages) || messages.length === 0) return null;
    for (let i = messages.length - 1; i >= 0; i--) {
        const message = messages[i];
        if (typeof message !== 'object' || message === null) continue;
        const { role, content: messageContent } = message as { role?: unknown; content?: unknown };
        if (role !== 'user') continue;
        let text = '';
        if (typeof messageContent === 'string') {
            text = messageContent;
        } else if (Array.isArray(messageContent)) {
            text = messageContent
                .map((part) => (typeof part === 'object' && part !== null && typeof (part as { text?: unknown }).text === 'string' ? (part as { text: string }).text : ''))
                .filter(Boolean)
                .join('\n');
        }
        text = text.trim();
        if (!text) continue;
        return {
            role: 'user',
            text: text.length > 20000 ? `${text.slice(0, 20000)}…` : text,
            count: messages.length,
        };
    }
    return null;
}

// SimpleRequestBody 简洁档的请求体区: 最后一条消息 + 消息总数 + 大小摘要, 全量按需展开。
function SimpleRequestBody({ content, sizeText, onViewFull }: { content: string; sizeText: string; onViewFull: () => void }) {
    const t = useTranslations('log.card');
    const last = useMemo(() => lastMessageOf(content), [content]);

    return (
        <div className="flex h-full flex-col gap-3 p-4">
            {last ? (
                <div className="min-h-0 flex-1 flex flex-col gap-2">
                    <div className="flex items-center gap-2 shrink-0">
                        <Badge variant="secondary" className="text-xs">{last.role}</Badge>
                        <span className="text-xs text-muted-foreground">{t('messageCount', { count: last.count })}</span>
                    </div>
                    <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-muted/50 p-3 text-xs leading-relaxed text-foreground/90">{last.text}</pre>
                </div>
            ) : (
                <div className="min-h-0 flex-1 flex items-center justify-center text-center">
                    <p className="text-xs text-muted-foreground">{t('requestBodySummary', { size: sizeText })}</p>
                </div>
            )}
            <Button variant="outline" size="sm" className="rounded-xl shrink-0 self-center" onClick={onViewFull}>
                {t('viewRequestBody')}
            </Button>
        </div>
    );
}

export function RequestDetailDialog({ open, onOpenChange, requestId }: RequestDetailDialogProps) {
    const t = useTranslations('log.card');
    const tLog = useTranslations('log.channelAttempts');
    // 简洁模式(默认)不渲染请求体; 「查看请求体」开启后进入调试档, 记忆于会话内即可(不持久化)。
    const [showRequestBody, setShowRequestBody] = useState(false);
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

    // 请求体字节数只按原文统计(不 JSON.parse), 用于默认简洁档的大小摘要。
    const requestBodyBytes = detail?.request_content ? new Blob([detail.request_content]).size : 0;
    // 输出速度由落库字段推导, 无输出 token 或分母无效时不展示。
    const speed = detail ? outputSpeed(detail.output_tokens, detail.use_time, detail.ftut) : null;
    const speedText = speed !== null ? formatRate(speed).formatted : null;

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
                            {speedText && (
                                <Badge variant="secondary" className="gap-1" title={t('speed')}>
                                    <Gauge className="size-3" /> {speedText.value} {speedText.unit}
                                </Badge>
                            )}
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
                                    {/* 展开后提供收起入口; 收起即卸载请求体渲染, 不保留 DOM。 */}
                                    {showRequestBody && (
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            className="ml-auto h-6 rounded-lg px-2 text-xs text-muted-foreground"
                                            onClick={() => setShowRequestBody(false)}
                                        >
                                            {t('hideRequestBody')}
                                        </Button>
                                    )}
                                </div>
                                <div className="flex-1 overflow-auto min-h-0">
                                    {showRequestBody ? (
                                        // 调试档: 渲染完整请求体(与现状一致的 JsonView 折叠视图)。
                                        renderContent(detail.request_content, false, t('noRequestContent'))
                                    ) : detail.request_content ? (
                                        // 简洁档: JSON.parse 便宜(卡顿根因在渲染几千条消息的 DOM), 只渲染
                                        // messages 最后一条(本次请求新增的用户输入)与消息总数; 解析失败或
                                        // 无 messages 结构(embeddings 等)退回大小摘要。全量仍走「查看请求体」。
                                        <SimpleRequestBody
                                            content={detail.request_content}
                                            sizeText={formatSizeBytes(requestBodyBytes)}
                                            onViewFull={() => setShowRequestBody(true)}
                                        />
                                    ) : (
                                        <div className="p-4 text-sm text-muted-foreground">{t('noRequestContent')}</div>
                                    )}
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
