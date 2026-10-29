// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1246-group-delete-with-sessions.test.tsx
//
// tl1246 验收：「删除分组及其会话」全链。
// 根因：确认原走 app.ConfirmAction —— Windows 端 Wails MessageDialog 忽略
// Buttons 数组（destructive → MB_OK 单按钮），返回通用值 "Ok"，后端按
// 「返回值==本地化按钮文案」判定恒 false，整条链在确认处静默折返（实测
// 「点了没效果」）。修复：确认改走应用内 ConfirmDialog（与解散同载体）；
// 批量 TrashTopic 失败不再吞进 console —— toast 可见且不拆分组壳；全成功
// 后除 roster 回拉外还回拉主题页（被删会话不得以未分组身份留在树上）。
//   ① 取消 → 零 TrashTopic、零 DeleteSessionGroup，roster 原样；
//   ② 确认 → 逐成员 TrashTopic（topicIds 序）→ DeleteSessionGroup
//      confirm=true cascade=false → 分组消失 → 主题页回拉发生；
//   ③ 单个成员回收失败 → error toast 可见（新键 deleteGroupSessionsFailed）、
//      分组壳保留、已成功成员不回滚、零 shell 删除调用；
//   ④ 无 A-slice 绑定（老 mock）：确认后仍全量回收 + CAS 解散回退生效。

import { JSDOM } from "jsdom";
import type { Root } from "react-dom/client";
import type { Translator } from "../lib/i18n";
import type {
  ManageSessionsGroupResult,
  ProjectNode,
  ProjectTreeOrganizationBindings,
  SessionGroup,
} from "../lib/types";

interface DeleteCall { scope: string; root: string; groupID: string; cascade: boolean; confirm: boolean }

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

// ── jsdom harness（对齐 group-delete-confirm 测试的挂载形态） ─────────────────
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
// same reason the sibling group tests use dynamic imports.
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTreeGroupRows, useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");
const { ToastProvider } = await import("../lib/toast");

// Identity translator surfacing substituted variables, so assertions can pin
// both the key and the values passed into it (count / error).
const t = ((key: string, vars?: Record<string, string>) => {
  if (!vars) return key;
  const tail = [
    vars.count !== undefined ? `#${vars.count}` : "",
    vars.error !== undefined ? `[${vars.error}]` : "",
    vars.title !== undefined ? `:${vars.title}` : "",
  ].join("");
  return tail ? `${key}${tail}` : key;
}) as unknown as Translator;

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

const groupWithThree: SessionGroup[] = [
  { id: "g-1", title: "审计对抗", topicIds: ["t1", "t2", "t3"] },
];

interface DeleteCallRecord { scope: string; root: string; groupID: string; cascade: boolean; confirm: boolean }
type DeleteCall = DeleteCallRecord;

/** A-slice bindings with DeleteSessionGroup call recording (confirm=true
 * applies the promote semantics to the mock roster so the post-execute
 * re-pull reads the truth back). */
function asliceBindings(
  initial: SessionGroup[],
  calls: DeleteCall[],
): { bindings: ProjectTreeOrganizationBindings; state: () => SessionGroup[] } {
  let groups = initial.map((g) => ({ ...g }));
  let revision = 1;
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
    DeleteSessionGroup: async (scope, root, groupID, cascade, confirm) => {
      calls.push({ scope, root, groupID, cascade, confirm });
      if (!confirm) {
        return {
          kind: "session", scope, workspaceRoot: root, groupId: groupID,
          applied: false, plan: { deleted: [groupID] },
        } satisfies ManageSessionsGroupResult;
      }
      groups = groups.filter((g) => g.id !== groupID);
      revision += 1;
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, applied: true } satisfies ManageSessionsGroupResult;
    },
  };
  return { bindings, state: () => groups.map((g) => ({ ...g })) };
}

/** Legacy bindings: no A-slice method at all (pre-892 mock shape) — the
 * execute path must fall back to the CAS roster filter. */
