import type { ComponentType } from 'react';
// 客户端品牌图标：优先复用上游 @thesvg/react 品牌 SVG（与 model-icons.tsx 同机制），
// 无品牌图标的客户端用 lucide-react 兜底。label 为静态显示名（产品名多为专有名词，不走 i18n）。
import ClaudeIcon from '@thesvg/react/claude';
import ClineIcon from '@thesvg/react/cline';
import CursorIcon from '@thesvg/react/cursor';
import WindsurfIcon from '@thesvg/react/windsurf';
import CopilotIcon from '@thesvg/react/github-copilot';
import CodexIcon from '@thesvg/react/codex';
import ContinueIcon from '@thesvg/react/continue';
import AmazonQIcon from '@thesvg/react/amazon-q';
import AmpIcon from '@thesvg/react/amp';
import GeminiIcon from '@thesvg/react/gemini';
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
import AlibabaIcon from '@thesvg/react/alibaba';
import AnthropicIcon from '@thesvg/react/anthropic';
import OpenAIIcon from '@thesvg/react/openai';
import { Bot, Terminal, Globe, FileCode, GitBranch, Diamond, Cog, Bird, Feather, Binary, Scan, Puzzle } from 'lucide-react';

// ClientIconConfig 客户端图标配置：图标组件、可选样式类、品牌色、显示名。
type ClientIconConfig = {
    Icon: ComponentType<{ className?: string }>;
    className?: string;
    color: string;
    label: string;
};

// CLIENT_ICON_CONFIGS 按 client_name（与后端 DetectClient 返回值一致）索引的客户端图标配置。
const CLIENT_ICON_CONFIGS: Record<string, ClientIconConfig> = {
    'claude-code': { Icon: ClaudeIcon, color: '#D7765A', label: 'Claude Code' },
    'cline': { Icon: ClineIcon, color: '#6B4EFF', label: 'Cline' },
    'roo-code': { Icon: Bot, color: '#8B5CF6', label: 'Roo Code' },
    'cursor': { Icon: CursorIcon, color: '#6366F1', label: 'Cursor' },
    'windsurf': { Icon: WindsurfIcon, color: '#09B6A2', label: 'Windsurf' },
    'copilot': { Icon: CopilotIcon, color: '#000000', className: 'brightness-0 dark:invert', label: 'GitHub Copilot' },
    'aider': { Icon: Terminal, color: '#FF6B00', label: 'Aider' },
    'codex': { Icon: CodexIcon, color: '#10A37F', label: 'Codex' },
    'continue': { Icon: ContinueIcon, color: '#6366F1', label: 'Continue' },
    'amazon-q': { Icon: AmazonQIcon, color: '#FF9900', label: 'Amazon Q' },
    'augment': { Icon: Globe, color: '#7C3AED', label: 'Augment' },
    'amp': { Icon: AmpIcon, color: '#F59E0B', label: 'Amp' },
    'auto-coder': { Icon: FileCode, color: '#3B82F6', label: 'AutoCoder' },
    'codebuddy': { Icon: Bot, color: '#22C55E', label: 'CodeBuddy' },
    'codebuff': { Icon: GitBranch, color: '#F97316', label: 'Codebuff' },
    'codegpt': { Icon: Bot, color: '#10A37F', label: 'CodeGPT' },
    'crush': { Icon: Diamond, color: '#EF4444', label: 'Crush' },
    'factory-droid': { Icon: Cog, color: '#6366F1', label: 'Factory Droid' },
    'gemini-cli': { Icon: GeminiIcon, color: '#4285F4', label: 'Gemini CLI' },
    'gemini-code-assist': { Icon: GeminiIcon, color: '#4285F4', label: 'Gemini Code Assist' },
    'goose': { Icon: Bird, color: '#F59E0B', label: 'Goose' },
    'jules': { Icon: Feather, color: '#4285F4', label: 'Jules' },
    'junie': { Icon: JunieIcon, color: '#8B5CF6', label: 'Junie' },
    'kilo-code': { Icon: KiloCodeIcon, color: '#22C55E', label: 'Kilo Code' },
    'kiro': { Icon: KiroIcon, color: '#3B82F6', label: 'Kiro' },
    'opencode': { Icon: OpenCodeIcon, color: '#6366F1', label: 'OpenCode' },
    'openhands': { Icon: OpenHandsIcon, color: '#F97316', label: 'OpenHands' },
    'qoder': { Icon: Binary, color: '#8B5CF6', label: 'Qoder' },
    'qwen-code': { Icon: QwenIcon, color: '#6B4EFF', label: 'Qwen Code' },
    'replit': { Icon: ReplitIcon, color: '#F26207', label: 'Replit' },
    'rovidev': { Icon: Scan, color: '#3B82F6', label: 'Rovo Dev' },
    'tabnine': { Icon: Puzzle, color: '#6B4EFF', label: 'Tabnine' },
    'trae': { Icon: TraeIcon, color: '#10A981', label: 'Trae' },
    'warp': { Icon: WarpIcon, color: '#01A4FF', label: 'Warp' },
    'zed': { Icon: ZedIcon, color: '#E8E8E8', label: 'Zed' },
    'baidu-comate': { Icon: BaiduIcon, color: '#2932E1', label: 'Baidu Comate' },
    'tongyi-lingma': { Icon: AlibabaIcon, color: '#6B4EFF', label: 'Tongyi Lingma' },
    'anthropic-ts': { Icon: AnthropicIcon, color: '#D7765A', label: 'Anthropic TS SDK' },
    'openai-python': { Icon: OpenAIIcon, color: '#10A37F', label: 'OpenAI Python SDK' },
    'openai-js': { Icon: OpenAIIcon, color: '#10A37F', label: 'OpenAI JS SDK' },
};

// getClientIcon 按 client_name 取图标配置，未识别返回 null（调用方不渲染徽标）。
export function getClientIcon(clientName: string): ClientIconConfig | null {
    return CLIENT_ICON_CONFIGS[clientName] ?? null;
}
