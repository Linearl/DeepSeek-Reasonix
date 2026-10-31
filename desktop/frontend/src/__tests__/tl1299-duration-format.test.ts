// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1299-duration-format.test.ts
//
// tl1299（P3 desktop）：相对时间/绝对时长人性化显示。本文件钉死
// lib/durationFormat.ts 双契约的段位边界：
//
// 绝对时长 formatDurationLabel：
//   <1s→0秒 / 59s→秒段 / 60s→分段（含 0 秒尾） / 3599s→分段顶格 /
//   3600s→小时段（含 0 分 0 秒尾） / 86399s→小时段顶格 /
//   86400s→天段降级（丢分秒） / 多天带余数→"Dd Hh"
// 相对时间 formatRelativeAgeLabel：
//   0s→刚刚 / 59s→秒前 / 60s→分钟前 / 3599s→分钟前顶格 /
//   3600s→"H小时M分钟前" / 86399s→小时段顶格 / 86400s→天前 / 多天
//
// 三语（en/zh/zh-TW）同跑，en 直灌字典、zh/zh-TW 动态 import——三份字典的
// 键集由 Record<DictKey,string> 在 tsc 层强制一致，这里另断言值形状
// （占位符齐备 + 语言风格锚点），防止后续改字典时悄悄破坏契约。

import type { Translator } from "../lib/i18n";
import { formatDurationLabel, formatRelativeAgeLabel } from "../lib/durationFormat";
import { en } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// 直灌字典的最小 Translator（与 task973 同款拼装契约）。
function stubTranslator(dict: Record<string, string>): Translator {
  return ((key: keyof typeof en, vars?: Record<string, string | number>) => {
    let out: string = dict[key as string] ?? String(key);
    if (vars) {
      for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
    }
    return out;
  }) as Translator;
}

const tEn = stubTranslator(en as unknown as Record<string, string>);

// ── 契约①：绝对时长 formatDurationLabel（en）──────────────────────────────
{
  process.stdout.write("formatDurationLabel (en)\n");
  eq(formatDurationLabel(0, tEn), "0s", "<1s → 0s");
  eq(formatDurationLabel(0.9, tEn), "0s", "fractional <1s floors to 0s");
  eq(formatDurationLabel(1, tEn), "1s", "1s");
  eq(formatDurationLabel(59, tEn), "59s", "59s 秒段顶格");
  eq(formatDurationLabel(60, tEn), "1m 0s", "60s 进分段（含 0 秒尾）");
  eq(formatDurationLabel(61, tEn), "1m 1s", "61s");
  eq(formatDurationLabel(3599, tEn), "59m 59s", "3599s 分段顶格");
  eq(formatDurationLabel(3600, tEn), "1h 0m 0s", "3600s 进小时段（含 0 分 0 秒尾）");
  eq(formatDurationLabel(3600 + 15 * 60 + 7, tEn), "1h 15m 7s", "4507s → 1h 15m 7s（痛点样例）");
  eq(formatDurationLabel(86399, tEn), "23h 59m 59s", "86399s 小时段顶格");
  eq(formatDurationLabel(86400, tEn), "1d 0h", "86400s 进天段（丢分秒）");
  eq(formatDurationLabel(86400 + 3 * 3600, tEn), "1d 3h", "1天3小时");
  eq(formatDurationLabel(2 * 86400 + 3 * 3600, tEn), "2d 3h", "多天带余数 → 2d 3h");
  eq(formatDurationLabel(30 * 86400, tEn), "30d 0h", "30 天整");
}

// ── 契约②：相对时间 formatRelativeAgeLabel（en）──────────────────────────
{
  process.stdout.write("formatRelativeAgeLabel (en)\n");
  eq(formatRelativeAgeLabel(0, tEn), "just now", "0s → just now");
  eq(formatRelativeAgeLabel(0.9, tEn), "just now", "fractional <1s → just now");
  eq(formatRelativeAgeLabel(1, tEn), "1s ago", "1s");
  eq(formatRelativeAgeLabel(59, tEn), "59s ago", "59s 秒段顶格");
  eq(formatRelativeAgeLabel(60, tEn), "1m ago", "60s 进分钟段");
  eq(formatRelativeAgeLabel(3599, tEn), "59m ago", "3599s 分钟段顶格");
  eq(formatRelativeAgeLabel(3600, tEn), "1h 0m ago", "3600s 进小时段");
  eq(formatRelativeAgeLabel(2 * 3600 + 7 * 60, tEn), "2h 7m ago", "2h 7m ago");
  eq(formatRelativeAgeLabel(86399, tEn), "23h 59m ago", "86399s 小时段顶格");
  eq(formatRelativeAgeLabel(86400, tEn), "1d ago", "86400s → 1d ago");
  eq(formatRelativeAgeLabel(3 * 86400 + 5 * 3600, tEn), "3d ago", "多天带余数 → 3d ago（丢小时）");
}

