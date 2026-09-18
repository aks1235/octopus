import { useMemo, useState } from 'react';
import { Loader2, Logs } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useLogs, type RequestState } from '@/api/log';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { LogCard } from './Item';
import { HistoryPanel } from './HistoryPanel';

/** 实时区: 进程内日志概览, 按 RequestID 实时更新卡片。 */
function LivePanel() {
    const t = useTranslations('log');
    const { logs, isLoading, error } = useLogs();
    // 「只看进行中」是纯前端视图状态, 不影响 SSE 数据流的接收与累积。
    const [onlyRunning, setOnlyRunning] = useState(false);

    // 进行中 = 未取得终态的请求: running(上游未回复)与 committed(流式已开始回复但仍在推送)都算;
    // 只按 running 过滤会把"上游已回复、还在流式中"的长请求漏掉(2026-09-18 用户实测反馈)。
    const isOngoing = (status: RequestState) => status === 'running' || status === 'committed';
    // 进行中数量随 SSE 推送实时变化, 供徽标常显。
    const runningCount = useMemo(
        () => logs.reduce((count, log) => (isOngoing(log.status) ? count + 1 : count), 0),
        [logs],
    );
    const visibleLogs = useMemo(
        () => (onlyRunning ? logs.filter((log) => isOngoing(log.status)) : logs),
        [logs, onlyRunning],
    );

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
            {/* 过滤工具行: 进行中计数徽标常显, 开关控制列表只保留 running 请求。 */}
            <div className="flex shrink-0 items-center gap-3">
                <Badge variant="secondary" className="gap-1.5 text-xs">
                    <Loader2 className={runningCount > 0 ? 'size-3 animate-spin text-blue-500' : 'size-3 text-muted-foreground'} />
                    {t('list.runningCount', { count: runningCount })}
                </Badge>
                <label className="ml-auto flex cursor-pointer items-center gap-2 text-xs text-muted-foreground select-none">
                    <span>{t('list.onlyRunning')}</span>
                    <Switch checked={onlyRunning} onCheckedChange={setOnlyRunning} aria-label={t('list.onlyRunning')} />
                </label>
            </div>
            <div className="min-h-0 flex-1">
                {visibleLogs.length === 0 ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                        <Logs className="size-8" />
                        <span className="text-sm">{t('list.noRunning')}</span>
                    </div>
                ) : (
                    <VirtualizedGrid
                        items={visibleLogs}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={104}
                        overscan={8}
                        getItemKey={(log) => `log-${log.id}`}
                        renderItem={(log) => <LogCard log={log} />}
                    />
                )}
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
