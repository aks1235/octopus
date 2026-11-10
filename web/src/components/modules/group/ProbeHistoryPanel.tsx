import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Loader2 } from 'lucide-react';
import { useGroupProbeSummary } from '@/api/group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { cn } from '@/lib/utils';

// 探测历史的可选窗口（天）。默认 30 天：一周太短看不清趋势，90 天对"最近有没有变坏"又太钝。
const windowOptions = [7, 30, 90];

// ProbeHistoryPanel 是分组级的探测历史：一行一个成员，失败多的排前面，点行看单成员明细。
// 与成员级弹窗的关系是"总览 → 明细"，省得为了找一个坏成员把整组 30 个成员逐一点开。
export function ProbeHistoryPanel({
    groupId,
    onOpenMember,
}: {
    groupId: number;
    onOpenMember: (grantId: number) => void;
}) {
    const t = useTranslations('group.probe');
    const [days, setDays] = useState(30);
    const query = useGroupProbeSummary(groupId, days);
    const rows = query.data ?? [];

    return (
        <div className="flex h-full min-h-0 flex-col gap-2">
            <div className="flex items-center justify-between gap-2 px-1">
                <span className="text-xs text-muted-foreground">{t('summaryHint')}</span>
                <Select value={String(days)} onValueChange={(value) => setDays(Number(value))}>
                    <SelectTrigger className="h-7 w-28 rounded-lg text-xs">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        {windowOptions.map((option) => (
                            <SelectItem key={option} value={String(option)}>
                                {t('window', { days: option })}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto rounded-xl border border-border/60">
                {query.isLoading && (
                    <div className="flex items-center gap-2 px-3 py-3 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}
                {!query.isLoading && rows.length === 0 && (
                    <p className="px-3 py-6 text-center text-sm text-muted-foreground">{t('summaryEmpty')}</p>
                )}
                {rows.map((row) => (
                    <button
                        key={row.grant_id}
                        type="button"
                        onClick={() => onOpenMember(row.grant_id)}
                        className="flex w-full items-start justify-between gap-3 border-b border-border/40 px-3 py-2 text-left transition-colors last:border-b-0 hover:bg-muted/60"
                    >
                        <span className="flex min-w-0 flex-col">
                            <span className="truncate text-sm font-medium">{row.model_name}</span>
                            <span className="truncate text-[11px] text-muted-foreground">
                                {row.channel_name} · {row.key_name}
                            </span>
                            {row.last_error && (
                                <span className="mt-0.5 truncate text-[11px] text-destructive/80">{row.last_error}</span>
                            )}
                        </span>
                        <span className="flex shrink-0 items-center gap-1 text-xs tabular-nums">
                            <span className="text-emerald-600 dark:text-emerald-500">{row.ok}</span>
                            <span className="text-muted-foreground">/</span>
                            <span className={cn(row.failed > 0 ? 'text-destructive' : 'text-muted-foreground')}>
                                {row.failed}
                            </span>
                        </span>
                    </button>
                ))}
            </div>
        </div>
    );
}
