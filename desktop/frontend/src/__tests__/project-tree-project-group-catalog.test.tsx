// Run: tsx src/__tests__/project-tree-project-group-catalog.test.tsx
//
// 892-B 验收⑤（项目分组半）：localStorage → 后端 catalog 迁移 + 任意嵌套 UI 接线。
//   ① catalogToProjectGroups：视图 parentId → 存储 parent；扁平视图不长 parent 键；
//   ② pullProjectGroupCatalog：无绑定→null（留在 localStorage）；catalog 空且
//      本地有组→一次性导入（父先建、parentID 逐位、失败不搁浅）；catalog 非空→
//      以后端为准、不重复导入；
//   ③ 写面包装：create/rename/delete 的参数钉（parentID/confirm=false→true/cascade）；
//   ④ pruneProjectGroupAssign：组离开 roster（含 cascade 子组）→ 项目回未分组；
//      无变化保持引用；
//   ⑤ 本地回退：addProjectGroup(parent) 写存储拼法 parent；clearProjectGroupRoster
//      清影子防复活；源码钉住 ProjectTree 接线（递归渲染/新建子分组菜单/提案删除/
//      v2 metadata 刷新）。
//   ⑥ 908 data-loss 守卫三态（892B harness 漏网原因=只测正常 prune）：
//      pull 空 roster → 归属保持 + WARN 快照；pull null → 组件提前返回
//      （prune 不可达）；roster 非空（正常删组）→ 仍按差集 prune 且快照入遥测。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import type { ProjectGroupCatalogBindings } from "../lib/projectGroupCatalog";
import type { DeleteGroupsPlan, ManageSessionsGroupResult, ManageSessionsListResult } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// jsdom 提供 localStorage（projectGroups lib 的行为级验证）。
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

// 库在 DOM 全局安装后动态加载（bridge 模块在加载时探测 window）。
const {
  addProjectGroup,
  clearProjectGroupRoster,
  loadProjectGroupAssign,
  loadProjectGroups,
  pruneProjectGroupAssign,
} = await import("../lib/projectGroups");
const {
  catalogToProjectGroups,
  createProjectGroupViaCatalog,
  executeProjectGroupDelete,
  proposeProjectGroupDelete,
  pullProjectGroupCatalog,
  renameProjectGroupViaCatalog,
} = await import("../lib/projectGroupCatalog");
// 908：守卫 WARN 与 prune 快照遥测经 frontendLog ring 断言（DOM 装好后动态加载）。
const { drainFrontendLogRing, peekFrontendLogRing } = await import("../lib/frontendLog");

const thisFile = fileURLToPath(import.meta.url);
const dir = dirname(thisFile);
const treeSource = readFileSync(resolve(dir, "../components/ProjectTree.tsx"), "utf8");

// ── ① 视图 → 存储映射 ────────────────────────────────────────────────────────
console.log("\n892-B⑤ catalogToProjectGroups（视图 parentId → 存储 parent）");
{
  const views = [
    { id: "g1", title: "L1", depth: 0, sortOrder: 0 },
    { id: "g2", title: "L2", parentId: "g1", depth: 1, sortOrder: 1 },
  ];
  const roster = catalogToProjectGroups(views);
  eq(roster.map((g) => [g.id, g.title, g.parent, g.sortOrder]), [["g1", "L1", undefined, 0], ["g2", "L2", "g1", 1]],
    "parentId 逐位落到 parent（存储拼法）");
  eq("parent" in roster[0]!, false, "扁平视图不长出 parent 键（迁移兼容）");
  eq(catalogToProjectGroups(undefined), [], "缺字段读为空目录");
}

