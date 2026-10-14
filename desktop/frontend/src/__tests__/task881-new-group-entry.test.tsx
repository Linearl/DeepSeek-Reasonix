// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task881-new-group-entry.test.tsx
//
// Task 881 — 项目分组「新建分组」入口发现性（两条验收逐条钉）:
//   ① 树空白区右键菜单有「新建分组」，且不依赖任何已有分组/项目行：
//      handler 对拥有自己菜单的行（topic/folder/group-main）做 closest 排除，
//      其余目标（含空列表/空状态）一律可开菜单。
//   ② 顶部工具栏有直选按钮：classic 既有（f90030），workbench 本轮补齐——
//      按钮 onClick 直开 NewGroupPanel（setNewGroupOpen(true)），与 827 既有
//      建组流程同一条路径（复用非重写：唯一 onConfirm 仍是 handleAddGroup）。
//
// UI 结构以源码断言钉住（同 task 827/383 先例）；三语文案断言 locale 键值齐备。

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
const zh = readFileSync(resolve(dir, "../locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(dir, "../locales/zh-TW.ts"), "utf8");
const en = readFileSync(resolve(dir, "../locales/en.ts"), "utf8");

// ── 验收 ①：空白区右键菜单 ─────────────────────────────────────────────────
console.log("\n881-① blank-area right-click menu");

// 两个 list 容器（classic + workbench）都接线 onContextMenu=openBlankMenu
ok(count(treeSource, 'className="project-tree__list project-tree__list--workbench" onContextMenu={openBlankMenu}') === 1,
  "the workbench list container opens the blank menu on right-click");
ok(count(treeSource, 'className="project-tree__list" onScroll={cancelHoverCard} onContextMenu={openBlankMenu}') === 1,
  "the classic list container opens the blank menu on right-click");

// handler 守卫：拥有自己菜单的行被 closest 排除（分组行不 stopPropagation，必须守卫）
ok(treeSource.includes('target.closest(".project-tree__topic,.project-tree__folder,.project-tree__group-main")'),
  "the blank handler excludes rows that own their own context menu (topic/folder/group rows)");

// 菜单项直开 NewGroupPanel（复用 827 流程，非重写）
{
  const handlerSlice = treeSource.slice(treeSource.indexOf("const blankMenuItems"));
  const itemsBody = handlerSlice.slice(0, handlerSlice.indexOf("];"));
  ok(itemsBody.includes('key: "blank-new-group"') && itemsBody.includes('label: t("projectTree.newGroup")'),
    "the blank menu carries one \"new group\" item labelled via projectTree.newGroup");
  ok(itemsBody.includes("setBlankMenuPoint(null)") && itemsBody.includes("setNewGroupOpen(true)"),
    "the item closes the menu and opens the shared NewGroupPanel (setNewGroupOpen), not a parallel flow");
}

// ContextMenu 渲染与 NewGroupPanel 同层复用
ok(treeSource.includes("open={blankMenuPoint !== null}") && treeSource.includes("onClose={() => setBlankMenuPoint(null)}"),
  "the blank ContextMenu is driven by the blankMenuPoint state and closes itself");

// ── 验收 ②：顶部工具栏直选按钮 ─────────────────────────────────────────────
console.log("\n881-② toolbar direct buttons");

// classic 既有直选按钮（回归钉：f90030 引入，本轮不得丢）
ok(count(treeSource, 'aria-label={t("projectGroup.createNew")}') >= 2,
  "both classic and workbench headers expose a direct create-group button (aria-label projectGroup.createNew)");

// workbench 头部按钮：renderProjectHeader 内 mode === "workbench" 分支、onClick 直开 NewGroupPanel
{
  const workbenchBranch = treeSource.slice(treeSource.indexOf('{mode === "workbench" ? ('), treeSource.indexOf(") : (\n          <>\n            {renderTimeFilterControl(\"classic\")}"));
  ok(workbenchBranch.includes("onClick={() => setNewGroupOpen(true)}"),
    "the workbench header button opens NewGroupPanel directly (one click, no menu)");
  ok(workbenchBranch.includes("<FolderInput size={15}"),
    "the workbench group button uses the FolderInput icon (consistent with the classic group button)");
}

// 建组流程复用：唯一 onConfirm 仍是 handleAddGroup，NewGroupPanel 只挂一处
ok(count(treeSource, "<NewGroupPanel") === 1 && treeSource.includes("<NewGroupPanel open={newGroupOpen} onClose={() => setNewGroupOpen(false)} onConfirm={handleAddGroup} />"),
  "all entries funnel into the single 827 NewGroupPanel wired to handleAddGroup (reuse, not rewrite)");

// ── 验收 ③：三语文案齐备 ───────────────────────────────────────────────────
console.log("\n881-③ three-language labels");
for (const [name, source] of [["zh", zh], ["zh-TW", zhTW], ["en", en]] as const) {
  ok(source.includes('"projectTree.newGroup"') && source.includes('"projectGroup.createNew"'),
    `${name}: both create-group keys exist (projectTree.newGroup + projectGroup.createNew)`);
}

process.stdout.write(`\n881 new-group entry points: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
