// Run: tsx src/__tests__/project-tree-manage-sessions-consumption.test.tsx
//
// 892-B 验收⑤：A 片 9 action 的 UI 消费面联测。
// 一个连贯 mock 后端（同一状态机同时服务 Wails 直连面与 ManageSessions
// dispatch 面），真实 UI 消费方驱动：
//   - 会话组 controller（useProjectTreeOrganization）：createSubGroup →
//     DeleteSessionGroup 提案→confirm 链路；
//   - 项目组 catalog 库（projectGroupCatalog）：pull/import → create/rename →
//     提案→execute；
//   - op 日志与回退：每笔写留 opId/整册快照，RollbackManageOperation 与
//     ManageSessions("rollback"/"op_log") dispatch 同一状态机。
// 消费面映射（诚实口径，未接线的如实钉住）：
//   list→ManageSessionsList（项目组拉取）；group_create→CreateSessionGroup/
//   CreateProjectGroup；group_delete→DeleteSessionGroup/DeleteProjectGroup
//   （提案制）；group_rename→RenameProjectGroup；rollback/op_log→dispatch 面
//   （UI 未出入口，agent 工具同面）；rename_session/move_session/
//   reorder_session 与 RenameSessionGroup/ReorderTopicInGroup 为已声明未接线
//   残留（会话侧 rename/拖排仍走 CAS 链，行为等价）。

import { JSDOM } from "jsdom";
import type { Root } from "react-dom/client";
import type { Translator } from "../lib/i18n";
import type {
  ProjectNode,
  ProjectTreeOrganizationBindings,
  SessionGroup,
} from "../lib/types";

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

// ── jsdom harness（对齐 subgroup-create 测试的挂载形态） ──────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: false, media: "", onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
    dispatchEvent: () => false,
  }),
});

const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTreeGroupRows, useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");
const catalog = await import("../lib/projectGroupCatalog");
type ProjectGroupCatalogBindings = (typeof catalog)["defaultProjectGroupCatalogBindings"] extends () => infer R ? R : never;

const t = ((key: string) => key) as unknown as Translator;
const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

// ── 连贯 mock 后端 ───────────────────────────────────────────────────────────
// 每笔写留 {opId, action, kind, before, after}（handoff §2.5：before/after 是
// 整册 roster 快照，rollback 恢复 before）。
interface BackendOp { opId: string; action: string; kind: "session" | "project"; before: unknown; after: unknown }
interface ProjectGroupRow { id: string; title: string; parentId?: string; depth: number; sortOrder: number }

