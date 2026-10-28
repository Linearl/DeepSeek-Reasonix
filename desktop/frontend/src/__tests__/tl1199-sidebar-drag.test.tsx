// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1199-sidebar-drag.test.tsx
//
// tl 1199 验收逐条钉（侧栏拖拽体验统一 + 项目跨分组拖拽 bug 根修）:
//   ① 对话分组整行/标题可拖：鼠标按住标题（非箭头/计数/badge）立即进入拖拽
//      机器（无 350ms 等待），与对话行按住即拖手感一致；把手（task 169）保留
//      作 affordance；触屏/笔保留 350ms 长按（防滚动手势误触）；单击仍折叠。
//   ② 项目跨分组拖拽生效：分组头接受项目 drop → 走右键「移动到分组」同一条
//      handleMoveToGroup 落盘链（拖拽只是另一触发入口，不另造机制）；项目行间
//      跨组 drop 同样转 move；同组/均未分组保持平铺重排。
//   ③ 不静默：同组 drop（防御路径）有 toast 反馈（projectTree.projectAlready
//      InGroup 三语键）；落点高亮 --drop-into CSS 有定义。
//   ④ 零回归源码钉：350ms 触屏路径保留、拖拽视觉跟随 moved 门控。

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

// ── source guards ────────────────────────────────────────────────────────────
{
  const thisFile = fileURLToPath(import.meta.url);
  const orgSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTreeOrganization.tsx"), "utf8");
  const treeSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTree.tsx"), "utf8");
  const cssSource = readFileSync(resolve(dirname(thisFile), "../styles.css"), "utf8");

  // ① 标题即拖（鼠标路径）与触屏长按并存
  ok(orgSource.includes('if (pointerType === "mouse")'), "mouse press enters the drag machine immediately (no 350ms wait)");
  ok(orgSource.includes("}, 350)"), "the task-50 long-press path is retained for touch/pen (350ms timer intact)");
  ok(orgSource.includes("beginGroupPress(group.id, event.pointerType)"), "the header press forwards the pointer type");
  // ① 行内交互区不劫持（展开箭头/计数/活跃 badge）
  ok(orgSource.includes('.project-tree__group-chevron,.project-tree__group-count,.project-tree__group-state,.project-tree__group-active-count'),
    "the fold chevron / count / activity badges are excluded from the drag start zones");
  // ① 把手保留（用户 20261027 拍板：affordance）
  ok(orgSource.includes('className="project-tree__group-drag"') && orgSource.includes("GripVertical"), "the grip handle stays as the affordance");
  // ① 拖拽视觉跟随 moved（单击不闪 dim）
  ok(orgSource.includes("draggingGroup === group.id && groupDragRef.current.moved"), "the dragging paint is gated on actual motion (plain click does not flash)");
  ok(cssSource.includes("user-select: none;") && cssSource.includes(".project-tree__group-main {"), "the group header disables text selection (pointer drag must not race selection)");

  // ② 项目拖拽专用 MIME + 分组头接项目 drop
  ok(treeSource.includes('const PROJECT_ROW_DRAG_TYPE = "application/x-reasonix-project-root"'), "project drags carry a dedicated MIME type (topic drags share text/plain)");
  ok(treeSource.includes("event.dataTransfer.setData(PROJECT_ROW_DRAG_TYPE, projectDragKey)"), "the project row drag start sets the dedicated type");
  ok(treeSource.includes("handleMoveToGroup(droppedProjectRoot, group.id)"), "a project drop on a group header runs the SAME chain as the menu's move-to-group");
  ok(treeSource.includes("handleMoveToGroup(draggedRoot, targetGroupId)"), "a project drop on another group's row runs the SAME move chain");
  ok(treeSource.includes('projectTree.projectAlreadyInGroup'), "the same-group defensive drop toasts instead of failing silently");
  ok(cssSource.includes(".project-tree__pgroup-main--drop-into {"), "the group-header landing highlight has a CSS definition");
}

const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
const en = (await import("../locales/en")).en as unknown as Record<string, string>;
const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
eq(zh["projectTree.projectAlreadyInGroup"], "项目已在分组「{title}」中", "zh alreadyInGroup key");
ok(Boolean(en["projectTree.projectAlreadyInGroup"]) && Boolean(zhTw["projectTree.projectAlreadyInGroup"]), "alreadyInGroup present in en and zh-TW (no dead key)");

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
    matches: false, media: "", onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
    dispatchEvent: () => false,
  }),
});

// React 必须在 jsdom 全局之后加载（canUseDOM 在模块加载时定格）。

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

function headerOf(index: number): HTMLElement {
  const header = document.querySelectorAll(".project-tree__group")[index]?.querySelector(".project-tree__group-main");
  if (!(header instanceof HTMLElement)) throw new Error(`missing group header ${index}`);
  return header;
}

function titleSpanOf(index: number): Element {
  const title = headerOf(index).querySelector(".project-tree__group-title");
  if (!title) throw new Error(`missing group title ${index}`);
  return title;
}

function chevronOf(index: number): Element {
  const chevron = headerOf(index).querySelector(".project-tree__group-chevron");
  if (!chevron) throw new Error(`missing group chevron ${index}`);
  return chevron;
}

