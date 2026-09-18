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

    // probe 探测指定凭据可用的模型, 结果**替换**该凭据名下的模型与授权(对账语义)。
    // 上游没有了的模型不再保留: 留着只会继续向不存在的模型发请求, 并集语义已被否定
    // (2026-09-18 用户定:"上游都没有了这个模型, 还一直请求, 不符合逻辑"); 分组成员
    // 随授权级联消失属预期, 日志统计不受影响。其他凭据未参与本轮探测, 其授权原样保留。
    // 模型集合按授权表实际存在重导出; 合并后按当前过滤口径收缩, 早前吸进来的垃圾一并清除。
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
            const grants = new Map(state.grants);
            // 本凭据名下的旧授权先全部移除: 本轮探测结果就是该凭据的真实集合(替换, 非并集)。
            for (const mapKey of grants.keys()) {
                if (mapKey.split('\0')[1] === channelKey.name) grants.delete(mapKey);
            }
            for (const { name, protocols } of fetched) {
                const mapKey = grantKey(name, channelKey.name);
                grants.set(mapKey, (grants.get(mapKey) ?? 0) | protocols);
            }
            // 模型集合 = 授权表里实际存在的模型(本凭据替换后与其他凭据取并), 无授权的孤立模型不再保留。
            let models = [...new Set([...grants.keys()].map((mapKey) => mapKey.split('\0')[0]))];

            // 按当前过滤口径收缩: 与后端拉取判定同语义(全局命中排除, 渠道级配置则命中保留),
            // 编译失败按不过滤处理(与后端留空不生效口径一致, 非法表达式由保存与拉取路径报错)。
            // 本轮探测结果已过后端过滤, 这一步主要清扫其他凭据名下早前留下的存量。
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
                models = kept;
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
