// Run: tsx src/__tests__/theme-light-contrast-wcag.test.ts
//
// tl1546 / tl1377-V8+V9：亮色主题对比度契约钉。
// ①亮色 --fg-faint 对 --bg ≥ 4.5:1（WCAG 2.x AA 正文阈值，公式钉非拍脑袋）；
// ②暗色主题零变化（负钉：逐字节断言暗色各档 token 值不动）；
// ③var(--fg-faint) 引用面抽查（时间戳/meta/禁用态典型消费规则在位）；
// ④var(--fg-faint, <fallback>) 回退值撒谎面对齐真值（V2）。
// ⑤--scrollbar-thumb 派生（color-mix 42% fg-faint）亮色等效读数只升不降。
// tl1546 打回重修（r2）扩面：
// ⑥全风格 light 档（graphite/aurora/slate/carbon/nocturne）--fg-faint 对各自档
//   bg/bg-elev/sidebar-bg ≥ 4.5:1（第一轮只修通用 auto/forced 两档，装机实测打回）；
// ⑥a amber 档无 token 覆盖走通用兜底 #67717f，兜底路径逐对实算达标；
// ⑨官方主题包 tokens.light.fgFaint 对各自包 bg/bgElev/sidebar ≥ 4.5:1 +
//   accent 族（takeover/主按钮配色链 accentFg vs accent，light 与 dark）≥ 4.5:1。

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

console.log("\ntl1546 danger/yolo foreground WCAG contract (V9)");

// ---- ⑥亮色正钉：yolo / danger 前景深字 ≥4.5:1 ----

// --danger 本体只在 :root 定义（暗色基准），亮色档不覆盖 → 从 rootBlock 提取底色。
const dangerBase = extractColor(rootBlock, "--danger");

for (const [label, block] of lightBlocks) {
  const yoloFg = extractColor(block, "--mode-yolo-fg");
  const yoloBg = extractColor(block, "--mode-yolo-bg");
  const dangerFg = extractColor(block, "--danger-fg");

  ok(yoloFg !== "" && yoloBg !== "", `${label}: --mode-yolo-fg/--mode-yolo-bg 可提取`);
  ok(wcagContrast(yoloFg, yoloBg) >= 4.5, `${label}: yolo-fg ${yoloFg} vs yolo-bg ${yoloBg} = ${wcagContrast(yoloFg, yoloBg).toFixed(2)}:1 ≥ 4.5`);
  ok(dangerFg !== "" && wcagContrast(dangerFg, dangerBase) >= 4.5, `${label}: danger-fg ${dangerFg} vs danger ${dangerBase} = ${wcagContrast(dangerFg, dangerBase).toFixed(2)}:1 ≥ 4.5`);
  ok(yoloFg !== "#fff" && dangerFg !== "#fff", `${label}: 白字前景已退场`);
}

// ---- ⑦暗色负钉：danger/yolo/autopilot 暗色基准逐字节不变 ----

ok(rootBlock.includes("--danger: #e5484d;") && rootBlock.includes("--danger-fg: #fff;"), "dark :root --danger/--danger-fg 逐字节不变");
ok(
  rootBlock.includes("--mode-yolo-bg: #f04b52;") &&
    rootBlock.includes("--mode-yolo-border: #ff6268;") &&
    rootBlock.includes("--mode-yolo-fg: #fff;"),
  "dark :root yolo 三元组逐字节不变（暗色白字 3.6:1 ≥AA-large 现状保持）",
);
ok(
  rootBlock.includes("--mode-autopilot-bg: #f97316;") && rootBlock.includes("--mode-autopilot-fg: #2b1503;"),
  "dark :root autopilot 三元组不动（595/614 暗色档）",
);
ok(
  autoLightBlock.includes("--mode-autopilot-bg: #ea580c;") && autoLightBlock.includes("--mode-autopilot-fg: #2b1503;"),
  "light autopilot 先例（614）不动：#2b1503 vs #ea580c = 4.87 保持",
);
ok(wcagContrast("#ffffff", "#f04b52") >= 3, `dark yolo 白字读数保持 AA-large: ${wcagContrast("#ffffff", "#f04b52").toFixed(2)}:1 ≥ 3`);
ok(rootBlock.includes("--pill-yolo-fg: var(--danger-fg);"), "pill 消费链 --pill-yolo-fg→var(--danger-fg) 未动");

// ---- ⑧composer-approval-select 混合底连带面（yolo-fg 字直接铺 color-mix 底） ----

