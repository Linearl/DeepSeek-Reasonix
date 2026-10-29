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
//   ⑤ workbench 布局零变化。（tl1222 起本条被取代：用户验收要求 workbench 头部
//     对齐 classic 同一形态，旧 6 图标行退场——见 ⑥，classic 断言 ①-④ 不变。）
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

// tl1222：classic 头部 = 共享 renderProjectHeader 本体（workbench 已对齐同一形态，
// 无独立 classic 分支）；筛选面板 = classicFilterActive 起到 renderProjectHeader 止。
const headerBody = treeSource.slice(
  treeSource.indexOf('const renderProjectHeader = (mode: "classic" | "workbench") => ('),
  treeSource.indexOf("// Project rows render under their group heading"),
);
const createItems = treeSource.slice(
  treeSource.indexOf("const classicCreateItems"),
  treeSource.indexOf("const timeFilterDisplayLabel"),
);
const filterPanel = treeSource.slice(
  treeSource.indexOf("const classicFilterActive = timeFilter !== \"all\""),
  treeSource.indexOf("const renderProjectHeader = (mode:"),
);
const emptyState = treeSource.slice(
  treeSource.indexOf("const renderEmptyState = () => {"),
  treeSource.indexOf("const hasTreeRows ="),
);

// ── 验收 ①：classic 头部恰好 4 个一级操作按钮（tl1254 收回批量管理开关） ────
console.log("\n1157-① classic header has exactly 4 action buttons");

// 4 个一级按钮 = 筛选控件调用（其函数体内 1 个触发按钮）+ 折叠 1 个字面 button +
// 批量管理开关 1 个字面 button（tl1254：1240 头部入口在 1222 重写滚合中丢失，
// 在共享头部单处收回）+ ProjectTreeHeaderAddControl 内部 1 个（新建下拉触发
// 按钮）；面板/菜单内的按钮都不在头部行。
ok(count(headerBody, "<button") === 2 && count(headerBody, "<ProjectTreeHeaderAddControl") === 1 && count(headerBody, "renderClassicFilterControl(mode)") === 1,
  "the classic header mounts exactly 4 header buttons (filter control + collapse + batch toggle + create control)");
ok(!headerBody.includes('t("projectGroup.createNew")'),
  "no standalone create-group entry remains in the classic header (aria/label gone)");
ok(headerBody.includes("renderClassicFilterControl(mode)") && headerBody.includes("toggleCollapsedView") && headerBody.includes("classicCreateItems") && headerBody.includes("toggleBatchMode"),
  "the 4 buttons are: merged filter control, collapse toggle, batch toggle, create dropdown");

// ── 验收 ②：筛选下拉面板（两组控件 + 即时生效 + 徽标 + 摘要 + 清除） ────────
console.log("\n1157-② merged filter panel");

// tl1170: both sections remain in the panel, now behind collapsible toggles.
ok(filterPanel.includes('toggleFilterPanelSection("time")') && filterPanel.includes('toggleFilterPanelSection("color")'),
  "the panel hosts both the time-range and the project-colour sections (collapsible toggles, tl1170)");
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
ok(headerBody.includes("onClick={toggleCollapsedView}") && headerBody.includes("canRestoreCollapsedView ? <ListRestart size={14} /> : <ListCollapse size={14} />"),
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

// ── 验收 ⑥：workbench 与 classic 同形态（tl1222 对齐，取代原「workbench 零变化」） ──
console.log("\n1157-⑥ workbench header aligned to the classic form (tl1222)");

// 头部唯一共享形态：renderProjectHeader 无 mode 三元分支，两布局同一 JSX。
ok(!headerBody.includes('{mode === "workbench" ? ('),
  "the header has a single shared body (no workbench/classic ternary)");
ok(treeSource.includes('{renderProjectHeader("workbench")}') && treeSource.includes('{renderProjectHeader("classic")}'),
  "both layouts mount the same shared header");
ok(count(headerBody, "<button") === 2 && count(headerBody, "<ProjectTreeHeaderAddControl") === 1 && headerBody.includes("renderClassicFilterControl(mode)"),
  "the shared header mounts exactly 4 controls (filter panel + collapse + batch toggle + create dropdown)");
ok(headerBody.includes("project-tree__collapse-all") && headerBody.includes("<ListRestart size={14} />") && headerBody.includes("<ListCollapse size={14} />"),
  "the collapse toggle uses the classic ListCollapse/ListRestart button form");

// 旧 6 图标行全部退场（文件内零残留）
ok(count(treeSource, "renderTimeFilterControl") === 0 && count(treeSource, "renderColorFilterControl") === 0,
  "the separate time/colour filter renderers are gone");
ok(count(treeSource, "workbenchHeaderMoreItems") === 0 && count(treeSource, "workbenchHeaderAddItems") === 0,
  "the more/add menu item builders are gone");
ok(!treeSource.includes("<Maximize2") && !treeSource.includes("<Minimize2") && !treeSource.includes("<FolderInput size={15}"),
  "the old collapse/create-group icon buttons are gone");
ok(!treeSource.includes("project-tree__header-icon-btn--filter"),
  "the old workbench filter icon-button class is gone");

// workbench 组织方式入口随迁筛选面板（workbench-only 分区）；新建下拉两布局共用。
ok(filterPanel.includes('mode === "workbench" && (') && filterPanel.includes('setWorkbenchOrganizeMode("project")') && filterPanel.includes('setWorkbenchOrganizeMode("recent")') && filterPanel.includes('setWorkbenchOrganizeMode("time")'),
  "the organize section moved into the merged filter panel (workbench-only)");
ok(headerBody.includes("items={classicCreateItems}"),
  "workbench consumes the classic create items (one dropdown for both layouts)");

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
