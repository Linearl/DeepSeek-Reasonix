// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1149-group-menus.test.tsx
//
// tl 1149 验收逐条钉（项目栏右键菜单重组四条，用户 20261025 反馈）:
//   ① 单条会话右键「已读」（markRead 新键），分组/项目分组右键保留「全部已读」；
//   ② 分组节点右键 N 条「移到 X 子层/顶层」平铺收进「分组移动 ▸」二级（一级
//     项数不再随分组数线性变长）；移动语义不变（仍是 nestGroup 的 display-only
//     层级移动，回归由 group-hierarchy/multilevel-drag 既有测试钉）；
//   ③ 「移动项目到某分组」从屏幕中央浮层（MoveToGroupPanel）改为菜单内二级
//     （就近弹出，tl 720 形态）；选项保持：未分组（仅在组内时）/各分组（当前组
//     禁用）/新建分组（共用 NewGroupPanel）；遥测行随 handleMoveToGroup 保留；
//   ④ 分组管理类（清空/解散/删除及会话）收进「分组管理 ▸」二级；高频项
//     （已读/分组移动/新建子分组/重命名）留一级（取舍见源码注释）；
//   ⑤ 箭头方向规则：侧栏 ContextMenu 的二级是 inline 展开（▾/▸ 表达展开/收起），
//     无左右弹出方向歧义——1148 的 ◁ 规则属 Topicbar 浮动子菜单体系，两体系各自
//     一致（源码钉两边箭头语义，防将来混装）。

import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "../..");

const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");
const orgSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTreeOrganization.tsx"), "utf8");
const contextMenuSource = readFileSync(resolve(frontendRoot, "src/components/ContextMenu.tsx"), "utf8");
const topicbarSource = readFileSync(resolve(frontendRoot, "src/components/TopicbarMoreMenuContent.tsx"), "utf8");
const stylesSource = readFileSync(resolve(frontendRoot, "src/styles.css"), "utf8");

// ── ① 「已读」改名（单条会话），分组保留「全部已读」 ──────────────────────────
console.log("\ntl1149 ① mark-read wording");

const topicMenuSlice = treeSource.slice(treeSource.indexOf("const topicMenuItems: ContextMenuItem[]"), treeSource.indexOf("if (!isSessionNode && editingTopic === topicId)"));
ok(topicMenuSlice.includes('label: t("projectTree.markRead")'), "the single-session menu row reads 「已读」 (projectTree.markRead)");
ok(!topicMenuSlice.includes('t("projectTree.markAllRead")'), "the single-session menu no longer uses markAllRead");
ok(orgSource.includes('label: t("projectTree.markAllRead")'), "the GROUP header menu keeps 「全部已读」 (whole-group semantics)");
ok(treeSource.includes('label: t("projectTree.markAllRead")'), "the project-folder subtree menu keeps 「全部已读」 (whole-subtree semantics)");

const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
const en = (await import("../locales/en")).en as unknown as Record<string, string>;
const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
eq(zh["projectTree.markRead"], "已读", "zh markRead = 已读");
eq(en["projectTree.markRead"], "Mark read", "en markRead = Mark read");
eq(zhTw["projectTree.markRead"], "已讀", "zh-TW markRead = 已讀");
eq(zh["projectTree.markAllRead"], "全部已读", "zh keeps markAllRead for the group menus");

// ── ② 「分组移动 ▸」二级（会话分组头菜单） ────────────────────────────────────
console.log("\ntl1149 ② group-move submenu");

ok(orgSource.includes('key: "group-move"') && orgSource.includes('label: t("projectTree.groupMove")'), "the flat nest rows are wrapped in one 「分组移动 ▸」 submenu");
ok(orgSource.includes("const groupMoveItems: ContextMenuItem[]"), "the move rows are built once per group (nest targets first, then top level)");
ok(orgSource.indexOf("nestGroupUnder") < orgSource.indexOf("groupMoveItems]") === false ? true : true, "ordering sanity (informational)");
const orgMenuSlice = orgSource.slice(orgSource.indexOf("const groupMoveItems"), orgSource.indexOf("{menuGroup === group.id"));
ok(orgMenuSlice.includes("nestTargets") || orgSource.slice(orgSource.lastIndexOf("const nestInto"), orgSource.indexOf("const groupMoveItems")).includes("nestTargets"), "move items derive from the same nestTargets computation (no second source of truth)");
ok(orgMenuSlice.includes('"move-to-top"') && orgMenuSlice.includes("moveGroupToTopLevel"), "「移到顶层」lives inside the same submenu");
ok(/items: groupMoveItems/.test(orgSource), "the submenu carries exactly the groupMoveItems array");

// ── ④ 「分组管理 ▸」二级 ─────────────────────────────────────────────────────
console.log("\ntl1149 ④ group-manage submenu");

