// Run: tsx src/__tests__/side-dock-width-tristate.test.ts
// tl 1108: 边栏方案 E 的宽度三态纯函数（App 的 preferredWorkspacePanelWidth
// 用同一公式）——无文件 246 / 有文件 min(720, vw×46%) 且受对话区 380 保底
// 约束 / 用户拖过则记住用户值。

import {
  RIGHT_DOCK_MIN_RENDER_WIDTH,
  RIGHT_DOCK_TREE_NARROW_WIDTH,
  SIDE_DOCK_PANEL_CHAT_FLOOR,
  SIDE_DOCK_WIDE_TARGET_WIDTH,
  SIDE_DOCK_WIDE_VIEWPORT_RATIO,
  clampRightDockTreeWidth,
} from "../store/layout";
import { resolveWorkspacePanelPlacement } from "../lib/workspaceLayout";

let passed = 0;
let failed = 0;

function eq(label: string, actual: unknown, expected: unknown) {
  if (Object.is(actual, expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${String(expected)}, got ${String(actual)}\n`);
    failed += 1;
  }
}

console.log("\nside dock width tristate (tl1108 方案 E)");

/** 与 App.tsx 的 sideDockWideTargetWidth useMemo 同式（对话区保底先行）。 */
function wideTarget(viewportWidth: number, sidebarWidth: number, sidebarCollapsed: boolean): number {
  const viewportCap = Math.round(viewportWidth * SIDE_DOCK_WIDE_VIEWPORT_RATIO);
  const available = Math.max(
    0,
    viewportWidth - (sidebarCollapsed ? 0 : sidebarWidth) - SIDE_DOCK_PANEL_CHAT_FLOOR - 8,
  );
  return Math.min(SIDE_DOCK_WIDE_TARGET_WIDTH, viewportCap, available);
}

eq("narrow state is the 246px rail", RIGHT_DOCK_TREE_NARROW_WIDTH, 246);
eq("wide target is 720", SIDE_DOCK_WIDE_TARGET_WIDTH, 720);
eq("viewport ratio is 46%", SIDE_DOCK_WIDE_VIEWPORT_RATIO, 0.46);
eq("chat floor is 380", SIDE_DOCK_PANEL_CHAT_FLOOR, 380);

eq("wide: roomy viewport hits the 720 target", wideTarget(1920, 264, false), 720);
eq("wide: mid viewport hits the 46% cap", wideTarget(1400, 264, false), Math.round(1400 * 0.46));
eq(
  "wide: narrow window leaves the chat pane its 380 floor",
  wideTarget(900, 264, false),
  900 - 264 - 380 - 8,
);
eq(
  "wide: collapsed sidebar widens the cap",
  wideTarget(900, 264, true),
  Math.min(720, Math.round(900 * 0.46)),
);
eq(
  "wide: an absurdly narrow window never goes negative",
  wideTarget(400, 264, false),
  0,
);

// 用户拖过记住用户值：store 的 rightDockTreeWidth 承载拖动值，wide 态取
// max(用户值, wide 目标)——用户拖得比 720 更宽时以用户为准。
eq("user value above the target wins", Math.max(clampRightDockTreeWidth(800, 1200), wideTarget(1920, 264, false)), 800);
eq("user value below the target is lifted", Math.max(clampRightDockTreeWidth(300, 1200), wideTarget(1920, 264, false)), 720);

// tl1118 回归断言：窄态 rail 246 必须能以网格形态挂载（layout--workspace-open）。
// 旧代码把 minRenderWidth 固定在 280，246 < 280 使 placement 恒进 overlay 分支，
// 右侧 dock（概览/文件/改动/任务）整体打不开。门槛必须随态取
// min(RIGHT_DOCK_MIN_RENDER_WIDTH, RIGHT_DOCK_TREE_NARROW_WIDTH) = 246。
const NARROW_MIN_RENDER_WIDTH = Math.min(RIGHT_DOCK_MIN_RENDER_WIDTH, RIGHT_DOCK_TREE_NARROW_WIDTH);

/** 与 App.tsx 窄态（sideDockWideActive=false）的 placement 参数同式。 */
function narrowPlacement() {
  return resolveWorkspacePanelPlacement({
    viewportWidth: 1440,
    sidebarCollapsed: false,
    sidebarWidth: 264,
    chatMinWidth: 400,
    resizerWidth: 8,
    open: true,
    maximized: false,
    preferredWidth: RIGHT_DOCK_TREE_NARROW_WIDTH,
    minWidth: RIGHT_DOCK_TREE_NARROW_WIDTH,
    minRenderWidth: NARROW_MIN_RENDER_WIDTH,
  });
}

const narrow = narrowPlacement();
eq("narrow: render floor follows the rail (246)", NARROW_MIN_RENDER_WIDTH, 246);
eq("narrow: dock is renderable", narrow.renderable, true);
eq("narrow: dock mounts the grid form (layout--workspace-open)", narrow.gridOpen, true);
eq("narrow: dock is not stuck in the overlay branch", narrow.overlay, false);

// 宽态门槛维持 280，真实空间不足时才回落 overlay（对话语义不回归）。
const squeezed = resolveWorkspacePanelPlacement({
  viewportWidth: 700,
  sidebarCollapsed: false,
  sidebarWidth: 264,
  chatMinWidth: 400,
  resizerWidth: 8,
  open: true,
  maximized: false,
  preferredWidth: 720,
  minWidth: 246,
  minRenderWidth: RIGHT_DOCK_MIN_RENDER_WIDTH,
});
eq("wide: a genuinely squeezed viewport still overlays", squeezed.overlay, true);
eq("wide: overlay stays renderable", squeezed.renderable, true);

// tl1129: preferred 公式升四态——宽态不变（自动加宽 + 用户值取大），窄态区分
// 「用户拖过」（尊重 rightDockTreeWidth，拖宽不再被打回 rail）与「没拖过」
// （初始 246 rail）。与 App.tsx 的 preferredWorkspacePanelWidth 三元链同式。
function preferredWidth(input: { wideActive: boolean; userWidthActive: boolean; storedWidth: number; targetWidth: number }): number {
  return input.wideActive
    ? Math.max(input.storedWidth, input.targetWidth)
    : input.userWidthActive
      ? input.storedWidth
      : 246;
}

// 宽态：公式与 tl1108 语义一致（用户值高于目标以用户为准，低于目标被抬升）。
eq("tl1129 wide+user: value above target wins", preferredWidth({ wideActive: true, userWidthActive: true, storedWidth: 800, targetWidth: 720 }), 800);
eq("tl1129 wide+user: value below target is lifted", preferredWidth({ wideActive: true, userWidthActive: true, storedWidth: 300, targetWidth: 720 }), 720);
eq("tl1129 wide without user: auto wide target applies", preferredWidth({ wideActive: true, userWidthActive: false, storedWidth: 246, targetWidth: 720 }), 720);
// 窄态 + 用户拖过：用户值接管——拖宽松手后不再回缩（tl1129 验收 2）。
eq("tl1129 narrow+user: remembered width survives", preferredWidth({ wideActive: false, userWidthActive: true, storedWidth: 500, targetWidth: 720 }), 500);
eq("tl1129 narrow+user: stored keeps the 246 floor via clamp", Math.max(246, preferredWidth({ wideActive: false, userWidthActive: true, storedWidth: 246, targetWidth: 720 })), 246);
// 窄态 + 没拖过：初始 246 rail 不回归（tl1108 语义保留，tl1129 验收 3）。
eq("tl1129 narrow without user: stays the 246 rail", preferredWidth({ wideActive: false, userWidthActive: false, storedWidth: 500, targetWidth: 720 }), 246);

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