// ── ② 拉取与一次性导入 ──────────────────────────────────────────────────────
console.log("\n892-B⑤ pullProjectGroupCatalog（无绑定/一次性导入/以后端为准）");
{
  // 无绑定 → null（组件留在 localStorage 模式）
  eq(await pullProjectGroupCatalog({}, [{ id: "a", title: "A", sortOrder: 0 }]), null,
    "无 ManageSessionsList 绑定 → null（不迁移不误伤）");

  // catalog 空 + 本地有组 → 一次性导入（父先建、parentID 逐位、失败不搁浅）
  {
    let catalog: Array<{ id: string; title: string; parentId?: string; depth: number; sortOrder: number }> = [];
    const creates: Array<[string, string, string]> = [];
    const bindings: ProjectGroupCatalogBindings = {
      ManageSessionsList: async () => ({ groups: {}, projectGroups: catalog, sessions: [] } as unknown as ManageSessionsListResult),
      CreateProjectGroup: async (groupID, title, parentID) => {
        if (title === "Broken") throw new Error("boom"); // 单组失败不搁浅其余
        creates.push([groupID, title, parentID]);
        catalog = [...catalog, { id: groupID, title, ...(parentID ? { parentId: parentID } : {}), depth: parentID ? 1 : 0, sortOrder: catalog.length }];
        return { kind: "project", groupId: groupID, title, parentId: parentID, applied: true } as ManageSessionsGroupResult;
      },
    };
    const local = [
      { id: "g-parent", title: "Parent", sortOrder: 0 },
      { id: "g-broken", title: "Broken", sortOrder: 1 },
      { id: "g-child", title: "Child", sortOrder: 2, parent: "g-parent" },
    ];
    const pull = await pullProjectGroupCatalog(bindings, local);
    ok(pull !== null, "导入路径返回 roster");
    ok(pull?.imported === true, "标记 imported=true（调用方清影子）");
    eq(creates.length, 2, "3 个本地组、1 个失败 → 成功导入 2 个（失败不搁浅其余）");
    eq(creates[0], ["g-parent", "Parent", ""], "顶层组先建、parentID 传空串");
    eq(creates[1], ["g-child", "Child", "g-parent"], "子组后建、parentID 逐位");
    eq(pull?.roster.map((g) => g.id), ["g-parent", "g-child"], "失败组不进回读 roster");
    eq(pull?.roster.find((g) => g.id === "g-child")?.parent, "g-parent", "回读 roster 携带嵌套");
  }

  // catalog 非空 → 以后端为准，零导入
  {
    let creates = 0;
    const bindings: ProjectGroupCatalogBindings = {
      ManageSessionsList: async () => ({
        groups: {},
        projectGroups: [{ id: "srv", title: "Server", depth: 0, sortOrder: 0 }],
        sessions: [],
      } as unknown as ManageSessionsListResult),
      CreateProjectGroup: async () => { creates += 1; throw new Error("should not run"); },
    };
    const pull = await pullProjectGroupCatalog(bindings, [{ id: "stale", title: "Stale", sortOrder: 0 }]);
    eq(pull?.roster.map((g) => g.id), ["srv"], "catalog 非空 → 直接采用后端 roster");
    eq(pull?.imported, false, "不触发导入");
    eq(creates, 0, "本地影子不会倒灌进 catalog");
  }

  // 读失败 → null（组件留在 localStorage 模式）
  {
    const bindings: ProjectGroupCatalogBindings = {
      ManageSessionsList: async () => { throw new Error("offline"); },
    };
    eq(await pullProjectGroupCatalog(bindings, []), null, "读失败 → null（不误标 catalog 模式）");
  }
}

// ── ③ 写面参数钉 ────────────────────────────────────────────────────────────
console.log("\n892-B⑤ 写面包装（create/rename/提案→confirm/cascade）");
{
  const calls: Array<{ method: string; args: unknown[] }> = [];
  const bindings: ProjectGroupCatalogBindings = {
    CreateProjectGroup: async (groupID, title, parentID) => {
      calls.push({ method: "create", args: [groupID, title, parentID] });
      return { kind: "project", groupId: groupID, applied: true } as ManageSessionsGroupResult;
    },
    RenameProjectGroup: async (groupID, title) => {
      calls.push({ method: "rename", args: [groupID, title] });
      return { kind: "project", groupId: groupID, applied: true } as ManageSessionsGroupResult;
    },
    DeleteProjectGroup: async (groupID, cascade, confirm) => {
      calls.push({ method: "delete", args: [groupID, cascade, confirm] });
      return { kind: "project", groupId: groupID, applied: confirm, ...(confirm ? {} : { plan: { deleted: [groupID], promoted: ["kid"] } as DeleteGroupsPlan }) } as ManageSessionsGroupResult;
    },
  };
  ok(await createProjectGroupViaCatalog(bindings, "gid", "Title", "pid"), "create 成功返回 true");
  eq(calls.pop(), { method: "create", args: ["gid", "Title", "pid"] }, "create 参数逐位（含 parentID）");
  ok(await createProjectGroupViaCatalog(bindings, "top", "Title", null), "create 顶层（parentID null）");
  eq(calls.pop(), { method: "create", args: ["top", "Title", ""] }, "顶层 parentID 归一为空串（后端拼法）");
  ok(await renameProjectGroupViaCatalog(bindings, "gid", "New"), "rename 成功返回 true");
  eq(calls.pop(), { method: "rename", args: ["gid", "New"] }, "rename 参数逐位");

  const plan = await proposeProjectGroupDelete(bindings, "gid");
  eq(calls.pop(), { method: "delete", args: ["gid", false, false] }, "提案 = confirm=false 零写入");
  eq(plan?.promoted, ["kid"], "提案返回 plan（子组升顶清单）");
  await executeProjectGroupDelete(bindings, "gid", true);
  eq(calls.pop(), { method: "delete", args: ["gid", true, true] }, "执行 = confirm=true + cascade 逐位");

  // 缺绑定 → false/null，不炸
  ok(await createProjectGroupViaCatalog({}, "x", "y", null) === false, "无 create 绑定 → false");
  ok(await renameProjectGroupViaCatalog({}, "x", "y") === false, "无 rename 绑定 → false");
  eq(await proposeProjectGroupDelete({}, "x"), null, "无 delete 绑定 → null（走本地回退）");
  await executeProjectGroupDelete({}, "x", false); // 不炸即可
  ok(true, "无 delete 绑定的 execute 是安全 no-op");

  // 后端报错 → 包装吞掉并返回 false/null（调用方回拉收敛）
  const failing: ProjectGroupCatalogBindings = {
    CreateProjectGroup: async () => { throw new Error("rejected"); },
    DeleteProjectGroup: async () => { throw new Error("rejected"); },
  };
  ok(await createProjectGroupViaCatalog(failing, "x", "y", null) === false, "create 失败 → false");
  eq(await proposeProjectGroupDelete(failing, "x"), null, "提案失败 → null（不弹半截窗的前提）");
}

