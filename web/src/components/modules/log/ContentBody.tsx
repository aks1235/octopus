import { memo, useMemo } from 'react';
import { useTranslations } from 'use-intl';
import JsonView from '@uiw/react-json-view';
import { githubDarkTheme } from '@uiw/react-json-view/githubDark';
import { githubLightTheme } from '@uiw/react-json-view/githubLight';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { useTheme } from '@/provider/theme';
import { cn } from '@/lib/utils';

/**
 * ContentBody 是历史详情弹窗(RequestDetailDialog)与实时详情弹窗(Item.tsx)共用的请求/响应内容视图。
 *
 * 抽出两档是为了解决同一个卡顿根因: 一个 agent 请求的 messages 动辄几千条,
 * 直接 <JsonView collapsed={false}> 会铺开数万个 DOM 节点, 弹窗一打开就卡。
 * 因此:
 *   - SimpleContentBody(简洁档): 只解析并渲染「本次请求新增的上下文」消息(最多 5 条) + 消息总数
 *     + 大小摘要, DOM 恒为小体量;
 *   - FullContentBody(全量档): JsonView 折叠视图 / 纯文本视图, 以内容字符串为 memo 比较依据。
 * 两档都按需挂载(收起即卸载), 未挂载时不解析、不渲染任何大内容。
 *
 * 单一实现避免历史与实时两侧漂移; 各处的留白/字号差异由调用方通过 props 保留, 行为与改前一致。
 */

// MONO_FONT 与实时档既有的 JsonView 等宽字体栈保持一致。
const MONO_FONT = 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace';

// ParsedContent 是内容解析结果: 能解析成 JSON 时 data 为对象, 否则回退纯文本。
// 做成判别联合, 调用处按 isJson 收窄后即可直接喂给 JsonView(它只接受 object)。
type ParsedContent = { isJson: true; data: object } | { isJson: false; data: string };

// MESSAGE_LIMIT 简洁档最多展示的消息条数; 超出时取最靠近末尾的若干条。
const MESSAGE_LIMIT = 5;
// MESSAGE_TEXT_LIMIT 单条消息正文的截断上限(与既有单条展开时的上限一致)。
const MESSAGE_TEXT_LIMIT = 20000;

// ExtractedMessage 是抽取出来要展示的一条消息。
interface ExtractedMessage {
    role: string;
    text: string;
}

// formatSizeBytes 将字节数格式化为 KB/MB 摘要文本。
function formatSizeBytes(bytes: number): string {
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
}

// messageTextOf 从一条消息的 content 取可读文本: 字符串直接用, 数组拼 text 段
// (tool_result 等无 text 的块产出空串, 由调用方按空内容处理)。
function messageTextOf(content: unknown): string {
    if (typeof content === 'string') return content;
    if (Array.isArray(content)) {
        return content
            .map((part) => (typeof part === 'object' && part !== null && typeof (part as { text?: unknown }).text === 'string' ? (part as { text: string }).text : ''))
            .filter(Boolean)
            .join('\n');
    }
    return '';
}

