import type { ComponentType, CSSProperties, SVGProps } from 'react';
import { useTranslations } from 'use-intl';
import {
    Terminal, Bot, Globe, FileCode, GitBranch,
    Diamond, Cog, Bird, Binary, Scan, Puzzle, Moon, Brain,
    type LucideIcon,
} from 'lucide-react';
import ClaudeCodeIcon from '@thesvg/react/claude-code';
import ClineIcon from '@thesvg/react/cline';
import CodexIcon from '@thesvg/react/codex';
import ContinueIcon from '@thesvg/react/continue';
import CursorIcon from '@thesvg/react/cursor';
import WindsurfIcon from '@thesvg/react/windsurf';
import GitHubCopilotIcon from '@thesvg/react/github-copilot';
import AmazonQIcon from '@thesvg/react/amazon-q';
import AmpIcon from '@thesvg/react/amp';
import GeminiCliIcon from '@thesvg/react/gemini-cli';
import GeminiIcon from '@thesvg/react/gemini';
import GoogleJulesIcon from '@thesvg/react/google-jules';
import JunieIcon from '@thesvg/react/junie';
import KiloCodeIcon from '@thesvg/react/kilo-code';
import KiroIcon from '@thesvg/react/kiro';
import OpenCodeIcon from '@thesvg/react/opencode';
import OpenHandsIcon from '@thesvg/react/openhands';
import QwenIcon from '@thesvg/react/qwen';
import ReplitIcon from '@thesvg/react/replit';
import TraeIcon from '@thesvg/react/trae';
import WarpIcon from '@thesvg/react/warp';
import ZedIcon from '@thesvg/react/zed';
import BaiduIcon from '@thesvg/react/baidu';
import AnthropicIcon from '@thesvg/react/anthropic';
import OpenAIIcon from '@thesvg/react/openai';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

// thesvg 官方品牌图标: default 变体的原始填色不一致(白/黑/currentColor 混杂),
// 由 globals.css 的 .client-icon-badge 规则统一强制为 currentColor 单色, 颜色由配置给出。
type OfficialClientConfig = {
    Icon: ComponentType<SVGProps<SVGSVGElement>>;
    color: string;
    labelKey: string;
};

// 官方图标缺失的客户端, 以 lucide 通用图标 + 品牌色兜底。
type FallbackClientConfig = {
    icon: LucideIcon;
    color: string;
    labelKey: string;
};

// 客户端标识 → 官方图标, 名称与后端 detectClient 规则表一一对应; 无官方图标的客户端不在此表。
const OFFICIAL_CLIENT_CONFIGS: Record<string, OfficialClientConfig> = {
    'claude-code': { Icon: ClaudeCodeIcon, color: '#D97757', labelKey: 'claudeCode' },
    'cline': { Icon: ClineIcon, color: '#8A9BFF', labelKey: 'cline' },
    'codex': { Icon: CodexIcon, color: '#10A37F', labelKey: 'codex' },
    'continue': { Icon: ContinueIcon, color: '#6366F1', labelKey: 'continue' },
    'cursor': { Icon: CursorIcon, color: '#6366F1', labelKey: 'cursor' }, // 品牌黑白标在明暗两态不可兼读, 沿用 fork 靛蓝
    'windsurf': { Icon: WindsurfIcon, color: '#09B6A2', labelKey: 'windsurf' }, // codeium 同品牌归并
    'copilot': { Icon: GitHubCopilotIcon, color: '#6E7E91', labelKey: 'copilot' },
    'amazon-q': { Icon: AmazonQIcon, color: '#FF9900', labelKey: 'amazonQ' },
    'amp': { Icon: AmpIcon, color: '#F59E0B', labelKey: 'amp' },
    'gemini-cli': { Icon: GeminiCliIcon, color: '#4285F4', labelKey: 'geminiCli' },
    'gemini-code-assist': { Icon: GeminiIcon, color: '#4285F4', labelKey: 'geminiCodeAssist' },
    'jules': { Icon: GoogleJulesIcon, color: '#4285F4', labelKey: 'jules' },
    'junie': { Icon: JunieIcon, color: '#8B5CF6', labelKey: 'junie' },
    'kilo-code': { Icon: KiloCodeIcon, color: '#22C55E', labelKey: 'kiloCode' },
    'kiro': { Icon: KiroIcon, color: '#3B82F6', labelKey: 'kiro' },
    'opencode': { Icon: OpenCodeIcon, color: '#6366F1', labelKey: 'opencode' },
    'openhands': { Icon: OpenHandsIcon, color: '#F97316', labelKey: 'openhands' },
    'qwen-code': { Icon: QwenIcon, color: '#6B4EFF', labelKey: 'qwenCode' },
    'replit': { Icon: ReplitIcon, color: '#F26207', labelKey: 'replit' },
    'trae': { Icon: TraeIcon, color: '#10B981', labelKey: 'trae' },
    'warp': { Icon: WarpIcon, color: '#01A4FF', labelKey: 'warp' },
    'zed': { Icon: ZedIcon, color: '#8B8B8B', labelKey: 'zed' },
    'baidu-comate': { Icon: BaiduIcon, color: '#2932E1', labelKey: 'baiduComate' },
    'anthropic-ts': { Icon: AnthropicIcon, color: '#D97757', labelKey: 'anthropicTs' },
    'openai-python': { Icon: OpenAIIcon, color: '#10A37F', labelKey: 'openaiPython' },
    'openai-js': { Icon: OpenAIIcon, color: '#10A37F', labelKey: 'openaiJs' },
};