// ── ④ 赋值清理 ──────────────────────────────────────────────────────────────
console.log("\n892-B⑤ pruneProjectGroupAssign（组离开 roster → 项目回未分组）");
{
  dom.window.localStorage.clear();
  const assign = { "/kept": "g1", "/gone": "g2", "/cascade-child": "g3" };
  const pruned = pruneProjectGroupAssign(assign, new Set(["g1"]));
  eq(pruned, { "/kept": "g1" }, "死组（直删或 cascade 子组）的赋值被清");
  eq(JSON.parse(dom.window.localStorage.getItem("projectTree:projectGroupAssign") ?? "{}"), { "/kept": "g1" }, "清理已持久化");
  const same = pruneProjectGroupAssign(pruned, new Set(["g1"]));
  ok(same === pruned, "无变化保持同一引用（跳过渲染）");
}

// ── ⑤ 本地回退与影子清理 ────────────────────────────────────────────────────
console.log("\n892-B⑤ 本地回退（parent 存储拼法 / 清影子防复活）");
{
  dom.window.localStorage.clear();
  const { groups: withParent } = addProjectGroup("Child", [{ id: "p", title: "Parent", sortOrder: 0 }], "p");
  eq(withParent.at(-1)?.parent, "p", "addProjectGroup 写存储拼法 parent");
  const { groups: withoutParent } = addProjectGroup("Top", []);
  eq("parent" in withoutParent[0]!, false, "顶层创建不长 parent 键（扁平兼容）");
  clearProjectGroupRoster();
  eq(loadProjectGroups(), [], "clearProjectGroupRoster 清空本地影子（防删组复活）");
}

