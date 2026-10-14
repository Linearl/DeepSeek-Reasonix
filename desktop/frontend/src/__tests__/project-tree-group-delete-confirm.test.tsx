// Run: tsx src/__tests__/project-tree-group-delete-confirm.test.tsx
//
// 892-B 验收④：分组级联删除确认（A 片提案制 confirm=false → plan →
// ConfirmDialog 两选项 → confirm=true）。
//   ① 中间层删除先发提案（confirm=false 零写入），弹窗展示「子组升顶」
//      清单与 cascade 勾选；确认后 execute 携带 cascade=false；
//   ② 勾选 cascade → execute cascade=true（整删子树）；
//   ③ 取消 → 只有提案调用，零执行；
//   ④ 无子组的叶子组：无 cascade 勾选项，批次（batch）时给 wide 弹窗与提示；
//   ⑤ 老 bindings（无 DeleteSessionGroup）→ 无弹窗，回退 CAS 解散
//      （子组父指针悬空=渲染期升顶，与提案默认同语义）；
//   ⑥ 提案调用失败 → 同样回退 CAS 解散，不弹半截窗。

import { JSDOM } from "jsdom";
import type { Root } from "react-dom/client";
import type { Translator } from "../lib/i18n";
import type {
  DeleteGroupsPlan,
  ManageSessionsGroupResult,
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

// React must load AFTER the jsdom globals (canUseDOM pins at module load) —
// same reason ask-card-identity / subgroup-create use dynamic imports.
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTreeGroupRows, useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");

// Identity translator that still surfaces the substituted variables, so the
// test can pin both the key and the plan values passed into it.
const t = ((key: string, vars?: Record<string, string>) => {
  if (!vars) return key;
  const tail = [
    vars.title !== undefined ? `:${vars.title}` : "",
    vars.titles !== undefined ? `[${vars.titles}]` : "",
    vars.count !== undefined ? `#${vars.count}` : "",
  ].join("");
  return tail ? `${key}${tail}` : key;
}) as unknown as Translator;

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

interface DeleteCall { scope: string; root: string; groupID: string; cascade: boolean; confirm: boolean }

function clone(groups: SessionGroup[]): SessionGroup[] {
  return groups.map((g) => ({ ...g }));
}

/** A-slice bindings: DeleteSessionGroup records every call; confirm=false
 * serves the plan untouched (zero write), confirm=true applies promote/cascade
 * to the mock roster so the post-execute re-pull reads the truth back.
 * CAS methods ride along so the legacy fallback paths are exercisable too. */
function asliceBindings(
  initial: SessionGroup[],
  calls: DeleteCall[],
  plans: Record<string, DeleteGroupsPlan>,
  opts: { failPropose?: boolean } = {},
): { bindings: ProjectTreeOrganizationBindings; state: () => SessionGroup[]; saved: SessionGroup[][] } {
  let groups = clone(initial);
  let revision = 1;
  const saved: SessionGroup[][] = [];
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => clone(groups),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: clone(groups), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: clone(groups), revision, applied: false };
      groups = clone(next);
      revision += 1;
      saved.push(clone(next));
      return { groups: clone(groups), revision, applied: true };
    },
    DeleteSessionGroup: async (scope, root, groupID, cascade, confirm) => {
      calls.push({ scope, root, groupID, cascade, confirm });
      if (!confirm) {
        if (opts.failPropose) throw new Error("proposal failed");
        return {
          kind: "session", scope, workspaceRoot: root, groupId: groupID,
          applied: false, plan: plans[groupID] ?? { deleted: [groupID] },
        } satisfies ManageSessionsGroupResult;
      }
      if (cascade) {
        const doomed = new Set([groupID]);
        let grew = true;
        while (grew) {
          grew = false;
          for (const g of groups) {
            if (g.parent && doomed.has(g.parent) && !doomed.has(g.id)) { doomed.add(g.id); grew = true; }
          }
        }
        groups = groups.filter((g) => !doomed.has(g.id));
      } else {
        groups = groups
          .filter((g) => g.id !== groupID)
          .map((g) => (g.parent === groupID ? { ...g, parent: undefined } : g));
      }
      revision += 1;
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, applied: true } satisfies ManageSessionsGroupResult;
    },
  };
  return { bindings, state: () => groups, saved };
}

