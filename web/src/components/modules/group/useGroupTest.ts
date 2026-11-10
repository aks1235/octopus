import { useCallback, useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { create } from 'zustand';
import {
    applyGroupItemProbe,
    streamGroupMemberTests,
    type Group,
    type GroupTestMode,
} from '@/api/group';

// 测试即探测: 结论由后端逐条落库并随事件回报, 前端只保留"进度"这一份瞬态信息。
//
// 早先这里把整组结果快照写进 localStorage, 与授权上的 probe_* 形成两套持久状态——
// 成员行因此同时挂两个徽标, 卡片头还多一行"上次检测"。合并后不再持久化任何测试结果:
// 关掉页面再进来读到的只有探测结论, 与定时探测完全一致。
interface TestState {
    signature: string;
    runningMode: GroupTestMode | null;
    controller: AbortController | null;
    completed: number;
    total: number;
}

const useTestStore = create<Record<number, TestState>>(() => ({}));

export function useGroupTest(group: Group) {
    const t = useTranslations('group.card');
    // 成员签名变化(增删成员、换凭据)后上一轮进度不再有意义, 也不该继续显示。
    const signature = JSON.stringify([
        group.id,
        group.items
            .map((item) => [item.id, item.channel_grant_id])
            .sort((first, second) => Number(first[0]) - Number(second[0])),
    ]);
    const stored = useTestStore((state) => state[group.id]);
    const state = stored?.signature === signature ? stored : undefined;
    const runningMode = state?.runningMode ?? null;
    const progress = useMemo(
        () => ({ completed: state?.completed ?? 0, total: state?.total || group.items.length }),
        [state?.completed, state?.total, group.items.length]
    );

    const runTest = useCallback(async (mode: GroupTestMode) => {
        if (useTestStore.getState()[group.id]?.controller) return;
        const controller = new AbortController();
        const publish = (next: Partial<TestState>) =>
            useTestStore.setState({
                [group.id]: {
                    signature,
                    runningMode: mode,
                    controller,
                    completed: 0,
                    total: group.items.length,
                    ...next,
                },
            });
        publish({});
        let completed = 0;
        let success = 0;
        let timeout = 0;
        try {
            await streamGroupMemberTests(
                group.id,
                mode,
                (event) => {
                    if (event.type === 'start') {
                        publish({ completed: 0, total: event.total });
                        return;
                    }
                    if (event.type !== 'result') return;
                    completed++;
                    if (event.result.success) success++;
                    if (event.result.status === 'timeout') timeout++;
                    // 结论直接搬进缓存: 成员行徽标与"是否排到末尾"都跟着这条事件即时变化。
                    if (event.probe) applyGroupItemProbe(group.id, event.result.item_id, event.probe);
                    publish({ completed });
                },
                controller.signal
            );
            const summary = t('testSummary', { success, timeout, failed: completed - success - timeout, total: completed });
            if (success === completed) toast.success(summary);
            else toast.warning(summary);
        } catch (error) {
            if (!controller.signal.aborted) {
                toast.error(t('testFailed'), { description: error instanceof Error ? error.message : String(error) });
            }
        } finally {
            useTestStore.setState({
                [group.id]: { signature, runningMode: null, controller: null, completed, total: group.items.length },
            });
        }
    }, [group.id, group.items.length, signature, t]);

    return { runningMode, progress, runTest };
}
