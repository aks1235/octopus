import { useState } from 'react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useFetchModel } from '@/api/channel';
import { useSettingList, SettingKey } from '@/api/setting';
import { compileMemberRegex } from '@/lib/member-regex';
import { grantKey, toChannelConfig, type ChannelFormState } from './state';

// useModelProbe 按凭据探测上游模型列表, 供模型页与凭据页共用。
// 两侧并发探测与协议位判定都在后端完成, 此处只负责转圈状态, 结果并入表单和结果提示。
export function useModelProbe() {
    const t = useTranslations('channel.form');
    const fetchModel = useFetchModel();
    const { data: settings } = useSettingList(); // 全局模型过滤(黑名单)随设置实时读, 探测合并时同步收缩存量。
    const [pendingKey, setPendingKey] = useState<string | null>(null); // 正在探测的凭据名称, 只转动该行的图标。

    // probe 探测指定凭据可用的模型, 结果并入模型集合与授权表。
    // 上游未返回但本地已有的模型保留: 静默删除会打断正在使用该模型的路由。
    // 但过滤规则有追溯力: 合并后按当前口径再收缩一遍——命中全局黑名单(命中排除)的、
    // 不满足渠道白名单(配置了命中保留)的既有模型一并移除, 否则早前吸进来的垃圾模型
    // 会因「只增不减」永远留在表单里, 黑名单形同虚设(2026-09-18 用户实测反馈)。
    const probe = async (
        state: ChannelFormState,
        setState: (next: ChannelFormState) => void,
        keyName: string,
    ) => {
        // 模型页的刷新按钮不按凭据禁用, 选中的凭据可能还没填 Key, 在此挡掉空请求。
        const channelKey = state.keys.find((k) => k.name === keyName);
        if (!channelKey || channelKey.key.trim() === '') return;

        setPendingKey(keyName);
        try {
            // 探测用的地址, 路径, 代理和过滤表达式必须和保存后生效的完全一致, 否则这里探到的模型
            // 与实际转发时能用的模型会不一样; 故与提交共用同一份配置, 探测用不上的字段由后端忽略。
            const fetched = await fetchModel.mutateAsync({
                channel: toChannelConfig(state),
                key: channelKey.key.trim(),
            });
            if (fetched.length === 0) {
                toast.warning(t('modelRefreshEmpty'));
                return;
            }
            const models = [...state.models];
            const grants = new Map(state.grants);
            for (const { name, protocols } of fetched) {
                if (!models.includes(name)) models.push(name);
                const mapKey = grantKey(name, channelKey.name);
                grants.set(mapKey, (grants.get(mapKey) ?? 0) | protocols);
            }

            // 按当前过滤口径收缩: 与后端拉取判定同语义(全局命中排除, 渠道级配置则命中保留),
            // 编译失败按不过滤处理(与后端留空不生效口径一致, 非法表达式由保存与拉取路径报错)。
            const globalRe = compileMemberRegex(
                settings?.find((setting) => setting.key === SettingKey.ModelFilter)?.value ?? '',
            );
            const channelRe = compileMemberRegex(state.match_regex);
            const kept = models.filter((name) => {
                if (globalRe?.test(name)) return false;
                if (channelRe && !channelRe.test(name)) return false;
                return true;
            });
            if (kept.length !== models.length) {
                const keptSet = new Set(kept);
                for (const mapKey of grants.keys()) {
                    if (!keptSet.has(mapKey.split('\0')[0])) grants.delete(mapKey);
                }
                models.length = 0;
                models.push(...kept);
            }

            setState({ ...state, models, grants });
            toast.success(t('modelRefreshSuccess', { count: fetched.length }));
        } catch (error) {
            toast.error(t('modelRefreshFailed'), { description: String(error) });
        } finally {
            setPendingKey(null);
        }
    };

    return { probe, pendingKey };
}