ok(
  /color-mix\(in srgb, var\(--mode-yolo-bg\) 55%, var\(--surface-2\)\)/.test(stylesSource),
  ".composer-approval-select--yolo 混合底公式（55% yolo-bg + surface-2）未动",
);
const bgElev2Light = extractColor(forcedLightBlock, "--bg-elev-2");
const yoloBgLight = extractColor(forcedLightBlock, "--mode-yolo-bg");
const yoloFgLight = extractColor(forcedLightBlock, "--mode-yolo-fg");
const selMix = mixOver(yoloBgLight, 55, bgElev2Light);
ok(wcagContrast(yoloFgLight, selMix) >= 4.5, `approval-select--yolo 亮色混合底 ${selMix} 上前景 = ${wcagContrast(yoloFgLight, selMix).toFixed(2)}:1 ≥ 4.5（改前白字 2.24）`);

console.log("\ntl1546-r2 全风格 light 档 × 各自 bg WCAG contract（打回重修扩面）");

// ---- ⑥全风格档 light 正钉：--fg-faint 对各自档 bg/bg-elev/sidebar-bg ≥4.5:1 ----
// 每档 forced-light 与 auto-light 两块同值同钉；--text-3/--control-disabled-fg
// 与 --fg-faint 同值同替（压深时三 token 一致性钉）；层级钉保 faint 仍浅于 fg-dim。

const styleLights: Array<{ style: string; prev: string; next: string }> = [
  { style: "graphite", prev: "#85888f", next: "#6a6d74" },
  { style: "aurora", prev: "#8c84a2", next: "#72698b" },
  { style: "slate", prev: "#828a99", next: "#676f7e" },
  { style: "carbon", prev: "#8a8378", next: "#736d63" },
  { style: "nocturne", prev: "#8a85a0", next: "#706a88" },
];

for (const { style, prev, next } of styleLights) {
  const forcedBlock = blockAfter(stylesSource, `:root[data-theme="light"][data-theme-style="${style}"]`);
  const autoBlock = blockAfter(stylesSource, `:root[data-theme-style="${style}"]:not([data-theme])`);
  const blocks: Array<[string, string]> = [
    ["forced-light", forcedBlock],
    ["auto-light", autoBlock],
  ];
  for (const [label, block] of blocks) {
    const fgFaint = extractColor(block, "--fg-faint");
    const fgDim = extractColor(block, "--fg-dim");
    const bg = extractColor(block, "--bg");
    const bgElev = extractColor(block, "--bg-elev");
    const sidebarBg = extractColor(block, "--sidebar-bg");
    ok(fgFaint !== "" && bg !== "", `${style} ${label}: --fg-faint/--bg 可提取`);
    ok(wcagContrast(fgFaint, bg) >= 4.5, `${style} ${label}: fg-faint ${fgFaint} vs bg ${bg} = ${wcagContrast(fgFaint, bg).toFixed(2)}:1 ≥ 4.5`);
    ok(wcagContrast(fgFaint, bgElev) >= 4.5, `${style} ${label}: fg-faint vs bg-elev ${bgElev} = ${wcagContrast(fgFaint, bgElev).toFixed(2)}:1 ≥ 4.5`);
    ok(wcagContrast(fgFaint, sidebarBg) >= 4.5, `${style} ${label}: fg-faint vs sidebar-bg ${sidebarBg} = ${wcagContrast(fgFaint, sidebarBg).toFixed(2)}:1 ≥ 4.5`);
    ok(
      fgDim !== "" && wcagContrast(fgFaint, bg) < wcagContrast(fgDim, bg),
      `${style} ${label}: 层级保持（faint ${wcagContrast(fgFaint, bg).toFixed(2)} < dim ${wcagContrast(fgDim, bg).toFixed(2)} 对 bg）`,
    );
    // 三 token 同值同替（压深一致性）：text-3 / control-disabled-fg 与 fg-faint 同值。
    ok(block.includes(`--text-3: ${fgFaint};`), `${style} ${label}: --text-3 与 --fg-faint 同值（${fgFaint}）`);
    ok(block.includes(`--control-disabled-fg: ${fgFaint};`), `${style} ${label}: --control-disabled-fg 与 --fg-faint 同值（${fgFaint}）`);
  }
  // 旧值显式退场 + 新值在位（防复制粘贴回滚；公式断言本身也会拦，此为语义显式化）。
  ok(!stylesSource.includes(`--fg-faint: ${prev};`), `${style}: 旧值 --fg-faint: ${prev} 已退场`);
  ok(!stylesSource.includes(`--text-3: ${prev};`), `${style}: 旧值 --text-3: ${prev} 已退场`);
  ok(stylesSource.includes(`--fg-faint: ${next};`), `${style}: 新值 --fg-faint: ${next} 在位`);
}

// ---- ⑥a amber 档兜底路径：无 --fg-faint 覆盖，走通用 forced-light #67717f ----

