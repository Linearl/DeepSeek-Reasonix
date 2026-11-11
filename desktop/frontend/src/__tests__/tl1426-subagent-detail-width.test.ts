// Run: tsx src/__tests__/tl1426-subagent-detail-width.test.ts
//
// tl1426 需求①（用户 20261105）：查看子代理详情时，对话区↔右侧栏默认 1:1，
// 且记住用户上次调整（持久化）。钉四层：
// ① 纯函数层——subagentDetailSplitTargetWidth 的 1:1 公式（对半 + 对话区
//    保底先行 + clamp 家族），App 与本文件同式调用零漂移；
// ② 公式链层——preferred 四级链（子代理 1:1/用户值 → 720 宽态 → tl1129
//    用户值 → 246 rail），断言新增分支不扰动既有三族；
// ③ 持久化层——subagentDetailDockWidth sizes 键 + userWidthActive 布尔键
//    的真实 localStorage 往返（jsdom window），「调整→重挂→读数保持」；
// ④ 接线层——App.tsx / SubagentsDockPanel.tsx 的调用点静态断言
//    （side-dock-user-width.test.ts 先例：渲染编排行为无法组件级挂载，
//    静态断言防回归漂移）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const layoutSource = readFileSync(resolve(testDir, "../store/layout.ts"), "utf8");
const panelSource = readFileSync(resolve(testDir, "../components/SubagentsDockPanel.tsx"), "utf8");

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

console.log("\nsubagent detail width (tl1426 需求①: 1:1 默认 + 宽度记忆)");

// ① 纯函数层：1:1 目标宽（先装 window 再动态 import，持久化段共用）。
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;

const layout = await import("../store/layout");
const prefs = await import("../lib/layoutPreferences");

eq(layout.SUBAGENT_DETAIL_SPLIT_RATIO, 0.5, "比例常量 = 0.5（1:1）");

eq(
  layout.subagentDetailSplitTargetWidth({ viewportWidth: 1920, sidebarWidth: 264, sidebarCollapsed: false, resizerWidth: 8, chatMinWidth: 400 }),
  824,
  "1920/左栏264: dock=chat=824（(1920-264-8)/2，真 1:1）",
);
eq(
  layout.subagentDetailSplitTargetWidth({ viewportWidth: 1920, sidebarWidth: 264, sidebarCollapsed: true, resizerWidth: 8, chatMinWidth: 400 }),
  860,
  "1920/左栏折叠: 对半 956 被 RIGHT_DOCK_MAX_WIDTH=860 封顶（族规上限先于 1:1）",
);
eq(
  layout.subagentDetailSplitTargetWidth({ viewportWidth: 1440, sidebarWidth: 264, sidebarCollapsed: false, resizerWidth: 8, chatMinWidth: 400 }),
  584,
  "1440/左栏264: (1440-264-8)/2=584",
);
eq(
  layout.subagentDetailSplitTargetWidth({ viewportWidth: 900, sidebarWidth: 264, sidebarCollapsed: false, resizerWidth: 8, chatMinWidth: 400 }),
  246,
  "窄窗 900: 对话区保底先行（available=228），宁可不足 1:1 也不压穿对话区；clamp 家族抬回 246 地板",
);
eq(
  layout.subagentDetailSplitTargetWidth({ viewportWidth: 4000, sidebarWidth: 264, sidebarCollapsed: false, resizerWidth: 8, chatMinWidth: 400 }),
  860,
  "超宽视口: 对半 1864 被 RIGHT_DOCK_MAX_WIDTH=860 封顶",
);

eq(layout.clampSubagentDetailDockWidth(5000, 1200), 1200, "clamp: maxWidth 参数封顶（拖动时按可用宽截断）");
eq(layout.clampSubagentDetailDockWidth(50), 246, "clamp: 地板 246");
eq(layout.clampSubagentDetailDockWidth(824.6), 825, "clamp: 四舍五入取整");