/** Legacy bindings: no A-slice method at all (pre-892 mock shape). */
function legacyBindings(initial: SessionGroup[]): { bindings: ProjectTreeOrganizationBindings; saved: SessionGroup[][] } {
  let groups = clone(initial);
  let revision = 1;
  const saved: SessionGroup[][] = [];
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => clone(groups),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: clone(groups), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: clone(groups), revision, applied: false };
      groups = clone(next);
      revision += 1;
      saved.push(clone(next));
      return { groups: clone(groups), revision, applied: true };
    },
  };
  return { bindings, saved };
}

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
  return root;
}

async function cleanup() {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  activeRoot = null;
}

function headers(): HTMLElement[] {
  return [...document.querySelectorAll(".project-tree__group-main")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

async function openMenuAndSelect(index: number, label: string) {
  await act(async () => {
    headers()[index].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const item = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .find((el) => (el.textContent ?? "").trim() === label) as HTMLElement | undefined;
  ok(Boolean(item), `菜单含「${label}」`);
  if (item) await act(async () => { item.click(); await flush(); });
}

const dialog = () => document.querySelector(".reasonix-confirm-dialog") as HTMLElement | null;
const dialogMessage = () => (document.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "");
const cascadeBox = () => document.querySelector(".reasonix-confirm-dialog__cascade input") as HTMLInputElement | null;
const dialogButtons = () => [...document.querySelectorAll(".reasonix-confirm-dialog .modal__actions button")] as HTMLElement[];
const confirmButton = () => dialogButtons().find((b) => b.classList.contains("btn--danger")) ?? null;
const cancelButton = () => dialogButtons().find((b) => !b.classList.contains("btn--danger")) ?? null;

async function click(element: HTMLElement | null) {
  if (!element) throw new Error("click target missing");
  await act(async () => { element.click(); await flush(); });
}

const parentChild: SessionGroup[] = [
  { id: "g-a", title: "A", topicIds: ["t1"] },
  { id: "g-b", title: "B", topicIds: ["t2"], parent: "g-a" },
];

console.log("\n892-B④ 提案→确认：中间层删除展示子组升顶，确认执行 cascade=false");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(parentChild, calls, {
    "g-a": { deleted: ["g-a"], promoted: ["g-b"], unfiledTopics: ["t1", "t2"] },
  });
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  eq(calls.length, 1, "菜单选择只发起一次提案（confirm=false 零写入）");
  eq(calls[0]?.scope, "project", "提案 scope 按 roster 键派生");
  eq(calls[0]?.root, "/repo", "提案 workspaceRoot 逐位传递");
  eq(calls[0]?.groupID, "g-a", "提案目标是被右键的中间层分组");
  eq(calls[0]?.cascade, false, "提案按默认（子组升顶）语义取计划");
  eq(calls[0]?.confirm, false, "提案调用 confirm=false");
  ok(dialog() !== null, "提案返回 plan 后弹出确认对话框");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanIntro:A"), "弹窗标题带分组名");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanUnfiled#2"), "弹窗展示受影响会话数");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanPromote[B]"), "弹窗展示子组升顶清单（含标题）");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanCascade#1"), "弹窗提供 cascade 整删选项");
  ok(cascadeBox() !== null, "cascade 以 checkbox 形式呈现");
  eq(state().length, 2, "确认前零写入（roster 原样）");

  await click(confirmButton());
  await settle();
  eq(calls.length, 2, "确认后追加一次执行调用");
  eq(calls[1]?.groupID, "g-a", "执行目标同提案");
  eq(calls[1]?.cascade, false, "未勾选 → cascade=false（子组升顶）");
  eq(calls[1]?.confirm, true, "执行调用 confirm=true");
  eq(state().length, 1, "执行后后端 roster 去掉中间层");
  eq(state().find((g) => g.id === "g-b")?.parent, undefined, "子组父指针被清（升顶）");
  await settle();
  eq(headers().map((h) => h.getAttribute("title")), ["B"], "回拉渲染：子组升到顶层，中间层消失");
  eq(dialog(), null, "执行后对话框关闭");
  await cleanup();
}