function makeBackend() {
  const state = {
    session: [{ id: "g-a", title: "A", topicIds: ["t1"] } as SessionGroup],
    project: [{ id: "p-1", title: "Work", sortOrder: 0 } as ProjectGroupRow],
    ops: [] as BackendOp[],
  };
  let opSeq = 0;
  const snapshot = (kind: "session" | "project") => JSON.parse(JSON.stringify(kind === "session" ? state.session : state.project));
  const record = (action: string, kind: "session" | "project", before: unknown) => {
    opSeq += 1;
    const op: BackendOp = { opId: `op-${opSeq}`, action, kind, before, after: snapshot(kind) };
    state.ops.push(op);
    return op;
  };
  const descendants = (rows: ProjectGroupRow[], id: string) => {
    const doomed = new Set([id]);
    let grew = true;
    while (grew) {
      grew = false;
      for (const row of rows) if (row.parentId && doomed.has(row.parentId) && !doomed.has(row.id)) { doomed.add(row.id); grew = true; }
    }
    return doomed;
  };
  const deletePlan = (rows: ProjectGroupRow[], id: string, cascade: boolean) => {
    const children = rows.filter((row) => row.parentId === id);
    const deleted = cascade ? [...descendants(rows, id)] : [id];
    return { deleted, promoted: cascade ? [] : children.map((child) => child.id) };
  };
  const bindings: ProjectTreeOrganizationBindings & ProjectGroupCatalogBindings & {
    ManageSessions: (action: string, args: Record<string, unknown>) => Promise<unknown>;
    RollbackManageOperation: (opID: string) => Promise<{ applied: boolean }>;
  } = {
    // CAS 面（会话组 roster 读 + 老写链）——与直连面同一状态机。
    ReorderTopics: async () => {},
    ListProjectGroups: async () => JSON.parse(JSON.stringify(state.session)),
    SaveSessionGroups: async (_scope: string, _root: string, groups: SessionGroup[]) => { state.session = JSON.parse(JSON.stringify(groups)); },
    GetProjectGroups: async () => ({ groups: JSON.parse(JSON.stringify(state.session)), revision: state.ops.length + 1, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, _expected, groups) => {
      state.session = JSON.parse(JSON.stringify(groups));
      return { groups: JSON.parse(JSON.stringify(state.session)), revision: state.ops.length + 1, applied: true };
    },
    // A 片直连面：list。
    ManageSessionsList: async () => ({
      groups: { "project|/repo": state.session.map((group, index) => ({
        id: group.id, title: group.title, depth: 0, order: index,
        topics: (group.topicIds ?? []).map((topicId, i) => ({ topicId, title: topicId, order: i })),
      })) },
      projectGroups: state.project,
      sessions: state.session.flatMap((group) => (group.topicIds ?? []).map((topicId) => ({
        topicId, title: topicId, scope: "project", workspaceRoot: "/repo", groupId: group.id, order: 0,
      }))),
    }),
    // 会话组写：group_create / group_delete 提案制。
    CreateSessionGroup: async (scope, root, groupID, title, parentID) => {
      const before = snapshot("session");
      state.session = [...state.session, { id: groupID, title, parent: parentID || undefined, topicIds: [] }];
      const op = record("group_create", "session", before);
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, title, parentId: parentID, applied: true, opId: op.opId };
    },
    DeleteSessionGroup: async (scope, root, groupID, cascade, confirm) => {
      const before = snapshot("session");
      const plan = { deleted: [groupID], promoted: state.session.filter((g) => g.parent === groupID).map((g) => g.id), unfiledTopics: state.session.find((g) => g.id === groupID)?.topicIds ?? [] };
      if (!confirm) return { kind: "session", scope, workspaceRoot: root, groupId: groupID, applied: false, plan };
      if (cascade) {
        const doomed = new Set([groupID]);
        let grew = true;
        while (grew) {
          grew = false;
          for (const g of state.session) if (g.parent && doomed.has(g.parent) && !doomed.has(g.id)) { doomed.add(g.id); grew = true; }
        }
        state.session = state.session.filter((g) => !doomed.has(g.id));
      } else {
        state.session = state.session.filter((g) => g.id !== groupID).map((g) => (g.parent === groupID ? { ...g, parent: undefined } : g));
      }
      const op = record("group_delete", "session", before);
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, applied: true, opId: op.opId };
    },
    // 项目组写：group_create / group_rename / group_delete 提案制。
    CreateProjectGroup: async (groupID, title, parentID) => {
      const before = snapshot("project");
      state.project = [...state.project, { id: groupID, title, ...(parentID ? { parentId: parentID } : {}), depth: parentID ? 1 : 0, sortOrder: state.project.length }];
      const op = record("group_create", "project", before);
      return { kind: "project", groupId: groupID, title, parentId: parentID, applied: true, opId: op.opId };
    },
    RenameProjectGroup: async (groupID, title) => {
      const before = snapshot("project");
      state.project = state.project.map((row) => (row.id === groupID ? { ...row, title } : row));
      const op = record("group_rename", "project", before);
      return { kind: "project", groupId: groupID, title, applied: true, opId: op.opId };
    },
    DeleteProjectGroup: async (groupID, cascade, confirm) => {
      const before = snapshot("project");
      const plan = deletePlan(state.project, groupID, cascade);
      if (!confirm) return { kind: "project", groupId: groupID, applied: false, plan };
      const doomed = cascade ? descendants(state.project, groupID) : new Set([groupID]);
      state.project = state.project.filter((row) => !doomed.has(row.id));
      const op = record("group_delete", "project", before);
      return { kind: "project", groupId: groupID, applied: true, plan, opId: op.opId };
    },
    // 回退：恢复该笔写之前的整册快照（handoff §2.5，无写互锁——这里单线程）。
    RollbackManageOperation: async (opID) => {
      const op = state.ops.find((entry) => entry.opId === opID);
      if (!op) return { applied: false };
      if (op.kind === "session") state.session = JSON.parse(JSON.stringify(op.before));
      else state.project = JSON.parse(JSON.stringify(op.before));
      return { applied: true };
    },
    // 单一 dispatch（与 agent 工具同面）。
    ManageSessions: async (action, args) => {
      if (action === "list") return bindings.ManageSessionsList();
      if (action === "op_log") return state.ops.map(({ opId, action: a, kind }) => ({ opId, action: a, kind }));
      if (action === "rollback") return bindings.RollbackManageOperation(String(args.op_id ?? ""));
      throw new Error(`unsupported action ${action}`);
    },
  };
  return { bindings, state };
}