// ② 公式链层：与 App.tsx preferredWorkspacePanelWidth 四级三元链同式。
function preferredWidth(input: {
  splitActive: boolean; userActive: boolean; userWidth: number; splitTarget: number;
  wideActive: boolean; wideUserActive: boolean; treeWidth: number; wideTarget: number;
}): number {
  return input.splitActive
    ? input.userActive
      ? input.userWidth
      : input.splitTarget
    : input.wideActive
      ? Math.max(input.treeWidth, input.wideTarget)
      : input.wideUserActive
        ? input.treeWidth
        : 246;
}

eq(
  preferredWidth({ splitActive: true, userActive: false, userWidth: 0, splitTarget: 824, wideActive: false, wideUserActive: false, treeWidth: 300, wideTarget: 720 }),
  824,
  "详情打开+未调过: 默认 1:1 目标宽 824",
);
eq(
  preferredWidth({ splitActive: true, userActive: true, userWidth: 500, splitTarget: 824, wideActive: false, wideUserActive: false, treeWidth: 500, wideTarget: 720 }),
  500,
  "详情打开+拖窄过: 记住用户值 500（不被 1:1 抬回——tl1129 教训同向钉）",
);
eq(
  preferredWidth({ splitActive: true, userActive: true, userWidth: 900, splitTarget: 824, wideActive: false, wideUserActive: false, treeWidth: 900, wideTarget: 720 }),
  900,
  "详情打开+拖宽过: 记住用户值 900",
);
eq(
  preferredWidth({ splitActive: false, userActive: false, userWidth: 0, splitTarget: 824, wideActive: true, wideUserActive: false, treeWidth: 300, wideTarget: 720 }),
  720,
  "详情关闭+文件宽态: 720 族不动（tl1108 语义保留）",
);
eq(
  preferredWidth({ splitActive: false, userActive: true, userWidth: 0, splitTarget: 824, wideActive: false, wideUserActive: true, treeWidth: 500, wideTarget: 720 }),
  500,
  "详情关闭+窄态+拖过: tl1129 用户值保留",
);
eq(
  preferredWidth({ splitActive: false, userActive: false, userWidth: 0, splitTarget: 824, wideActive: false, wideUserActive: false, treeWidth: 500, wideTarget: 720 }),
  246,
  "详情关闭+窄态+没拖过: 246 rail 不回归（tl1108 语义保留）",
);

// ③ 持久化层：真实 localStorage 往返（重挂 = 同一套 loader 被 store 初始化调用）。
prefs.saveLayoutSize; // 触碰引用防误删（save 走 layout.saveSubagentDetailDockWidth）
layout.saveSubagentDetailDockWidth(800);
eq(prefs.loadOptionalLayoutSize("subagentDetailDockWidth", layout.clampSubagentDetailDockWidth), 800, "宽度数值键往返: 保存 800 → 读回 800");
layout.saveSubagentDetailDockWidth(50);
eq(prefs.loadOptionalLayoutSize("subagentDetailDockWidth", layout.clampSubagentDetailDockWidth), 246, "保存越界值被 clamp 家族拦回 246 地板");

layout.saveSubagentDetailUserWidthActive(true);
eq(layout.loadSubagentDetailUserWidthActive(), true, "指示位键往返: 置 true → 读回 true");
layout.saveSubagentDetailUserWidthActive(false);
eq(layout.loadSubagentDetailUserWidthActive(), false, "指示位键往返: 置 false → 读回 false");

