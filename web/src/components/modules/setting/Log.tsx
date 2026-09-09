import { useEffect, useRef, useState } from 'react';
import { ScrollText, Trash2, HardDriveDownload, CalendarClock } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useClearLogs } from '@/api/log';
import { useClearLogHistory } from '@/api/log-history';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';

// SettingLog 提供转发日志的保留策略(是否落库、保留天数)与两类清空操作:
// 清空实时流(进程内状态)与清空历史(持久化日志)是两个独立入口。
export function SettingLog() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const clearLogs = useClearLogs();
    const clearHistory = useClearLogHistory();

    const [keepEnabled, setKeepEnabled] = useState(true);
    const [keepPeriod, setKeepPeriod] = useState('7');
    const initialKeepPeriod = useRef('7');

    useEffect(() => {
        if (settings) {
            const enabled = settings.find((s) => s.key === SettingKey.RelayLogKeepEnabled);
            const period = settings.find((s) => s.key === SettingKey.RelayLogKeepPeriod);
            if (enabled) {
                queueMicrotask(() => setKeepEnabled(enabled.value === 'true'));
            }
            if (period) {
                queueMicrotask(() => setKeepPeriod(period.value));
                initialKeepPeriod.current = period.value;
            }
        }
    }, [settings]);

    const handleKeepEnabledChange = (checked: boolean) => {
        setKeepEnabled(checked);
        setSetting.mutate(
            { key: SettingKey.RelayLogKeepEnabled, value: String(checked) },
            { onSuccess: () => toast.success(t('saved')) }
        );
    };

    const handleKeepPeriodSave = () => {
        const value = keepPeriod.trim();
        if (value === initialKeepPeriod.current) return;
        if (!/^\d+$/.test(value) || Number(value) < 0) {
            toast.error(t('log.keepPeriod.invalid'));
            return;
        }
        setSetting.mutate(
            { key: SettingKey.RelayLogKeepPeriod, value },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    initialKeepPeriod.current = value;
                },
            }
        );
    };

    const handleClearLogs = () => {
        clearLogs.mutate(undefined, {
            onSuccess: () => toast.success(t('log.clearSuccess')),
            onError: () => toast.error(t('log.clearFailed')),
        });
    };

    const handleClearHistory = () => {
        clearHistory.mutate(undefined, {
            onSuccess: () => toast.success(t('log.clearHistorySuccess')),
            onError: () => toast.error(t('log.clearHistoryFailed')),
        });
    };

    return (
        <div className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <ScrollText className="size-5" />
                {t('log.title')}
            </h2>

            {/* 保留策略 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <HardDriveDownload className="size-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('log.keep.label')}</span>
                </div>
                <Switch
                    checked={keepEnabled}
                    onCheckedChange={handleKeepEnabledChange}
                    disabled={setSetting.isPending}
                />
            </div>
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <CalendarClock className="size-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('log.keepPeriod.label')}</span>
                </div>
                <Input
                    className="h-9 w-28 rounded-xl text-right"
                    value={keepPeriod}
                    onChange={(e) => setKeepPeriod(e.target.value)}
                    onBlur={handleKeepPeriodSave}
                    disabled={!keepEnabled || setSetting.isPending}
                    placeholder={t('log.keepPeriod.placeholder')}
                />
            </div>

            {/* 清空实时流(进程内状态) */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Trash2 className="size-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('log.clearLive.label')}</span>
                </div>
                <Button
                    variant="outline"
                    size="sm"
                    onClick={handleClearLogs}
                    disabled={clearLogs.isPending}
                    className="rounded-xl"
                >
                    {clearLogs.isPending ? t('log.clear.clearing') : t('log.clearLive.button')}
                </Button>
            </div>

            {/* 清空历史(持久化日志, 含迁移数据, 不可恢复) */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Trash2 className="size-5 text-destructive/70" />
                    <span className="text-sm font-medium">{t('log.clear.label')}</span>
                </div>
                <Button
                    variant="destructive"
                    size="sm"
                    onClick={handleClearHistory}
                    disabled={clearHistory.isPending}
                    className="rounded-xl"
                >
                    {clearHistory.isPending ? t('log.clear.clearing') : t('log.clear.button')}
                </Button>
            </div>
        </div>
    );
}