// contextMessagesOf 按「本次请求新增的上下文」抽取要展示的消息(用户看日志要的是这一轮新带来的内容):
//   1. 没有 assistant 消息(首轮请求)→ 展示全部消息(去掉纯空内容的), system + user 两条都能看到;
//   2. 有 assistant → 只展示最后一条 assistant 之后的消息(本次新增的尾巴);
//   3. 不额外补 head 的 system: 首轮已由规则 1 覆盖, 无条件补会把 agent 工具插入的 system 类内容
//      (如 system-reminder)也带进来, 用户明确反对;
//   4. 尾巴内容全空(纯 tool_result 块等)→ 回退展示最近一条有正文的 user 消息(保持既有行为);
//   5. 条数上限 MESSAGE_LIMIT(取最靠近末尾的), 单条正文截断 MESSAGE_TEXT_LIMIT。
// 解析与截断在此一次完成, 渲染只落这几条, DOM 恒为小体量; 解析失败或结构不符返回 null 由调用方退回大小摘要。
function contextMessagesOf(content: string): { messages: ExtractedMessage[]; count: number } | null {
    let data: unknown;
    try {
        data = JSON.parse(content);
    } catch {
        return null;
    }
    if (typeof data !== 'object' || data === null) return null;
    const rawMessages = (data as { messages?: unknown }).messages;
    if (!Array.isArray(rawMessages) || rawMessages.length === 0) return null;

    const messages: ExtractedMessage[] = [];
    for (const raw of rawMessages) {
        if (typeof raw !== 'object' || raw === null) continue;
        const { role, content: messageContent } = raw as { role?: unknown; content?: unknown };
        messages.push({ role: typeof role === 'string' ? role : '', text: messageTextOf(messageContent).trim() });
    }
    if (messages.length === 0) return null;

    // 规则 1/2: 定位最后一条 assistant, 决定取「全部」还是「尾巴」。
    let lastAssistant = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].role === 'assistant') {
            lastAssistant = i;
            break;
        }
    }
    let picked = (lastAssistant === -1 ? messages : messages.slice(lastAssistant + 1)).filter((message) => message.text !== '');

    // 规则 4: 尾巴全空(纯 tool_result 块等)时回退最近一条有正文的 user 消息。
    if (picked.length === 0) {
        for (let i = messages.length - 1; i >= 0; i--) {
            if (messages[i].role === 'user' && messages[i].text !== '') {
                picked = [messages[i]];
                break;
            }
        }
    }
    if (picked.length === 0) return null;

    // 规则 5: 只留最靠近末尾的若干条, 并截断单条正文(超长截断)。
    return {
        messages: picked.slice(-MESSAGE_LIMIT).map((message) => ({
            role: message.role,
            text: message.text.length > MESSAGE_TEXT_LIMIT ? `${message.text.slice(0, MESSAGE_TEXT_LIMIT)}…` : message.text,
        })),
        count: messages.length,
    };
}

// SimpleContentBody 是简洁档: 请求体只画本次新增的上下文消息 + 消息总数 + 大小摘要, 全量按需展开。
// 调用方负责在无内容时渲染各自的空态文案, 因此这里的 content 恒为非空字符串。
export interface SimpleContentBodyProps {
    content: string;
    /**
     * 内容主体: 请求体(request)按 messages 抽「本次请求新增的上下文」并用请求向文案;
     * 响应体(response)不是消息数组, 只给大小摘要, 且不做 JSON.parse(零解析), 用响应向文案。
     */
    mode?: 'request' | 'response';
    /** 点「查看请求体/查看响应体」进入全量档(由调用方切换挂载, 收起即卸载)。 */
    onViewFull: () => void;
}

export function SimpleContentBody({ content, mode = 'request', onViewFull }: SimpleContentBodyProps) {
    const t = useTranslations('log.card');
    const isRequest = mode === 'request';
    // content 不变时(SSE 心跳只更新状态字段)复用上一次的解析结果, 心跳不触发重复 JSON.parse;
    // 响应体不抽消息, 直接跳过解析。
    const context = useMemo(() => (isRequest ? contextMessagesOf(content) : null), [content, isRequest]);
    // 大小按原文 UTF-8 字节数统计, 不做 JSON 结构分析。
    const sizeText = useMemo(() => formatSizeBytes(new Blob([content]).size), [content]);
    const summaryKey = isRequest ? 'requestBodySummary' : 'responseBodySummary';
    const viewKey = isRequest ? 'viewRequestBody' : 'viewResponseBody';

    return (
        <div className="flex h-full flex-col gap-3 p-4">
            {context ? (
                <div className="min-h-0 flex-1 flex flex-col gap-2">
                    <span className="shrink-0 text-xs text-muted-foreground">{t('messageCount', { count: context.count })}</span>
                    {/* 紧凑消息列表: 每条 role 徽标 + 正文, 各自滚动区共用同一容器。 */}
                    <div className="min-h-0 flex-1 overflow-auto flex flex-col gap-2">
                        {context.messages.map((message, index) => (
                            <div key={`${message.role}-${index}`} className="rounded-lg bg-muted/50 p-3">
                                <Badge variant="secondary" className="text-xs">{message.role}</Badge>
                                <pre className="mt-2 whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">{message.text}</pre>
                            </div>
                        ))}
                    </div>
                </div>
            ) : (
                <div className="min-h-0 flex-1 flex items-center justify-center text-center">
                    <p className="text-xs text-muted-foreground">{t(summaryKey, { size: sizeText })}</p>
                </div>
            )}
            <Button variant="outline" size="sm" className="rounded-xl shrink-0 self-center" onClick={onViewFull}>
                {t(viewKey)}
            </Button>
        </div>
    );
}

