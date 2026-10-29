// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1157-classic-header-3-actions.test.tsx
//
// Task 1157 — classic 侧栏项目行头部 5 → 3（用户拍板方案 A，2026-10-25）:
//   ① classic 头部恰 3 个一级操作按钮：筛选（合并面板）/ 折叠（双态保留）/ 新建（下拉一级三项）。
//   ② 筛选 = 单个下拉面板：时间范围 + 项目颜色两组控件，选项即时生效（无"应用"按钮），
//     按钮带已选态圆点徽标 + tooltip 摘要，底部「清除筛选」。排序区从旧时间菜单随迁
//     （arrangeClassicProjectTree 仍按 workbenchSortMode 排序，砍掉 = classic 失去唯一排序入口）。
//   ③ 新建下拉一级恰三项：新建项目（二级 = 新建空白项目 + 使用现有文件夹）/ 新建项目分组 /
//     远程连接；原 5 按钮覆盖的 8 个动作零丢失。
//   ④ 空态兜底：项目列表为空时可见「新建项目分组」文字链（不占头部图标位）。
//   ⑤ workbench 布局零变化。
//
// UI 结构以源码断言钉住（同 task 881/827/383 先例）；三语文案断言 key 存在 + 值逐字 + 孤词零命中。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

function count(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1;
}