// ── ⑥ 908 空 roster 守卫（data-loss 三态钉） ────────────────────────────────
console.log("\n908 空 roster 守卫（空拉取保持归属 / null 提前返回 / 正常删组仍 prune）");
{
  // 态1：启动竞态下 list 空/缺 projectGroups 字段 + 本地影子已清 →
  // {roster: [], imported: false} 非 null（:82 catch 安全分支不覆盖）→ 归属必须保持。
  {
    dom.window.localStorage.clear();
    const assign = { "/a": "g1", "/b": "g2" };
    localStorage.setItem("projectTree:projectGroupAssign", JSON.stringify(assign));
    let creates = 0;
    const bindings: ProjectGroupCatalogBindings = {
      ManageSessionsList: async () => ({ groups: {}, sessions: [] } as unknown as ManageSessionsListResult),
      CreateProjectGroup: async () => { creates += 1; throw new Error("must not run"); },
    };
    const pull = await pullProjectGroupCatalog(bindings, []);
    ok(pull !== null, "态1 前提：projectGroups 字段缺失 → 返回非 null（catch 安全分支不覆盖）");
    eq(pull?.roster.length, 0, "态1 前提：roster 为空");
    eq(pull?.imported, false, "态1 前提：本地影子已清 → imported=false（不走迁移重建）");
    eq(creates, 0, "态1 前提：影子已清 → 迁移重建分支不触发（根因④实钉）");
    drainFrontendLogRing();
    const kept = pruneProjectGroupAssign(assign, new Set((pull?.roster ?? []).map((group) => group.id)));
    ok(kept === assign, "态1：空 roster → 跳过 prune，同一引用（跳过渲染）");
    eq(kept, { "/a": "g1", "/b": "g2" }, "态1：归属逐位保持（8 项事故的等价面）");
    eq(loadProjectGroupAssign(), { "/a": "g1", "/b": "g2" }, "态1：localStorage 持久化面未被清空");
    const ring = peekFrontendLogRing();
    eq(ring.length, 1, "态1：守卫恰好 WARN 一条");
    ok(ring.some((line) => line.includes("[frontend:project-group-assign]") && line.includes("kept=2")),
      "态1：WARN 带归属快照计数（kept=2）");
  }

  // 态2：pull null（无绑定/读失败）→ 组件在 setAssign 之前提前返回，归属保持。
  {
    eq(await pullProjectGroupCatalog({}, []), null, "态2：无绑定/读失败 → null（留在本地）");
    const guardIdx = treeSource.indexOf("if (!pull || seq !== projectGroupPullSeqRef.current) return;");
    const pruneIdx = treeSource.indexOf("pruneProjectGroupAssign(current, new Set(pull.roster.map");
    ok(guardIdx > -1 && pruneIdx > guardIdx, "态2：null 提前返回位于 prune 接线之前（pull null 不触达 setAssign）");
  }

  // 态3：roster 非空（正常删组，其余组仍在）→ 差集 prune 语义不变 + 快照入遥测。
  {
    dom.window.localStorage.clear();
    const assign = { "/kept": "g1", "/gone": "g2" };
    drainFrontendLogRing();
    const pruned = pruneProjectGroupAssign(assign, new Set(["g1", "g3"]));
    eq(pruned, { "/kept": "g1" }, "态3：roster 非空 → 死组（直删/cascade）赋值仍被清");
    eq(loadProjectGroupAssign(), { "/kept": "g1" }, "态3：清理已持久化");
    ok(peekFrontendLogRing().some((line) => line.includes("before=2") && line.includes("kept=1")),
      "态3：prune 前归属快照入遥测（before=2 kept=1，data-loss 面可追溯）");
  }
}

// ── ProjectTree 接线源码钉 ──────────────────────────────────────────────────
console.log("\n892-B⑤ ProjectTree 接线（递归渲染 / 菜单 / 提案删除 / v2 刷新）");
{
  ok(treeSource.includes("const renderGroupRow = (group: ProjectGroup, level: number): ReactNode => {"),
    "分组行渲染改为递归函数（level 每层 +1）");
  ok(treeSource.includes("kidGroups.map((kid) => renderGroupRow(kid, level + 1))"),
    "子组在父块内任意层递归渲染");
  ok(treeSource.includes("const topGroups = groups.filter((group) => !group.parent || !groups.some((candidate) => candidate.id === group.parent))"),
    "顶层判定 = 无 parent 或父已离开（悬空指针渲染期升顶）");
  ok(/key: "create-subgroup",[\s\S]{0,160}label: t\("projectTree\.createSubGroup"\)/.test(treeSource),
    "分组右键菜单含「新建子分组」");
  ok(treeSource.includes("if (event.key === \"Enter\") commitProjectSubGroup(group.id);"),
    "composer Enter 提交到本组（parent 逐位）");
  ok(treeSource.includes("createProjectGroupViaCatalog(projectGroupCatalogRef.current, nextProjectGroupID(), trimmed, parentID)"),
    "子分组创建走 catalog 直连（parentID 逐位）");
  ok(treeSource.includes("proposeProjectGroupDelete(projectGroupCatalogRef.current, group.id)"),
    "解散走提案制（confirm=false 零写入取 plan）");
  ok(treeSource.includes("executeProjectGroupDelete(projectGroupCatalogRef.current, group.id, cascadeRef.current)"),
    "确认后 execute 携带勾选的 cascade 值");
  ok(treeSource.includes("void refreshProjectGroupsRef.current();"),
    "v2 reason=metadata 事件触发项目组 roster 回拉（每写已发）");
  ok(treeSource.includes("useEffect(() => { void refreshProjectGroups(); }, [refreshProjectGroups]);"),
    "挂载时拉取 catalog（含一次性导入路径）");
  ok(treeSource.includes("clearProjectGroupRoster()") && treeSource.includes("pruneProjectGroupAssign(current, new Set(pull.roster.map((group) => group.id)))"),
    "导入后清本地影子 + 赋值按新 roster 收敛");
  ok(treeSource.includes("await projectGroupDeleteDialog.confirm({")
    && treeSource.includes("<p>{t(\"projectGroup.deleteConfirm\", { title: group.title })}</p>")
    && !treeSource.includes("app.ConfirmAction("),
    "guard: 无 catalog 模式本地溶解同样走应用内确认对话框（tl1249：老 ConfirmAction 在 Windows 是死按钮，已清除；827 契约保位）");
}

process.stdout.write(`\nproject-tree-project-group-catalog: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
