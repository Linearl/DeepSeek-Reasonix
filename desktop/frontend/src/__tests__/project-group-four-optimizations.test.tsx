// Run: tsx src/__tests__/project-group-four-optimizations.test.tsx
//
// Task 827 — 项目分组四件优化（五条验收逐条钉）:
//   ① 分组行右键菜单：重命名 + 解散（确认弹窗；解散=删组壳，项目回未分组）
//   ② 分组字号加大一号：--project-tree-group-font = project-font + 1px（token 化）
//   ③ 折叠/展开标识对齐：图标列统一网格（四种行同一 --project-tree-icon-col）
//   ④ 分组拖拽排序：reorderProjectGroups（sortOrder 重排 + GROUPS_KEY 持久化）
//   ⑤ 项目行拖拽与分组拖拽互不干扰（专属 MIME + 各自状态门卫）
//
// renderProjectSection 是 ProjectTree 的内联渲染函数（同 task 313/169 的
// 先例），UI 结构以源码断言钉住；数据语义用真实函数 + localStorage 行为级
// 验证。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import {
  addProjectGroup,
  deleteProjectGroup,
  dropProjectGroupCollapsed,
  loadProjectGroups,
  renameProjectGroup,
  reorderProjectGroups,
  moveProjectToGroup,
} from "../lib/projectGroups";
import type { ProjectGroup } from "../lib/projectGroups";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// jsdom provides localStorage for the lib-level behaviour tests.
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

const thisFile = fileURLToPath(import.meta.url);
const dir = dirname(thisFile);
const treeSource = readFileSync(resolve(dir, "../components/ProjectTree.tsx"), "utf8");
// tl1191: mode-scoped rules live in src/styles/mode-*.css now.
const cssSource = [
  resolve(dir, "../styles.css"),
  resolve(dir, "../styles/mode-classic.css"),
  resolve(dir, "../styles/mode-workbench.css"),
  resolve(dir, "../styles/mode-creation.css"),
].map((p) => readFileSync(p, "utf8")).join("\n");
const libSource = readFileSync(resolve(dir, "../lib/projectGroups.ts"), "utf8");

// ── 验收 ④：reorderProjectGroups（数据层行为） ──────────────────────────────
console.log("\n827-④ group reorder (lib behaviour)");

dom.window.localStorage.clear();
let groups: ProjectGroup[] = [];
groups = addProjectGroup("A", groups).groups;
groups = addProjectGroup("B", groups).groups;
groups = addProjectGroup("C", groups).groups;
const idOf = (title: string) => groups.find((g) => g.title === title)!.id;

// after：A 拖到 B 之后 → B, A, C；sortOrder 归一为 0..n-1
groups = reorderProjectGroups(groups, idOf("A"), idOf("B"), "after");
eq(groups.map((g) => g.title), ["B", "A", "C"], "drag A after B reorders to B, A, C");
eq(groups.map((g) => g.sortOrder), [0, 1, 2], "sortOrder rewritten dense 0..n-1");

// before：C 拖到 B 之前 → C, B, A
groups = reorderProjectGroups(groups, idOf("C"), idOf("B"), "before");
eq(groups.map((g) => g.title), ["C", "B", "A"], "drag C before B reorders to C, B, A");
eq(groups.map((g) => g.sortOrder), [0, 1, 2], "sortOrder stays dense after the second move");

// 同组拖到自身：no-op（原数组返回，不写 storage）
const beforeSelf = JSON.stringify(groups);
eq(reorderProjectGroups(groups, idOf("A"), idOf("A"), "after") === groups, true, "self-drag is a no-op (same reference)");
eq(JSON.stringify(groups), beforeSelf, "self-drag writes nothing");

// 端点消失（另一窗口解散）：no-op
eq(reorderProjectGroups(groups, "ghost-id", idOf("A"), "after") === groups, true, "missing dragged id is a no-op");
eq(reorderProjectGroups(groups, idOf("A"), "ghost-id", "after") === groups, true, "missing target id is a no-op");

