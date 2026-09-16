export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

export function memberKey(member: { channel_grant_id: number }) {
    return String(member.channel_grant_id);
}

export function matchesGroupName(modelName: string, groupKey: string) {
    if (!groupKey) return false;
    return modelName.toLowerCase().includes(groupKey);
}

// channelOrderOf 把成员列表折算为渠道顺序：按渠道首次出现的顺序去重。
// 拖拽单位是渠道块（同渠道成员在列表内连续），折算后的渠道顺序即提交给顺序端点的终态；
// 渠道内成员顺序由后端按（模型，凭据）自然序保持，不参与提交。
export function channelOrderOf(members: { channel_id: number }[]): number[] {
    const order: number[] = [];
    const seen = new Set<number>();
    for (const member of members) {
        // 渠道 ID 为 0 表示该成员的渠道当下查不到(缓存刷新的瞬时窗口): 它不是合法渠道,
        // 提交它会被顺序表的外键约束拒绝而使整次保存失败, 故丢弃该项, 其余渠道顺序照常保存。
        if (member.channel_id <= 0 || seen.has(member.channel_id)) continue;
        seen.add(member.channel_id);
        order.push(member.channel_id);
    }
    return order;
}