const thisFile = fileURLToPath(import.meta.url);
const dir = dirname(thisFile);
const treeSource = readFileSync(resolve(dir, "../components/ProjectTree.tsx"), "utf8");
const styles = readFileSync(resolve(dir, "../styles.css"), "utf8");
const zh = readFileSync(resolve(dir, "../locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(dir, "../locales/zh-TW.ts"), "utf8");
const en = readFileSync(resolve(dir, "../locales/en.ts"), "utf8");

// classic 分支 = 从筛选控件调用到分支收尾；workbench 分支 = mode 判断到 classic 分支开头。
const classicBranch = treeSource.slice(
  treeSource.indexOf('{renderClassicFilterControl()}'),
  treeSource.indexOf("          </>\n        )}"),
);
const workbenchBranch = treeSource.slice(
  treeSource.indexOf('{mode === "workbench" ? ('),
  treeSource.indexOf('{renderClassicFilterControl()}'),
);
const createItems = treeSource.slice(
  treeSource.indexOf("const classicCreateItems"),
  treeSource.indexOf("const workbenchHeaderAddItems"),
);
const filterPanel = treeSource.slice(
  treeSource.indexOf("const classicFilterActive = timeFilter !== \"all\""),
  treeSource.indexOf("const renderProjectHeader = (mode:"),
);
const emptyState = treeSource.slice(
  treeSource.indexOf("const renderEmptyState = () => {"),
  treeSource.indexOf("const hasTreeRows ="),
);

// ── 验收 ①：classic 头部恰好 3 个一级操作按钮 ───────────────────────────────
console.log("\n1157-① classic header has exactly 3 action buttons");

// 3 个一级按钮 = 筛选控件调用（其函数体内 1 个触发按钮）+ 折叠 1 个字面 button +
// ProjectTreeHeaderAddControl 内部 1 个（新建下拉触发按钮）；面板/菜单内的按钮
// 都不在头部行。
ok(count(classicBranch, "<button") === 1 && count(classicBranch, "<ProjectTreeHeaderAddControl") === 1 && count(classicBranch, "renderClassicFilterControl()") === 1,
  "the classic branch mounts exactly 3 header buttons (filter control + collapse + create control)");
ok(!classicBranch.includes('t("projectGroup.createNew")'),
  "no standalone create-group entry remains in the classic header (aria/label gone)");
ok(classicBranch.includes("renderClassicFilterControl()") && classicBranch.includes("toggleCollapsedView") && classicBranch.includes("classicCreateItems"),
  "the 3 buttons are: merged filter control, collapse toggle, create dropdown");

// ── 验收 ②：筛选下拉面板（两组控件 + 即时生效 + 徽标 + 摘要 + 清除） ────────
console.log("\n1157-② merged filter panel");

ok(filterPanel.includes('t("projectTree.timeFilter")}') && filterPanel.includes('t("projectTree.filterByColor")}</div>'),
  "the panel hosts both the time-range and the project-colour sections");
ok(filterPanel.includes('onTimeFilterChange(value); setFilterMenuOpen(false); }}') && filterPanel.includes("setColorFilter((current) =>"),
  "time and colour options apply immediately in their onClick (no apply button)");
ok(filterPanel.includes("project-tree__filter-badge") && filterPanel.includes("classicFilterActive &&"),
  "the trigger carries a dot badge only while a filter is active");
ok(filterPanel.includes("classicFilterSummary") && filterPanel.includes('t("projectTree.filterColorCount", { count: colorFilter.length })'),
  "the tooltip/aria label spells out the active-filter summary (time · colour count)");
ok(filterPanel.includes('t("projectTree.clearTimeFilter")}') && filterPanel.includes('onTimeFilterChange("all");') && filterPanel.includes("setColorFilter([]);"),
  "a footer \"clear filters\" action resets both filters at once");
ok(filterPanel.includes('disabled={!classicFilterActive}'),
  "clear filters is disabled when nothing is filtered");
ok(filterPanel.includes('t("projectTree.sortCriteria")}</div>') && filterPanel.includes('selectWorkbenchSortMode("updated")') && filterPanel.includes('selectWorkbenchSortMode("created")'),
  "the sort section moved into the panel (classic keeps its only sorting door)");
ok(styles.includes(".project-tree__filter-badge") && styles.includes(".project-tree__filter-panel") && styles.includes(".project-tree__filter-clear"),
  "styles.css carries the badge / panel / clear styles");

// ── 验收 ③：新建下拉一级三项（逐字）+ 二级两项 ─────────────────────────────
console.log("\n1157-③ create dropdown: three top-level entries + second level");

ok(createItems.includes('type: "submenu"') && createItems.includes('key: "create-project"'),
  "\"new project\" is a second-level submenu entry");
ok(createItems.includes('label: t("projectTree.createProject")') && createItems.includes('label: t("projectGroup.createNew")') && createItems.includes('label: t("projectTree.remoteConnection")'),
  "the three top-level entries wire createProject / createNew (group) / remoteConnection");
ok(createItems.includes('key: "blank-project"') && createItems.includes('label: t("projectTree.createBlankProject")') && createItems.includes("openBlankProjectFlow()"),
  "second level keeps the blank-project flow");
ok(createItems.includes('key: "use-existing-folder"') && createItems.includes('label: t("projectTree.useExistingFolder")') && createItems.includes("handleAddProject()"),
  "second level keeps the existing-folder flow");
ok(createItems.includes('key: "new-project-group"') && createItems.includes("setNewGroupOpen(true)"),
  "the group entry opens the shared NewGroupPanel (setNewGroupOpen)");
ok(createItems.includes('key: "remote-connection"') && createItems.includes("openRemoteConnectFlow()"),
  "the remote entry opens the shared remote wizard");

// 8 动作零丢失（classic 形态逐项可触发）
console.log("\n1157-④ all 8 legacy actions reachable");

ok(filterPanel.includes("onTimeFilterChange"), "① time filter reachable in the panel");
ok(filterPanel.includes("setColorFilter"), "② colour filter reachable in the panel");
ok(classicBranch.includes("onClick={toggleCollapsedView}") && classicBranch.includes("canRestoreCollapsedView ? <ListRestart size={14} /> : <ListCollapse size={14} />"),
  "③④ collapse/restore double-state toggle unchanged");
ok(createItems.includes("openBlankProjectFlow()"), "⑤ blank project reachable (create ▸ new project)");
ok(createItems.includes("handleAddProject()"), "⑥ existing folder reachable (create ▸ new project)");
ok(createItems.includes("setNewGroupOpen(true)"), "⑦ new project group reachable (create dropdown)");
ok(createItems.includes("openRemoteConnectFlow()"), "⑧ remote connection reachable (create dropdown)");

// ── 验收 ⑤：空态兜底 ───────────────────────────────────────────────────────
console.log("\n1157-⑤ empty-state group entry");

ok(emptyState.includes('className="project-tree__empty-secondary"') && emptyState.includes("onClick={() => setNewGroupOpen(true)}") && emptyState.includes('t("projectGroup.createNew")'),
  "the empty tree shows a visible create-group action wired to NewGroupPanel");
ok(!emptyState.includes("project-tree__header-actions"),
  "the empty-state entry stays out of the header icon row");

// ── 验收 ⑥：workbench 零变化 ───────────────────────────────────────────────
console.log("\n1157-⑥ workbench layout untouched");

ok(workbenchBranch.includes('renderTimeFilterControl("workbench")') && workbenchBranch.includes('renderColorFilterControl("workbench")'),
  "workbench keeps its separate time and colour filter buttons");
ok(workbenchBranch.includes('aria-label={t("projectGroup.createNew")}') && workbenchBranch.includes("<FolderInput size={15}"),
  "workbench keeps its direct create-group button");
ok(workbenchBranch.includes("<MoreHorizontal") && workbenchBranch.includes("<FolderPlus size={16}") && workbenchBranch.includes("<Maximize2") && workbenchBranch.includes("<Minimize2"),
  "workbench keeps more / add / collapse buttons (6-button header unchanged)");
ok(count(workbenchBranch, "classicCreateItems") === 0,
  "workbench does not consume the classic create items");

// ── 验收 ⑦：三语文案（key 不动、值逐字、孤词零命中） ───────────────────────
console.log("\n1157-⑦ three-language copy");

ok(zh.includes('"projectTree.newGroup": "新建项目分组"') && zh.includes('"projectGroup.createNew": "新建项目分组"'),
  "zh: both create-group keys read 新建项目分组");
ok(en.includes('"projectTree.newGroup": "New project group"') && en.includes('"projectGroup.createNew": "New project group"'),
  "en: both create-group keys read New project group");
ok(zhTW.includes('"projectTree.newGroup": "新增專案分組"') && zhTW.includes('"projectGroup.createNew": "新增專案分組"'),
  "zh-TW: both create-group keys read 新增專案分組");
ok(!zh.includes("新建分组") && !zhTW.includes("新建分組") && !zhTW.includes("新增分組"),
  "no standalone 「新建分组」 copy left in zh / zh-TW locales");
ok(zh.includes('"projectTree.createProject": "新建项目"') && en.includes('"projectTree.createProject": "New project"') && zhTW.includes('"projectTree.createProject": "新增專案"'),
  "the create-project top-level entry copy exists in all three locales");
ok(zh.includes('"projectTree.createBlankProject": "新建空白项目"') && zh.includes('"projectTree.useExistingFolder": "使用现有文件夹"'),
  "second-level copy (blank project / existing folder) keeps its keys and values in zh");
// tl1170: the trigger label key filterMenu retired (→ filterAndSort); the
// locale assertions below cover the surviving trigger labels only.
ok(zh.includes('"projectTree.createMenu": "新建"') && zh.includes('"projectTree.filterColorCount": "{count} 个颜色"')
  && en.includes('"projectTree.createMenu": "New"') && en.includes('"projectTree.filterColorCount": "{count} color(s)"')
  && zhTW.includes('"projectTree.createMenu": "新增"') && zhTW.includes('"projectTree.filterColorCount": "{count} 個顏色"'),
  "the trigger labels exist in all three locales (createMenu / filterColorCount; filterMenu retired by tl1170)");

process.stdout.write(`\n1157 classic header 5→3: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