// 持久化：GROUPS_KEY 落盘，重读（刷新/重启模拟）顺序保持
const persisted = JSON.parse(dom.window.localStorage.getItem("projectTree:groups")!);
eq((persisted as ProjectGroup[]).map((g) => g.title), ["C", "B", "A"], "the dropped order is persisted to GROUPS_KEY");
eq(loadProjectGroups().map((g) => g.title), ["C", "B", "A"], "a fresh loadProjectGroups reads the new order back (restart keeps it)");

// ── 验收 ①：解散=删组壳，项目完好回未分组（lib 行为） ────────────────────────
console.log("\n827-① dissolve keeps projects (lib behaviour)");

dom.window.localStorage.clear();
let groups2 = addProjectGroup("Work", []).groups;
const workId = groups2[0].id;
let assign = moveProjectToGroup(moveProjectToGroup({}, "/proj/a", workId), "/proj/b", workId);
let collapsedSet: ReadonlySet<string> = new Set([workId]);

// 菜单 onSelect 的真实调用序列：deleteProjectGroup → dropProjectGroupCollapsed
const res = deleteProjectGroup(groups2, assign, workId);
collapsedSet = dropProjectGroupCollapsed(collapsedSet, workId);
eq(res.groups.length, 0, "dissolve removes the group shell");
eq(groupForProjectOf(res.assign, "/proj/a"), null, "project a returns to ungrouped");
eq(groupForProjectOf(res.assign, "/proj/b"), null, "project b returns to ungrouped");
eq(collapsedSet.has(workId), false, "the collapsed marker is dropped with the group");
eq(loadProjectGroups().length, 0, "GROUPS_KEY no longer holds the dissolved group");
{
  const raw = JSON.parse(dom.window.localStorage.getItem("projectTree:projectGroupAssign")!) as Record<string, string>;
  eq(Object.keys(raw).length, 0, "assignments persisted without the dissolved group id");
}
function groupForProjectOf(a: Record<string, string>, root: string): string | null {
  return a[root] ?? null;
}

// 重命名：改名落盘；空名保持原标题
groups2 = addProjectGroup("Client", res.groups).groups;
groups2 = renameProjectGroup(groups2, groups2[0].id, "Renamed");
eq(groups2[0].title, "Renamed", "renameProjectGroup commits the new title");
eq((JSON.parse(dom.window.localStorage.getItem("projectTree:groups")!) as ProjectGroup[])[0].title, "Renamed", "rename persists to GROUPS_KEY");
groups2 = renameProjectGroup(groups2, groups2[0].id, "   ");
eq(groups2[0].title, "Renamed", "a blank draft keeps the old title (input blur cannot wipe a group)");

// ── 验收 ②：分组字号加大一号（token 化） ─────────────────────────────────────
console.log("\n827-② group font one step larger (token)");

ok(cssSource.includes("--project-tree-group-font: calc(var(--project-tree-project-font) + 1px)"),
  "group font token = project font token + 1px (the 'one step larger' relation, token-driven)");
ok(/--project-tree-project-font: 13\.5px/.test(cssSource), "base project font token matches the classic 13.5px project title");
ok(/:root\[data-theme-style\]\s*\{[^}]*--project-tree-project-font: 12\.5px/.test(cssSource),
  "the theme-style skin keeps the token in step with its 12.5px project title");