// 带 pointerType 的 pointer 事件派发（jsdom 的 MouseEvent 无该属性，运行时补）。
async function pointerOn(el: Element, type: string, pointerType?: string) {
  const event = new window.MouseEvent(type, { bubbles: true, button: 0 });
  if (pointerType) Object.defineProperty(event, "pointerType", { value: pointerType });
  await act(async () => { el.dispatchEvent(event); await flush(); });
}

async function releasePointer() {
  await act(async () => { window.dispatchEvent(new window.MouseEvent("pointerup")); await flush(); });
}

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
  return { root, render };
}

async function cleanup(root: Root) {
  await act(async () => root.unmount());
  activeRoot = null;
}

function casBindings(state: { groups: SessionGroup[]; revision: number }, saves: SessionGroup[][]): ProjectTreeOrganizationBindings {
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => state.groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: state.groups.map((g) => ({ ...g })), revision: state.revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, groups) => {
      if (expected !== state.revision) return { groups: state.groups.map((g) => ({ ...g })), revision: state.revision, applied: false };
      state.groups = structuredClone(groups);
      state.revision += 1;
      saves.push(structuredClone(groups));
      return { groups: state.groups.map((g) => ({ ...g })), revision: state.revision, applied: true };
    },
  };
}

console.log("\ntl1199 ① mouse title press = instant drag");

// ── ① 鼠标按住标题立即拖（无 350ms 等待），CAS 链落盘 ─────────────────────────
{
  const state = { groups: [groupA, groupB], revision: 7 };
  const saves: SessionGroup[][] = [];
  const { root } = await mount(casBindings(state, saves));
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  // 不等待任何长按间隔：down(title) → hover(B) → up 直接落。
  await pointerOn(titleSpanOf(0), "pointerdown", "mouse");
  await pointerOn(headerOf(1), "pointerover", "mouse");
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);

  eq(renderedOrder(), ["B", "A"], "title drag reorders without any long-press delay (same feel as topic rows)");
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the save goes through the versioned CAS chain (revision-locked)");
  ok(state.revision === 8, "the optimistic lock advanced exactly once");
  await cleanup(root);
}

// ── ① 箭头区按下不进拖拽机器（行内交互区豁免） ───────────────────────────────
{
  const state = { groups: [groupA, groupB], revision: 1 };
  const saves: SessionGroup[][] = [];
  const { root } = await mount(casBindings(state, saves));
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  await pointerOn(chevronOf(0), "pointerdown", "mouse");
  await pointerOn(headerOf(1), "pointerover", "mouse");
  await releasePointer();
  await act(flush);
  eq(saves.length, 0, "a press on the fold chevron never starts a drag (no reorder)");
  eq(renderedOrder(), ["A", "B"], "chevron press leaves the roster untouched");
  await cleanup(root);
}

// ── ① 鼠标快速按+放仍是普通点击（折叠切换），且单击不闪拖拽态 ─────────────────
{
  const state = { groups: [groupA, groupB], revision: 1 };
  const saves: SessionGroup[][] = [];
  const { root } = await mount(casBindings(state, saves));
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  await pointerOn(titleSpanOf(0), "pointerdown", "mouse");
  ok(!headerOf(0).className.includes("project-tree__group-main--dragging"),
    "a motionless press does not paint the dragging dim (visual gated on moved)");
  await pointerOn(headerOf(0), "pointerup", "mouse");
  await act(async () => { headerOf(0).click(); await flush(); });
  eq(saves.length, 0, "a quick mouse press+release never commits a reorder");
  ok(headerOf(0).closest(".project-tree__group")?.className.includes("project-tree__group--collapsed") ?? false,
    "the click still toggles collapse (click semantics unchanged)");

  // 拖拽进行中（moved）才亮拖拽态。
  await pointerOn(titleSpanOf(0), "pointerdown", "mouse");
  await pointerOn(headerOf(1), "pointerover", "mouse");
  ok(headerOf(0).className.includes("project-tree__group-main--dragging"),
    "the source header paints dragging only once the drag is in motion");
  await releasePointer();
  await cleanup(root);
}

// ── ① 触屏保留 350ms 长按路径 ────────────────────────────────────────────────
{
  const state = { groups: [groupA, groupB], revision: 3 };
  const saves: SessionGroup[][] = [];
  const { root } = await mount(casBindings(state, saves));
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);

  // 长按不到 350ms + 位移 + 释放：不落盘（长按路径未进入）。释放走元素事件，
  // 让 onPointerUp 的 cancelGroupPress 真正清掉未触发的长按计时器（真实指针
  // 的释放路径）。
  await pointerOn(titleSpanOf(0), "pointerdown", "touch");
  await pointerOn(headerOf(1), "pointerover", "touch");
  await pointerOn(headerOf(0), "pointerup", "touch");
  await act(flush);
  eq(saves.length, 0, "a short touch press never drags (scroll-gesture safety kept)");

  // 长按过 350ms：拖拽落盘（task-50 路径保留）。
  await pointerOn(titleSpanOf(0), "pointerdown", "touch");
  await act(async () => { await new Promise((r) => setTimeout(r, 400)); });
  await pointerOn(headerOf(1), "pointerover", "touch");
  await releasePointer();
  await waitFor("touch long-press save", () => saves.length === 1);
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the retained 350ms long-press path still reorders");
  await cleanup(root);
}

console.log(`\ntl1199: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
await act(async () => activeRoot?.unmount());
dom.window.close();
