import { getClientIcon } from '@/lib/client-icons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

// ClientIconBadge 在模型图标右下角叠加客户端标识图标，鼠标悬停显示客户端名。
// clientName 为空或未识别时返回 null（不渲染）。
export function ClientIconBadge({ clientName, className }: { clientName: string; className?: string }) {
    const config = getClientIcon(clientName);
    if (!config) return null;
    const { Icon, className: iconClassName, color, label } = config;
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span
                    className={cn('inline-flex items-center justify-center rounded-full bg-card border border-border shadow-sm', className)}
                    style={{ color }}
                >
                    <Icon className={cn('size-full', iconClassName)} />
                </span>
            </TooltipTrigger>
            <TooltipContent>{label}</TooltipContent>
        </Tooltip>
    );
}