// ── 契约③：兜底（负数 / 非有限值钳 0，不产出 nonsense）──────────────────
{
  process.stdout.write("guards\n");
  eq(formatDurationLabel(-5, tEn), "0s", "负数钳 0");
  eq(formatDurationLabel(NaN, tEn), "0s", "NaN 钳 0");
  eq(formatDurationLabel(Infinity, tEn), "0s", "Infinity 钳 0");
  eq(formatRelativeAgeLabel(-1, tEn), "just now", "负数钳 0 → just now");
  eq(formatRelativeAgeLabel(NaN, tEn), "just now", "NaN 钳 0 → just now");
}

// ── 契约④：三语字典值形状（zh / zh-TW 动态 import）────────────────────────
{
  process.stdout.write("locale shapes (zh / zh-TW)\n");
  const { zh } = await import("../locales/zh");
  const { zhTW } = await import("../locales/zh-TW");
  const tZh = stubTranslator(zh as unknown as Record<string, string>);
  const tZhTW = stubTranslator(zhTW as unknown as Record<string, string>);

  // zh 风格锚点：痛点样例 4507s → 1小时15分7秒；86400+3h → 1天3小时；
  // 相对 2h7m → 2小时7分钟前。
  eq(formatDurationLabel(4507, tZh), "1小时15分7秒", "zh 4507s → 1小时15分7秒（痛点样例）");
  eq(formatDurationLabel(86400 + 3 * 3600, tZh), "1天3小时", "zh 1天3小时");
  eq(formatDurationLabel(59, tZh), "59秒", "zh 59秒");
  eq(formatDurationLabel(60, tZh), "1分0秒", "zh 1分0秒");
  eq(formatRelativeAgeLabel(0, tZh), "刚刚", "zh 刚刚");
  eq(formatRelativeAgeLabel(2 * 3600 + 7 * 60, tZh), "2小时7分钟前", "zh 2小时7分钟前");
  eq(formatRelativeAgeLabel(86400 * 2, tZh), "2天前", "zh 2天前");

  // zh-TW 风格锚点（繁体 + 「分鐘」用词）。
  eq(formatDurationLabel(4507, tZhTW), "1小時15分7秒", "zh-TW 1小時15分7秒");
  eq(formatDurationLabel(86400 + 3 * 3600, tZhTW), "1天3小時", "zh-TW 1天3小時");
  eq(formatRelativeAgeLabel(0, tZhTW), "剛剛", "zh-TW 剛剛");
  eq(formatRelativeAgeLabel(2 * 3600 + 7 * 60, tZhTW), "2小時7分鐘前", "zh-TW 2小時7分鐘前");
  eq(formatRelativeAgeLabel(60, tZhTW), "1分鐘前", "zh-TW 1分鐘前");

  // 占位符齐备性：新 9 键在三份字典中都不缺占位符（缺了会渲染出字面 {m}）。
  const required: Array<[Record<string, string>, string[]]> = [
    [en as unknown as Record<string, string>, ["{s}"]],
    [zh as unknown as Record<string, string>, ["{s}"]],
    [zhTW as unknown as Record<string, string>, ["{s}"]],
  ];
  const keyVars: Array<[string, string[]]> = [
    ["time.duration.seconds", ["{s}"]],
    ["time.duration.minutesSeconds", ["{m}", "{s}"]],
    ["time.duration.hoursMinutesSeconds", ["{h}", "{m}", "{s}"]],
    ["time.duration.daysHours", ["{d}", "{h}"]],
    ["time.relative.secondsAgo", ["{s}"]],
    ["time.relative.minutesAgo", ["{m}"]],
    ["time.relative.hoursMinutesAgo", ["{h}", "{m}"]],
    ["time.relative.daysAgo", ["{d}"]],
  ];
  for (const [dict, _anchor] of required) {
    for (const [key, vars] of keyVars) {
      const value = dict[key];
      ok(typeof value === "string" && value.length > 0 && vars.every((v) => value.includes(v)),
        `${(dict === (en as unknown as Record<string, string>) ? "en" : dict === (zh as unknown as Record<string, string>) ? "zh" : "zh-TW")}:${key} 占位符齐备`);
    }
  }
  ok(typeof (en as unknown as Record<string, string>)["time.relative.justNow"] === "string"
    && typeof (zh as unknown as Record<string, string>)["time.relative.justNow"] === "string"
    && typeof (zhTW as unknown as Record<string, string>)["time.relative.justNow"] === "string",
    "time.relative.justNow 三语齐备");
}

process.stdout.write(`\ntl1299 duration format: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
