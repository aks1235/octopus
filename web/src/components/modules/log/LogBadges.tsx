import { useTranslations } from 'use-intl';
import { Protocol } from '@/api/channel';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

const protocolLabels: Record<number, string> = {
    [Protocol.OpenAIChatCompletion]: 'Chat',
    [Protocol.OpenAIResponse]: 'Response',
    [Protocol.AnthropicMessage]: 'Message',
};

export function ProtocolLabel({ protocol }: { protocol?: number }) {
    return <span className="shrink-0 text-xs text-muted-foreground/70">{protocolLabels[protocol ?? 0] ?? '-'}</span>;
}

export function StreamingBadge({ streaming }: { streaming?: boolean | null }) {
    const t = useTranslations('log.card');
    const known = typeof streaming === 'boolean';
    return (
        <Badge
            variant="secondary"
            className={cn(
                'shrink-0 gap-1 text-xs px-1.5 py-0',
                !known ? 'bg-muted text-muted-foreground'
                    : streaming ? 'bg-green-500/15 text-green-600 dark:text-green-400'
                        : 'bg-blue-500/15 text-blue-600 dark:text-blue-400',
            )}
        >
            {known && <span aria-hidden="true" className={cn('size-1.5 rounded-full', streaming ? 'bg-green-500' : 'bg-blue-500')} />}
            {!known ? t('streamingUnknown') : streaming ? t('streaming') : t('nonStreaming')}
        </Badge>
    );
}
