// Run: tsx src/__tests__/project-tree-group-drag.test.tsx
//
// Task 169 — 分组头拖拽手柄（任务 50 的桌面可用性补齐）:
//   the task-50 reorder machine (press → hover → release commits once) gains a
//   visible drag handle that enters the SAME state machine instantly. Long-press
//   stays for touch. Covered here: handle renders per header (hover visibility
//   is a CSS contract, guarded by source), handle drag skips the 350ms delay and
//   lands through the versioned CAS save chain, a handle press without movement
//   neither reorders nor toggles collapse, long-press and short-press behaviour
//   are unchanged, collapse state survives a reorder untouched, and the metadata
//   re-pull (emitProjectTreeMetadataChanged) cannot repaint the pre-save order.
//
// The blocks share ONE jsdom (remounted per block): closing a window between
// blocks leaves React's scheduler for the closed root with dead timers and the
// next mount's passive effects never flush.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import type { Translator } from "../lib/i18n";
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
  const same = actual === expected ||
    JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── source guards: handle markup + hover CSS + retained long-press ──────────
{
  const thisFile = fileURLToPath(import.meta.url);
  const orgSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTreeOrganization.tsx"), "utf8");
  const cssSource = readFileSync(resolve(dirname(thisFile), "../styles.css"), "utf8");
  ok(orgSource.includes('className="project-tree__group-drag"'), "every group header renders the drag handle");
  ok(orgSource.includes("GripVertical"), "the handle uses the established grip glyph");
  ok(orgSource.includes("}, 350)"), "the task-50 long-press path is retained (350ms timer intact)");
  ok(cssSource.includes(".project-tree__group-main:hover .project-tree__group-drag"), "handle appears on header hover (desktop affordance)");
  ok(cssSource.includes(".project-tree__group-main--dragging .project-tree__group-drag"), "handle stays visible while its group is dragged");
}

console.log("\nproject tree group drag handle (task 169)");

// ── harness: one jsdom for every block ───────────────────────────────────────
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
    matches: false,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }),
});

const t = ((key: string) => key) as unknown as Translator;

const folder: ProjectNode = {
  key: "project-/repo",
  kind: "project",
  label: "Repo",
  root: "/repo",
  children: [],
};

const groupA: SessionGroup = { id: "g-a", title: "A", topicIds: [] };
const groupB: SessionGroup = { id: "g-b", title: "B", topicIds: [] };

function GroupHarness({ bindings, revision }: { bindings: ProjectTreeOrganizationBindings; revision: number }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: revision,
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

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(flush);
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

function renderedOrder(): string[] {
  return [...document.querySelectorAll(".project-tree__group")].map((row) =>
    row.querySelector(".project-tree__group-main")?.getAttribute("title") ?? "",
  );
}

async function pointerDownOn(el: Element) {
  await act(async () => { el.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 })); await flush(); });
}

// React derives onPointerEnter from bubbling pointerover events.
async function pointerOverOn(el: Element) {
  await act(async () => { el.dispatchEvent(new window.MouseEvent("pointerover", { bubbles: true })); await flush(); });
}

async function releasePointer() {
  await act(async () => { window.dispatchEvent(new window.MouseEvent("pointerup")); await flush(); });
}

// A release over an element (what a real pointer produces) also runs the
// header's own onPointerUp press-cancel, not just the window finish listener.
async function pointerUpOn(el: Element) {
  await act(async () => { el.dispatchEvent(new window.MouseEvent("pointerup", { bubbles: true })); await flush(); });
}

function handleOf(index: number): HTMLElement {
  const handle = document.querySelectorAll(".project-tree__group")[index]?.querySelector(".project-tree__group-drag");
  if (!(handle instanceof HTMLElement)) throw new Error(`missing drag handle for group row ${index}`);
  return handle;
}

function headerOf(index: number): HTMLElement {
  const header = document.querySelectorAll(".project-tree__group")[index]?.querySelector(".project-tree__group-main");
  if (!(header instanceof HTMLElement)) throw new Error(`missing group header ${index}`);
  return header;
}

