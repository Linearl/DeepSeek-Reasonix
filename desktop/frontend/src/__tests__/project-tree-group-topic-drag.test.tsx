// Run: tsx src/__tests__/project-tree-group-topic-drag.test.tsx
//
// 任务 950 — 组内会话拖拽排序接线：
//   组内成员拖放此前走顶层 reorderedTopicIDs → 组内 id 不在 project children
//   的落位集合里 → return null → 无操作（用户看到"拖了没反应"）。本测试钉住
//   修复后的行为：
//   - 纯函数 reorderedGroupTopicIDs：同组 before/after 插位、跨组/顶层 null；
//   - UI 拖放消费：dragged/target 同组 → 直连 ReorderTopicInGroup（逐位参数
//     对齐 Go 签名 (scope, root, topicID, groupID, index)）+ 乐观插位；
//   - 持久化：写入后 re-pull / 重新挂载（重启语义）读回新顺序；
//   - 防复位（981 同族）：写在途时元数据 re-pull 不得回刷旧顺序；
//   - 零回归：跨组拖到组头仍走 moveTopicInRoster 追加语义（不调组内 reorder）；
//     顶层 topic 拖排仍走 ReorderTopics（不调组内 reorder）。
//
// The blocks share ONE jsdom (remounted per block) — the closing-timers
// pattern inherited from project-tree-group-drag.test.tsx.

import { JSDOM } from "jsdom";
// tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import * as React from "react";
import { StrictMode, act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  ProjectTreeGroupRows,
  orderedGroupMembers,
  reorderedGroupTopicIDs,
  useProjectTreeOrganization,
} from "../components/ProjectTreeOrganization";
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
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── 纯函数单测 ──────────────────────────────────────────────────────────────
console.log("\n950 纯函数：reorderedGroupTopicIDs / orderedGroupMembers");
{
  const groups: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1", "t2", "t3"] },
    { id: "g-b", title: "B", topicIds: ["t4"] },
  ];
  const after = reorderedGroupTopicIDs(groups, "t3", "t1", "after");
  eq(after, { groupID: "g-a", index: 1, topicIds: ["t1", "t3", "t2"] }, "同组 after：插到目标后一位（index 为移除后下标）");
  const before = reorderedGroupTopicIDs(groups, "t1", "t3", "before");
  eq(before, { groupID: "g-a", index: 1, topicIds: ["t2", "t1", "t3"] }, "同组 before：插到目标前一位");
  eq(reorderedGroupTopicIDs(groups, "t1", "t4", "after"), null, "跨组：null（维持 moveTopicInRoster 语义）");
  eq(reorderedGroupTopicIDs(groups, "t1", "t1", "after"), null, "自身：null");
  eq(reorderedGroupTopicIDs(groups, "t1", "t-x", "after"), null, "目标不在任何组：null（顶层拖排路径）");

  eq(
    orderedGroupMembers(["c", "a", "b", "d"], ["a", "b", "c"], (id) => id),
    ["a", "b", "c", "d"],
    "orderedGroupMembers：按 topicIds 排序，缺席者保序垫底",
  );
}

// ── jsdom harness ───────────────────────────────────────────────────────────
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
globalThis.DragEvent = dom.window.MouseEvent as unknown as typeof DragEvent;
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

const t = ((key: string) => key) as unknown as Translator;

const folder: ProjectNode = {
  key: "project-/repo",
  kind: "project",
  label: "Repo",
  root: "/repo",
  children: [],
};

function topicNode(id: string): ProjectNode {
  return { key: "topic_" + id, kind: "topic", label: id, root: "/repo", topicId: id, children: [] };
}

// 连贯 mock：ReorderTopicInGroup 语义对齐 Go（移除后按收敛下标插位），
// 写入留在 state 里供「重启/刷新」断言读回。
function makeBackend(initial: SessionGroup[]) {
  const state = { groups: structuredClone(initial), revision: 1, reorderCalls: [] as unknown[][], casSaves: [] as string[] };
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => structuredClone(state.groups),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: structuredClone(state.groups), revision: state.revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, groups) => {
      if (expected !== state.revision) return { groups: structuredClone(state.groups), revision: state.revision, applied: false };
      state.groups = structuredClone(groups);
      state.revision += 1;
      state.casSaves.push(...groups.map((g) => g.id));
      return { groups: structuredClone(state.groups), revision: state.revision, applied: true };
    },
    ReorderTopicInGroup: async (scope, root, topicID, groupID, index) => {
      state.reorderCalls.push([scope, root, topicID, groupID, index]);
      const group = state.groups.find((candidate) => candidate.id === groupID);
      if (!group) throw new Error(`group ${groupID} not found`);
      const members = group.topicIds ?? [];
      const pos = members.indexOf(topicID);
      if (pos < 0) throw new Error(`topic ${topicID} is not in group ${groupID}`);
      const clamped = Math.min(Math.max(index, 0), members.length - 1);
      const rest = members.filter((id) => id !== topicID);
      group.topicIds = [...rest.slice(0, clamped), topicID, ...rest.slice(clamped)];
      state.revision += 1;
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, applied: true, opId: `op-${state.revision}` };
    },
  };
  return { bindings, state };
}