function legacyBindings(initial: SessionGroup[]): { bindings: ProjectTreeOrganizationBindings; saved: SessionGroup[][] } {
  let groups = initial.map((g) => ({ ...g }));
  let revision = 1;
  const saved: SessionGroup[][] = [];
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      saved.push(groups.map((g) => ({ ...g })));
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
  return { bindings, saved };
}

/** The Go-side bridge the component reaches through the lazy `app` proxy:
 * TrashTopic (with per-topic failure injection) + the log sink. */
function makeGoBridge(fail: Record<string, string> = {}) {
  const trashCalls: string[] = [];
  const goBridge = {
    async TrashTopic(topicID: string) {
      trashCalls.push(topicID);
      if (fail[topicID]) throw new Error(fail[topicID]!);
    },
    async ReportFrontendLog() {},
  };
  return { trashCalls, goBridge };
}

let refreshCount = 0;
let onTopicsChangedCount = 0;

function Harness({ bindings }: { bindings: ProjectTreeOrganizationBindings }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => { refreshCount += 1; },
    onTopicsChanged: () => { onTopicsChangedCount += 1; },
    organizationRevision: 0,
    bindings,
  });
  return <ToastProvider>
    <ProjectTreeGroupRows
      folder={folder}
      children={[]}
      depth={1}
      section="pinned"
      visible
      organization={organization}
      renderNode={() => null}
      t={t}
    />
  </ToastProvider>;
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

async function settle(rounds = 6) {
  for (let i = 0; i < rounds; i += 1) await act(flush);
}

let activeRoot: Root | null = null;
async function mount(bindings: ProjectTreeOrganizationBindings, goBridge?: unknown) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  refreshCount = 0;
  onTopicsChangedCount = 0;
  (dom.window as unknown as { go: unknown }).go = { main: { App: goBridge ?? {} } };
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

async function openMenuAndSelect(label: string) {
  await act(async () => {
    headers()[0]!.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  let item = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .find((el) => (el.textContent ?? "").trim() === label) as HTMLElement | undefined;
  if (!item) {
    const manageTrigger = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
      .find((el) => (el.textContent ?? "").includes("projectTree.groupManage")) as HTMLElement | undefined;
    if (manageTrigger) {
      await act(async () => { manageTrigger.click(); await flush(); });
      item = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
        .find((el) => (el.textContent ?? "").trim() === label) as HTMLElement | undefined;
    }
  }
  ok(Boolean(item), `菜单含「${label}」`);
  if (item) await act(async () => { item.click(); await flush(); });
}

const dialog = () => document.querySelector(".reasonix-confirm-dialog") as HTMLElement | null;
const dialogMessage = () => (document.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "");
const dialogButtons = () => [...document.querySelectorAll(".reasonix-confirm-dialog .modal__actions button")] as HTMLElement[];
const confirmButton = () => dialogButtons().find((b) => b.classList.contains("btn--danger")) ?? null;
const cancelButton = () => dialogButtons().find((b) => !b.classList.contains("btn--danger")) ?? null;
const errorToasts = () => [...document.querySelectorAll(".toast--error")] as HTMLElement[];

async function click(element: HTMLElement | null) {
  if (!element) throw new Error("click target missing");
  await act(async () => { element.click(); await flush(); });
}

console.log("\ntl1246① 取消 → 零 TrashTopic、零 shell 删除，roster 原样");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(groupWithThree, calls);
  const { trashCalls, goBridge } = makeGoBridge();
  await mount(bindings, goBridge);
  await settle(2);

  await openMenuAndSelect("projectTree.deleteGroupSessions");
  await settle();
  ok(dialog() !== null, "应用内确认对话框弹出（不再依赖原生 ConfirmAction）");
  ok(dialogMessage().includes("projectTree.deleteGroupSessionsConfirm#3"), "弹窗带成员数 3");
  ok(dialogMessage().includes("projectTree.deleteGroupSessionsDetail"), "弹窗带回收路径可恢复说明");

  await click(cancelButton());
  await settle();
  eq(trashCalls, [], "取消后零 TrashTopic 调用");
  eq(calls, [], "取消后零 DeleteSessionGroup 调用");
  eq(state().length, 1, "取消后 roster 原样");
  eq(dialog(), null, "取消后对话框关闭");
  eq(headers().map((h) => h.getAttribute("title")), ["审计对抗"], "取消不改变渲染");
  await cleanup();
}

console.log("\ntl1246② 确认 → 逐成员回收 → A-slice 删壳（cascade=false）→ 主题页回拉");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(groupWithThree, calls);
  const { trashCalls, goBridge } = makeGoBridge();
  await mount(bindings, goBridge);
  await settle(2);

  await openMenuAndSelect("projectTree.deleteGroupSessions");
  await settle();
  await click(confirmButton());
  await settle();
  eq(trashCalls, ["t1", "t2", "t3"], "按 topicIds 序逐成员 TrashTopic");
  eq(calls.length, 1, "shell 删除恰好一次");
  eq(calls[0]?.groupID, "g-1", "删除目标是该分组");
  eq(calls[0]?.cascade, false, "cascade=false（本流程只删单组，成员已先行回收）");
  eq(calls[0]?.confirm, true, "执行调用 confirm=true");
  eq(state().length, 0, "后端 roster 去掉该分组");
  eq(dialog(), null, "执行后对话框关闭");
  ok(refreshCount >= 1, "主题页回拉发生（被删会话不得留在树上）");
  await cleanup();
}

