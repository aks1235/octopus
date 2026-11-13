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
// MESSAGE_TEXT_LIMIT 单条消息正文/单段响应正文的截断上限(与既有单条展开时的上限一致)。
const MESSAGE_TEXT_LIMIT = 20000;
// SECTION_LIMIT 分段上限: 探测日志一个协议一段(最多三种), 留一格余量。
const SECTION_LIMIT = 4;

// TOOL_ROLES 是"机器之间"的消息角色: 工具执行结果不是这一轮人说的话, 排查时只会挤占视线。
const TOOL_ROLES = new Set(['tool', 'function']);
// NON_TEXT_BLOCK_TYPES 是内容数组里不按正文展示的块: 工具调用/工具结果/图片/思考过程。
// 只认 type 明确声明的块; 无 type 但带 text 的块(部分客户端)仍按正文处理。
const NON_TEXT_BLOCK_TYPES = new Set([
    'tool_result',
    'tool_use',
    'tool_calls',
    'function_call',
    'function_call_output',
    'image',
    'image_url',
    'input_image',
    'input_audio',
    'document',
    'thinking',
    'redacted_thinking',
]);

// ExtractedMessage 是抽取出来要展示的一条消息。
interface ExtractedMessage {
    role: string;
    text: string;
}

// ContentSection 是内容里的一段: 普通请求只有一段(label 为空), 探测日志按协议分段。
interface ContentSection {
    label: string;
    messages: ExtractedMessage[];
    count: number;
    /** hidden 是本段里因为属于工具调用/工具结果而没有展示的消息条数, 用于向用户交代"另外几条去哪了"。 */
    hidden: number;
    text: string;
}

// formatSizeBytes 将字节数格式化为 KB/MB 摘要文本。
function formatSizeBytes(bytes: number): string {
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
}

// messageTextOf 从一条消息的 content 取可读正文: 字符串直接用; 数组只拼 text 段,
// 工具结果/工具调用/图片等块一律跳过(它们的载荷是终端输出或二进制, 不是对话内容)。
function messageTextOf(content: unknown): string {
    if (typeof content === 'string') return content;
    if (!Array.isArray(content)) return '';
    const parts: string[] = [];
    for (const part of content) {
        if (typeof part !== 'object' || part === null) continue;
        const { type, text } = part as { type?: unknown; text?: unknown };
        if (typeof type === 'string' && NON_TEXT_BLOCK_TYPES.has(type)) continue;
        if (typeof text === 'string') parts.push(text);
    }
    return parts.join('\n');
}

// hasNonTextBlock 判断一条消息的内容里是否只有工具/图片类块(这类消息过滤后正文为空)。
function hasNonTextBlock(content: unknown): boolean {
    if (!Array.isArray(content)) return false;
    return content.some((part) => (
        typeof part === 'object'
        && part !== null
        && typeof (part as { type?: unknown }).type === 'string'
        && NON_TEXT_BLOCK_TYPES.has((part as { type: string }).type)
    ));
}

// responsesInputToMessages 把 Responses API 的 input 展开成消息数组:
// 字符串视作一条 user 消息; 数组本身已是消息形状(input_text 块带 text, 由 messageTextOf 取正文)。
function responsesInputToMessages(input: unknown): unknown[] | null {
    if (typeof input === 'string') return [{ role: 'user', content: input }];
    if (Array.isArray(input)) return input;
    return null;
}

// parseJsonMaybe 把"可能是 JSON 字符串"的字段解开; 解不开就原样返回字符串。
function parseJsonMaybe(value: unknown): unknown {
    if (typeof value !== 'string') return value;
    const trimmed = value.trim();
    if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return value;
    try {
        return JSON.parse(trimmed) as unknown;
    } catch {
        return value;
    }
}