// 真实消费面：topic 行的 drag props 来自 organization.topicRow（与
// ProjectTree.tsx renderNode 同一来源），组头 drop 走 dropTopicInto。
function Harness({ bindings, topics }: { bindings: ProjectTreeOrganizationBindings; topics: ProjectNode[] }) {
  const organization = useProjectTreeOrganization({
    tree: [{ ...folder, children: topics }],
    refresh: async () => {},
    organizationRevision: 0,
    bindings,
  });
  const folderWithTopics = { ...folder, children: topics };
  return <>
    <ProjectTreeGroupRows
      folder={folderWithTopics}
      children={topics}
      depth={1}
      section="pinned"
      visible
      organization={organization}
      renderNode={(node) => {
        const { props } = organization.topicRow(node, false);
        return <div key={node.key} data-testid={`topic-${node.topicId}`} {...props}>{node.topicId}</div>;
      }}
      t={t}
    />
  </>;
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

let activeRoot: Root | null = null;
async function mount(bindings: ProjectTreeOrganizationBindings, topics: ProjectNode[]) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(<StrictMode><Harness bindings={bindings} topics={topics} /></StrictMode>);
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  activeRoot = null;
}

// ── HTML5 拖拽事件模拟（jsdom 无原生 DragEvent/dataTransfer）────────────────
interface FakeDataTransfer {
  data: Record<string, string>;
  effectAllowed: string;
  dropEffect: string;
  setData(type: string, value: string): void;
  getData(type: string): string;
}

function fakeDataTransfer(entries: Record<string, string> = {}): FakeDataTransfer {
  return {
    data: { ...entries },
    effectAllowed: "none",
    dropEffect: "none",
    setData(type: string, value: string) { this.data[type] = value; },
    getData(type: string) { return this.data[type] ?? ""; },
  };
}

function dragEvent(type: string, target: Element, clientY: number, transfer: FakeDataTransfer) {
  // jsdom 的 getBoundingClientRect 恒 0：显式给出可判定上/下半区的盒子，
  // 否则 position 恒为 "after"，before 语义永远测不到。
  target.getBoundingClientRect = () => ({ top: 100, bottom: 120, left: 0, right: 100, width: 100, height: 20, x: 0, y: 100, toJSON: () => ({}) }) as DOMRect;
  const event = new window.MouseEvent(type, { bubbles: true, cancelable: true, clientY });
  Object.defineProperty(event, "dataTransfer", { value: transfer });
  return event;
}

async function dragDrop(draggedID: string, targetID: string, clientY: number) {
  const transfer = fakeDataTransfer({ "application/x-reasonix-topic-id": draggedID, "text/plain": draggedID });
  const source = document.querySelector(`[data-testid="topic-${draggedID}"]`)!;
  const target = document.querySelector(`[data-testid="topic-${targetID}"]`)!;
  await act(async () => { source.dispatchEvent(dragEvent("dragstart", source, 0, transfer)); await flush(); });
  await act(async () => { target.dispatchEvent(dragEvent("dragover", target, clientY, transfer)); await flush(); });
  await act(async () => { target.dispatchEvent(dragEvent("drop", target, clientY, transfer)); await flush(); });
}

const topics = [topicNode("t1"), topicNode("t2"), topicNode("t3"), topicNode("t4")];

// 组内成员渲染顺序（DOM 里 topic-* 出现顺序，不含组头）
function renderedMembers(): string[] {
  return [...document.querySelectorAll("[data-testid^='topic-']")].map((el) => el.getAttribute("data-testid")!.slice("topic-".length));
}