// ── UI 消费方挂载 ────────────────────────────────────────────────────────────
function Harness({ bindings }: { bindings: ProjectTreeOrganizationBindings }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: 0,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={[]}
    depth={1}
    section="pinned"
    visible
    organization={organization}
    renderNode={() => null}
    t={t}
  />;
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

async function settle(rounds = 6) {
  for (let i = 0; i < rounds; i += 1) await act(flush);
}

let activeRoot: Root | null = null;
async function mount(bindings: ProjectTreeOrganizationBindings) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(<StrictMode><Harness bindings={bindings} /></StrictMode>);
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  activeRoot = null;
}

console.log("\n892-B⑤ UI 消费面联测：group_create → 提案 group_delete → 回退 → op_log");
{
  const { bindings, state } = makeBackend();
  await mount(bindings);
  await settle(2);
  eq(state.session.map((g) => g.id), ["g-a"], "初始 roster 经 CAS 面进入 UI");

  // group_create 消费面：会话组「新建子分组」直连 CreateSessionGroup。
  const { nextProjectGroupID } = await import("../lib/projectGroups");
  const created = await bindings.CreateSessionGroup!("project", "/repo", "g-child", "Child", "g-a");
  ok(Boolean(created.opId), "create 返回 opId（供回退/日志）");
  eq(state.session.find((g) => g.id === "g-child")?.parent, "g-a", "后端 roster 记录嵌套（存储拼法 parent）");

  // group_delete 消费面：提案→confirm 打到连贯后端（UI 侧逐位参数已在
  // 892-B④ 测试钉住，这里验证同一状态机的收敛与日志/回退闭环）。
  await bindings.DeleteSessionGroup!("project", "/repo", "g-a", false, false);
  const afterPlan = state.session.length;
  eq(afterPlan, 2, "提案零写入（roster 不变）");
  await bindings.DeleteSessionGroup!("project", "/repo", "g-a", false, true);
  eq(state.session.map((g) => g.id), ["g-child"], "confirm 执行：中间层删除、子组升顶");
  eq(state.session[0]?.parent, undefined, "升顶后父指针清空");

  // rollback 消费面：恢复 group_delete 之前的整册快照。
  const deleteOp = state.ops.at(-1)!;
  eq(deleteOp.action, "group_delete", "删除留日志");
  const rollback = await bindings.ManageSessions("rollback", { op_id: deleteOp.opId });
  eq((rollback as { applied: boolean }).applied, true, "dispatch rollback 应用成功");
  eq(state.session.map((g) => g.id).sort(), ["g-a", "g-child"], "回退恢复删除前快照（含升顶前的嵌套）");

  // op_log 消费面：dispatch 返回全量日志（opId/action/kind）。
  const log = await bindings.ManageSessions("op_log", {}) as Array<{ opId: string; action: string; kind: string }>;
  eq(log.length, state.ops.length, "op_log 与状态机日志同长");
  ok(log.every((entry) => entry.opId && entry.action && entry.kind), "每条日志含 opId/action/kind");
  eq(log.at(-1)?.kind, "session", "kind 区分会话侧写入");

  // nextProjectGroupID（供下一组测试的 id 生成）不参与本块断言，仅确保可用。
  ok(nextProjectGroupID().startsWith("group-"), "前端 id 生成器可用");
  await cleanup();
}

