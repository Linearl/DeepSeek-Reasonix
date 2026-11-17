// tl1671: 思考块指标（耗时 / 字数）的口径与格式化。
//
// 耗时口径：reasoning 块的首末时间戳差（毫秒）。
//   · 实时流：前端在流上量——live.reasoningStartedAt → live.reasoningCompletedAt
//     （useController 的 reasoningDurationMs，首个思考 delta → 思考收尾）。
//   · 历史会话：后端随助手消息落库的 reasoningDurationMs 回填（同一口径：
//     首个思考文本 → 首个答复输出）。
//   展示精度：一位小数（对齐上游 studio 的「思考 0.4 秒」形态）。
//
// 字数口径：思考正文的 Unicode 码点数（code point，Array.from 展开），
// 与上游 studio 的 [...text].length 同口径。组合字符（ZWJ emoji 序列、
// 基线+变音符）按多个码点计，因此「码点数」可能略大于人眼看到的可见字符数。
// 本机不引 Intl.Segmenter：studio 也没引，两边口径一致比口径更精细重要。

import type { Locale } from "./i18n";

/** 思考正文的字数（Unicode 码点数）。 */
export function reasoningCharCount(text: string | undefined): number {
  if (!text) return 0;
  return Array.from(text).length;
}

function numberLocale(locale: Locale | string): string {
  if (locale === "zh") return "zh-CN";
  if (locale === "zh-TW") return "zh-TW";
  return "en";
}

/**
 * 一位小数的秒。输入是秒（调用方把毫秒换算好），输出对齐 studio 的
 * decimals(v, 1)：0.4 → "0.4"，12 → "12.0"。
 */
export function formatThinkingSeconds(seconds: number, locale: Locale | string): string {
  const safe = Number.isFinite(seconds) ? Math.max(0, seconds) : 0;
  return safe.toLocaleString(numberLocale(locale), { minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

/** 字数（千分位分组，与 studio 的 count() 同形）。 */
export function formatThinkingCharCount(chars: number, locale: Locale | string): string {
  const safe = Number.isFinite(chars) ? Math.max(0, Math.round(chars)) : 0;
  return safe.toLocaleString(numberLocale(locale));
}

/**
 * 耗时是否可展示。缺值/非有限/非正一律当作「没有这一项」——
 * 卡片据此只显示字数行，不显示一个假的 0.0 秒。
 */
export function hasThinkingDuration(durationMs: number | undefined): durationMs is number {
  return typeof durationMs === "number" && Number.isFinite(durationMs) && durationMs > 0;
}