console.log("\n950 组内拖拽：同组 drop → ReorderTopicInGroup 落盘 + 乐观插位 + 重启保持");
{
  const { bindings, state } = makeBackend([
    { id: "g-a", title: "A", topicIds: ["t1", "t2", "t3"] },
  ]);
  await mount(bindings, topics);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 1);
  // 未入组行先渲染、组随后（ProjectTreeGroupRows 的既有布局）；组内按 topicIds。
  eq(renderedMembers(), ["t4", "t1", "t2", "t3"], "初始：组内按 topicIds 顺序渲染（t4 未入组在组前）");

  // t3 拖到 t1 下半区（after）→ 期望组内 t1, t3, t2。
  await dragDrop("t3", "t1", 115);
  await waitFor("reorder write", () => state.reorderCalls.length === 1);
  eq(state.reorderCalls[0], ["project", "/repo", "t3", "g-a", 1], "ReorderTopicInGroup 逐位参数（Go 签名序）");
  eq(state.groups[0]?.topicIds, ["t1", "t3", "t2"], "落盘 topicIds：t3 紧邻 t1 后");
  eq(renderedMembers(), ["t4", "t1", "t3", "t2"], "乐观插位：拖放后立即按新顺序渲染");

  // 写在途防复位（981 同族）已在 organization-races 覆盖直写守卫；这里钉
  // settle 后 re-pull 读回新顺序（= 刷新/重启保持）。
  await waitFor("settle re-pull", () => document.querySelectorAll(".project-tree__group").length === 1);
  eq(renderedMembers(), ["t4", "t1", "t3", "t2"], "写 settle 后 re-pull 不回刷旧顺序");
  await cleanup();

  // 「重启」语义：全新挂载（清空组件内存），后端 state 保留。
  await mount(bindings, topics);
  await waitFor("reload", () => document.querySelectorAll(".project-tree__group").length === 1);
  eq(renderedMembers(), ["t4", "t1", "t3", "t2"], "重新挂载（重启语义）读回持久化顺序");
  eq(state.reorderCalls.length, 1, "重挂载不再产生新的 reorder 写");
  await cleanup();
}

console.log("\n950 零回归①：跨组拖到组头仍是 moveTopicInRoster 追加（不调组内 reorder）");
{
  const { bindings, state } = makeBackend([
    { id: "g-a", title: "A", topicIds: ["t1", "t2"] },
    { id: "g-b", title: "B", topicIds: ["t3"] },
  ]);
  await mount(bindings, topics);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  state.reorderCalls.length = 0;
  state.casSaves.length = 0;

  // t1 拖到 g-b 组头（真实 dragstart → 组头 drop，任务 720 的 dropTopicInto 路径）。
  const transfer = fakeDataTransfer({ "application/x-reasonix-topic-id": "t1", "text/plain": "t1" });
  const source = document.querySelector("[data-testid='topic-t1']")!;
  const headerB = [...document.querySelectorAll(".project-tree__group-main")].find((el) => el.getAttribute("title") === "B")!;
  await act(async () => { source.dispatchEvent(dragEvent("dragstart", source, 0, transfer)); await flush(); });
  await act(async () => { headerB.dispatchEvent(dragEvent("drop", headerB, 110, transfer)); await flush(); });
  await waitFor("CAS save", () => state.casSaves.length > 0);
  eq(state.reorderCalls.length, 0, "跨组拖放不调 ReorderTopicInGroup");
  const gA = state.groups.find((g) => g.id === "g-a");
  const gB = state.groups.find((g) => g.id === "g-b");
  eq(gA?.topicIds, ["t2"], "源组移除该成员");
  eq(gB?.topicIds, ["t3", "t1"], "目标组追加尾部（append 语义不变）");
  await cleanup();
}

console.log("\n950 零回归②：顶层 topic 拖排仍走 ReorderTopics（不调组内 reorder）");
{
  const { bindings, state } = makeBackend([
    { id: "g-a", title: "A", topicIds: ["t1"] },
  ]);
  let reorderTopicsCalls = 0;
  const wrapped: ProjectTreeOrganizationBindings = {
    ...bindings,
    ReorderTopics: async () => { reorderTopicsCalls += 1; },
  };
  await mount(wrapped, topics);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 1);
  state.reorderCalls.length = 0;

  // t4（未入组）拖到 t2 前半区（before）→ 顶层链 ReorderTopics。
  await dragDrop("t4", "t2", 90);
  await waitFor("top-level reorder", () => reorderTopicsCalls === 1);
  eq(state.reorderCalls.length, 0, "顶层拖排不调 ReorderTopicInGroup");
  await cleanup();
}

await act(async () => activeRoot?.unmount());
dom.window.close();

process.stdout.write(`\nproject-tree-group-topic-drag: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