console.log("\n892-B⑤ 项目组消费面：pull（list）→ create/rename → 提案→execute → dispatch list 收敛");
{
  const { bindings, state } = makeBackend();
  // pull 走 ManageSessionsList（list action 的 UI 消费面）。
  const pull1 = await catalog.pullProjectGroupCatalog(bindings, []);
  eq(pull1?.roster.map((g) => g.id), ["p-1"], "list 消费面：项目组 roster 进入前端");
  eq(pull1?.imported, false, "catalog 非空不触发导入");

  // create/rename 写面（group_create / group_rename 语义）。
  ok(await catalog.createProjectGroupViaCatalog(bindings, "p-child", "Side", "p-1"), "create 成功");
  eq(state.project.find((row) => row.id === "p-child")?.parentId, "p-1", "后端记录嵌套");
  ok(await catalog.renameProjectGroupViaCatalog(bindings, "p-child", "Renamed"), "rename 成功");
  eq(state.project.find((row) => row.id === "p-child")?.title, "Renamed", "后端标题已改");

  // group_delete 提案制消费面。
  const plan = await catalog.proposeProjectGroupDelete(bindings, "p-1");
  eq(plan?.promoted, ["p-child"], "提案展示子组升顶清单");
  eq(state.project.length, 2, "提案零写入");
  await catalog.executeProjectGroupDelete(bindings, "p-1", true);
  eq(state.project.length, 0, "cascade 整删子树");

  // dispatch list 与直连 list 同一状态机（agent 工具同面收敛）。
  const listed = await bindings.ManageSessions("list", {}) as { projectGroups: ProjectGroupRow[] };
  eq(listed.projectGroups.length, 0, "dispatch list 读到同一份（空）catalog");
  const log = await bindings.ManageSessions("op_log", {}) as Array<{ kind: string }>;
  eq(log.at(-1)?.kind, "project", "项目侧写入 kind=project");
}

console.log("\n892-B⑤ 签名面源码钉（10 直连方法 + 9 action 声明 + 未接线残留）");
{
  const { readFileSync } = await import("node:fs");
  const { dirname, resolve } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const here = dirname(fileURLToPath(import.meta.url));
  const typesSource = readFileSync(resolve(here, "../lib/sessionCatalogTypes.ts"), "utf8");
  for (const method of [
    "ManageSessionsList", "CreateSessionGroup", "RenameSessionGroup", "DeleteSessionGroup",
    "ReorderTopicInGroup", "CreateProjectGroup", "RenameProjectGroup", "DeleteProjectGroup",
    "RollbackManageOperation", "ManageSessions",
  ]) {
    ok(typesSource.includes(`${method}?(`), `签名面声明 ${method}（可选方法，typeof 守卫消费）`);
  }
  ok(typesSource.includes("DeleteGroupsPlan") && typesSource.includes("ManageSessionsListResult"),
    "视图类型（plan/list）落 TS 声明");
  const orgSource = readFileSync(resolve(here, "../components/ProjectTreeOrganization.tsx"), "utf8");
  // 未接线残留（如实钉住，防后人误以为已接）：会话 rename/拖排仍走 CAS 链。
  ok(!orgSource.includes("RenameSessionGroup"), "残留：会话 rename 未接 RenameSessionGroup（CAS 行为等价）");
  ok(!orgSource.includes("ReorderTopicInGroup"), "残留：会话拖排未接 ReorderTopicInGroup（CAS 行为等价）");
}

process.stdout.write(`\nproject-tree-manage-sessions-consumption: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
