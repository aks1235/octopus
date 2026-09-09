import { useState } from 'react';
import { Loader2, Logs } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useLogs } from '@/api/log';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { LogCard } from './Item';
import { HistoryPanel } from './HistoryPanel';

/** 实时区: 进程内日志概览, 按 RequestID 实时更新卡片(上游原生形态, 零改动)。 */
function LivePanel() {
    const t = useTranslations('log');
    const { logs, isLoading, error } = useLogs();

    if (isLoading) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (logs.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                {!error && <Logs className="size-8" />}
                <span className="text-sm">{error ? t('list.disconnected') : t('list.empty')}</span>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {error && (
                <div className="flex shrink-0 items-center justify-center px-1 pb-3 text-xs text-destructive">
                    <span>{t('list.disconnected')}</span>
                </div>
            )}
            <div className="min-h-0 flex-1">
                <VirtualizedGrid
                    items={logs}
                    layout="list"
                    columns={{ default: 1 }}
                    estimateItemHeight={104}
                    overscan={8}
                    getItemKey={(log) => `log-${log.id}`}
                    renderItem={(log) => <LogCard log={log} />}
                />
            </div>
        </div>
    );
}

// Log 页: 实时流(进程内概览) + 历史(持久化日志回溯)两个视角。
export function Log() {
    const t = useTranslations('log');
    const [tab, setTab] = useState<'live' | 'history'>('live');

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <Tabs value={tab} onValueChange={(value) => setTab(value as 'live' | 'history')} className="flex h-full min-h-0 flex-col gap-3">
                <TabsList variant="text" className="p-0 self-start shrink-0">
                    <TabsTrigger value="live">{t('tabs.live')}</TabsTrigger>
                    <TabsTrigger value="history">{t('tabs.history')}</TabsTrigger>
                </TabsList>
                <div className="min-h-0 flex-1">
                    {tab === 'live' && <LivePanel />}
                    {tab === 'history' && <HistoryPanel />}
                </div>
            </Tabs>
        </div>
    );
}
