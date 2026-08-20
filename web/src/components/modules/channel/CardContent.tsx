import { useMemo, useState } from 'react';
import {
    Trash2,
    CheckCircle2,
    XCircle,
    FileText,
    DollarSign,
    Clock,
    Activity,
    TrendingUp,
    Zap,
    Loader2,
    History
} from 'lucide-react';
import { useUpdateChannel, useDeleteChannel, useTestModels, type Channel, type TestModelResult, type UpdateChannelRequest } from '@/api/channel';
import { toast } from 'sonner';
import {
    MorphingDialogTitle,
    MorphingDialogDescription,
    MorphingDialogClose,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Tabs, TabsContent } from '@/components/ui/tabs';
import { type StatsMetricsFormatted } from '@/api/stats';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { ChannelForm, type ChannelFormData } from './Form';
import { CallDetail } from './CallDetail';

export function CardContent({ channel, stats }: { channel: Channel; stats: StatsMetricsFormatted }) {
    const { setIsOpen } = useMorphingDialog();
    const updateChannel = useUpdateChannel();
    const deleteChannel = useDeleteChannel();
    const testModels = useTestModels();
    const [testResults, setTestResults] = useState<TestModelResult[] | null>(null);
    const modelsToTest = useMemo(() => {
        const split = (s: string) => s.split(',').map((m) => m.trim()).filter(Boolean);
        return Array.from(new Set([...split(channel.model), ...split(channel.custom_model)]));
    }, [channel.model, channel.custom_model]);
    const [isEditing, setIsEditing] = useState(false);
    const [isConfirmingDelete, setIsConfirmingDelete] = useState(false);
    const [showCallDetail, setShowCallDetail] = useState(false);
    const [formData, setFormData] = useState<ChannelFormData>({
        name: channel.name,
        type: channel.type,
        enabled: channel.enabled,
        base_url: channel.base_url,
        key: channel.key,
        custom_header: channel.custom_header ?? [],
        channel_proxy: channel.channel_proxy ?? '',
        param_override: channel.param_override ?? '',
        model: channel.model,
        custom_model: channel.custom_model,
        proxy: channel.proxy,
        auto_sync: channel.auto_sync,
        match_regex: channel.match_regex ?? '',
    });
    const t = useTranslations('channel.detail');

    const currentView = showCallDetail ? 'callDetail' : isEditing ? 'editing' : 'viewing';

    const headersEqual = (a: Channel['custom_header'] | undefined, b: Channel['custom_header'] | undefined) =>
        JSON.stringify(a ?? []) === JSON.stringify(b ?? []);

    const handleUpdate = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        const req: UpdateChannelRequest = { id: channel.id };

        // only send changed fields to avoid accidental clears
        if (formData.name !== channel.name) req.name = formData.name;
        if (formData.type !== channel.type) req.type = formData.type;
        if (formData.enabled !== channel.enabled) req.enabled = formData.enabled;
        if (formData.base_url.trim() !== channel.base_url) req.base_url = formData.base_url.trim();
        if (formData.key.trim() !== channel.key) req.key = formData.key.trim();
        if (formData.model !== channel.model) req.model = formData.model;
        if (formData.custom_model !== channel.custom_model) req.custom_model = formData.custom_model;
        if (formData.proxy !== channel.proxy) req.proxy = formData.proxy;
        if (formData.auto_sync !== channel.auto_sync) req.auto_sync = formData.auto_sync;

        if (!headersEqual(formData.custom_header, channel.custom_header)) {
            req.custom_header = (formData.custom_header ?? [])
                .map((h) => ({ header_key: h.header_key.trim(), header_value: h.header_value }))
                .filter((h) => h.header_key && h.header_value !== '');
        }

        const nextChannelProxy = formData.channel_proxy.trim();
        const curChannelProxy = channel.channel_proxy ?? '';
        if (nextChannelProxy !== curChannelProxy) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.channel_proxy = nextChannelProxy;
        }

        const nextParamOverride = formData.param_override.trim();
        const curParamOverride = channel.param_override ?? '';
        if (nextParamOverride !== curParamOverride) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.param_override = nextParamOverride;
        }

        const nextMatchRegex = formData.match_regex.trim();
        const curMatchRegex = channel.match_regex ?? '';
        if (nextMatchRegex !== curMatchRegex) {
            // Empty string means "clear" for patch semantics; backend maps it to NULL.
            req.match_regex = nextMatchRegex;
        }

        updateChannel.mutate(req, {
            onSuccess: () => {
                setIsEditing(false);
                setIsOpen(false);
            }
        });
    };

    const handleDeleteClick = () => {
        if (!isConfirmingDelete) {
            setIsConfirmingDelete(true);
            return;
        }

        setIsOpen(false);
        setTimeout(() => {
            deleteChannel.mutate(channel.id);
        }, 300);
    };

    const handleTestModels = () => {
        if (modelsToTest.length === 0) return;
        setTestResults(null);
        testModels.mutate(
            { channel_id: channel.id, models: modelsToTest },
            {
                onSuccess: (data) => {
                    setTestResults(data);
                    const passed = data.filter((r) => r.passed).length;
                    toast.success(t('test.summary', { passed, total: data.length }));
                },
                onError: (error) => toast.error(error.message),
            }
        );
    };

    return (
        <>
            <MorphingDialogTitle>
                <header className="mb-6 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">
                        {showCallDetail ? t('title.callDetail') : isEditing ? t('title.edit') : t('title.view')}
                    </h2>
                    <MorphingDialogClose
                        className="relative top-0 right-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 }
                        }}
                    />
                </header>
            </MorphingDialogTitle>

            <MorphingDialogDescription>
                <Tabs value={currentView}>
                    <TabsContent value="viewing" >
                            <div className="max-h-[60vh] overflow-y-auto space-y-4 sm:space-y-5">
                                <dl className="grid gap-3 grid-cols-1 sm:grid-cols-3">
                                    <div className="rounded-2xl border bg-linear-to-br from-chart-1/10 to-chart-1/5 p-3 sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <Activity className="size-4 text-chart-1" />
                                            {t('metrics.totalRequests')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-1">
                                            {stats.request_count.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.request_count.formatted.unit}</span>
                                        </dd>
                                    </div>

                                    <div className="rounded-2xl border bg-linear-to-br from-chart-3/10 to-chart-3/5 p-3 sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <FileText className="size-4 text-chart-3" />
                                            {t('metrics.totalToken')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-3">
                                            {stats.total_token.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.total_token.formatted.unit}</span>
                                        </dd>
                                    </div>

                                    <div className="rounded-2xl border bg-linear-to-br from-chart-5/10 to-chart-5/5 p-3 sm:p-4">
                                        <dt className="flex items-center gap-2 mb-2 text-xs font-medium text-muted-foreground">
                                            <DollarSign className="size-4 text-chart-5" />
                                            {t('metrics.totalCost')}
                                        </dt>
                                        <dd className="text-xl sm:text-2xl font-bold text-chart-5">
                                            {stats.total_cost.formatted.value}
                                            <span className="text-xs font-normal ml-1 text-muted-foreground">{stats.total_cost.formatted.unit}</span>
                                        </dd>
                                    </div>
                                </dl>

                                {/* 请求详情 */}
                                <section className="space-y-3">
                                    <h4 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                                        <TrendingUp className="size-3.5" />
                                        {t('sections.requests')}
                                    </h4>
                                    <dl className="grid gap-3 grid-cols-1 sm:grid-cols-2">
                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <CheckCircle2 className="size-4 text-accent" />
                                                {t('metrics.successRequests')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-accent">
                                                {stats.request_success.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.request_success.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <XCircle className="size-4 text-destructive" />
                                                {t('metrics.failedRequests')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-destructive">
                                                {stats.request_failed.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.request_failed.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                {/* Token 使用 */}
                                <section className="space-y-3">
                                    <h4 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                                        <FileText className="size-3.5" />
                                        {t('sections.tokens')}
                                    </h4>
                                    <dl className="grid gap-3 grid-cols-1 sm:grid-cols-2">
                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-1" />
                                                {t('metrics.inputToken')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.input_token.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.input_token.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-3" />
                                                {t('metrics.outputToken')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.output_token.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.output_token.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                {/* 成本详情 */}
                                <section className="space-y-3">
                                    <h4 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                                        <DollarSign className="size-3.5" />
                                        {t('sections.costs')}
                                    </h4>
                                    <dl className="grid gap-3 grid-cols-1 sm:grid-cols-2">
                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-2" />
                                                {t('metrics.inputCost')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.input_cost.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.input_cost.formatted.unit}</span>
                                            </dd>
                                        </div>

                                        <div className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                            <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                                <div className="size-2 rounded-full bg-chart-5" />
                                                {t('metrics.outputCost')}
                                            </dt>
                                            <dd className="text-2xl font-bold text-card-foreground">
                                                {stats.output_cost.formatted.value}
                                                <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.output_cost.formatted.unit}</span>
                                            </dd>
                                        </div>
                                    </dl>
                                </section>

                                {/* 等待时间 */}
                                <dl className="rounded-2xl border bg-card p-3 sm:p-4 transition-colors hover:bg-accent/5">
                                    <dt className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                                        <Clock className="size-4 text-primary" />
                                        {t('metrics.avgWaitTime')}
                                    </dt>
                                    <dd className="text-2xl font-bold text-primary">
                                        {stats.wait_time.formatted.value}
                                        <span className="text-sm font-normal ml-1 text-muted-foreground">{stats.wait_time.formatted.unit}</span>
                                    </dd>
                                </dl>
                            </div>

                            {/* 模型连通性测试 */}
                            <section className="space-y-3">
                                <h4 className="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
                                    <Zap className="size-3.5" />
                                    {t('test.title')}
                                </h4>
                                <Button
                                    onClick={handleTestModels}
                                    disabled={testModels.isPending || modelsToTest.length === 0}
                                    variant="secondary"
                                    className="w-full rounded-2xl h-11"
                                >
                                    {testModels.isPending ? (
                                        <Loader2 className="size-4 animate-spin" />
                                    ) : (
                                        <Zap className="size-4" />
                                    )}
                                    {testModels.isPending ? t('test.pending') : t('test.button')}
                                </Button>
                                {testResults && testResults.length > 0 && (
                                    <ul className="space-y-1.5">
                                        {testResults.map((r) => (
                                            <li
                                                key={r.model}
                                                className="flex items-center gap-2 rounded-xl border bg-card px-3 py-2 text-sm"
                                            >
                                                {r.passed ? (
                                                    <CheckCircle2 className="size-4 text-accent shrink-0" />
                                                ) : (
                                                    <XCircle className="size-4 text-destructive shrink-0" />
                                                )}
                                                <span className="truncate font-medium">{r.model}</span>
                                                {r.passed ? (
                                                    r.delay != null && (
                                                        <span className="ml-auto text-xs text-muted-foreground">
                                                            {r.delay}ms
                                                        </span>
                                                    )
                                                ) : (
                                                    r.error && (
                                                        <span
                                                            className="ml-auto truncate text-xs text-destructive max-w-[50%]"
                                                            title={r.error}
                                                        >
                                                            {r.error}
                                                        </span>
                                                    )
                                                )}
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </section>

                            {/* 操作按钮 */}
                            <div className="grid gap-3 pt-2">
                                <Button
                                    onClick={() => setShowCallDetail(true)}
                                    variant="outline"
                                    className="w-full rounded-2xl h-12"
                                >
                                    <History className="size-4" />
                                    {t('actions.callDetail')}
                                </Button>
                                <div className="grid gap-3 sm:grid-cols-2">
                                    <Button
                                        onClick={() => (isConfirmingDelete ? setIsConfirmingDelete(false) : setIsEditing(true))}
                                        variant={isConfirmingDelete ? 'secondary' : 'default'}
                                        className="w-full rounded-2xl h-12"
                                    >
                                        {isConfirmingDelete ? t('actions.cancel') : t('actions.edit')}
                                    </Button>
                                    <Button
                                        onClick={handleDeleteClick}
                                        disabled={deleteChannel.isPending}
                                        variant="destructive"
                                        className="w-full rounded-2xl h-12"
                                    >
                                        <Trash2 className={`size-4 transition-transform ${isConfirmingDelete ? 'scale-110' : ''}`} />
                                        {deleteChannel.isPending
                                            ? t('actions.deleting')
                                            : isConfirmingDelete
                                                ? t('actions.confirmDelete')
                                                : t('actions.delete')}
                                    </Button>
                                </div>
                            </div>
                    </TabsContent>

                    <TabsContent value="editing">
                        <ChannelForm
                            formData={formData}
                            onFormDataChange={setFormData}
                            onSubmit={handleUpdate}
                            isPending={updateChannel.isPending}
                            submitText={t('actions.save')}
                            pendingText={t('actions.saving')}
                            onCancel={() => setIsEditing(false)}
                            cancelText={t('actions.cancel')}
                            idPrefix="channel"
                        />
                    </TabsContent>
                    <TabsContent value="callDetail">
                        <CallDetail channelName={channel.name} onBack={() => setShowCallDetail(false)} />
                    </TabsContent>
                </Tabs>
            </MorphingDialogDescription>
        </>
    );
}
