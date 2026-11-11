// Run: tsx src/__tests__/theme-light-contrast-wcag.test.ts
//
// tl1546 / tl1377-V8+V9：亮色主题对比度契约钉。
// ①亮色 --fg-faint 对 --bg ≥ 4.5:1（WCAG 2.x AA 正文阈值，公式钉非拍脑袋）；
// ②暗色主题零变化（负钉：逐字节断言暗色各档 token 值不动）；
// ③var(--fg-faint) 引用面抽查（时间戳/meta/禁用态典型消费规则在位）；
// ④var(--fg-faint, <fallback>) 回退值撒谎面对齐真值（V2）。
// ⑤--scrollbar-thumb 派生（color-mix 42% fg-faint）亮色等效读数只升不降。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const stylesSource = readFileSync(resolve(testDir, "../styles.css"), "utf8");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

// ---- WCAG 2.x 相对亮度与对比度（公式入测试，钉为计算值而非常量对照） ----

function srgbChannelToLinear(channel: number): number {
  return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
}

function relativeLuminance(hex: string): number {
  const h = hex.replace("#", "");
  const r = srgbChannelToLinear(parseInt(h.slice(0, 2), 16) / 255);
  const g = srgbChannelToLinear(parseInt(h.slice(2, 4), 16) / 255);
  const b = srgbChannelToLinear(parseInt(h.slice(4, 6), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function wcagContrast(a: string, b: string): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** srgb color-mix(in srgb, c1 p%, transparent) 等效混入不透明底后的等效色。 */
function mixOver(c1: string, p: number, backdrop: string): string {
  const p1 = p / 100;
  const a = [0, 2, 4].map((i) => parseInt(c1.replace("#", "").slice(i, i + 2), 16));
  const b = [0, 2, 4].map((i) => parseInt(backdrop.replace("#", "").slice(i, i + 2), 16));
  const m = a.map((ch, i) => Math.round(p1 * ch + (1 - p1) * b[i]));
  return `#${m.map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

// ---- CSS 块提取（同 theme-auto-background.test.ts 的 blockAfter 形态） ----

function blockAfter(source: string, needle: string, after = 0): string {
  const selectorIndex = source.indexOf(needle, after);
  if (selectorIndex < 0) return "";
  const open = source.indexOf("{", selectorIndex);
  if (open < 0) return "";
  let depth = 0;
  for (let index = open; index < source.length; index += 1) {
    const char = source[index];
    if (char === "{") depth += 1;
    else if (char === "}") {
      depth -= 1;
      if (depth === 0) return source.slice(open + 1, index);
    }
  }
  return "";
}

function extractColor(block: string, token: string): string {
  const match = block.match(new RegExp(`${token}:\\s*(#[0-9a-fA-F]{6})`));
  return match ? match[1] : "";
}

// 基础 :root 块（暗色基准档，文件第一个 :root）。
const rootBlock = blockAfter(stylesSource, ":root {");
// auto 亮色档：第一个 prefers-color-scheme: light media 内的 :root:not([data-theme])。
const autoLightMedia = stylesSource.indexOf("@media (prefers-color-scheme: light)");
const autoLightBlock = blockAfter(stylesSource, ":root:not([data-theme])", autoLightMedia);
// 强制亮色档：文件第一个 :root[data-theme="light"]。
const forcedLightBlock = blockAfter(stylesSource, ':root[data-theme="light"]');

const lightBlocks: Array<[string, string]> = [
  ["auto-light (prefers-color-scheme)", autoLightBlock],
  ["forced-light (data-theme=light)", forcedLightBlock],
];

console.log("\ntl1546 light contrast WCAG contract");

// ---- ①亮色正钉：fg-faint / list-row-meta 对各亮色背景 ≥4.5:1 ----

for (const [label, block] of lightBlocks) {
  const fgFaint = extractColor(block, "--fg-faint");
  const listMeta = extractColor(block, "--list-row-meta");
  const bg = extractColor(block, "--bg");
  const bgElev = extractColor(block, "--bg-elev");
  const sidebarBg = extractColor(block, "--sidebar-bg");

  ok(fgFaint !== "" && bg !== "", `${label}: --fg-faint/--bg 可提取`);
  ok(wcagContrast(fgFaint, bg) >= 4.5, `${label}: fg-faint ${fgFaint} vs bg ${bg} = ${wcagContrast(fgFaint, bg).toFixed(2)}:1 ≥ 4.5`);
  ok(wcagContrast(fgFaint, bgElev) >= 4.5, `${label}: fg-faint vs bg-elev ${bgElev} = ${wcagContrast(fgFaint, bgElev).toFixed(2)}:1 ≥ 4.5`);
  ok(wcagContrast(fgFaint, sidebarBg) >= 4.5, `${label}: fg-faint vs sidebar-bg ${sidebarBg} = ${wcagContrast(fgFaint, sidebarBg).toFixed(2)}:1 ≥ 4.5`);
  ok(listMeta !== "" && wcagContrast(listMeta, bg) >= 4.5, `${label}: list-row-meta ${listMeta} vs bg = ${wcagContrast(listMeta, bg).toFixed(2)}:1 ≥ 4.5`);
  // 旧值显式禁用（防复制粘贴回滚；公式断言本身也会拦，此为语义显式化）。
  ok(fgFaint !== "#8a94a6" && listMeta !== "#8a94a6", `${label}: 旧值 #8a94a6 已退场`);
}

// ---- ②暗色负钉：暗色各档 --fg-faint 逐字节不变 ----

ok(rootBlock.includes("--fg-faint: #858b96;"), "dark :root --fg-faint 逐字节 #858b96 不变");
ok(stylesSource.includes('--fg-faint: #6c6e74;'), "graphite dark --fg-faint #6c6e74 不变");
ok(stylesSource.includes('--fg-faint: #726c8c;'), "aurora dark --fg-faint #726c8c 不变");
ok(stylesSource.includes('--fg-faint: #646c7a;'), "slate dark --fg-faint #646c7a 不变");
ok(stylesSource.includes('--fg-faint: #6f6a61;'), "carbon dark --fg-faint #6f6a61 不变");
ok(stylesSource.includes('--fg-faint: #6e6a85;'), "nocturne dark --fg-faint #6e6a85 不变");
ok(wcagContrast("#858b96", "#0c0d10") >= 4.5, `dark 读数保持合规: #858b96 vs #0c0d10 = ${wcagContrast("#858b96", "#0c0d10").toFixed(2)}:1 ≥ 4.5`);

// ---- ③scrollbar-thumb 派生核对（color-mix 42% fg-faint，亮色等效读数只升不降） ----

ok(
  rootBlock.includes("--scrollbar-thumb: color-mix(in srgb, var(--fg-faint) 42%, transparent);"),
  ":root --scrollbar-thumb 派生公式（42% fg-faint）未变",
);
const oldThumbOnBg = wcagContrast(mixOver("#8a94a6", 42, "#f7f8fb"), "#f7f8fb");
const newThumbOnBg = wcagContrast(mixOver("#67717f", 42, "#f7f8fb"), "#f7f8fb");
ok(newThumbOnBg > oldThumbOnBg, `scrollbar-thumb 亮色等效读数只升不降: ${oldThumbOnBg.toFixed(3)} → ${newThumbOnBg.toFixed(3)}`);

// ---- ④回退值撒谎面对齐（V2）：var(--fg-faint, <hex>) 色值 fallback == 真值 ----

ok(!stylesSource.includes("#8a8f98"), "旧 fallback 色 #8a8f98 已退场");
const fallbacks = [...stylesSource.matchAll(/var\(--fg-faint,\s*(#[0-9a-fA-F]{6})\)/g)].map((m) => m[1]);
ok(fallbacks.length > 0, `存在 var(--fg-faint, <hex>) fallback 面（${fallbacks.length} 处）`);
ok(fallbacks.every((f) => f === "#67717f"), "全部 fg-faint 色值 fallback 对齐真值 #67717f");

// ---- ⑤var(--fg-faint) 引用面抽查（时间戳/meta/禁用态典型消费规则） ----

const referenceCount = (stylesSource.match(/var\(--fg-faint[,)]/g) ?? []).length;
process.stdout.write(`  INFO  var(--fg-faint…) 引用总数（styles.css）= ${referenceCount}（tl1377 报告基线 642，随开发浮动）\n`);
ok(referenceCount > 600, "fg-faint 仍为全局令牌（引用规模 >600）");
ok(/\.msg-meta\s*\{[^}]*color:\s*var\(--fg-faint\)/.test(stylesSource), "抽查: .msg-meta（消息时间戳行）直引 var(--fg-faint)");
ok(/\.msg-meta__time\s*\{[^}]*color:\s*var\(--fg-faint\)/.test(stylesSource), "抽查: .msg-meta__time 直引 var(--fg-faint)");
ok(rootBlock.includes("--control-disabled-fg: var(--fg-faint);"), "抽查: --control-disabled-fg 派生自 fg-faint");
ok(/\.ask-shelf__pager-button:disabled\s*\{[^}]*color:\s*var\(--fg-faint\)/.test(stylesSource), "抽查: ask-shelf 禁用态直引 var(--fg-faint)");
ok(/\.typography-settings__undo:disabled\s*\{[^}]*color:\s*var\(--fg-faint\)/.test(stylesSource), "抽查: 排版设置撤销钮禁用态直引 var(--fg-faint)");
ok(rootBlock.includes("--list-row-meta: var(--fg-faint);"), "抽查: 暗色 --list-row-meta 派生链未动");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