// FullContentBody 是全量档: 能解析为 JSON 时用 JsonView 折叠视图, 否则按纯文本展示。
// 调用方负责在无内容时渲染各自的空态文案, 因此这里的 content 恒为非空。
export interface FullContentBodyProps {
    /** 内容: 字符串按 JSON 尝试解析, 已是对象则直接渲染(兼容改前 JsonContent 的入参)。 */
    content: string | object;
    /** JsonView 初始展开深度: 历史调试档用 2(只展开两层, 避免一次铺开全部消息); 实时全量档用 false。 */
    collapsed?: number | boolean;
    /**
     * 实时档的紧凑呈现: 12px 等宽字体 + 透明底 + 入场动画。
     * 历史档不传, 沿用 JsonView 默认字号/底色, 与改前一致。
     */
    dense?: boolean;
    /** 外层容器类名(留白/滚动), 由调用方沿用改前各处的取值。 */
    className?: string;
}

function FullContentBodyImpl({ content, collapsed = 2, dense = false, className }: FullContentBodyProps) {
    const { resolvedTheme } = useTheme();

    // JSON.parse 只在 content 变化时执行一次; 未挂载本组件时(简洁档)零解析。
    // 用判别联合保存解析结果, 便于下面按 isJson 正确收窄 data 的类型。
    const parsed = useMemo<ParsedContent>(() => {
        if (typeof content !== 'string') return { isJson: true, data: content };
        try {
            return { isJson: true, data: JSON.parse(content) as object };
        } catch {
            // 流式内容可能是半截 JSON, 解析失败按纯文本渲染(与改前一致)。
            return { isJson: false, data: content };
        }
    }, [content]);

    if (!parsed.isJson) {
        return (
            <pre className={cn(
                'p-4 whitespace-pre-wrap break-words leading-relaxed text-muted-foreground font-mono text-xs',
                dense ? 'wrap-break-word animate-in fade-in duration-200' : 'overflow-auto h-full',
            )}>
                {parsed.data}
            </pre>
        );
    }

    const theme = resolvedTheme === 'dark' ? githubDarkTheme : githubLightTheme;

    return (
        <div className={cn(className, dense && 'animate-in fade-in duration-200')}>
            <JsonView
                value={parsed.data}
                style={dense ? { ...theme, fontSize: '12px', fontFamily: MONO_FONT, backgroundColor: 'transparent' } : theme}
                displayDataTypes={!dense}
                displayObjectSize={!dense}
                collapsed={collapsed}
            />
        </div>
    );
}

/**
 * memo 比较依据是内容字符串(content)及其余会改变呈现的标量:
 * SSE 心跳只更新日志的状态/指标字段、内容串不变时(react-query 同一 queryKey 返回同一份数据),
 * 这里直接命中缓存跳过重渲染, 已展开的 JSON 大树不重建; 内容真正变化(如流式追加)才重建。
 * 主题切换走 context 更新, 不受 memo 比较影响, 深/浅色仍能即时跟随。
 */
export const FullContentBody = memo(FullContentBodyImpl, (prev, next) => (
    prev.content === next.content
    && prev.collapsed === next.collapsed
    && prev.dense === next.dense
    && prev.className === next.className
));
