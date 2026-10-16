import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}


function formatNumber(num: number | undefined, compare: number[], units: string[]): { value: string, unit: string } {
  if (num === undefined) return { value: "0.00", unit: units[0] };
  else if (num >= compare[0]) return { value: (num / compare[0]).toFixed(2), unit: units[1] };
  else if (num >= compare[1]) return { value: (num / compare[1]).toFixed(2), unit: units[2] };
  else if (num >= compare[2]) return { value: (num / compare[2]).toFixed(2), unit: units[3] };
  else if (num >= compare[3]) return { value: (num / compare[3]).toFixed(2), unit: units[4] };
  else return { value: (num).toFixed(2), unit: units[5] };
}

export function formatCount(num: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: num ?? 0,
    formatted: formatNumber(num, [1000000000, 1000000, 1000, 1], ['', 'B', 'M', 'K', '', '']),
  };
}
export function formatMoney(num: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: num ?? 0,
    formatted: formatNumber(num, [1000000000, 1000000, 1000, 1], ['$', 'B$', 'M$', 'K$', '$', '$']),
  };
}

export function formatTime(ms: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: ms ?? 0,
    formatted: formatNumber(ms, [86400000, 3600000, 60000, 1000], ['', 'd', 'h', 'm', 's', 'ms']),
  };
}

// toRateText 去掉 toFixed(2) 的尾随零, 让 82.50 显示为 82.5、3.40 显示为 3.4。
function toRateText(value: number): string {
  return value.toFixed(2).replace(/\.?0+$/, "");
}

// formatRate 将输出速率(tok/s)格式化为 K/M 缩写, 口径对齐 v1 formatRate。
export function formatRate(num: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  const value = num ?? 0;
  if (value >= 1_000_000) return { raw: value, formatted: { value: toRateText(value / 1_000_000), unit: "M tok/s" } };
  if (value >= 1_000) return { raw: value, formatted: { value: toRateText(value / 1_000), unit: "K tok/s" } };
  return { raw: value, formatted: { value: toRateText(value), unit: "tok/s" } };
}

// MIN_GENERATE_RATIO 是"生成窗口"占总耗时比例的可信下限, 低于它就不认首字、回退总耗时做分母。
// 为何需要: ftut 与 use_time 取自两个独立时刻, 差值过小说明它代表的不是生成阶段 ——
// 实测既有只差 0/1ms 的取时抖动, 也有上游缓冲式交付(首帧到达时正文已全到)只差几十毫秒;
// 直接拿它做分母会把速率虚高上千倍(生产曾出现 2926 万 tok/s, 差值占比 <1% 的 286 条都在此列)。
// 阈值 10% 由 988 条实盘日志标定: 差值占比 ≥10% 的 614 条最高 160 tok/s(合理),
// 放宽到 5% 最高仍有 608 tok/s(不合理), 收紧到 20% 只再丢 61 条而最高速率不变。
const MIN_GENERATE_RATIO = 0.1;

// outputSpeed 由落库字段推导输出速度(tok/s): 生成窗口 = 总耗时扣除首字等待;
// 首字不可信(<=0 / 差值占比过小)时回退总耗时做分母; 无输出 token 或分母非正时返回 null(调用方不展示)。
export function outputSpeed(outputTokens: number, useTimeMs: number, ftutMs: number): number | null {
  if (outputTokens <= 0 || useTimeMs <= 0) return null;
  const generateMs = useTimeMs - ftutMs >= useTimeMs * MIN_GENERATE_RATIO ? useTimeMs - ftutMs : useTimeMs;
  if (generateMs <= 0) return null;
  return outputTokens / (generateMs / 1000);
}