// requestMessagesOf 按「本次请求新增的上下文」抽取要展示的消息(用户看日志要的是这一轮新带来的内容):
//   1. 工具结果消息(tool/function)整条丢弃, 工具结果块在 messageTextOf 里已跳过;
//   2. 没有 assistant 消息(首轮请求)→ 展示全部消息(去掉纯空内容的), system + user 两条都能看到;
//   3. 有 assistant → 只展示最后一条 assistant 之后的消息(本次新增的尾巴);
//   4. 不额外补 head 的 system: 首轮已由规则 2 覆盖, 无条件补会把 agent 工具插入的 system 类内容
//      (如 system-reminder)也带进来, 用户明确反对;
//   5. 尾巴内容全空(纯 tool_result 块等)→ 回退展示最近一条有正文的 user 消息(保持既有行为);
//   6. 条数上限 MESSAGE_LIMIT(取最靠近末尾的), 单条正文截断 MESSAGE_TEXT_LIMIT。
function requestMessagesOf(body: unknown): { messages: ExtractedMessage[]; count: number; hidden: number } | null {
    if (typeof body !== 'object' || body === null) return null;
    const rawMessages = (body as { messages?: unknown }).messages
        ?? responsesInputToMessages((body as { input?: unknown }).input);
    if (!Array.isArray(rawMessages) || rawMessages.length === 0) return null;

    const messages: ExtractedMessage[] = [];
    let hidden = 0;
    for (const raw of rawMessages) {
        if (typeof raw !== 'object' || raw === null) continue;
        const { role, content: messageContent } = raw as { role?: unknown; content?: unknown };
        const name = typeof role === 'string' ? role : '';
        // 工具结果消息整条不出现在对话里, 但要把条数记下来, 免得"共 N 条"与实际展示对不上。
        if (TOOL_ROLES.has(name)) {
            hidden += 1;
            continue;
        }
        const text = messageTextOf(messageContent).trim();
        if (text === '' && hasNonTextBlock(messageContent)) hidden += 1;
        messages.push({ role: name, text });
    }
    if (messages.length === 0) return null;

    // 规则 2/3: 定位最后一条 assistant, 决定取「全部」还是「尾巴」。
    let lastAssistant = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].role === 'assistant') {
            lastAssistant = i;
            break;
        }
    }
    let picked = (lastAssistant === -1 ? messages : messages.slice(lastAssistant + 1)).filter((message) => message.text !== '');

    // 规则 5: 尾巴全空(纯 tool_result 块等)时回退最近一条有正文的 user 消息。
    if (picked.length === 0) {
        for (let i = messages.length - 1; i >= 0; i--) {
            if (messages[i].role === 'user' && messages[i].text !== '') {
                picked = [messages[i]];
                break;
            }
        }
    }
    if (picked.length === 0) return null;

    // 规则 6: 只留最靠近末尾的若干条, 并截断单条正文(超长截断)。
    return {
        messages: picked.slice(-MESSAGE_LIMIT).map((message) => ({
            role: message.role,
            text: message.text.length > MESSAGE_TEXT_LIMIT ? `${message.text.slice(0, MESSAGE_TEXT_LIMIT)}…` : message.text,
        })),
        count: messages.length,
        hidden,
    };
}

// responseTextOf 从响应体里取可读正文, 覆盖三类上游回包:
//   - OpenAI Chat: choices[0].message.content(或 choices[0].text);
//   - Responses API: output_text / output[].content[].text;
//   - Anthropic Messages: content[].text;
// 失败响应没有正文字段, 退而取 error.message / message / detail —— 探测日志里绝大多数是这种。
function responseTextOf(body: unknown): string {
    if (typeof body === 'string') return body.trim();
    if (typeof body !== 'object' || body === null) return '';
    const obj = body as Record<string, unknown>;

    const choices = obj.choices;
    if (Array.isArray(choices) && choices.length > 0) {
        const first = choices[0];
        if (typeof first === 'object' && first !== null) {
            const message = (first as { message?: unknown }).message;
            if (typeof message === 'object' && message !== null) {
                const fromMessage = messageTextOf((message as { content?: unknown }).content).trim();
                if (fromMessage) return fromMessage;
            }
            const choiceText = (first as { text?: unknown }).text;
            if (typeof choiceText === 'string' && choiceText.trim()) return choiceText.trim();
        }
    }

    if (typeof obj.output_text === 'string' && obj.output_text.trim()) return obj.output_text.trim();
    if (Array.isArray(obj.output)) {
        const parts: string[] = [];
        for (const item of obj.output) {
            if (typeof item !== 'object' || item === null) continue;
            const content = (item as { content?: unknown }).content;
            if (!Array.isArray(content)) continue;
            for (const block of content) {
                if (typeof block !== 'object' || block === null) continue;
                const { type, text } = block as { type?: unknown; text?: unknown };
                if (type === 'output_text' && typeof text === 'string') parts.push(text);
            }
        }
        if (parts.join('').trim()) return parts.join('\n').trim();
    }

    const fromContent = messageTextOf(obj.content).trim();
    if (fromContent) return fromContent;

    const error = obj.error;
    if (typeof error === 'string' && error.trim()) return error.trim();
    if (typeof error === 'object' && error !== null) {
        const errorMessage = (error as { message?: unknown }).message;
        if (typeof errorMessage === 'string' && errorMessage.trim()) return errorMessage.trim();
    }
    for (const key of ['message', 'detail', 'title'] as const) {
        const value = obj[key];
        if (typeof value === 'string' && value.trim()) return value.trim();
    }
    return '';
}

// sectionOf 把一段已解析的请求体/响应体转成展示分段; 抽不出可读内容时返回 null。
function sectionOf(body: unknown, mode: 'request' | 'response'): Omit<ContentSection, 'label'> | null {
    if (mode === 'request') {
        const extracted = requestMessagesOf(body);
        if (!extracted) return null;
        return { messages: extracted.messages, count: extracted.count, hidden: extracted.hidden, text: '' };
    }
    const text = responseTextOf(body);
    if (!text) return null;
    return {
        messages: [],
        count: 0,
        hidden: 0,
        text: text.length > MESSAGE_TEXT_LIMIT ? `${text.slice(0, MESSAGE_TEXT_LIMIT)}…` : text,
    };
}