console.log("\n892-B④ 勾选 cascade → 整删子树");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(parentChild, calls, {
    "g-a": { deleted: ["g-a"], promoted: ["g-b"], unfiledTopics: ["t1", "t2"] },
  });
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  ok(cascadeBox() !== null, "cascade 复选框就位");
  await click(cascadeBox());
  eq(cascadeBox()?.checked, true, "复选框被勾上");
  await click(confirmButton());
  await settle();
  eq(calls.length, 2, "提案+执行各一次");
  eq(calls[1]?.cascade, true, "勾选后 execute 携带 cascade=true（整删）");
  eq(calls[1]?.confirm, true, "执行调用 confirm=true");
  eq(state().length, 0, "整删后子组一并消失");
  await settle();
  eq(headers().length, 0, "回拉渲染：整棵子树不再出现");
  await cleanup();
}

console.log("\n892-B④ 取消 → 只有提案，零执行");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(parentChild, calls, {
    "g-a": { deleted: ["g-a"], promoted: ["g-b"], unfiledTopics: ["t1", "t2"] },
  });
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  ok(dialog() !== null, "对话框弹出");
  await click(cancelButton());
  await settle();
  eq(calls.length, 1, "取消后没有执行调用（仍只有提案）");
  eq(state().length, 2, "取消后 roster 原样");
  eq(dialog(), null, "取消后对话框关闭");
  await settle();
  eq(headers().map((h) => h.getAttribute("title")), ["A", "B"], "取消不改变渲染");
  await cleanup();
}

console.log("\n892-B④ 叶子组：无 cascade 选项；批次（batch）弹窗加宽并提示");
{
  const calls: DeleteCall[] = [];
  const many = Array.from({ length: 10 }, (_, i) => `t${i + 1}`);
  const { bindings } = asliceBindings(
    [{ id: "g-s", title: "Solo", topicIds: many }],
    calls,
    { "g-s": { deleted: ["g-s"], unfiledTopics: many, batch: true } },
  );
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  ok(dialog() !== null, "叶子组删除同样走提案确认");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanUnfiled#10"), "展示 10 个受影响会话");
  ok(!dialogMessage().includes("projectTree.deleteGroupPlanPromote"), "无子组 → 不展示升顶清单");
  eq(cascadeBox(), null, "无子组 → 不提供 cascade 勾选");
  ok(dialogMessage().includes("projectTree.deleteGroupPlanBatch#10"), "批次阈值提示出现");
  ok(dialog()?.classList.contains("reasonix-confirm-dialog--wide") === true, "batch 时弹窗加宽（列表可读）");
  await click(cancelButton());
  await cleanup();
}

console.log("\n892-B④ 老 bindings 回退：无弹窗，CAS 解散（子组渲染期升顶）");
{
  const { bindings, saved } = legacyBindings(parentChild);
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  eq(dialog(), null, "无 DeleteSessionGroup 绑定 → 不弹提案窗（pre-892 行为）");
  eq(saved.length, 1, "回退走一次 CAS roster 保存");
  eq(saved[0]?.length, 1, "CAS payload 去掉被解散的分组");
  eq(saved[0]?.[0]?.id, "g-b", "子组留在 roster");
  eq(saved[0]?.[0]?.parent, "g-a", "父指针悬空 = 渲染期升顶（与提案默认同语义）");
  await settle();
  eq(headers().map((h) => h.getAttribute("title")), ["B"], "回拉渲染：子组顶上来了");
  await cleanup();
}

console.log("\n892-B④ 提案失败 → 回退 CAS 解散，不弹半截窗");
{
  const calls: DeleteCall[] = [];
  const { bindings, saved } = asliceBindings(parentChild, calls, {}, { failPropose: true });
  await mount(bindings);
  await settle(2);

  await openMenuAndSelect(0, "projectTree.dissolveGroup");
  await settle();
  eq(calls.length, 1, "提案尝试过一次（后端报错）");
  eq(dialog(), null, "提案失败不弹半截窗");
  eq(saved.length, 1, "回退走一次 CAS roster 保存");
  eq(saved[0]?.find((g) => g.id === "g-a"), undefined, "CAS payload 已去掉被解散分组");
  await settle();
  eq(headers().map((h) => h.getAttribute("title")), ["B"], "回拉渲染：子组顶上来了");
  await cleanup();
}

process.stdout.write(`\nproject-tree-group-delete-confirm: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
