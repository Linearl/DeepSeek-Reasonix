// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task842-sidebar-hover-zone.test.tsx
// 任务 842 — 826 hover 边栏「开关开但无反应」排查修复的验收钉。
//
// 排查结论（详见交付报告 zcode交付-842-hover无反应排查-20261016.md）：
//   开关读取链路（Go 双视图→bridge→labFlags→App state→热区渲染）静态逐环
//   全通；真实 Chromium 端到端复验（CDP 硬件级鼠标）hover 展开/离开 15s 折回
//   全绿——软件链路无断链。无反应的真因在交互面可用性：
//   ① 热区仅 6px 宽，真人鼠标以正常速度贴边无法在 6px 内驻留满 300ms 防误触
//      窗口（CDP 精确点放可以，人手不行）；
//   ② 热区与 Wails 无边框窗口边缘缩放带完全重叠（borderThickness 默认同为
//      6px），指针贴边先被 useWailsResizeFix 改成 w-resize 光标——「窗口
//      边缘」的反馈让用户不停留；贴边按下则被 Wails 抢去拖窗口。
// 修复：热区 6→12px + cursor 折回 default + hover 亮 2px 竖线提示 +
//   onMouseDown 清 Wails resizeEdge（让位，仅热区挂载时）。
//
// 本钉钉住修复面：CSS 三性质 + App 接线 + clearWailsResizeEdge 行为 +
// 渲染条件不回退（826 语义保持）。hook 时序行为由 task826 ① 覆盖，不重复。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const app = readFileSync(`${frontendRoot}/src/App.tsx`, "utf8");
const styles = readFileSync(`${frontendRoot}/src/styles.css`, "utf8");
const resizeFix = readFileSync(`${frontendRoot}/src/lib/useWailsResizeFix.ts`, "utf8");

console.log("\ntask 842 sidebar hover-zone usability fix (width, cursor, affordance, mousedown yield)");

// ── ① 热区 CSS：宽度、光标、hover 提示 ─────────────────────────────
{
  const block = styles.slice(styles.indexOf(".sidebar-hover-zone {"), styles.indexOf(".sidebar-hover-zone--left {"));
  ok(/\bwidth: 12px;/.test(block), "① zone: widened 6px → 12px (human-scale dwell target)");
  ok(/cursor: default;/.test(block), "① zone: cursor defaults (overrides the w-resize feedback from the edge-resize fix)");
  ok(styles.includes(".sidebar-hover-zone::after") &&
     styles.includes(".sidebar-hover-zone--left::after") &&
     styles.includes(".sidebar-hover-zone--right::after") &&
     styles.includes(".sidebar-hover-zone:hover::after"),
    "① zone: 2px accent affordance line on hover, per side");
  ok(/transition: opacity 0\.12s ease;/.test(styles.slice(styles.indexOf(".sidebar-hover-zone::after"), styles.indexOf(".sidebar-hover-zone:hover::after"))),
    "① zone: affordance fades in fast (0.12s) so the cue lands inside the 300ms guard");
}

// ── ② App 接线：mousedown 让位 + 渲染条件不回退 ────────────────────
{
  ok(app.includes('import { clearWailsResizeEdge, useWailsResizeFix } from "./lib/useWailsResizeFix";'),
    "② App: clearWailsResizeEdge imported from the resize-fix module");
  const leftZone = app.slice(app.indexOf('sidebar-hover-zone sidebar-hover-zone--left"'), app.indexOf("</div>", app.indexOf('sidebar-hover-zone sidebar-hover-zone--left"')));
  const rightZone = app.slice(app.indexOf('sidebar-hover-zone sidebar-hover-zone--right"'), app.indexOf("</div>", app.indexOf('sidebar-hover-zone sidebar-hover-zone--right"')));
  ok(leftZone.includes("onMouseDown={clearWailsResizeEdge}") && rightZone.includes("onMouseDown={clearWailsResizeEdge}"),
    "② App: both zones yield the mousedown to the hover affordance (no window-resize steal)");
  ok(leftZone.includes("onMouseEnter={leftSidebarHover.onEnter}") && rightZone.includes("onMouseEnter={rightDockHover.onEnter}"),
    "② App: zone enter wiring unchanged (826 semantics intact)");
  ok(app.includes("sidebarHoverLeftEnabled && sidebarCollapsed && !managementActive") &&
     app.includes("sidebarHoverRightEnabled && !effectiveWorkspacePanelRenderable && !managementActive"),
    "② App: render gates unchanged — switch off ⇒ no element (铁律 2, guards against 842 drift)");
}

// ── ③ clearWailsResizeEdge 行为钉（真实函数，jsdom window）─────────
{
  const { JSDOM } = (await import("jsdom")).default;
  const dom = new JSDOM("<div id='root'></div>");
  const prevWindow = (globalThis as { window?: unknown }).window;
  (globalThis as { window?: unknown }).window = dom.window;
  try {
    const { clearWailsResizeEdge } = await import(pathToFileURL(`${frontendRoot}/src/lib/useWailsResizeFix.ts`).href);
    const w = dom.window as Window & { wails?: { flags?: { resizeEdge?: string; borderThickness?: number } } };

    // ③-a 有 edge 标记时清掉（Wails 的 window 级 mousedown 随后读到 undefined）。
    w.wails = { flags: { resizeEdge: "w-resize", borderThickness: 6 } };
    clearWailsResizeEdge();
    ok(w.wails?.flags?.resizeEdge === undefined, "③ clearWailsResizeEdge: clears the pending edge flag");

    // ③-b 无 edge 时 no-op。
    clearWailsResizeEdge();
    ok(w.wails?.flags?.resizeEdge === undefined, "③ clearWailsResizeEdge: no-op when no edge is pending");

    // ③-c 无 wails runtime（浏览器预览/非 Wails 壳）不抛。
    delete w.wails;
    let threw = false;
    try { clearWailsResizeEdge(); } catch { threw = true; }
    ok(!threw, "③ clearWailsResizeEdge: safe without a wails runtime");
  } finally {
    (globalThis as { window?: unknown }).window = prevWindow;
    dom.window.close();
  }
  ok(resizeFix.includes("export function clearWailsResizeEdge(): void"),
    "③ clearWailsResizeEdge lives beside the resize-flag domain logic");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
