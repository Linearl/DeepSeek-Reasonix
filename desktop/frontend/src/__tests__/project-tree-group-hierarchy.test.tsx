// Run: tsx src/__tests__/project-tree-group-hierarchy.test.tsx
//
// Task 350 — 会话分组层级化（group parent 树 + 侧栏树 UI）。
// 设计第一条（0928 拍板固化）：层级 = 可见性 ≠ 指挥权——树是展示组织，
// 不派生任何权限/指挥语义；id/成员集在嵌套前后逐位不变。
// 覆盖：纯函数（canNestUnder/nestGroup/unnestGroup/inheritedMemberIDs/
// topLevelGroups 孤儿兜底）、控制器 nestGroup 走版本化 CAS 保存链、
// Alt+拖拽跨层、父组活跃点继承、右键菜单嵌套项、渲染顺序（子组在父块内）。

import { JSDOM } from "jsdom";
import React, { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import type { Translator } from "../lib/i18n";
import {
  canNestUnder,
  childGroups,
  hierarchyMutationIsDisplayOnly,
  inheritedMemberIDs,
  nestGroup,
  nestTargets,
  topLevelGroups,
  unnestGroup,
} from "../lib/sessionGroupTree";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "../lib/types";

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

// ── pure helpers ─────────────────────────────────────────────────────────────
console.log("\nsession group hierarchy — pure helpers (task 350)");
{
  const roster: SessionGroup[] = [
    { id: "top", title: "Top", topicIds: ["a"] },
    { id: "child", title: "Child", topicIds: ["b"], parent: "top" },
    { id: "orphan", title: "Orphan", topicIds: ["c"], parent: "dissolved" },
  ];
  eq(topLevelGroups(roster).map((g) => g.id), ["top", "orphan"], "orphaned pointer falls back to top level");
  eq(childGroups(roster, "top").map((g) => g.id), ["child"], "childGroups lists the nested group");
  eq(inheritedMemberIDs(roster, "top"), ["a", "b"], "inherited members = own + child group members");
  eq(inheritedMemberIDs(roster, "child"), ["b"], "a child aggregates only itself");
  ok(canNestUnder(roster, "top", "child") === false, "a group that already has children cannot become a child itself (depth cap)");
  ok(canNestUnder([{ id: "a", title: "A" }, { id: "b", title: "B" }], "a", "b"), "flat groups nest freely");
  ok(canNestUnder([{ id: "a", title: "A" }, { id: "b", title: "B" }], "a", "a") === false, "self-nesting rejected");
  ok(canNestUnder(roster, "child", "ghost") === false, "unknown parent rejected");
  eq(nestTargets(roster, "child").map((g) => g.id), ["top"], "nest targets exclude the group itself and orphaned groups");

  const flat: SessionGroup[] = [
    { id: "a", title: "A", topicIds: ["t1"] },
    { id: "b", title: "B", topicIds: ["t2"] },
  ];
  const nested = nestGroup(flat, "b", "a");
  ok(nested !== flat, "nestGroup returns a new roster (immutability)");
  eq(nested.map((g) => [g.id, g.parent]), [["a", undefined], ["b", "a"]], "nestGroup only sets the parent pointer");
  ok(hierarchyMutationIsDisplayOnly(flat, nested), "GUARD 层级≠指挥权: nesting touches nothing but parent");
  eq(nested.find((g) => g.id === "b")?.topicIds, ["t2"], "membership survives nesting byte for byte");
  const detached = unnestGroup(nested, "b");
  ok(detached.every((g) => g.parent === undefined), "unnestGroup clears the pointer");
  ok(hierarchyMutationIsDisplayOnly(nested, detached), "GUARD: un-nesting is display-only too");
  eq(nestGroup(flat, "b", "b"), flat, "illegal nest is a no-op (same roster back)");
}
{
  // Alt+drop decision surface mirrors the component: plain drop reorders, Alt
  // drop nests — the guard proves the nest path cannot mutate membership.
  const roster: SessionGroup[] = [
    { id: "a", title: "A", topicIds: ["t1"] },
    { id: "b", title: "B", topicIds: ["t2", "t3"] },
  ];
  ok(canNestUnder(roster, "a", "b"), "alt-drop a onto b is nestable");
  ok(hierarchyMutationIsDisplayOnly(roster, nestGroup(roster, "a", "b")), "GUARD: alt-drop nest keeps topic sets intact");
}

// ── jsdom mount: controller + rendering ──────────────────────────────────────
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
globalThis.MouseEvent = dom.window.MouseEvent as unknown as typeof MouseEvent;
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

const t = ((key: string, vars?: Record<string, string>) => vars?.title ? `${key}:${vars.title}` : key) as unknown as Translator;

function topicNode(id: string, status: ProjectNode["status"]): ProjectNode {
  return { key: `topic-${id}`, kind: "topic", label: id, topicId: id, status, running: status === "streaming" };
}

interface SavedState { payloads: SessionGroup[][]; revisions: number[] }

function makeBindings(initial: SessionGroup[], saved: SavedState): ProjectTreeOrganizationBindings {
  let revision = 1;
  let groups = initial.map((g) => ({ ...g }));
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups,
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope: string, _root: string, expected: number, next: SessionGroup[]) => {
      if (expected !== revision) {
        return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      }
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      saved.payloads.push(next.map((g) => ({ ...g })));
      saved.revisions.push(revision);
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
}

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

function HierarchyHarness({ bindings, revision, children }: {
  bindings: ProjectTreeOrganizationBindings; revision: number; children: ProjectNode[];
}) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: revision,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={children}
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

let activeRoot: Root | null = null;
async function mount(bindings: ProjectTreeOrganizationBindings, children: ProjectNode[] = []) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  let revision = 0;
  await act(async () => {
    root.render(<StrictMode><HierarchyHarness bindings={bindings} revision={revision} children={children} /></StrictMode>);
    await flush();
  });
  return {
    rerender: async () => {
      await act(async () => {
        root.render(<StrictMode><HierarchyHarness bindings={bindings} revision={revision} children={children} /></StrictMode>);
        await flush();
      });
    },
  };
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

function groupBlock(title: string): HTMLElement | null {
  return [...document.querySelectorAll(".project-tree__group")].find(
    (el) => el.querySelector(".project-tree__group-main")?.getAttribute("title") === title,
  ) as HTMLElement | null;
}

console.log("\nsession group hierarchy — controller & render (task 350)");
{
  const initial: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
  ];
  const saved: SavedState = { payloads: [], revisions: [] };
  const bindings = makeBindings(initial, saved);
  await mount(bindings);
  await flush();
  eq(headers().map((h) => h.getAttribute("title")), ["A", "B"], "flat roster renders both groups");

  // Right-click menu offers the nest move; selecting it goes through the CAS save.
  await act(async () => {
    headers()[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const nestItem = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .find((el) => (el.textContent ?? "").includes("projectTree.nestGroupUnder:B")) as HTMLElement | undefined;
  ok(Boolean(nestItem), "context menu offers 移到子层 for a top-level group");
  if (nestItem) {
    await act(async () => { nestItem.click(); await flush(); });
    await flush();
    eq(saved.payloads.length, 1, "nesting writes exactly one CAS payload");
    // The menu was opened on A, so the move is "A under B": B (the target)
    // keeps no parent, A carries the pointer.
    const payload = saved.payloads[0] ?? [];
    const a = payload.find((g) => g.id === "g-a");
    const b = payload.find((g) => g.id === "g-b");
    eq(b?.parent, undefined, "the nest target keeps no parent");
    eq(a?.parent, "g-b", "the nested group carries parent=g-b");
    eq(a?.topicIds, ["t1"], "GUARD 层级≠指挥权: nested payload keeps membership");
    eq(b?.topicIds, ["t2"], "GUARD: target membership untouched");
  }
  await cleanup();
}
{
  // Alt+drop machine: pointerdown on the dragged header's handle, hover the
  // target, release with altKey → nest; plain release keeps the task-50 reorder.
  const initial: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
  ];
  const savedAlt: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(initial, savedAlt));
  await flush();
  const handleB = headers()[1].querySelector(".project-tree__group-drag") as HTMLElement;
  await act(async () => { handleB.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 })); await flush(); });
  await act(async () => { headers()[0].dispatchEvent(new window.MouseEvent("pointerover", { bubbles: true })); await flush(); });
  await act(async () => { window.dispatchEvent(new window.MouseEvent("pointerup", { altKey: true })); await flush(); });
  await flush();
  eq(savedAlt.payloads.length, 1, "alt+drop commits one save");
  eq((savedAlt.payloads[0] ?? []).find((g) => g.id === "g-b")?.parent, "g-a", "alt+drop nests B under A");

  const savedPlain: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(initial, savedPlain));
  await flush();
  const handleB2 = headers()[1].querySelector(".project-tree__group-drag") as HTMLElement;
  await act(async () => { handleB2.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 })); await flush(); });
  await act(async () => { headers()[0].dispatchEvent(new window.MouseEvent("pointerover", { bubbles: true })); await flush(); });
  await act(async () => { window.dispatchEvent(new window.MouseEvent("pointerup")); await flush(); });
  await flush();
  eq(savedPlain.payloads.length, 1, "plain drop still commits the task-50 reorder");
  eq((savedPlain.payloads[0] ?? []).find((g) => g.id === "g-b")?.parent, undefined, "plain drop does not nest (reorder only)");
  await cleanup();
}
{
  // Tree render + activity inheritance: B nested under A; B holds the running
  // session; collapsed A still shows the dot (inherited).
  const initial: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"], parent: "g-a" },
  ];
  const saved: SavedState = { payloads: [], revisions: [] };
  const children = [topicNode("t1", "idle"), topicNode("t2", "streaming")];
  await mount(makeBindings(initial, saved), children);
  await flush();
  eq(headers().map((h) => h.getAttribute("title")), ["A", "B"], "tree renders parent then child group");
  const blockA = groupBlock("A");
  const blockB = groupBlock("B");
  ok(Boolean(blockA && blockB && blockA.contains(blockB) && blockA !== blockB), "B renders inside A's subtree");
  ok(Boolean(blockA?.querySelector(".project-tree__group-subtree")), "the child block uses the subtree container");
  // Collapse A: the dot must stand for A's own (idle) + B's running member.
  await act(async () => { headers()[0].click(); await flush(); });
  eq(headers().map((h) => h.getAttribute("title")), ["A"], "collapsing A hides its subtree");
  const dot = blockA?.querySelector(".project-tree__group-state");
  ok(Boolean(dot), "collapsed parent shows the inherited activity dot");
  const count = blockA?.querySelector(".project-tree__group-active-count")?.textContent ?? "";
  eq(count, "1", "inherited active count = 1 (the child group's running member)");
  await cleanup();
}
{
  // Nested group menu offers 移到顶层; selecting clears the pointer; a nested
  // top-level group offers no further nesting (depth cap surfaced in menu).
  const initial: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: [] },
    { id: "g-b", title: "B", topicIds: [], parent: "g-a" },
  ];
  const saved: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(initial, saved));
  await flush();
  await act(async () => {
    headers()[1].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const topItem = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .find((el) => (el.textContent ?? "").includes("projectTree.moveGroupToTopLevel")) as HTMLElement | undefined;
  ok(Boolean(topItem), "nested group menu offers 移到顶层");
  ok(![...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .some((el) => (el.textContent ?? "").includes("projectTree.nestGroupUnder")), "no nest-under items for an already nested group (depth cap)");
  if (topItem) {
    await act(async () => { topItem.click(); await flush(); });
    await flush();
    eq((saved.payloads[0] ?? []).find((g) => g.id === "g-b")?.parent, undefined, "move-to-top clears the parent pointer");
  }
  await cleanup();
}

console.log(`\ntotal: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