ok(/:root\[data-theme-style\] \.sidebar--workbench[^{]*\{[^}]*--project-tree-project-font: 15px/.test(cssSource.replace(/\n/g, " ")) ||
  cssSource.includes("--project-tree-project-font: 15px"),
  "the workbench skin keeps the token in step with its 15px project title");
ok(cssSource.includes(".project-tree__pgroup-main") && /--project-tree-group-font/.test(cssSource.slice(cssSource.indexOf(".project-tree__pgroup-main"), cssSource.indexOf(".project-tree__pgroup-main") + 300)),
  "the group row consumes the group font token");
ok(treeSource.includes("project-tree__pgroup-main"), "the group row carries the pgroup font class");
// 暗色主题正常：token 是纯字号声明（无颜色/背景依赖），暗色无需特判
{
  const tokenBlock = cssSource.slice(cssSource.indexOf("--project-tree-icon-col"), cssSource.indexOf("--project-tree-group-font") + 80);
  ok(!/color|background/.test(tokenBlock), "the grid/font tokens are pure sizing — dark themes need no special case");
}

// ── 验收 ③：图标列统一网格 ───────────────────────────────────────────────────
console.log("\n827-③ unified icon column grid");

// 硬约束：四种行的图标列同宽（同一 token）
ok(cssSource.includes("--project-tree-icon-col: 16px"), "the grid token is defined once (16px)");
const iconStackBlock = cssSource.slice(cssSource.indexOf(".project-tree__icon-stack {"), cssSource.indexOf(".project-tree__icon-stack > "));
ok(iconStackBlock.includes("width: var(--project-tree-icon-col)"), "project rows: the folder icon stack is the uniform column");
const chevronBlock = cssSource.slice(cssSource.indexOf(".project-tree__group-chevron {"), cssSource.indexOf(".project-tree__group-chevron {") + 240);
ok(chevronBlock.includes("var(--project-tree-icon-col)"), "session-group headers: the bare chevron occupies the uniform column");
ok(/\.project-tree__group-fold-stack\s*\{[^}]*var\(--project-tree-icon-col\)/.test(cssSource),
  "project-group headers: the folder+chevron stack is the uniform column");
ok(/\.project-tree__topic-lead\s*\{[^}]*var\(--project-tree-icon-col\)/.test(cssSource),
  "session rows: the lead placeholder is the uniform column");

// 折叠叠 ▸ / 展开旋 ▾：项目分组行 chevron 叠加在文件夹图标上
ok(treeSource.includes('className={`project-tree__group-fold${collapsed ? "" : " project-tree__group-fold--open"}`}>▸<'),
  "the group fold glyph is always ▸; the open state is a rotation (collapsed ▸, expanded ▾)");
ok(cssSource.includes(".project-tree__group-fold--open") && cssSource.includes("rotate(90deg)"),
  "the expanded fold rotates 90° (▸ → ▾)");
// 会话分组行保持裸 chevron（无文件夹语义）——会话分组行在 ProjectTreeOrganization
ok(readFileSync(resolve(dir, "../components/ProjectTreeOrganization.tsx"), "utf8").includes('<span className="project-tree__group-chevron" aria-hidden="true">{collapsed ? "▸" : "▾"}</span>'),
  "session-group headers keep the bare chevron as the column's whole content");

// 文字起点网格：分组行缩进 = 项目行同款公式（depth 0）+ 892-B 每层嵌套 +16；
// 会话行回到 depth 缩进
ok(treeSource.includes("style={{ paddingLeft: 8 + depth * 16 + level * 16 }}\n              onClick"),
  "the group row rides the project row's indent formula plus one 16px step per nesting level (892-B)");
ok(!/:root\[data-theme-style\] \.project-tree__topic-main\s*\{[^}]*padding-left:[^}]*!important/.test(cssSource),
  "the theme-style skin no longer flattens session rows (padding-left !important removed — depth indent restored)");

// 护栏（既有测试同款三公式未被本任务改动）
ok(treeSource.includes('className="project-tree__folder-main"\n            style={{ paddingLeft: 8 + depth * 16 }}'),
  "guard: project folder rows keep their indentation formula");
ok(/style=\{\{ paddingLeft: 14 \+ depth \* 16 \}\}/.test(treeSource),
  "guard: topic rows keep 14 + depth * 16");
ok(readFileSync(resolve(dir, "../components/ProjectTreeOrganization.tsx"), "utf8").includes("style={{ paddingLeft: 8 + (depth - 1) * 16 + level * 16 }}"),
  "guard: session-group headers keep the task-313 formula (892: one 16px step per level)");

// 文字起点推演（classic + data-theme-style，W=16，gap=7）：
//   项目分组行  8+0*16+16+7 = 31
//   项目行 d=0  8+0*16+16+7 = 31（色点并入图标列后 label 紧跟列）
//   会话分组头  8+(1-1)*16+16+7 = 31（与其所属项目行同列）
//   会话行 d=1  margin10 + padding(14+1*16) + lead16 + gap7 = 63 = 项目行文字+一层16
ok(treeSource.includes('{/* Task 827 ③: the project color dot lives inside the icon column'),
  "the project color dot is inside the icon column (folder + dot = one column)");

