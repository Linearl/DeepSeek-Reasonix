// tl1299: unified human-readable time formatting for every bare-seconds
// display (tab status "最后事件 Ns 前", run elapsed "4507m 46s", quota wait
// "预计 4507 秒后继续", …). Two faces, one contract each:
//
// ① formatDurationLabel — absolute duration ("运行时长"):
//      <1s      → "0s" / "0秒"
//      <60s     → "59s" / "59秒"
//      <1h      → "1m 0s" / "1分0秒"
//      <1d      → "1h 15m 7s" / "1小时15分7秒"
//      ≥1d      → "2d 3h" / "2天3小时"   (days/hours degradation: min/sec
//                                          below the day band are noise)
// ② formatRelativeAgeLabel — age of a past event ("xx 前"):
//      <1s      → "just now" / "刚刚"
//      <60s     → "59s ago" / "59秒前"
//      <1h      → "12m ago" / "12分钟前"
//      <1d      → "2h 7m ago" / "2小时7分钟前"
//      ≥1d      → "3d ago" / "3天前"
//
// Inputs are SECONDS (callers quantize ms with their existing rounding, so
// per-surface ceil/floor semantics stay put). Negative / non-finite input
// clamps to 0 — the em-dash "no data" guards stay at the call sites.

import type { Translator } from "./i18n";

function safeSeconds(seconds: number): number {
  if (!Number.isFinite(seconds)) return 0;
  return Math.max(0, Math.floor(seconds));
}

/** Absolute duration label; see module header for the band contract. */
export function formatDurationLabel(seconds: number, t: Translator): string {
  const s = safeSeconds(seconds);
  if (s < 60) return t("time.duration.seconds", { s });
  const minutes = Math.floor(s / 60);
  if (minutes < 60) return t("time.duration.minutesSeconds", { m: minutes, s: s % 60 });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("time.duration.hoursMinutesSeconds", { h: hours, m: minutes % 60, s: s % 60 });
  const days = Math.floor(hours / 24);
  return t("time.duration.daysHours", { d: days, h: hours % 24 });
}

/** Relative age-of-event label; see module header for the band contract. */
export function formatRelativeAgeLabel(seconds: number, t: Translator): string {
  const s = safeSeconds(seconds);
  if (s < 1) return t("time.relative.justNow");
  if (s < 60) return t("time.relative.secondsAgo", { s });
  const minutes = Math.floor(s / 60);
  if (minutes < 60) return t("time.relative.minutesAgo", { m: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t("time.relative.hoursMinutesAgo", { h: hours, m: minutes % 60 });
  const days = Math.floor(hours / 24);
  return t("time.relative.daysAgo", { d: days });
}
