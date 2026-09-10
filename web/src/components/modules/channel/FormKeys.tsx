import { Fragment, useState } from 'react';
import { Plus, Trash2, Pencil, Check, RefreshCw, Zap, ListChecks, CheckCircle2, XCircle } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Protocol, useTestChannelKey, type KeyTestResult } from '@/api/channel';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { useModelProbe } from './probe';
import { grantKey, toChannelConfig, toGrantConfigs, type ChannelFormState } from './state';

// maskKey 显示凭据尾部, 其余以点替代。
function maskKey(key: string) {
    return key.length <= 8 ? key : `${key.slice(0, 3)}••••${key.slice(-4)}`;
}

// protocolLabel 把成功的协议位显示成路径里讲的端点名, 与授权矩阵的列顺序一致。
function protocolLabel(bit: number) {
    if (bit & Protocol.AnthropicMessage) return 'message';
    if (bit & Protocol.OpenAIResponse) return 'response';
    return 'chat';
}

// KeyTestResults 单条凭据的连通性测试结果面板: 逐模型列出成败与错误摘要。
// 不引入模型选择对话框: 默认测活与测全部的入口都在凭据行, 单模型重测的入口就在结果行上。
// busy 表示任一凭据的测试进行中, 与凭据行的测试按钮同一把锁, 保证同一时间只跑一个测试。
function KeyTestResults({ results, pending, busy, onRetest }: {
    results: KeyTestResult[];
    pending: boolean;
    busy: boolean;
    onRetest: (modelName: string) => void;
}) {
    const t = useTranslations('channel.form');
    return (
        <div className="space-y-1 rounded-xl border border-border bg-muted/20 px-3 py-2">
            {results.map((result) => (
                <div key={result.model_name} className="flex items-center gap-2 text-xs">
                    {result.success ? (
                        <CheckCircle2 className="size-3.5 shrink-0 text-accent" />
                    ) : (
                        <XCircle className="size-3.5 shrink-0 text-destructive" />
                    )}
                    <span className="shrink-0 font-medium">{result.model_name}</span>
                    {result.success ? (
                        // 成功的模型带上讲得通的协议端点, 与授权矩阵的口径对得上。
                        <span className="shrink-0 text-muted-foreground">{protocolLabel(result.protocol)}</span>
                    ) : (
                        result.error && (
                            // 错误摘要可能较长, 行内截断展示, 完整内容挂在 title 上。
                            <span className="min-w-0 flex-1 truncate text-destructive/80" title={result.error}>
                                {result.error}
                            </span>
                        )
                    )}
                    <IconButton
                        onClick={() => onRetest(result.model_name)}
                        disabled={busy}
                        className="ml-auto size-6 shrink-0"
                        tip={t('keyTestRetest')}
                    >
                        <RefreshCw className={`size-3 ${pending ? 'animate-spin' : ''}`} />
                    </IconButton>
                </div>
            ))}
        </div>
    );
}