// ④ 接线层：store 初始化 + App/Panel 调用点。
ok(
  layoutSource.includes('const SUBAGENT_DETAIL_USER_WIDTH_ACTIVE_KEY = "reasonix.subagentDetail.userWidthActive"'),
  "持久化键 reasonix.subagentDetail.userWidthActive 定义（tl1129 布尔键风格）",
);
ok(
  layoutSource.includes("subagentDetailDockWidth: loadSubagentDetailDockWidth()"),
  "store 初始化从持久化读回详情宽度（重挂读数保持的落地点）",
);
ok(
  layoutSource.includes("subagentDetailUserWidthActive: loadSubagentDetailUserWidthActive()"),
  "store 初始化从持久化读回用户调宽指示",
);
ok(
  /subagentsDetailWideActive\s*\?\s*subagentDetailUserWidthActive\s*\?\s*subagentDetailDockWidth\s*:\s*subagentDetailSplitTarget\s*:\s*sideDockWideActive\s*\?\s*Math\.max\(rightDockTreeWidth, sideDockWideTargetWidth\)\s*:\s*sideDockUserWidthActive\s*\?\s*rightDockTreeWidth\s*:\s*RIGHT_DOCK_TREE_NARROW_WIDTH/.test(appSource),
  "App preferred 四级链: 子代理 1:1/用户值 → 720 宽态 → tl1129 用户值 → 246 rail（顺序不漂移）",
);

{
  const savedBody = appSource.slice(
    appSource.indexOf("const setSavedWorkspacePanelWidth = useCallback"),
    appSource.indexOf("const ensureWorkspacePanelWidth = useCallback"),
  );
  ok(savedBody.includes("subagentsDetailWideActive"), "setSavedWorkspacePanelWidth: 详情态判定在写入路径上");
  ok(savedBody.includes("setSubagentDetailDockWidth") && savedBody.includes("saveSubagentDetailDockWidth"), "setSavedWorkspacePanelWidth: 详情态显式写宽入记忆（拖动/键盘/加宽同路）");
  ok(savedBody.includes("setSubagentDetailUserWidthActive(true)") && savedBody.includes("saveSubagentDetailUserWidthActive(true)"), "setSavedWorkspacePanelWidth: 详情态指示位置位并持久化");
}

{
  const resetBody = appSource.slice(
    appSource.indexOf("const resetWorkspacePanelWidth = useCallback"),
    appSource.indexOf("const workspacePanelResizeMinWidth ="),
  );
  ok(resetBody.includes("saveSubagentDetailUserWidthActive(false)"), "双击复位: 清详情态用户指示（1:1 默认公式重新接管）");
  ok(resetBody.includes("subagentDetailSplitTargetWidth"), "双击复位: 详情态记忆覆写为 1:1 目标宽（复位≠写死 300 入记忆）");
  ok(resetBody.includes("setSavedWorkspacePanelWidth(workspacePanelResetWidth)"), "双击复位: 其余态维持 workspacePanelResetWidth 既有语义");
}

{
  const openBody = appSource.slice(
    appSource.indexOf("const openWorkspacePanel = useCallback"),
    appSource.indexOf("const closeWorkspacePanelRef = useRef"),
  );
  ok(openBody.includes("setSubagentsDetailWideActive(false)"), "openWorkspacePanel 切 tab 同源清理来源标识（1:1 分支不残留）");
  ok(!openBody.includes("setSubagentDetailUserWidthActive(false)"), "openWorkspacePanel 切 tab 不清详情宽度记忆（跨 tab 保留，同 tl1129 口径）");
}

// tl1548 改钉注记：原钉 `detailEnabled && Boolean(selected)`。tl1548 给右栏
// 加了后台命令输出详情（与子代理详情同一张右栏内容面、同一个分屏右栏），
// bash 详情打开时同样需要抬宽判据（否则分屏列表列固定 200px + rail 246px
// 会把详情挤死）。判据扩为 selected || bashDetail——上报机制本体不变（详情
// 打开才上报；文件面板 wide 上报仍不置位），故原钉语义保持、仅随实现更新。
ok(
  panelSource.includes("setSubagentsDetailWideActive(detailEnabled && (Boolean(selected) || Boolean(bashDetail)))"),
  "SubagentsDockPanel: 详情打开上报来源标识（子代理/bash 详情同据；文件面板上报不置位；tl1548 扩 bash 同据）",
);
ok(
  panelSource.includes("setSubagentsDetailWideActive(false)"),
  "SubagentsDockPanel: 卸载/回落清理来源标识",
);

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
