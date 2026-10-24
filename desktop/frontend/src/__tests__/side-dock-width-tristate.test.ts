// Run: tsx src/__tests__/side-dock-width-tristate.test.ts
// tl 1108: 边栏方案 E 的宽度三态纯函数（App 的 preferredWorkspacePanelWidth
// 用同一公式）——无文件 246 / 有文件 min(720, vw×46%) 且受对话区 380 保底
// 约束 / 用户拖过则记住用户值。

import {
  RIGHT_DOCK_TREE_NARROW_WIDTH,
  SIDE_DOCK_PANEL_CHAT_FLOOR,
  SIDE_DOCK_WIDE_TARGET_WIDTH,
  SIDE_DOCK_WIDE_VIEWPORT_RATIO,
  clampRightDockTreeWidth,
} from "../store/layout";

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

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
