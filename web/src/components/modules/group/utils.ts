import type { LLMChannel } from '@/api/model';

export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

export function modelChannelKey(channelId: number, modelName: string) {
    return `${channelId}-${modelName}`;
}

export function memberKey(member: Pick<LLMChannel, 'channel_id' | 'name'>) {
    return modelChannelKey(member.channel_id, member.name);
}

export function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}

// buildChannelNameByModelKey 按 channel_id+model_name 双键建渠道名映射。
// 日志详情页(log/Item.tsx)数据源是 RelayLogOverview(仅 channel_id),不走 group DTO,
// 仍需本地反查渠道名,故保留;分组列表(Card.tsx)已改用后端 DTO,不再调用本函数。
export function buildChannelNameByModelKey(modelChannels: LLMChannel[]) {
    const map = new Map<string, string>();
    modelChannels.forEach((mc) => {
        map.set(modelChannelKey(mc.channel_id, mc.name), mc.channel_name);
    });
    return map;
}
