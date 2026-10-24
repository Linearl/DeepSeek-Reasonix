// Run: tsx src/__tests__/side-dock-user-width.test.ts
//
// tl1129: 「宽度调整好后自动回缩」回归钉。tl1108 三态公式的窄态分支把
// preferred 钉死在 246 rail，用户拖分隔条松手后存进 rightDockTreeWidth 的
// 用户值被无视，同一渲染帧被打回 rail。修复引入 sideDockUserWidthActive：
// 显式宽度写入（setSavedWorkspacePanelWidth / onWidthPreset）置位，关面板
// 收口清位，切 tab 不清。本文件钉两层：
// ① store 层——指示位的初始值、置位/清位行为（真实 zustand store，非 mock）；
// ② 接线层——App.tsx / useShellGeometry.ts 的公式分支与置位/清位调用点
//    （源码静态断言，composer-autosize-scrollbar-css.test.ts 先例：这些是
//    渲染编排行为，组件级挂载 App.tsx 不可行，静态断言防回归漂移）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { useLayoutStore } from "../store/layout";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const shellGeometrySource = readFileSync(resolve(testDir, "../app-runtime/useShellGeometry.ts"), "utf8");

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

function eq(actual: unknown, expected: unknown, label: string) {
  ok(Object.is(actual, expected), `${label} (actual=${String(actual)}, expected=${String(expected)})`);
}

console.log("\nside dock user width (tl1129 拖宽不回缩)");

// ── store 层：指示位的真实行为 ────────────────────────────────────────────
eq(useLayoutStore.getState().sideDockUserWidthActive, false, "指示位初始为 false（未拖过 ⇒ 246 rail）");

useLayoutStore.getState().setSideDockUserWidthActive(true);
eq(useLayoutStore.getState().sideDockUserWidthActive, true, "置位 true 生效（拖宽松手后窄态尊重用户值）");

useLayoutStore.getState().setSideDockUserWidthActive(true);
eq(useLayoutStore.getState().sideDockUserWidthActive, true, "重复置位幂等（等值守卫）");

useLayoutStore.getState().setSideDockUserWidthActive(false);
eq(useLayoutStore.getState().sideDockUserWidthActive, false, "清位 false 生效（关面板收口回落 rail）");

// ── 接线层：App.tsx 公式与置位/清位调用点 ────────────────────────────────
// 窄态第三分支存在（用户值接管），且公式顺序保持 wide → user → rail。
ok(
  /sideDockWideActive\s*\?\s*Math\.max\(rightDockTreeWidth, sideDockWideTargetWidth\)\s*:\s*sideDockUserWidthActive\s*\?\s*rightDockTreeWidth\s*:\s*RIGHT_DOCK_TREE_NARROW_WIDTH/.test(appSource),
  "preferred 公式保持 wide→user→rail 三元链（App.tsx）",
);

// setSavedWorkspacePanelWidth 与 onWidthPreset 必须置位——显式写入 = 用户意图。
ok(
  /const setSavedWorkspacePanelWidth = useCallback[\s\S]*?setSideDockUserWidthActive\(true\)[\s\S]*?\];/.test(appSource),
  "setSavedWorkspacePanelWidth（拖动松手/键盘/双击复位）置位用户宽度指示",
);
ok(
  /onWidthPreset=\{\(percent\) => \{[\s\S]*?setSideDockUserWidthActive\(true\)[\s\S]*?\}\}/.test(appSource),
  "onWidthPreset（宽度预设档）置位用户宽度指示",
);

// 关面板收口清位；切 tab 清 wideActive 的收口点不得误清用户值（dock 宽度跨
// tab 一致是既有语义，误清会把「切 tab」变成第二种回缩）。
ok(
  /const closeWorkspacePanel = useCallback[\s\S]*?setSideDockUserWidthActive\(false\)[\s\S]*?\}, \[activeWorkspaceRoot, closeTransientOverlays, rightDockHover, workspacePanelOpen\]\);/.test(appSource),
  "closeWorkspacePanel 关面板收口时清位（重开回落 246 rail）",
);
const openPanelBody = appSource.slice(
  appSource.indexOf("const openWorkspacePanel = useCallback"),
  appSource.indexOf("const closeWorkspacePanelRef = useRef"),
);
ok(openPanelBody.includes("setSideDockWideActive(false)"), "openWorkspacePanel 切 tab 清 wideActive 的 1108 收口语义保留");
ok(
  !openPanelBody.includes("setSideDockUserWidthActive(false)"),
  "openWorkspacePanel 切 tab 不清用户宽度指示（不回归成切 tab 回缩）",
);

// ── 接线层：useShellGeometry.ts 平行实现同语义 ───────────────────────────
ok(
  /const setSavedWorkspacePanelWidth = useCommittedCommand[\s\S]*?setSideDockUserWidthActive\(true\)[\s\S]*?\}\);/.test(shellGeometrySource),
  "useShellGeometry 平行实现的同名命令同步置位（架构切换后不复活 bug）",
);

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
