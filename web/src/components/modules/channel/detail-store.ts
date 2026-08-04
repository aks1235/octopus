import { create } from 'zustand';

/**
 * channel 模块的内部视图分态(模块内自路由,不动主导航 NavItem):
 *  - list:   渠道列表(默认)
 *  - detail: 单个渠道的调用详情页
 *
 * 复用 toolbar 的 search-store 写法:轻量 zustand,纯客户端状态路由。
 * 刷新页面会回到 list(本期不持久化 query param,可接受)。
 */
export type ChannelView =
    | { mode: 'list' }
    | { mode: 'detail'; channelID: number; channelName: string };

interface ChannelViewState {
    view: ChannelView;
    setView: (view: ChannelView) => void;
    /** 返回列表 */
    backToList: () => void;
}

export const useChannelViewStore = create<ChannelViewState>((set) => ({
    view: { mode: 'list' },
    setView: (view) => set({ view }),
    backToList: () => set({ view: { mode: 'list' } }),
}));