// A fresh React root per block: the previous block's component tree (and its
// drag state) is unmounted, and persisted localStorage is cleared so blocks
// stay isolated while sharing the one window.
let activeRoot: Root | null = null;
async function mount(bindings: ProjectTreeOrganizationBindings) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  let revision = 0;
  const render = async (nextRevision?: number) => {
    if (nextRevision !== undefined) revision = nextRevision;
    await act(async () => {
      root.render(<StrictMode><GroupHarness bindings={bindings} revision={revision} /></StrictMode>);
      await flush();
    });
  };
  await render();
  return { root, render, bumpRevision: () => { revision += 1; return render(revision); } };
}

async function cleanup(root: Root) {
  await act(async () => root.unmount());
  activeRoot = null;
}

// ── render: one handle per group header ─────────────────────────────────────
{
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => [groupA, groupB],
    SaveSessionGroups: async () => {},
  };
  const { root } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  eq(document.querySelectorAll(".project-tree__group-drag").length, 2, "each group header renders exactly one drag handle");
  eq(renderedOrder(), ["A", "B"], "initial roster order A, B");
  await cleanup(root);
}

// ── handle drag: instant (no 350ms wait), commits once through the CAS chain ─
{
  let state: SessionGroup[] = [groupA, groupB];
  let revision = 7;
  const saves: SessionGroup[][] = [];
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => structuredClone(state),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: structuredClone(state), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, groups) => {
      if (expected !== revision) return { groups: structuredClone(state), revision, applied: false };
      state = structuredClone(groups);
      revision += 1;
      saves.push(structuredClone(groups));
      return { groups: structuredClone(state), revision, applied: true };
    },
  };
  const { root } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  await pointerDownOn(handleOf(0));
  await pointerOverOn(headerOf(1));
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);

  eq(renderedOrder(), ["B", "A"], "handle drag reorders immediately (no long-press delay involved)");
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the save writes the dropped order to the sidecar");
  ok(revision === 8, "the save went through the expectedRevision optimistic lock (revision advanced)");

  // The metadata event re-pull reads back the post-save state: order stays.
  await act(flush);
  eq(renderedOrder(), ["B", "A"], "order survives the post-save re-pull");
  await cleanup(root);
}

// ── 防复位: a metadata re-pull racing the in-flight save cannot repaint old ──
{
  let state: SessionGroup[] = [groupA, groupB];
  let revision = 3;
  let holdSave: { resolve: () => void } | null = null;
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => structuredClone(state),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: structuredClone(state), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, groups) => {
      if (expected !== revision) return { groups: structuredClone(state), revision, applied: false };
      await new Promise<void>((done) => { holdSave = { resolve: done }; });
      state = structuredClone(groups);
      revision += 1;
      return { groups: structuredClone(state), revision, applied: true };
    },
  };
  const { root, bumpRevision } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  await pointerDownOn(handleOf(0));
  await pointerOverOn(headerOf(1));
  await releasePointer();
  await waitFor("optimistic paint", () => renderedOrder()[0] === "B");
  ok(renderedOrder()[0] === "B" && renderedOrder()[1] === "A", "the dropped order paints optimistically before the save lands");

  // emitProjectTreeMetadataChanged fires while the save chain is still waiting:
  // the forced re-pull must not apply the pre-save snapshot (save-chain guard).
  await bumpRevision();
  eq(renderedOrder(), ["B", "A"], "the in-save metadata re-pull does not repaint the old order");

  // Save settles → the settled (new) roster wins; a further re-pull reads it back.
  holdSave?.resolve();
  await waitFor("settled save", () => state[0]?.id === "g-b");
  await bumpRevision();
  eq(renderedOrder(), ["B", "A"], "the post-save metadata re-pull reads back the new order — no reset");
  await cleanup(root);
}

// ── handle press without movement: no reorder, no collapse toggle ───────────
{
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => [groupA, groupB],
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: [groupA, groupB], revision: 1, applied: true }),
    SaveSessionGroupsVersioned: async () => {
      throw new Error("a motionless handle press must not write the roster");
    },
  };
  const { root } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  await pointerDownOn(handleOf(0));
  await releasePointer();
  eq(renderedOrder(), ["A", "B"], "a motionless handle press does not reorder");

  // The release falls through to a click on the handle; it must not toggle.
  await act(async () => { handleOf(0).click(); await flush(); });
  ok(!headerOf(0).closest(".project-tree__group")?.className.includes("project-tree__group--collapsed"),
    "clicking the handle never toggles collapse");
  await cleanup(root);
}

