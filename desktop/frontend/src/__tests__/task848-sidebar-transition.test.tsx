// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task848-sidebar-transition.test.tsx
// 任务 848 — hover 边栏展开/折回平滑过渡动画的验收钉。
//
// 背景：826/842 的 hover 展开（300ms 防误触到点）与 15s 折回在非 darwin
// 路径是瞬变（折叠 = display:none 立即卸下；dock = React 挂载/卸载）。
// 848 只补「已决定的折叠/展开」的视觉呈现层：
//   左栏  opacity/transform 180ms + display allow-discrete（折回渐隐到
//         结束才卸下 display；展开经 @starting-style 从 -18px/透明滑入）；
//   右栏 dock 挂载入场 @starting-style 渐入（卸载退场纯 CSS 不可动画，
//         由 layout 列宽 0.16s 收拢兜底——延迟卸载属改渲染时序，违反
//         动画/状态解耦约束，明确不做）；
//   门    全部规则挂 .app--ui-settled（首帧绘制完成后置位），首屏渲染
//         与状态恢复零动画；darwin 用 :not(.app--darwin) 排除（它自 826
//         起有自己的 0.22-0.28s 收缩动画，节奏不动）。
//
// 不变式（本钉的核心职责）：
//   1. 动画时长不参与任何判定——300ms 防误触窗口与 15s 折回计时仍完全
//      归 useSidebarHoverBehavior 的状态/定时器（task826 ①），hook 源码
//      与动画零耦合；
//   2. 既有钉零改动——task826 24 断言 + task842 12 断言回归全绿；
//   3. 首屏门完备——848 每条动画选择器都含 app--ui-settled，且门由
//      double rAF 置位（不是 setTimeout，不新增计时器域）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { SIDEBAR_HOVER_COLLAPSE_DELAY_MS, SIDEBAR_HOVER_EXPAND_DELAY_MS } from "../app-runtime/useSidebarHoverBehavior";

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
const hook = readFileSync(`${frontendRoot}/src/app-runtime/useSidebarHoverBehavior.ts`, "utf8");

// 848 注释块起点之后的 CSS 追加区（848 规则全部位于该块内，方便整段抽取）。
const blockStart = styles.indexOf("任务 848 — hover 展开/折回的平滑过渡");
const blockEnd = styles.indexOf(".app--darwin .sidebar--collapsed {", blockStart);
const block = blockStart >= 0 && blockEnd > blockStart ? styles.slice(blockStart, blockEnd) : "";

console.log("\ntask 848 sidebar hover expand/collapse transition (visual layer only)");

// ── ① 左栏：180ms + display allow-discrete 双向平滑 ────────────────
{
  ok(block.includes(".app--ui-settled:not(.app--darwin) .sidebar {"),
    "① left: transition rule exists, scoped to the settle gate and excluding darwin");
  const rule = block.slice(block.indexOf(".app--ui-settled:not(.app--darwin) .sidebar {"), block.indexOf("}", block.indexOf(".app--ui-settled:not(.app--darwin) .sidebar {")));
  ok(rule.includes("opacity var(--dur-base) var(--ease-out)") && rule.includes("transform var(--dur-base) var(--ease-out)"),
    "① left: opacity/transform ride the shared 180ms token + standard ease-out");
  ok(rule.includes("display var(--dur-base) allow-discrete"),
    "① left: display participates via allow-discrete (fold-back fades out before display drops; old WebView degrades to the instant cut)");
  const collapsedRule = block.slice(block.indexOf(".app--ui-settled:not(.app--darwin) .sidebar--collapsed {"), block.indexOf("}", block.indexOf(".app--ui-settled:not(.app--darwin) .sidebar--collapsed {")));
  ok(collapsedRule.includes("opacity: 0;") && collapsedRule.includes("translateX(-18px)"),
    "① left: collapsed terminal values mirror the darwin collapse language (-18px + fade)");
  const startingIdx = block.indexOf("@starting-style");
  const startingBlock = startingIdx >= 0 ? block.slice(startingIdx) : "";
  ok(startingBlock.includes(".app--ui-settled:not(.app--darwin) .sidebar:not(.sidebar--collapsed)") &&
     startingBlock.includes(".app--ui-settled .workbench-dock"),
    "① left/right: @starting-style entry origins exist for both the sidebar and the dock");
}

// ── ② 时长选型：--dur-base = 180ms，落在 150-250ms 任务书区间 ──────
{
  const tokenIdx = styles.indexOf("--dur-base:");
  const tokenLine = tokenIdx >= 0 ? styles.slice(tokenIdx, styles.indexOf(";", tokenIdx)) : "";
  const match = tokenLine.match(/--dur-base:\s*(\d+)ms/);
  const ms = match ? Number(match[1]) : 0;
  ok(ms >= 150 && ms <= 250, `② timing: --dur-base = ${ms}ms is inside the mandated 150-250ms band`);
  ok(block.includes("var(--dur-base)") && !/transition:[^;]*0\.\d+s/.test(block),
    "② timing: transitions reference the token, never a hard-coded duration (single source of truth)");
}

// ── ③ 首屏门：848 每条规则都在 .app--ui-settled 门内 + 门由 rAF 置位 ─
{
  const selectorLines = block.split("\n").filter((line) => /^\s*\.[a-z]/.test(line) && line.includes("{"));
  ok(selectorLines.length >= 4 && selectorLines.every((line) => line.includes("app--ui-settled")),
    "③ gate: every 848 selector is scoped under .app--ui-settled (first paint renders animation-free)");
  ok(app.includes('uiSettled ? "app--ui-settled" : ""'),
    "③ gate: App mounts the settle class on the app root");
  const effect = app.slice(app.indexOf("任务 848 — 首屏动画门"), app.indexOf("}, []);", app.indexOf("任务 848 — 首屏动画门")));
  ok(effect.includes("requestAnimationFrame") && !effect.includes("setTimeout"),
    "③ gate: the gate flips via double rAF (no new timer domain, cannot collide with hover timers)");
  ok(effect.includes("纯视觉呈现层标记"),
    "③ gate: the gate is documented as presentation-only (no decision input)");
}

// ── ④ 计时器与动画解耦：时长不参与判定，判定常量不出现在动画规则里 ──
{
  assert.equal(SIDEBAR_HOVER_EXPAND_DELAY_MS, 300, "guard window constant unchanged");
  assert.equal(SIDEBAR_HOVER_COLLAPSE_DELAY_MS, 15_000, "reclaim delay constant unchanged");
  ok(SIDEBAR_HOVER_EXPAND_DELAY_MS === 300 && SIDEBAR_HOVER_COLLAPSE_DELAY_MS === 15_000,
    "④ decouple: 300ms mis-touch guard and 15s reclaim constants unchanged (task826 ① owns the behavior)");
  ok(!/transition|--dur|animation/i.test(hook),
    "④ decouple: the timing hook carries no animation concept (duration is not an input to any decision)");
  const rulesOnly = block.slice(block.indexOf("*/") + 2);
  ok(!/(^|[^0-9])300([^0-9]|$)/.test(rulesOnly) && !/15[ _]?000|15s/.test(rulesOnly),
    "④ decouple: neither timing constant appears in the 848 CSS rules");
  const layoutRule = styles.slice(styles.indexOf(".layout {"), styles.indexOf("}", styles.indexOf("transition: grid-template-columns 0.16s ease")));
  ok(layoutRule.includes("grid-template-columns 0.16s ease"),
    "④ decouple: the pre-existing layout column transition is untouched (fold-back width travel unchanged)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
