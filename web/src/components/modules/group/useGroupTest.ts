import { useCallback, useEffect, useMemo } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { create } from 'zustand';
import { streamGroupMemberTests, type Group, type GroupMemberTestResult, type GroupTestMode } from '@/api/group';

interface TestSnapshot {
    signature: string;
    mode: GroupTestMode;
    startedAt: number;
    finishedAt: number | null;
    complete: boolean;
    total: number;
    results: Record<number, GroupMemberTestResult>;
}

interface TestState {
    signature: string;
    snapshot: TestSnapshot | null;
    runningMode: GroupTestMode | null;
    controller: AbortController | null;
    progress: { completed: number; total: number };
}

const useTestStore = create<Record<number, TestState>>(() => ({}));

function saveState(groupID: number, state: TestState) {
    useTestStore.setState({ [groupID]: state });
    if (!state.snapshot) return;
    try {
        localStorage.setItem(`octopus-group-test-v2-${groupID}`, JSON.stringify(state.snapshot));
    } catch {
        return;
    }
}

function readSnapshot(storageKey: string, signature: string): TestSnapshot | null {
    try {
        const cached = JSON.parse(localStorage.getItem(storageKey) || 'null') as TestSnapshot | null;
        if (!cached || cached.signature !== signature || cached.mode !== 'full' ||
            typeof cached.startedAt !== 'number' || !Number.isInteger(cached.total) || cached.total < 0 || typeof cached.complete !== 'boolean' ||
            typeof cached.results !== 'object' || !cached.results || Array.isArray(cached.results)) return null;
        if (Object.entries(cached.results).some(([itemID, result]) => !result || result.item_id !== Number(itemID) || typeof result.success !== 'boolean')) return null;
        return cached;
    } catch {
        return null;
    }
}

export function useGroupTest(group: Group) {
    const t = useTranslations('group.card');
    const storageKey = `octopus-group-test-v2-${group.id}`;
    const signature = JSON.stringify([group.id, group.name, group.items.map((item) => [
        item.id, item.channel_grant_id, item.channel_id, item.model_name, item.key_name, item.protocols, item.available,
    ]).sort((first, second) => Number(first[0]) - Number(second[0]))]);
    const cached = useMemo(() => readSnapshot(storageKey, signature), [storageKey, signature]);
    const stored = useTestStore((state) => state[group.id]);
    const state = stored?.signature === signature ? stored : undefined;
    const snapshot = state ? state.snapshot : cached;
    const runningMode = state?.runningMode ?? null;

    useEffect(() => {
        const active = useTestStore.getState()[group.id];
        if (active?.signature !== signature) active?.controller?.abort();
    }, [group.id, signature]);

    const runTest = useCallback(async (mode: GroupTestMode) => {
        const current = useTestStore.getState()[group.id];
        if (current?.controller) return;
        const controller = new AbortController();
        const previous = current?.signature === signature ? current.snapshot : readSnapshot(storageKey, signature);
        const initialProgress = { completed: 0, total: group.items.length };
        useTestStore.setState({ [group.id]: { signature, snapshot: previous, runningMode: mode, controller, progress: initialProgress } });
        let started = false;
        let next: TestSnapshot = { signature, mode, startedAt: Date.now(), finishedAt: null, complete: false, total: group.items.length, results: {} };
        const publish = () => {
            saveState(group.id, { signature, snapshot: next, runningMode: mode, controller, progress: { completed: Object.keys(next.results).length, total: next.total } });
        };
        try {
            await streamGroupMemberTests(group.id, mode, (event) => {
                if (event.type === 'start') {
                    started = true;
                    next = { ...next, total: event.total };
                }
                if (event.type === 'result') next = { ...next, results: { ...next.results, [event.result.item_id]: event.result } };
                publish();
            }, controller.signal);
            next = { ...next, complete: true, finishedAt: Date.now() };
            const results = Object.values(next.results);
            const success = results.filter((result) => result.success).length;
            const timeout = results.filter((result) => result.status === 'timeout').length;
            const summary = t('testSummary', { success, timeout, failed: results.length - success - timeout, total: next.total });
            if (success === next.total) toast.success(summary);
            else toast.warning(summary);
        } catch (error) {
            next = { ...next, finishedAt: Date.now() };
            if (!controller.signal.aborted) toast.error(t('testFailed'), { description: error instanceof Error ? error.message : String(error) });
        } finally {
            const snapshot = started ? next : previous;
            saveState(group.id, { signature, snapshot, runningMode: null, controller: null, progress: { completed: Object.keys(snapshot?.results ?? {}).length, total: snapshot?.total ?? group.items.length } });
        }
    }, [group.id, group.items.length, signature, storageKey, t]);

    const results = runningMode && state?.progress.completed === 0 ? {} : snapshot?.results ?? {};
    const completed = Object.keys(results).length;
    const total = snapshot?.total ?? group.items.length;
    const success = Object.values(results).filter((result) => result.success).length;
    const timeout = Object.values(results).filter((result) => result.status === 'timeout').length;
    return { snapshot, results, runningMode, runTest, progress: state?.progress ?? { completed, total }, success, timeout };
}
