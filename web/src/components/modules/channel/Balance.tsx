import { useTranslations } from 'use-intl';
import { useChannelBalance } from '@/api/channel';

/**
 * Balance 展示 DeepSeek 官方渠道的账户余额。
 *
 * 打开抽屉时实时查一次, 不入库、不定时、不聚合到首页 —— 余额就是一个数字,
 * 为它建表和挂任务都是多余的。后端只对官方域名返回数据, 其他渠道这里直接不渲染。
 *
 * 余额是账户级的: 同一个 DeepSeek 账号配几把 key 查到的都是同一份钱, 界面明确写出这一点,
 * 免得有人把它当成"这把 key 的余额"。
 */
export function Balance({ channelId }: { channelId: number }) {
    const t = useTranslations('channel.balance');
    const { data, isError } = useChannelBalance(channelId);

    // 非官方渠道拿到 400, 网络异常也什么都不显示: 这块只做"有话就说", 不制造空壳。
    if (isError || !data || data.entries.length === 0) return null;

    return (
        <section className="mb-3 flex flex-col gap-1.5 rounded-2xl border bg-card px-3 py-2.5">
            <div className="flex min-h-6 items-center gap-2">
                <h3 className="text-xs font-semibold tracking-wider text-muted-foreground">{t('title')}</h3>
                <span className={`text-[10px] font-medium ${data.available ? 'text-primary' : 'text-destructive'}`}>
                    {data.available ? t('available') : t('insufficient')}
                </span>
                <div className="flex-1" />
                <span className="text-[10px] text-muted-foreground/60">{t('accountLevel')}</span>
            </div>

            {data.entries.map((entry) => (
                <div key={entry.currency} className="flex items-baseline gap-2">
                    <span className="w-8 shrink-0 text-[11px] text-muted-foreground">{entry.currency}</span>
                    <span className={`text-sm font-semibold tabular-nums ${data.available ? 'text-card-foreground' : 'text-destructive'}`}>
                        {entry.total}
                    </span>
                    <span className="text-[11px] tabular-nums text-muted-foreground/60">
                        {t('detail', { granted: entry.granted, toppedUp: entry.topped_up })}
                    </span>
                </div>
            ))}
        </section>
    );
}
