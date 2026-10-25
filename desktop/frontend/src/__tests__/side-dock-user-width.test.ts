// Run: tsx src/__tests__/side-dock-user-width.test.ts
//
// tl1129: 「宽度调整好后自动回缩」回归钉。tl1108 三态公式的窄态分支把
// preferred 钉死在 246 rail，用户拖分隔条松手后存进 rightDockTreeWidth 的
// 用户值被无视，同一渲染帧被打回 rail。修复引入 sideDockUserWidthActive：
// 显式宽度写入（setSavedWorkspacePanelWidth / onWidthPreset）置位，切 tab
// 不清。本文件钉两层：
// ① store 层——指示位的初始值、置位/清位行为（真实 zustand store，非 mock）；
// ② 接线层——App.tsx / useShellGeometry.ts 的公式分支与置位调用点
//    （源码静态断言，composer-autosize-scrollbar-css.test.ts 先例：这些是
//    渲染编排行为，组件级挂载 App.tsx 不可行，静态断言防回归漂移）。
//
// tl1129 返修（用户 20261025 拍板：宽度记忆为核心）：
// ★ closeWorkspacePanel（折叠/关面板）不再清位——装机复测「拖宽后折叠再
//   展开重置最窄」的根因就是第一轮在 close 清位；旧版 94e9e1cd0 对照：
//   preferred 恒等于持久化的 rightDockTreeWidth，折叠/展开天然保宽度。
// ★ 指示位随 localStorage 持久化（reasonix.rightDock.userWidthActive）——
//   跨重启保留用户宽度；store 初始化从持久化读回。
// ★ 折叠/展开宽度回归段：store 层模拟「拖宽→折叠→展开」序列，断言用户
//   值全程存活、公式等效输出用户值而非 246 rail。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { RIGHT_DOCK_TREE_NARROW_WIDTH, useLayoutStore } from "../store/layout";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const shellGeometrySource = readFileSync(resolve(testDir, "../app-runtime/useShellGeometry.ts"), "utf8");
const layoutSource = readFileSync(resolve(testDir, "../store/layout.ts"), "utf8");

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

console.log("\nside dock user width (tl1129 拖宽不回缩 + 返修宽度记忆)");

// ── store 层：指示位的真实行为 ────────────────────────────────────────────
// node 直跑无 window ⇒ loadSideDockUserWidthActive() 落 false 分支。
eq(useLayoutStore.getState().sideDockUserWidthActive, false, "指示位初始为 false（无持久化数据 ⇒ 246 rail）");

useLayoutStore.getState().setSideDockUserWidthActive(true);
eq(useLayoutStore.getState().sideDockUserWidthActive, true, "置位 true 生效（拖宽松手后窄态尊重用户值）");

useLayoutStore.getState().setSideDockUserWidthActive(true);
eq(useLayoutStore.getState().sideDockUserWidthActive, true, "重复置位幂等（等值守卫）");

useLayoutStore.getState().setSideDockUserWidthActive(false);
eq(useLayoutStore.getState().sideDockUserWidthActive, false, "清位 false 生效（仅用户显式放弃宽度记忆时触达）");

// ── 折叠/展开宽度回归（tl1129 返修验收 1：拖宽→折叠→展开不重置最窄）──────
// store 层模拟完整序列：拖宽置位 → 折叠（close：对指示位无操作）→ 展开。
// 公式等效复刻 App.tsx preferred 三元链（wide→user→rail），断言展开后输出
// 用户值而非 RIGHT_DOCK_TREE_NARROW_WIDTH。
{
  const userWidth = 400;
  useLayoutStore.getState().setSideDockUserWidthActive(true);
  // 折叠 = closeWorkspacePanel：返修后对指示位零操作（无 setter 调用即序列
  // 里的空档），这里刻意不调用任何清位函数。
  const sideDockWideActive = false; // 窄态（无 wide 上报）
  const rightDockTreeWidth = userWidth; // 拖宽松手存入的用户值（持久化）
  const preferredAfterReexpand = sideDockWideActive
    ? Math.max(rightDockTreeWidth, 720)
    : useLayoutStore.getState().sideDockUserWidthActive
      ? rightDockTreeWidth
      : RIGHT_DOCK_TREE_NARROW_WIDTH;
  eq(preferredAfterReexpand, userWidth, "折叠→展开后 preferred=用户值 400（不重置 246 rail）");
  ok(preferredAfterReexpand !== RIGHT_DOCK_TREE_NARROW_WIDTH, "展开后不回落 246 rail（第一轮装机反馈回归钉）");
  useLayoutStore.getState().setSideDockUserWidthActive(false);
}