// ── long-press (task 50) still works; a short press stays a plain click ─────
{
  let saved: SessionGroup[] | null = null;
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => [groupA, groupB],
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: [groupA, groupB], revision: 1, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, _expected, groups) => {
      saved = structuredClone(groups);
      return { groups: structuredClone(groups), revision: 2, applied: true };
    },
  };
  const { root } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  // Long press: hold past 350ms, hover B, release → reorder commits.
  await pointerDownOn(headerOf(0));
  await act(async () => { await new Promise((r) => setTimeout(r, 400)); });
  await pointerOverOn(headerOf(1));
  await releasePointer();
  await waitFor("long-press save", () => saved !== null);
  eq(saved!.map((group) => group.id), ["g-b", "g-a"], "long-press drag still reorders (touch path retained)");
  eq(renderedOrder(), ["B", "A"], "long-press reorder paints optimistically");

  // The post-drag click lands on a common ancestor, so the header never
  // consumed it; the next press resets the stale swallow flag, and that
  // press's own quick release + click behaves as a plain click.
  await pointerDownOn(headerOf(1));
  await pointerUpOn(headerOf(1));
  await act(async () => { headerOf(1).click(); await flush(); });
  ok(headerOf(1).closest(".project-tree__group")?.className.includes("project-tree__group--collapsed") ?? false,
    "the first click after a finished drag is not swallowed (stale flag resets on press)");

  // Short press released before the timer: an ordinary click toggles collapse.
  saved = null;
  await pointerDownOn(headerOf(0));
  await act(async () => { await new Promise((r) => setTimeout(r, 40)); });
  await pointerUpOn(headerOf(0));
  await act(async () => { headerOf(0).click(); await flush(); });
  eq(saved, null, "a press shorter than the long-press delay never commits a reorder");
  ok(headerOf(0).closest(".project-tree__group")?.className.includes("project-tree__group--collapsed") ?? false,
    "a short press stays a plain click (collapse toggled)");
  await cleanup(root);
}

// ── collapse (#8699) and ordering are independent ───────────────────────────
{
  let state: SessionGroup[] = [groupA, groupB];
  let revision = 5;
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => structuredClone(state),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: structuredClone(state), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, groups) => {
      if (expected !== revision) return { groups: structuredClone(state), revision, applied: false };
      state = structuredClone(groups);
      revision += 1;
      return { groups: structuredClone(state), revision, applied: true };
    },
  };
  const { root } = await mount(bindings);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  // Collapse A (plain click), snapshot the persisted collapse set.
  await act(async () => { headerOf(0).click(); await flush(); });
  const collapsedBefore = window.localStorage.getItem("projectTree:sessionGroupCollapsed");
  ok(collapsedBefore?.includes("project|/repo|g-a") ?? false, "collapsing A persists its collapsed key");

  // Drag A below B via the handle.
  await pointerDownOn(handleOf(0));
  await pointerOverOn(headerOf(1));
  await releasePointer();
  await waitFor("save", () => state[0]?.id === "g-b");

  eq(renderedOrder(), ["B", "A"], "the collapsed group reorders like any other");
  const rowA = [...document.querySelectorAll(".project-tree__group")].find((row) => row.querySelector(".project-tree__group-main")?.getAttribute("title") === "A");
  const rowB = [...document.querySelectorAll(".project-tree__group")].find((row) => row.querySelector(".project-tree__group-main")?.getAttribute("title") === "B");
  ok(rowA?.className.includes("project-tree__group--collapsed") ?? false, "A stays collapsed after the move (collapse keyed by id, not position)");
  ok(!(rowB?.className.includes("project-tree__group--collapsed") ?? true), "B stays expanded after the move");
  eq(window.localStorage.getItem("projectTree:sessionGroupCollapsed"), collapsedBefore,
    "the reorder never writes the collapse set (persistSessionGroupCollapsed untouched)");
  await cleanup(root);
}

await act(async () => activeRoot?.unmount());
dom.window.close();

process.stdout.write(`\nproject-tree-group-drag: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