// lucide 兜底表: 仅收录官方图标缺失的客户端。
const FALLBACK_CLIENT_CONFIGS: Record<string, FallbackClientConfig> = {
    'roo-code': { icon: Bot, color: '#8B5CF6', labelKey: 'rooCode' },
    'aider': { icon: Terminal, color: '#FF6B00', labelKey: 'aider' },
    'augment': { icon: Globe, color: '#7C3AED', labelKey: 'augment' },
    'auto-coder': { icon: FileCode, color: '#3B82F6', labelKey: 'autoCoder' },
    'codebuddy': { icon: Bot, color: '#22C55E', labelKey: 'codebuddy' },
    'codebuff': { icon: GitBranch, color: '#F97316', labelKey: 'codebuff' },
    'codegpt': { icon: Bot, color: '#10A37F', labelKey: 'codegpt' },
    'crush': { icon: Diamond, color: '#EF4444', labelKey: 'crush' },
    'factory-droid': { icon: Cog, color: '#6366F1', labelKey: 'factoryDroid' },
    'goose': { icon: Bird, color: '#F59E0B', labelKey: 'goose' },
    'qoder': { icon: Binary, color: '#8B5CF6', labelKey: 'qoder' },
    'rovidev': { icon: Scan, color: '#3B82F6', labelKey: 'rovidev' },
    'tabnine': { icon: Puzzle, color: '#6B4EFF', labelKey: 'tabnine' },
    'tongyi-lingma': { icon: Moon, color: '#6B4EFF', labelKey: 'tongyiLingma' },
};

// ClientIconBadge 展示请求来源客户端的图标, 未识别(空串)时不渲染。
// 优先官方品牌图标(@thesvg/react mono 变体), 缺失时以 lucide 通用图标兜底。
export function ClientIconBadge({ clientName, className }: { clientName?: string; className?: string }) {
    const t = useTranslations('log.client');

    const official = clientName ? OFFICIAL_CLIENT_CONFIGS[clientName] : undefined;
    const fallback = clientName ? FALLBACK_CLIENT_CONFIGS[clientName] : undefined;
    if (!official && !fallback) return null;

    const style: CSSProperties = { color: (official ?? fallback)!.color };
    const content = official
        ? <official.Icon className="client-icon-badge size-full" style={style} />
        : (() => {
            const Icon = fallback!.icon;
            return <Icon className="size-full" style={style} />;
        })();

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span
                    className={`inline-flex items-center justify-center shrink-0 ${className ?? 'size-4'}`}
                >
                    {content}
                </span>
            </TooltipTrigger>
            <TooltipContent side="top" sideOffset={4}>
                {t((official ?? fallback)!.labelKey)}
            </TooltipContent>
        </Tooltip>
    );
}

// 思考等级配色, 移植自 fork c912f5e/7a37d98(含 xhigh)。
const REASONING_EFFORT_COLORS: Record<string, string> = {
    low: '#6b7280',
    medium: '#f59e0b',
    high: '#8b5cf6',
    xhigh: '#e11d48',
    max: '#dc2626',
};

// ReasoningEffortBadge 展示请求实际携带的思考等级, 非推理请求(空串)不渲染。
export function ReasoningEffortBadge({ effort, className }: { effort?: string; className?: string }) {
    if (!effort) return null;
    const color = REASONING_EFFORT_COLORS[effort] ?? '#6b7280';

    return (
        <span
            className={`inline-flex items-center gap-0.5 shrink-0 rounded-full px-1.5 py-0 text-[10px] font-medium leading-4 ${className ?? ''}`}
            style={{ backgroundColor: `${color}1a`, color }}
        >
            <Brain className="size-2.5" />
            {effort}
        </span>
    );
}
