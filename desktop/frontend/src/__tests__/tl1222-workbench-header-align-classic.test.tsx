// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1222-workbench-header-align-classic.test.tsx
//
// tl1222 — workbench 布局左侧项目栏头部与 classic 对齐（用户装机验收原话：
// 「工作台左侧项目栏要和我们现在的经典对齐下（目前工作台的项目栏顶部还是多个图标）」）。
//
// 改前差异（基线实读）：
//   classic 头部 = tl1157 shape A 三控件：合并筛选面板（时间+颜色+排序+清除）/
//     折叠切换（ListCollapse/ListRestart）/ 新建下拉（classicCreateItems）。
//   workbench 头部 = 6 图标按钮行：时间筛选/颜色筛选/折叠（Maximize2·Minimize2）/
//     建组直选（FolderInput 15）/ 更多菜单（archive-all 占位+组织方式+移动占位+排序）/
//     添加菜单（projectTreeHeaderAddItems 平铺四项）。
// 改后：workbench 头部消费与 classic 完全相同的共享 renderProjectHeader；
//   组织方式（project/recent/time）随迁合并筛选面板（workbench-only 分区，
//   classic 面板渲染零变化）；archive-all / move-section-down 两个 disabled
//   占位项为死条目，随更多菜单退场（无能力损失）。
//
// UI 结构以源码断言钉住（同 task 881/1157/827 先例）；三语断言=对齐后头部
// 所引用的每个 i18n key 三语齐备 + 本件退役 key 三语零残留。

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
const addControlsSource = readFileSync(resolve(dir, "../components/ProjectTreeAddControls.tsx"), "utf8");
const styles = readFileSync(resolve(dir, "../styles.css"), "utf8");
const zh = readFileSync(resolve(dir, "../locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(dir, "../locales/zh-TW.ts"), "utf8");
const en = readFileSync(resolve(dir, "../locales/en.ts"), "utf8");

const headerBody = treeSource.slice(
  treeSource.indexOf('const renderProjectHeader = (mode: "classic" | "workbench") => ('),
  treeSource.indexOf("// Project rows render under their group heading"),
);
const filterPanel = treeSource.slice(
  treeSource.indexOf("const classicFilterActive = timeFilter !== \"all\""),
  treeSource.indexOf("const renderProjectHeader = (mode:"),
);

// ── 验收 ①：workbench 项目栏头部 = classic 形态（单一共享头部） ─────────────
console.log("\ntl1222-① workbench header equals the classic form");

ok(headerBody.length > 0 && !headerBody.includes('{mode === "workbench" ? ('),
  "renderProjectHeader has one shared body — no workbench/classic ternary");
ok(treeSource.includes('{renderProjectHeader("workbench")}') && treeSource.includes('{renderProjectHeader("classic")}'),
  "workbench and classic mount the very same header renderer");
ok(count(headerBody, "renderClassicFilterControl(mode)") === 1 && count(headerBody, "<ProjectTreeHeaderAddControl") === 1 && count(headerBody, "<button") === 1,
  "the shared header mounts exactly 3 controls: merged filter / collapse / create dropdown");
ok(headerBody.includes("project-tree__collapse-all") && headerBody.includes('aria-pressed={canRestoreCollapsedView}') && headerBody.includes("<ListRestart size={14} />") && headerBody.includes("<ListCollapse size={14} />"),
  "the collapse toggle is the classic button form (ListCollapse/ListRestart + aria-pressed)");
ok(headerBody.includes('label={t("projectTree.createMenu")}') && headerBody.includes("items={classicCreateItems}"),
  "the create control is the classic dropdown (createMenu label + classicCreateItems)");

// 旧 workbench 6 图标行零残留
ok(count(treeSource, "renderTimeFilterControl") === 0 && count(treeSource, "renderColorFilterControl") === 0,
  "separate time/colour filter renderers are retired");
ok(count(treeSource, "workbenchHeaderMoreItems") === 0 && count(treeSource, "workbenchHeaderAddItems") === 0 && count(treeSource, "addItemCallbacks") === 0,
  "the more/add menu item builders are retired");
ok(count(treeSource, "menuLabelWithCheck") === 0 && count(treeSource, "workbenchCollapseToggleLabel") === 0,
  "the more-menu check-label helper and the dedicated workbench collapse label are retired");
ok(count(treeSource, "colorFilterRef") === 0 && count(treeSource, "colorFilterTriggerRef") === 0 && count(treeSource, "colorFilterMenuOpen") === 0,
  "the standalone colour-menu refs/state are retired");
ok(count(treeSource, '<Maximize2') === 0 && count(treeSource, '<Minimize2') === 0 && count(treeSource, "<FolderInput size={15}") === 0,
  "the old collapse/create-group icon buttons are gone");
ok(!treeSource.includes('type WorkbenchHeaderMenu = "more"') && !treeSource.includes('openWorkbenchHeaderMenu(event, "more")'),
  "the header menu state narrows to the create dropdown (\"more\" gone)");

// ── 验收 ②：旧图标行承载的每个活入口零丢失 ─────────────────────────────────
console.log("\ntl1222-② every live entry from the old icon row survives");

ok(filterPanel.includes('onClick={() => { onTimeFilterChange("all"); setFilterMenuOpen(false); }}') && filterPanel.includes("onTimeFilterChange(value)"),
  "time filter reachable in the merged panel (all + 10/20/1h/3h/5h/1d)");
ok(filterPanel.includes("setColorFilter((current) =>") && filterPanel.includes("project-tree__color-opt"),
  "colour filter reachable in the merged panel (multi-select toggles)");
ok(filterPanel.includes('selectWorkbenchSortMode("updated")') && filterPanel.includes('selectWorkbenchSortMode("created")'),
  "sort criteria reachable in the merged panel");
ok(filterPanel.includes('mode === "workbench" && (') && filterPanel.includes('setWorkbenchOrganizeMode("project")') && filterPanel.includes('setWorkbenchOrganizeMode("recent")') && filterPanel.includes('setWorkbenchOrganizeMode("time")'),
  "organize modes (project/recent/time) reachable in the merged panel — workbench-only section");
ok(headerBody.includes("onClick={toggleCollapsedView}") && headerBody.includes("canRestoreCollapsedView"),
  "collapse/restore double-state toggle reachable");
ok(filterPanel.includes('t("projectTree.clearTimeFilter")}') && filterPanel.includes("disabled={!classicFilterActive}"),
  "clear-filters footer kept");
const createItemsSlice = treeSource.slice(
  treeSource.indexOf("const classicCreateItems"),
  treeSource.indexOf("const timeFilterDisplayLabel"),
);
ok(createItemsSlice.includes('key: "create-project"') && createItemsSlice.includes('key: "blank-project"') && createItemsSlice.includes('key: "use-existing-folder"') && createItemsSlice.includes('key: "new-project-group"') && createItemsSlice.includes('key: "remote-connection"'),
  "create dropdown keeps all five doors (project submenu → blank/existing, group, remote)");

// ── 验收 ③：classic 零改动断言（面板 classic 路径渲染与 tl1157 逐字节同形） ─
console.log("\ntl1222-③ classic path unchanged (tl1157 shape intact)");

// 组织方式分区以 mode === "workbench" 门控——classic 渲染不含该分支输出；
// 分区仅插在排序区与清除区之间，classic 面板各分区标记原样。
ok(filterPanel.indexOf('{mode === "workbench" && (') > filterPanel.indexOf('t("projectTree.sortByCreatedAt")') && filterPanel.indexOf('{mode === "workbench" && (') < filterPanel.indexOf('t("projectTree.clearTimeFilter")}'),
  "the organize section sits between sort and clear, gated on mode === \"workbench\"");
ok(filterPanel.includes('<div className="project-tree__time-filter-title">{t("projectTree.timeFilter")}</div>')
  && filterPanel.includes('<div className="project-tree__time-filter-title">{t("projectTree.filterByColor")}</div>')
  && filterPanel.includes('<div className="project-tree__time-filter-title">{t("projectTree.sortCriteria")}</div>'),
  "classic panel sections (time / colour / sort) keep their tl1157 markup");
ok(filterPanel.includes('className={`project-tree__header-action-btn${classicFilterActive ? " project-tree__header-action-btn--active" : ""}`}')
  && filterPanel.includes('<Filter size={14} aria-hidden="true" />')
  && filterPanel.includes('project-tree__filter-badge'),
  "the filter trigger keeps the classic header-action-btn form (badge included)");
ok(addControlsSource.includes("export function ProjectTreeHeaderAddControl") && addControlsSource.includes("export function projectTreeHeaderAddItems"),
  "ProjectTreeAddControls helpers untouched (shared render face unchanged)");

// ── 验收 ④：CSS 零改动护栏（classic 头部类在 styles.css 原样可用） ──────────
console.log("\ntl1222-④ styles.css needs no change (classic classes are unscoped)");

for (const cls of ["project-tree__collapse-all", "project-tree__add-project", "project-tree__header-action-btn", "project-tree__filter-badge", "project-tree__filter-panel", "project-tree__filter-clear", "project-tree__time-filter-title"]) {
  ok(styles.includes(`.${cls}`), `styles.css still carries .${cls} (reused by the aligned workbench header)`);
}

// ── 验收 ⑤：三语零死键（对齐头部引用的 key 三语齐备；退役 key 零残留） ──────
console.log("\ntl1222-⑤ three-language keys: none missing, none dangling");

const headerKeys = [
  "projectTree.workspaceTitle", "projectTree.filterMenu", "projectTree.createMenu",
  "projectTree.timeFilter", "projectTree.timeFilterAll", "projectTree.timeFilter10", "projectTree.timeFilter20",
  "projectTree.timeFilter1h", "projectTree.timeFilter3h", "projectTree.timeFilter5h", "projectTree.timeFilter1d",
  "projectTree.filterByColor", "projectTree.filterByColorAll", "projectTree.filterColorCount", "projectTree.clearTimeFilter",
  "projectTree.sortCriteria", "projectTree.sortByUpdatedAt", "projectTree.sortByCreatedAt",
  "projectTree.organizeSidebar", "projectTree.organizeByProject", "projectTree.organizeRecentProjects", "projectTree.organizeByTime",
  "projectTree.collapseAllTooltip", "projectTree.restoreCollapsedTooltip",
  "projectTree.createProject", "projectTree.createBlankProject", "projectTree.useExistingFolder", "projectTree.remoteConnection",
  "projectGroup.createNew",
];
for (const [name, source] of [["zh", zh], ["zh-TW", zhTW], ["en", en]] as const) {
  const missing = headerKeys.filter((key) => !source.includes(`"${key}"`));
  ok(missing.length === 0, `${name}: all ${headerKeys.length} header keys exist (missing: ${missing.join(", ") || "none"})`);
}
for (const [name, source] of [["zh", zh], ["zh-TW", zhTW], ["en", en]] as const) {
  const dangling = ["projectTree.moreActions", "projectTree.archiveAllConversations", "projectTree.moveSectionDown", "projectTree.collapseAllWorkbench", "projectTree.restoreCollapsedWorkbench", "projectTree.filterAndSort"]
    .filter((key) => source.includes(`"${key}"`));
  ok(dangling.length === 0, `${name}: all 6 retired keys removed (dangling: ${dangling.join(", ") || "none"})`);
}

process.stdout.write(`\ntl1222 workbench header aligns classic: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