const amberForcedBlock = blockAfter(stylesSource, ':root[data-theme="light"][data-theme-style="amber"]');
ok(amberForcedBlock !== "", "amber forced-light 块可提取");
ok(!amberForcedBlock.includes("--fg-faint"), "amber 档无 --fg-faint 覆盖（走通用 light 兜底 #67717f）");
ok(amberForcedBlock.includes("--sidebar-bg: #f3f6fa;"), "amber 档 sidebar 覆盖 #f3f6fa 在位");
ok(wcagContrast("#67717f", "#f7f8fb") >= 4.5, `amber 兜底: #67717f vs 通用 bg #f7f8fb = ${wcagContrast("#67717f", "#f7f8fb").toFixed(2)}:1 ≥ 4.5`);
ok(wcagContrast("#67717f", "#ffffff") >= 4.5, `amber 兜底: #67717f vs 通用 bg-elev #ffffff = ${wcagContrast("#67717f", "#ffffff").toFixed(2)}:1 ≥ 4.5`);
ok(wcagContrast("#67717f", "#f3f6fa") >= 4.5, `amber 兜底: #67717f vs amber sidebar #f3f6fa = ${wcagContrast("#67717f", "#f3f6fa").toFixed(2)}:1 ≥ 4.5`);

console.log("\ntl1546-r2 官方主题包 fg-faint / accent 族 WCAG contract");

// ---- ⑨官方主题包：tokens.light.fgFaint 对各自包三背景 ≥4.5；accent 族 ≥4.5 ----
// takeover/主按钮配色链 = --control-primary-bg/--control-primary-fg，pack 注入值
// 与 accent/accentFg 相同（TOKEN_TO_CSS 映射），故 accent 族钉覆盖该消费面。

const officialPacks: Array<{ id: string; prevFgFaint: string }> = [
  { id: "official-crimson-horizon", prevFgFaint: "#A9807D" },
  { id: "official-cyan-stage", prevFgFaint: "#71939D" },
  { id: "official-fortune-forge", prevFgFaint: "#A98A68" },
  { id: "official-noir-gold", prevFgFaint: "#9A8F74" },
  { id: "official-rose-dawn", prevFgFaint: "#A97B87" },
  { id: "official-sage-breeze", prevFgFaint: "#7C8F83" },
  { id: "official-spark-notebook", prevFgFaint: "#848D98" },
  { id: "official-violet-starlight", prevFgFaint: "#9188B3" },
];

for (const { id, prevFgFaint } of officialPacks) {
  const raw = readFileSync(resolve(testDir, "../../../themes/official", id, "theme.json"), "utf8");
  const pack = JSON.parse(raw) as { tokens?: { light?: Record<string, string>; dark?: Record<string, string> } };
  const light = pack.tokens?.light;
  const dark = pack.tokens?.dark;
  ok(Boolean(light?.fgFaint && light?.bg && light?.bgElev && light?.sidebar), `${id}: light fgFaint/bg/bgElev/sidebar 可提取`);
  if (light?.fgFaint && light?.bg && light?.bgElev && light?.sidebar) {
    ok(wcagContrast(light.fgFaint, light.bg) >= 4.5, `${id}: light fg-faint ${light.fgFaint} vs bg ${light.bg} = ${wcagContrast(light.fgFaint, light.bg).toFixed(2)}:1 ≥ 4.5`);
    ok(wcagContrast(light.fgFaint, light.bgElev) >= 4.5, `${id}: light fg-faint vs bg-elev ${light.bgElev} = ${wcagContrast(light.fgFaint, light.bgElev).toFixed(2)}:1 ≥ 4.5`);
    ok(wcagContrast(light.fgFaint, light.sidebar) >= 4.5, `${id}: light fg-faint vs sidebar ${light.sidebar} = ${wcagContrast(light.fgFaint, light.sidebar).toFixed(2)}:1 ≥ 4.5`);
    ok(light.fgFaint.toUpperCase() !== prevFgFaint.toUpperCase(), `${id}: 旧值 ${prevFgFaint} 已退场`);
  }
  // accent 族（tl1546 关联核查面：sage-breeze ask 接管按钮等主按钮配色链）。
  if (light?.accent && light?.accentFg) {
    ok(wcagContrast(light.accentFg, light.accent) >= 4.5, `${id}: light accent-fg ${light.accentFg} vs accent ${light.accent} = ${wcagContrast(light.accentFg, light.accent).toFixed(2)}:1 ≥ 4.5`);
  }
  if (dark?.accent && dark?.accentFg) {
    ok(wcagContrast(dark.accentFg, dark.accent) >= 4.5, `${id}: dark accent-fg ${dark.accentFg} vs accent ${dark.accent} = ${wcagContrast(dark.accentFg, dark.accent).toFixed(2)}:1 ≥ 4.5`);
  }
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