// sectionsOf 把内容字符串拆成展示分段, 返回 null 表示抽不出可读内容(调用方退回大小摘要)。
//   1. 解析失败: 请求体看不出结构 → null; 响应体本身就是正文 → 单段纯文本;
//   2. 顶层是数组: 探测日志的形状 [{protocol, body}], 每个协议一段(请求与响应都适用);
//   3. 顶层是对象: 普通转发, 单段。
function sectionsOf(content: string, mode: 'request' | 'response'): ContentSection[] | null {
    let data: unknown;
    try {
        data = JSON.parse(content) as unknown;
    } catch {
        if (mode === 'response') {
            const text = content.length > MESSAGE_TEXT_LIMIT ? `${content.slice(0, MESSAGE_TEXT_LIMIT)}…` : content;
            return [{ label: '', messages: [], count: 0, hidden: 0, text }];
        }
        return null;
    }

    if (Array.isArray(data)) {
        const sections: ContentSection[] = [];
        for (const entry of data.slice(0, SECTION_LIMIT)) {
            if (typeof entry !== 'object' || entry === null) continue;
            const { protocol, body } = entry as { protocol?: unknown; body?: unknown };
            const section = sectionOf(parseJsonMaybe(body), mode);
            if (!section) continue;
            sections.push({ label: typeof protocol === 'string' ? protocol : '', ...section });
        }
        return sections.length > 0 ? sections : null;
    }

    const single = sectionOf(data, mode);
    return single ? [{ label: '', ...single }] : null;
}

// SimpleContentBody 是简洁档: 请求体只画本次新增的对话消息, 响应体只画上游回的正文;
// 探测日志按协议分段。全量内容按需展开, 未挂载时不解析、不渲染。
// 调用方负责在无内容时渲染各自的空态文案, 因此这里的 content 恒为非空字符串。
export interface SimpleContentBodyProps {
    /** 内容主体: request 按 messages 抽「本次请求新增的上下文」, response 抽正文; 两者都自动隐藏工具结果。 */
    content: string;
    mode?: 'request' | 'response';
    /** 点「查看请求体/查看响应体」进入全量档(由调用方切换挂载, 收起即卸载)。 */
    onViewFull: () => void;
}

export function SimpleContentBody({ content, mode = 'request', onViewFull }: SimpleContentBodyProps) {
    const t = useTranslations('log.card');
    const isRequest = mode === 'request';
    // content 不变时(SSE 心跳只更新状态字段)复用上一次的解析结果, 心跳不触发重复 JSON.parse。
    const sections = useMemo(() => sectionsOf(content, mode), [content, mode]);
    // 大小按原文 UTF-8 字节数统计, 不做 JSON 结构分析。
    const sizeText = useMemo(() => formatSizeBytes(new Blob([content]).size), [content]);
    const messageTotal = useMemo(
        () => (sections ?? []).reduce((sum, section) => sum + section.count, 0),
        [sections],
    );
    const hiddenTotal = useMemo(
        () => (sections ?? []).reduce((sum, section) => sum + section.hidden, 0),
        [sections],
    );
    const summaryKey = isRequest ? 'requestBodySummary' : 'responseBodySize';
    const viewKey = isRequest ? 'viewRequestBody' : 'viewResponseBody';

    return (
        <div className="flex h-full flex-col gap-3 p-4">
            {sections ? (
                <div className="min-h-0 flex-1 flex flex-col gap-2">
                    {/* 顶部只留一行摘要: 请求报消息总数, 响应报字节数, 正文本身在下面直接可读。 */}
                    <span className="shrink-0 text-xs text-muted-foreground">
                        {isRequest
                            ? (hiddenTotal > 0
                                ? t('messageCountWithHidden', { count: messageTotal, hidden: hiddenTotal })
                                : t('messageCount', { count: messageTotal }))
                            : t(summaryKey, { size: sizeText })}
                    </span>
                    <div className="min-h-0 flex-1 overflow-auto flex flex-col gap-3">
                        {sections.map((section, sectionIndex) => (
                            <div key={`${section.label}-${sectionIndex}`} className="flex flex-col gap-2">
                                {/* 探测日志按协议分段: 标题写明这是哪个协议端点的收发。 */}
                                {section.label !== '' && (
                                    <Badge variant="outline" className="self-start text-[10px] px-1.5 py-0">
                                        {section.label}
                                    </Badge>
                                )}
                                {isRequest
                                    ? section.messages.map((message, index) => (
                                        <div key={`${message.role}-${index}`} className="rounded-lg bg-muted/50 p-3">
                                            <Badge variant="secondary" className="text-xs">{message.role}</Badge>
                                            <pre className="mt-2 whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">{message.text}</pre>
                                        </div>
                                    ))
                                    : (
                                        <div className="rounded-lg bg-muted/50 p-3">
                                            <pre className="whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">{section.text}</pre>
                                        </div>
                                    )}
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