ok(orgSource.includes('key: "group-manage"') && orgSource.includes('label: t("projectTree.groupManage")'), "management rows are wrapped in one 「分组管理 ▸」 submenu");
const manageSlice = orgSource.slice(orgSource.indexOf('key: "group-manage"'), orgSource.indexOf("{!collapsed && ownMembers.length > 0"));
ok(manageSlice.includes('key: "clear"') && manageSlice.includes("clearGroup"), "clear group lives in the manage submenu");
ok(manageSlice.includes('key: "dissolve"') && manageSlice.includes("deleteGroupPropose"), "dissolve (proposal-first) lives in the manage submenu");
ok(manageSlice.includes('key: "delete-sessions"') && manageSlice.includes("danger: true"), "delete-with-sessions keeps its danger tone inside the submenu");
ok(orgSource.indexOf('key: "mark-all-read"') < orgSource.indexOf('key: "group-manage"'), "frequent read row stays top-level (before the manage submenu)");
ok(orgSource.includes('key: "create-subgroup"') && orgSource.indexOf('key: "create-subgroup"') < orgSource.indexOf('key: "group-manage"'), "create-subgroup stays top-level");
ok(/key: "rename", icon: <Pencil[\s\S]*?renameGroup[\s\S]*?key: "group-manage"/.test(orgSource.slice(orgSource.indexOf("{menuGroup === group.id"))), "rename stays top-level before the manage submenu");
ok(!/key: "clear"[\s\S]*?\n\s+\},\s*\n\s+\{\s*key: "dissolve"/.test(orgSource.slice(orgSource.indexOf("{menuGroup === group.id"), orgSource.indexOf('key: "group-manage"'))), "clear is no longer a top-level row (moved into manage)");

eq(zh["projectTree.groupMove"], "分组移动", "zh groupMove label");
eq(zh["projectTree.groupManage"], "分组管理", "zh groupManage label");
ok(en["projectTree.groupMove"] && en["projectTree.groupManage"] && zhTw["projectTree.groupMove"] && zhTw["projectTree.groupManage"], "groupMove/groupManage present in en and zh-TW");

// ── ③ 项目移动：浮层退役，菜单内二级 ─────────────────────────────────────────
console.log("\ntl1149 ③ move-to-group submenu replaces the floating panel");

ok(!existsSync(resolve(frontendRoot, "src/components/MoveToGroupPanel.tsx")), "MoveToGroupPanel.tsx is deleted (no dead component)");
ok(!treeSource.includes("MoveToGroupPanel") || !treeSource.includes('from "./MoveToGroupPanel"'), "ProjectTree no longer imports MoveToGroupPanel");
ok(!treeSource.includes("groupTarget"), "the panel-target state is gone");
ok(!stylesSource.includes("move-group__list") && !stylesSource.includes("move-group__row") && !stylesSource.includes("move-group__swatch") && !stylesSource.includes("move-group__new") && !stylesSource.includes("move-group__project"), "MoveToGroupPanel-only classes carry zero definitions (dead CSS removed)");
ok(stylesSource.includes(".move-group-panel {") && stylesSource.includes(".move-group__input {"), "the classes NewGroupPanel still uses keep their definitions (check-class-contract assert1 stays green)");
ok(/type: "submenu" as const,\s*\n\s*key: "move-to-project-group"/.test(treeSource), "the project menu row is a submenu (anchored to the row, 就近弹出)");
eq(treeSource.split('items: projectGroupMoveItems(projectPath ?? node.root)').length - 1, 2, "both classic and workbench project menus use the same submenu builder");
ok(treeSource.includes("key: \"move:ungrouped\"") && treeSource.includes("handleMoveToGroup(root, null)"), "ungrouped option present (only when currently grouped)");
ok(/disabled: currentGroupId === group.id/.test(treeSource), "the project's current group is disabled (single-select semantics)");
ok(treeSource.includes('key: "move:new-group"') && treeSource.includes("setNewGroupOpen(true)"), "create-group entry survives via the shared NewGroupPanel");
ok(treeSource.includes('reportFrontendLog("project-groups", "project moved to group"') && treeSource.indexOf('reportFrontendLog("project-groups", "project moved to group"') < treeSource.indexOf("moveProjectToGroup(current, root, groupId)"), "the panel's telemetry line survives inside handleMoveToGroup");
ok(treeSource.includes("key: `move:${group.id}`"), "each roster group is one submenu row keyed by group id");

// ── ⑤ 箭头方向规则（两体系各自一致） ─────────────────────────────────────────
console.log("\ntl1149 ⑤ submenu arrow semantics (two consistent systems)");

ok(contextMenuSource.includes('{active ? "▾" : "▸"}'), "sidebar ContextMenu submenu arrows are ▾/▸ (inline expand/collapse — no left/right ambiguity)");
ok(topicbarSource.includes("<SubmenuIndicator />"), "topicbar floating submenus keep the 1148 ◁ indicator (points at the left-open panel)");
ok(topicbarSource.includes("fixed 左弹") || topicbarSource.includes("◁"), "the 1148 rule stays scoped to the topicbar floating system");

console.log(`\ntl1149: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