// ── 持久化：store 初始化读回 + 键定义（tl1129 返修验收 2：跨重启保留）─────
ok(
  layoutSource.includes('const RIGHT_DOCK_USER_WIDTH_ACTIVE_KEY = "reasonix.rightDock.userWidthActive"'),
  "持久化键 reasonix.rightDock.userWidthActive 定义",
);
ok(
  layoutSource.includes("sideDockUserWidthActive: loadSideDockUserWidthActive()"),
  "store 初始化从 localStorage 读回用户宽度指示（跨重启保留）",
);
ok(
  /export function saveSideDockUserWidthActive\(active: boolean\): void \{[\s\S]*?setItem\(RIGHT_DOCK_USER_WIDTH_ACTIVE_KEY, active \? "1" : "0"\)/.test(layoutSource),
  "saveSideDockUserWidthActive 写布尔键（sidebarCollapsed 同款风格）",
);

// ── 接线层：App.tsx 公式与置位调用点 ─────────────────────────────────────
// 窄态第三分支存在（用户值接管），且公式顺序保持 wide → user → rail。
ok(
  /sideDockWideActive\s*\?\s*Math\.max\(rightDockTreeWidth, sideDockWideTargetWidth\)\s*:\s*sideDockUserWidthActive\s*\?\s*rightDockTreeWidth\s*:\s*RIGHT_DOCK_TREE_NARROW_WIDTH/.test(appSource),
  "preferred 公式保持 wide→user→rail 三元链（App.tsx）",
);

// setSavedWorkspacePanelWidth 与 onWidthPreset 必须置位并持久化——显式写入 = 用户意图。
{
  const savedBody = appSource.slice(
    appSource.indexOf("const setSavedWorkspacePanelWidth = useCallback"),
    appSource.indexOf("const ensureWorkspacePanelWidth = useCallback"),
  );
  ok(savedBody.includes("setSideDockUserWidthActive(true)"), "setSavedWorkspacePanelWidth（拖动松手/键盘/双击复位）置位用户宽度指示");
  ok(savedBody.includes("saveSideDockUserWidthActive(true)"), "setSavedWorkspacePanelWidth 置位随 localStorage 持久化");
}
ok(
  /onWidthPreset=\{\(percent\) => \{[\s\S]*?setSideDockUserWidthActive\(true\)[\s\S]*?saveSideDockUserWidthActive\(true\)[\s\S]*?\}\}/.test(appSource),
  "onWidthPreset（宽度预设档）置位并持久化用户宽度指示",
);

// ★ 返修核心：折叠/关面板（closeWorkspacePanel）不得清位——装机复测「拖宽后
// 折叠再展开重置最窄」的根因。close 体内不得出现任何 setSideDockUserWidthActive
// 调用；hover 折叠与 toggle 折叠都走 close，一并被此断言覆盖。
{
  const closeBody = appSource.slice(
    appSource.indexOf("const closeWorkspacePanel = useCallback"),
    appSource.indexOf("closeWorkspacePanelRef.current = closeWorkspacePanel"),
  );
  ok(
    !closeBody.includes("setSideDockUserWidthActive"),
    "closeWorkspacePanel（折叠/关面板）不清用户宽度指示（宽度记忆：折叠→展开保持用户值）",
  );
}
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
  /const setSavedWorkspacePanelWidth = useCommittedCommand[\s\S]*?setSideDockUserWidthActive\(true\)[\s\S]*?saveSideDockUserWidthActive\(true\)[\s\S]*?\}\);/.test(shellGeometrySource),
  "useShellGeometry 平行实现的同名命令同步置位并持久化（架构切换后不复活 bug）",
);
ok(
  !shellGeometrySource.includes("setSideDockUserWidthActive(false)"),
  "useShellGeometry 平行实现无清位调用（与 close 不清位语义一致）",
);

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
