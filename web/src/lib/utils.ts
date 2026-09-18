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

// outputSpeed 由落库字段推导输出速度(tok/s): 生成窗口 = 总耗时扣除首字等待;
// ftut 无效(<=0 或 >= use_time)时回退总耗时做分母; 无输出 token 或分母非正时返回 null(调用方不展示)。
export function outputSpeed(outputTokens: number, useTimeMs: number, ftutMs: number): number | null {
  if (outputTokens <= 0 || useTimeMs <= 0) return null;
  const generateMs = ftutMs > 0 && ftutMs < useTimeMs ? useTimeMs - ftutMs : useTimeMs;
  if (generateMs <= 0) return null;
  return outputTokens / (generateMs / 1000);
}