console.log("\ntl1246③ 单成员回收失败 → error toast 可见、壳保留、零 shell 删除");
{
  const calls: DeleteCall[] = [];
  const { bindings, state } = asliceBindings(groupWithThree, calls);
  const { trashCalls, goBridge } = makeGoBridge({ t2: "session lease held" });
  await mount(bindings, goBridge);
  await settle(2);

  await openMenuAndSelect("projectTree.deleteGroupSessions");
  await settle();
  await click(confirmButton());
  await settle();
  eq(trashCalls, ["t1", "t2", "t3"], "尽力回收全部成员（一个失败不拖累其余会话）");
  eq(calls, [], "任一失败 → 零 DeleteSessionGroup 调用（壳保留）");
  eq(state().length, 1, "分组壳仍在 roster");
  eq(state()[0]?.topicIds, ["t1", "t2", "t3"], "roster 成员清单不动");
  ok(errorToasts().length > 0, "错误 toast 出现（失败不再吞进 console）");
  ok(errorToasts().some((el) => (el.textContent ?? "").includes("projectTree.deleteGroupSessionsFailed#1")), "toast 带失败计数 1");
  ok(errorToasts().some((el) => (el.textContent ?? "").includes("[session lease held]")), "toast 带首个失败原因原文");
  eq(headers().map((h) => h.getAttribute("title")), ["审计对抗"], "分组头仍渲染（用户可重试）");
  eq(refreshCount, 0, "失败路径不触发主题页回拉（树未被改动）");
  await cleanup();
}

console.log("\ntl1246④ 无 A-slice 绑定：确认后仍全量回收 + CAS 解散回退");
{
  const { bindings, saved } = legacyBindings(groupWithThree);
  const { trashCalls, goBridge } = makeGoBridge();
  await mount(bindings, goBridge);
  await settle(2);

  await openMenuAndSelect("projectTree.deleteGroupSessions");
  await settle();
  await click(confirmButton());
  await settle();
  eq(trashCalls, ["t1", "t2", "t3"], "回退路径同样逐成员回收");
  eq(saved.length, 1, "回退走一次 CAS roster 保存");
  eq(saved[0]?.length, 0, "CAS payload 去掉该分组");
  eq(headers().length, 0, "回拉渲染：分组消失");
  ok(refreshCount >= 1, "回退路径同样回拉主题页");
  await cleanup();
}

process.stdout.write(`\ntl1246-group-delete-with-sessions: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