// FormKeys 渠道凭据的增删改。
// 凭据改名后其授权要跟着改名, 否则授权会指向已不存在的凭据; 删除凭据同时删掉它的全部授权。
// channelId 是编辑渠道的主键, 新建渠道为 0: 测试日志由此引用渠道, 未保存也照写。
export function FormKeys({ state, setState, channelId }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    channelId: number;
}) {
    const t = useTranslations('channel.form');
    const { probe, pendingKey } = useModelProbe();
    const testKey = useTestChannelKey();
    const [editing, setEditing] = useState<number | null>(null);
    const [draft, setDraft] = useState({ name: '', key: '' });
    // 同一时间只跑一个测试(与探测一致), 转圈状态按凭据名记录; 结果按凭据名缓存到表单生命周期内。
    const [testingKey, setTestingKey] = useState<string | null>(null);
    const [testResults, setTestResults] = useState<Record<string, KeyTestResult[]>>({});

    // defaultTestModel ⚡ 默认测活只发一个模型: 取该凭据有授权位的第一个模型,
    // 无任何授权时退回渠道第一个模型 —— 单发请求控制真实计费消耗, 全量交给第二入口。
    const defaultTestModel = (keyName: string) =>
        state.models.find((m) => (state.grants.get(grantKey(m, keyName)) ?? 0) !== 0) ?? state.models[0];

    // runTest 对指定凭据按模型列表逐个发起最小真实请求; models 给单个即测活/重测, 给全量即整测。
    // 请求携带凭据名与授权清单: 后端按「模型 x 凭据」取实际协议位精确试测, 并把测试凭据名写进日志。
    // 单模型重测的结果只覆盖该模型在旧面板里的条目, 其余模型的结果保留。
    const runTest = async (keyName: string, models: string[]) => {
        const channelKey = state.keys.find((k) => k.name === keyName);
        if (!channelKey || channelKey.key.trim() === '' || models.length === 0) return;
        setTestingKey(keyName);
        try {
            // 测试用的配置必须和保存后生效的完全一致, 否则测到的连通性与实际转发会不一样。
            const results = await testKey.mutateAsync({
                channel: toChannelConfig(state),
                channel_id: channelId,
                key: channelKey.key.trim(),
                key_name: keyName,
                models,
                grants: toGrantConfigs(state),
            });
            setTestResults((prev) => {
                const merged = new Map((prev[keyName] ?? []).map((r) => [r.model_name, r]));
                for (const result of results) merged.set(result.model_name, result);
                return { ...prev, [keyName]: [...merged.values()] };
            });
        } catch (error) {
            toast.error(t('keyTestFailed'), { description: String(error) });
        } finally {
            setTestingKey(null);
        }
    };

    const renameGrants = (from: string, to: string) => {
        const grants = new Map(state.grants);
        for (const modelName of state.models) {
            const old = grantKey(modelName, from);
            const grant = grants.get(old);
            if (grant === undefined) continue;
            grants.delete(old);
            grants.set(grantKey(modelName, to), grant);
        }
        return grants;
    };

    const commit = (index: number) => {
        const name = draft.name.trim();
        if (!name || state.keys.some((k, i) => i !== index && k.name === name)) return;
        const previous = state.keys[index].name;
        setState({
            ...state,
            keys: state.keys.map((k, i) => (i === index ? { ...k, name, key: draft.key.trim() } : k)),
            grants: name === previous ? state.grants : renameGrants(previous, name),
        });
        setEditing(null);
    };

    const remove = (index: number) => {
        const removed = state.keys[index].name;
        const grants = new Map(state.grants);
        for (const modelName of state.models) grants.delete(grantKey(modelName, removed));
        setState({ ...state, keys: state.keys.filter((_, i) => i !== index), grants });
    };

    const grantCount = (keyName: string) =>
        state.models.filter((m) => (state.grants.get(grantKey(m, keyName)) ?? 0) !== 0).length;

    const add = () => {
        // 名称在渠道内唯一, 递增取一个未占用的默认名。
        let n = state.keys.length + 1;
        while (state.keys.some((k) => k.name === `key-${n}`)) n += 1;
        setState({ ...state, keys: [...state.keys, { name: `key-${n}`, key: '', enabled: true }] });
        setDraft({ name: `key-${n}`, key: '' });
        setEditing(state.keys.length);
    };

    return (
        // 撑满步骤区高度, 凭据列表内部滚动。
        <div className="flex flex-col gap-3 h-full min-h-0">
            <div className="flex items-center justify-between shrink-0">
                <span className="text-sm font-medium">{t('keys')} ({state.keys.length})</span>
                <IconButton onClick={add} className="size-9" tip={t('keyAdd')}>
                    <Plus className="size-4" />
                </IconButton>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain space-y-2">
                {state.keys.map((channelKey, index) => editing === index ? (
                    <div key={index} className="flex items-center gap-2 rounded-xl border border-border p-2">
                        <Input
                            value={draft.name}
                            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                            placeholder={t('keyName')}
                            className="rounded-lg h-9 w-32"
                        />
                        <Input
                            value={draft.key}
                            onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                            placeholder={t('apiKey')}
                            className="rounded-lg h-9 flex-1"
                        />
                        <IconButton
                            onClick={() => commit(index)}
                            disabled={!draft.name.trim()}
                            className="size-9"
                            tip={t('save')}
                        >
                            <Check className="size-4" />
                        </IconButton>
                    </div>
                ) : (
                    <Fragment key={index}>
                        <div className="flex items-center gap-3 rounded-xl border border-border px-3 py-2">
                            <span className="text-sm font-medium truncate w-32">{channelKey.name}</span>
                            <span className="text-xs font-mono text-muted-foreground flex-1 truncate">
                                {channelKey.key ? maskKey(channelKey.key) : t('keyEmpty')}
                            </span>
                            {/* 只显示该凭据自己的授权数, 模型集合是渠道级共享的, 总数对每条凭据都一样, 摆出来会被误读成它已获取的模型数。 */}
                            <span className="text-xs text-muted-foreground tabular-nums">
                                {grantCount(channelKey.name)}
                            </span>
                            <Switch
                                checked={channelKey.enabled}
                                onCheckedChange={(checked) => setState({
                                    ...state,
                                    keys: state.keys.map((k, i) => (i === index ? { ...k, enabled: checked } : k)),
                                })}
                            />
                            {/* 逐条凭据探测: 各凭据在上游被授权的模型不同, 需按凭据分别取回。 */}
                            <IconButton
                                onClick={() => probe(state, setState, channelKey.name)}
                                disabled={pendingKey !== null || !channelKey.key.trim() || !state.base_url.trim()}
                                className="size-8"
                                tip={t('modelRefresh')}
                            >
                                <RefreshCw className={`size-4 ${pendingKey === channelKey.name ? 'animate-spin' : ''}`} />
                            </IconButton>
                            {/* 逐条凭据连通性测试: ⚡ 默认只测一个模型(该凭据有授权的第一个), 只发一个请求控制计费消耗, 结果展示在该行下方。 */}
                            <IconButton
                                onClick={() => runTest(channelKey.name, [defaultTestModel(channelKey.name)])}
                                disabled={testingKey !== null || !channelKey.key.trim() || !state.base_url.trim() || state.models.length === 0}
                                className="size-8"
                                tip={t('keyTest')}
                            >
                                <Zap className={`size-4 ${testingKey === channelKey.name ? 'animate-pulse' : ''}`} />
                            </IconButton>
                            {/* 第二入口: 对渠道已配的全部模型逐个测试, 需要看全量可用性时再点。 */}
                            <IconButton
                                onClick={() => runTest(channelKey.name, state.models)}
                                disabled={testingKey !== null || !channelKey.key.trim() || !state.base_url.trim() || state.models.length === 0}
                                className="size-8"
                                tip={t('keyTestAll')}
                            >
                                <ListChecks className={`size-4 ${testingKey === channelKey.name ? 'animate-pulse' : ''}`} />
                            </IconButton>
                            <IconButton
                                onClick={() => { setDraft({ name: channelKey.name, key: channelKey.key }); setEditing(index); }}
                                className="size-8"
                                tip={t('edit')}
                            >
                                <Pencil className="size-4" />
                            </IconButton>
                            <IconButton
                                onClick={() => remove(index)}
                                className="size-8 hover:text-destructive"
                                tip={t('delete')}
                            >
                                <Trash2 className="size-4" />
                            </IconButton>
                        </div>
                        {testResults[channelKey.name] && (
                            <KeyTestResults
                                results={testResults[channelKey.name]}
                                pending={testingKey === channelKey.name}
                                busy={testingKey !== null}
                                onRetest={(modelName) => runTest(channelKey.name, [modelName])}
                            />
                        )}
                    </Fragment>
                ))}
            </div>
        </div>
    );
}
