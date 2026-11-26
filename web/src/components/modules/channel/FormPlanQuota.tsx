import { useTranslations } from 'use-intl';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { ChannelFormState } from './state';

// selectClassName 与项目里的 Input 保持同一外形; 项目尚未封装 Select, 原生控件套同一套边框与焦点环。
const selectClassName = 'h-9 w-full rounded-xl border border-border bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';

/**
 * FormPlanQuota 配置订阅套餐额度的采集。
 *
 * 与推理凭据分开是有意的: 套餐用量在云厂商的控制面上, 只认账号级 AK/SK,
 * 渠道里那把推理 key 调过去会被上游拒绝。留空厂商即整组关闭, 不采集也不占面板。
 */
export function FormPlanQuota({ state, setState, idPrefix }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    idPrefix: string;
}) {
    const t = useTranslations('channel.form.planQuota');
    const enabled = state.plan_quota_vendor !== '';

    return (
        <div className="space-y-4 pt-2">
            <div className="space-y-2">
                <Label htmlFor={`${idPrefix}-plan-vendor`}>{t('vendor')}</Label>
                <select
                    id={`${idPrefix}-plan-vendor`}
                    value={state.plan_quota_vendor}
                    onChange={(e) => setState({
                        ...state,
                        plan_quota_vendor: e.target.value,
                        // 选中厂商时补上默认套餐类型, 免得用户还要多选一次; 关闭时整组清空。
                        plan_quota_kind: e.target.value ? (state.plan_quota_kind || 'agent_plan') : '',
                    })}
                    className={selectClassName}
                >
                    <option value="">{t('vendorNone')}</option>
                    <option value="volcengine">{t('vendorVolcengine')}</option>
                </select>
                <p className="text-xs text-muted-foreground">{t('vendorHint')}</p>
            </div>

            {enabled && (
                <>
                    <div className="space-y-2">
                        <Label htmlFor={`${idPrefix}-plan-kind`}>{t('kind')}</Label>
                        <select
                            id={`${idPrefix}-plan-kind`}
                            value={state.plan_quota_kind}
                            onChange={(e) => setState({ ...state, plan_quota_kind: e.target.value })}
                            className={selectClassName}
                        >
                            <option value="agent_plan">{t('kindAgentPlan')}</option>
                            {/* Coding Plan 走另一个 Action 与另一套字段, 后端尚未接入, 先置灰避免选了没反应。 */}
                            <option value="coding_plan" disabled>{t('kindCodingPlan')}</option>
                        </select>
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor={`${idPrefix}-plan-ak`}>{t('accessKeyId')}</Label>
                        <Input
                            id={`${idPrefix}-plan-ak`}
                            value={state.plan_quota_access_key_id}
                            onChange={(e) => setState({ ...state, plan_quota_access_key_id: e.target.value })}
                            className="rounded-xl"
                            autoComplete="off"
                            spellCheck={false}
                        />
                    </div>

                    <div className="space-y-2">
                        <Label htmlFor={`${idPrefix}-plan-sk`}>{t('secretAccessKey')}</Label>
                        <Input
                            id={`${idPrefix}-plan-sk`}
                            type="password"
                            value={state.plan_quota_secret_access_key}
                            onChange={(e) => setState({ ...state, plan_quota_secret_access_key: e.target.value })}
                            className="rounded-xl"
                            autoComplete="new-password"
                            placeholder={state.plan_secret_configured ? t('secretKeep') : ''}
                        />
                        <p className="text-xs text-muted-foreground">{t('secretHint')}</p>
                    </div>
                </>
            )}
        </div>
    );
}
