import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Monitor, Globe, Clock, Shield, HelpCircle, HeartPulse, ShieldAlert, Filter, X, Activity } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { runGroupProbe } from '@/api/group';
import { compileMemberRegex } from '@/lib/member-regex';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export function SettingSystem() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [proxyUrl, setProxyUrl] = useState('');
    const [statsSaveInterval, setStatsSaveInterval] = useState('');
    const [healthCheckInterval, setHealthCheckInterval] = useState('');
    const [healthFailThreshold, setHealthFailThreshold] = useState('');
    const [corsAllowOrigins, setCorsAllowOrigins] = useState('');
    const [corsInputValue, setCorsInputValue] = useState('');
    const [modelFilter, setModelFilter] = useState('');
    const [groupProbeEnabled, setGroupProbeEnabled] = useState(false);
    const [probeRunning, setProbeRunning] = useState(false);

    const initialProxyUrl = useRef('');
    const initialStatsSaveInterval = useRef('');
    const initialHealthCheckInterval = useRef('');
    const initialHealthFailThreshold = useRef('');
    const initialCorsAllowOrigins = useRef('');
    const initialModelFilter = useRef('');
    const initialGroupProbeEnabled = useRef(false);

    useEffect(() => {
        if (settings) {
            const proxy = settings.find(s => s.key === SettingKey.ProxyURL);
            const interval = settings.find(s => s.key === SettingKey.StatsSaveInterval);
            const healthInterval = settings.find(s => s.key === SettingKey.HealthCheckInterval);
            const healthThreshold = settings.find(s => s.key === SettingKey.HealthFailThreshold);
            const cors = settings.find(s => s.key === SettingKey.CORSAllowOrigins);
            if (proxy) {
                queueMicrotask(() => setProxyUrl(proxy.value));
                initialProxyUrl.current = proxy.value;
            }
            if (interval) {
                queueMicrotask(() => setStatsSaveInterval(interval.value));
                initialStatsSaveInterval.current = interval.value;
            }
            if (healthInterval) {
                queueMicrotask(() => setHealthCheckInterval(healthInterval.value));
                initialHealthCheckInterval.current = healthInterval.value;
            }
            if (healthThreshold) {
                queueMicrotask(() => setHealthFailThreshold(healthThreshold.value));
                initialHealthFailThreshold.current = healthThreshold.value;
            }
            if (cors) {
                queueMicrotask(() => setCorsAllowOrigins(cors.value));
                initialCorsAllowOrigins.current = cors.value;
            }
            const modelFilterSetting = settings.find(s => s.key === SettingKey.ModelFilter);
            if (modelFilterSetting) {
                queueMicrotask(() => setModelFilter(modelFilterSetting.value));
                initialModelFilter.current = modelFilterSetting.value;
            }
            const groupProbe = settings.find(s => s.key === SettingKey.GroupProbeEnabled);
            if (groupProbe) {
                queueMicrotask(() => setGroupProbeEnabled(groupProbe.value === 'true'));
                initialGroupProbeEnabled.current = groupProbe.value === 'true';
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.ProxyURL) {
                    initialProxyUrl.current = value;
                } else if (key === SettingKey.StatsSaveInterval) {
                    initialStatsSaveInterval.current = value;
                } else if (key === SettingKey.HealthCheckInterval) {
                    initialHealthCheckInterval.current = value;
                } else if (key === SettingKey.HealthFailThreshold) {
                    initialHealthFailThreshold.current = value;
                } else if (key === SettingKey.CORSAllowOrigins) {
                    initialCorsAllowOrigins.current = value;
                } else if (key === SettingKey.ModelFilter) {
                    initialModelFilter.current = value;
                } else if (key === SettingKey.GroupProbeEnabled) {
                    initialGroupProbeEnabled.current = value === 'true';
                }
            }
        });
    };

    // 定时探测开关: 只有开着才会每 6–12 小时自动跑一轮; 关掉后已落库的判死结论最多一天自动失效。
    const handleGroupProbeToggle = (next: boolean) => {
        setGroupProbeEnabled(next);
        setSetting.mutate({ key: SettingKey.GroupProbeEnabled, value: next ? 'true' : 'false' }, {
            onSuccess: () => {
                initialGroupProbeEnabled.current = next;
                toast.success(t('saved'));
            },
            onError: (error) => {
                // 失败必须回滚开关: 否则界面显示的与库里的不一致, 用户以为已经打开了。
                setGroupProbeEnabled(!next);
                toast.error(error.message);
            },
        });
    };

    // 手动跑一轮: 一轮可能持续数分钟, 这里只确认"已开始", 不占着请求等它结束。
    const handleRunProbe = async () => {
        setProbeRunning(true);
        try {
            const { started } = await runGroupProbe();
            toast.success(started ? t('groupProbe.started') : t('groupProbe.busy'));
        } catch (error) {
            toast.error(error instanceof Error ? error.message : t('groupProbe.failed'));
        } finally {
            setProbeRunning(false);
        }
    };

    const corsAllowOriginsList = useMemo(() => {
        const value = corsAllowOrigins.trim();
        if (!value) return [];
        if (value === '*') return ['*'];
        return Array.from(new Set(
            value
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
    }, [corsAllowOrigins]);

    const corsAllowOriginsDisplay = useMemo(
        () => (corsAllowOriginsList.length > 0 ? corsAllowOriginsList.join(', ') : t('corsAllowOrigins.hint')),
        [corsAllowOriginsList, t]
    );

    const saveCorsAllowOrigins = (origins: string[]) => {
        const normalizedOrigins = Array.from(new Set(
            origins
                .map(origin => origin.trim())
                .filter(Boolean)
        ));
        const normalizedValue = normalizedOrigins.includes('*') ? '*' : normalizedOrigins.join(',');
        setCorsAllowOrigins(normalizedValue);
        handleSave(SettingKey.CORSAllowOrigins, normalizedValue, initialCorsAllowOrigins.current);
    };

    const handleAddCorsOrigin = () => {
        const newOrigins = Array.from(new Set(
            corsInputValue
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
        if (newOrigins.length === 0) return;

        if (newOrigins.includes('*')) {
            saveCorsAllowOrigins(['*']);
            setCorsInputValue('');
            return;
        }

        const base = corsAllowOriginsList.includes('*') ? [] : corsAllowOriginsList;
        const merged = Array.from(new Set([...base, ...newOrigins]));
        saveCorsAllowOrigins(merged);
        setCorsInputValue('');
    };

    const handleRemoveCorsOrigin = (originToRemove: string) => {
        const nextOrigins = corsAllowOriginsList.filter(origin => origin !== originToRemove);
        saveCorsAllowOrigins(nextOrigins);
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Monitor className="h-5 w-5" />
                {t('system')}
            </h2>

            {/* 代理地址 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('proxyUrl.label')}</span>
                </div>
                <Input
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    onBlur={() => handleSave('proxy_url', proxyUrl, initialProxyUrl.current)}
                    placeholder={t('proxyUrl.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 统计保存周期 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('statsSaveInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={statsSaveInterval}
                    onChange={(e) => setStatsSaveInterval(e.target.value)}
                    onBlur={() => handleSave('stats_save_interval', statsSaveInterval, initialStatsSaveInterval.current)}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 渠道健康检查间隔, 0 表示关闭定时探测 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <HeartPulse className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('healthCheck.interval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={healthCheckInterval}
                    onChange={(e) => setHealthCheckInterval(e.target.value)}
                    onBlur={() => handleSave(SettingKey.HealthCheckInterval, healthCheckInterval, initialHealthCheckInterval.current)}
                    placeholder={t('healthCheck.interval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 连续失败多少次后自动禁用渠道 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <ShieldAlert className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('healthCheck.threshold.label')}</span>
                </div>
                <Input
                    type="number"
                    value={healthFailThreshold}
                    onChange={(e) => setHealthFailThreshold(e.target.value)}
                    onBlur={() => handleSave(SettingKey.HealthFailThreshold, healthFailThreshold, initialHealthFailThreshold.current)}
                    placeholder={t('healthCheck.threshold.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 分组成员定时探测: 6-12 小时随机一轮, 结论用于成员标注与选路熔断 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Activity className="h-5 w-5 text-muted-foreground" />
                    <div className="flex flex-col">
                        <span className="text-sm font-medium">{t('groupProbe.label')}</span>
                        <span className="text-xs text-muted-foreground">{t('groupProbe.hint')}</span>
                    </div>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center" className="max-w-72">
                            {t('groupProbe.tip')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        className="rounded-xl"
                        onClick={handleRunProbe}
                        disabled={probeRunning}
                    >
                        {probeRunning ? t('groupProbe.running') : t('groupProbe.runNow')}
                    </Button>
                    <Switch checked={groupProbeEnabled} onCheckedChange={handleGroupProbeToggle} />
                </div>
            </div>

            {/* 全局模型过滤: 渠道拉取模型列表时与渠道自身过滤表达式取 AND */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Filter className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('modelFilter.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('modelFilter.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Input
                    value={modelFilter}
                    onChange={(e) => setModelFilter(e.target.value)}
                    onBlur={() => {
                        // 与分组成员正则同一口径预检, 后端 Validate 再兜底一道。
                        if (modelFilter !== '' && compileMemberRegex(modelFilter) === null) {
                            toast.error(t('modelFilter.invalid'));
                            return;
                        }
                        handleSave(SettingKey.ModelFilter, modelFilter, initialModelFilter.current);
                    }}
                    placeholder={t('modelFilter.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* CORS 跨域白名单 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Shield className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('corsAllowOrigins.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('corsAllowOrigins.hint')}
                            <br />
                            {t('corsAllowOrigins.example')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Popover>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            className="border-input focus-visible:border-ring focus-visible:ring-ring/50 w-48 min-h-9 rounded-xl border bg-transparent px-3 py-2 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px]"
                        >
                            <span className={`block overflow-hidden text-ellipsis whitespace-nowrap ${corsAllowOriginsList.length === 0 ? 'text-muted-foreground' : ''}`}>
                                {corsAllowOriginsDisplay}
                            </span>
                        </button>
                    </PopoverTrigger>
                    <PopoverContent className="w-72 space-y-2 rounded-3xl p-3 bg-card">
                        <Input
                            value={corsInputValue}
                            onChange={(e) => setCorsInputValue(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    handleAddCorsOrigin();
                                }
                            }}
                            placeholder={t('corsAllowOrigins.example')}
                            className="h-9 rounded-xl"
                            autoFocus
                        />
                        <div className="max-h-48 space-y-1 overflow-y-auto">
                            {corsAllowOriginsList.length > 0 && (
                                corsAllowOriginsList.map((origin) => (
                                    <div key={origin} className="flex items-center justify-between gap-2 rounded-xl border border-border/60 px-2 py-1">
                                        <span className="break-all text-xs leading-5">{origin}</span>
                                        <button
                                            type="button"
                                            onClick={() => handleRemoveCorsOrigin(origin)}
                                            className="text-muted-foreground transition-colors hover:text-destructive"
                                            aria-label={`remove ${origin}`}
                                        >
                                            <X className="size-4" />
                                        </button>
                                    </div>
                                ))
                            )}
                        </div>
                    </PopoverContent>
                </Popover>
            </div>
        </div>
    );
}