// ── 验收 ⑤ 前置 + ①/④：行结构源码断言 ─────────────────────────────────────
console.log("\n827-①④⑤ row wiring (source)");

ok(treeSource.includes("const PROJECT_GROUP_DRAG_TYPE = \"application/x-reasonix-project-group-id\""),
  "group drags carry a dedicated MIME type (not the project row's text/plain)");
ok(treeSource.includes('onContextMenu={(event) => {\n                event.preventDefault();\n                setGroupMenuId(group.id);'),
  "right-click opens the group row menu (acceptance ① entry point)");
ok(/label: t\("projectGroup\.rename"\)/.test(treeSource) && /label: t\("projectGroup\.delete"\)/.test(treeSource),
  "the menu offers exactly rename + dissolve (both labels present)");
ok(/key: "dissolve",[\s\S]{0,200}danger: true/.test(treeSource), "dissolve is marked destructive");
ok(treeSource.includes("await app.ConfirmAction({\n        title: t(\"projectGroup.delete\"),") && treeSource.includes("destructive: true,"),
  "dissolve goes through a destructive confirm dialog (mis-click guard)");
ok(treeSource.includes("deleteProjectGroup(groups, assign, group.id)") && treeSource.includes("dropProjectGroupCollapsed(current, group.id)"),
  "dissolve removes the shell + clears assignments + drops the stale collapsed marker");
ok(treeSource.includes("setGroups((current) => reorderProjectGroups(current, draggedId, group.id, position))"),
  "dropping a group commits through reorderProjectGroups (persisted order)");
ok(treeSource.includes('draggable={projectDragEnabled}'), "the group row is draggable outside search (same gate as project rows)");

// ── 验收 ⑤：项目行拖拽与分组拖拽互不干扰 ────────────────────────────────────
console.log("\n827-⑤ the two drags never interfere");

// 分组拖拽门卫：只有分组拖拽（dragGroupId 置位）能让分组行成为 drop 目标
ok(treeSource.includes("if (!dragGroupId || dragGroupId === group.id) return;"),
  "a group row ignores any drag that is not a group drag (project drags keep flowing to project rows)");
// 项目拖拽门卫（既有源码）：项目行 dragover 在 dragProjectRoot 为空时直接返回
ok(treeSource.includes("if (!draggableProject || !dragProjectRoot || dragProjectRoot === projectDragKey) return;"),
  "a project row ignores any drag that is not a project drag (group drags never set dragProjectRoot)");
// 分组 dragstart 只设置分组状态与分组 MIME；不触碰 dragProjectRoot / text/plain
{
  const dragStart = treeSource.slice(treeSource.indexOf("onDragStart={(event) => {\n                if (!projectDragEnabled) return;"));
  const dragStartBody = dragStart.slice(0, dragStart.indexOf("onDragOver"));
  ok(dragStartBody.includes("PROJECT_GROUP_DRAG_TYPE") && dragStartBody.includes("setDragGroupId(group.id)"),
    "the group dragstart sets only the group MIME and the group drag state");
  ok(!dragStartBody.includes("setDragProjectRoot") && !dragStartBody.includes('setData("text/plain"'),
    "the group dragstart never touches the project drag channel");
}
// 分组 drop 只认分组 MIME 兜底，不认 text/plain（项目行的兜底通道）
{
  const drop = treeSource.slice(treeSource.indexOf("onDrop={(event) => {\n                event.preventDefault();\n                const draggedId = dragGroupId"));
  const dropBody = drop.slice(0, drop.indexOf("onDragEnd"));
  ok(dropBody.includes("getData(PROJECT_GROUP_DRAG_TYPE)") && !dropBody.includes('getData("text/plain")'),
    "the group drop reads only the group MIME (never the project row's text/plain fallback)");
}

process.stdout.write(`\n827 four optimizations: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
