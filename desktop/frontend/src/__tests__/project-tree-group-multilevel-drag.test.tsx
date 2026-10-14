// Run: tsx src/__tests__/project-tree-group-multilevel-drag.test.tsx
//
// 892-B 验收③：拖拽多层级语义（交互设计拍板并钉死）——
//   拖到组行「上三分之一/下三分之一」= 与目标同级排序（before/after，
//   拖拽组换到目标的 parent 层级）；
//   拖到组行「中间」= 入组尾（成为目标最后一个子组）；
//   拖进自己的子树 = 环拒绝（placeGroup 原引用返回，零写入）；
//   Alt+drop 保留 task-350 强制入组语义；
//   无布局环境（jsdom 零 rect）回退 after —— task-50 扁平重排契约不变。

import { JSDOM } from "jsdom";
import type { Root } from "react-dom/client";
import type { Translator } from "../lib/i18n";
import { placeGroup } from "../lib/sessionGroupTree";
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

// ── 纯函数语义（placeGroup） ─────────────────────────────────────────────────
console.log("\n892-B③ placeGroup 纯语义");
{
  const roster: SessionGroup[] = [
    { id: "a", title: "A", topicIds: ["ta"] },
    { id: "b", title: "B", topicIds: ["tb"] },
    { id: "c", title: "C", topicIds: [] },
  ];
  // into：成为目标最后一个子组（数组位置进目标子树尾部）。
  const into = placeGroup(roster, "a", "b", "into");
  eq(into.map((g) => g.id), ["b", "a", "c"], "into：拖拽组插到目标子树尾部");
  eq(into.find((g) => g.id === "a")?.parent, "b", "into：parent 指向目标");
  eq(into.find((g) => g.id === "a")?.topicIds, ["ta"], "into：GUARD 成员逐位不变");
  // before/after：与目标同级（parent := 目标的 parent）。
  const after = placeGroup(roster, "a", "b", "after");
  eq(after.map((g) => g.id), ["b", "a", "c"], "after：落到目标之后");
  eq(after.find((g) => g.id === "a")?.parent, undefined, "after：顶层目标 → 拖拽组仍在顶层");
  const before = placeGroup(roster, "c", "a", "before");
  eq(before.map((g) => g.id), ["c", "a", "b"], "before：落到目标之前");
}

{
  // 跨层换级：深层组拖到顶层组旁 → 换到顶层（parent 跟随目标层级）。
  const nested: SessionGroup[] = [
    { id: "root", title: "Root" },
    { id: "p", title: "P", parent: "root" },
    { id: "deep", title: "Deep", parent: "p", topicIds: ["t"] },
    { id: "other", title: "Other" },
  ];
  const res = placeGroup(nested, "deep", "other", "after");
  eq(res.find((g) => g.id === "deep")?.parent, undefined, "跨层拖拽：深层组换到目标的顶层");
  eq(res.find((g) => g.id === "deep")?.topicIds, ["t"], "GUARD 成员不变");
  const down = placeGroup(nested, "other", "p", "into");
  eq(down.find((g) => g.id === "other")?.parent, "p", "拖进组行中间：入组成为子组");
}

{
  // 环拒绝：目标在拖拽组自己的子树里 → 原引用返回（零写入信号）。
  const tree: SessionGroup[] = [
    { id: "x", title: "X" },
    { id: "y", title: "Y", parent: "x" },
    { id: "z", title: "Z", parent: "y" },
  ];
  ok(placeGroup(tree, "x", "z", "into") === tree, "into 自己的孙子组：no-op（原引用）");
  ok(placeGroup(tree, "x", "y", "before") === tree, "before 自己的儿子：no-op（会成环）");
  ok(placeGroup(tree, "x", "z", "after") === tree, "after 自己的孙子：no-op（会成环）");
  // 目标失踪（另一窗口已解散）：同样 no-op。
  ok(placeGroup(tree, "ghost", "y", "into") === tree, "拖拽组失踪：no-op");
  ok(placeGroup(tree, "x", "ghost", "into") === tree, "目标失踪：no-op");
  // 同组放下：no-op。
  ok(placeGroup(tree, "y", "y", "into") === tree, "放下到自身：no-op");
}

{
  // into 目标的既有子组之后（入组尾相对已有子组）。
  const withKids: SessionGroup[] = [
    { id: "p", title: "P" },
    { id: "k1", title: "K1", parent: "p" },
    { id: "k2", title: "K2", parent: "p" },
    { id: "mover", title: "M" },
  ];
  const res = placeGroup(withKids, "mover", "p", "into");
  eq(res.map((g) => g.id), ["p", "k1", "k2", "mover"], "into：排在目标既有子组之后（真正的入组尾）");
  eq(res.find((g) => g.id === "mover")?.parent, "p", "入组尾 parent 正确");
}

// ── 组件机器：zone 由指针带决定 ─────────────────────────────────────────────
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
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTreeGroupRows, useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");

const t = ((key: string) => key) as unknown as Translator;

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

interface Saved { payloads: SessionGroup[][] }

function casBindings(initial: SessionGroup[], saved: Saved): ProjectTreeOrganizationBindings {
  let groups = initial.map((g) => ({ ...g }));
  let revision = 1;
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      saved.payloads.push(next.map((g) => ({ ...g })));
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
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

function headers(): HTMLElement[] {
  return [...document.querySelectorAll(".project-tree__group-main")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

/** 给 header 钉一个可控的 90px 直角盒（jsdom 默认零 rect → 恒 after）。 */
function stubRect(el: HTMLElement, top = 0, height = 90) {
  el.getBoundingClientRect = () => ({ top, left: 0, right: 400, bottom: top + height, width: 400, height, x: 0, y: top, toJSON: () => ({}) });
}

async function dragWithBand(fromIndex: number, toIndex: number, clientY: number, opts: { alt?: boolean; noStub?: boolean } = {}) {
  const handle = document.querySelectorAll(".project-tree__group")[fromIndex]?.querySelector(".project-tree__group-drag") as HTMLElement;
  await act(async () => { handle.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true, button: 0 })); await flush(); });
  const target = headers()[toIndex]!;
  if (!opts.noStub) stubRect(target);
  await act(async () => {
    target.dispatchEvent(new window.MouseEvent("pointerover", { bubbles: true, clientY }));
    await flush();
  });
  await act(async () => {
    window.dispatchEvent(new window.MouseEvent("pointerup", opts.alt ? { altKey: true } : {}));
    await flush();
  });
}

console.log("\n892-B③ 组件 zone 机器");
{
  // 拖 A 的手柄 → hover B 的中带 → 入组。
  const saved: Saved = { payloads: [] };
  await mount(casBindings([
    { id: "g-a", title: "A", topicIds: ["ta"] },
    { id: "g-b", title: "B", topicIds: [] },
    { id: "g-c", title: "C", topicIds: [] },
  ], saved));
  await act(flush);
  await dragWithBand(0, 1, 45); // 中带 (0..90) → into
  eq(saved.payloads.length, 1, "中带放下提交一次 CAS 保存");
  eq(saved.payloads[0]?.map((g) => g.id), ["g-b", "g-a", "g-c"], "中带 = 入组尾");
  eq(saved.payloads[0]?.find((g) => g.id === "g-a")?.parent, "g-b", "入组 parent 指向目标");
  await act(async () => activeRoot!.unmount());
  activeRoot = null;
}
{
  // 拖 A → hover B 的下带 → 同级 after。
  const saved: Saved = { payloads: [] };
  await mount(casBindings([
    { id: "g-a", title: "A", topicIds: [] },
    { id: "g-b", title: "B", topicIds: [] },
    { id: "g-c", title: "C", topicIds: [] },
  ], saved));
  await act(flush);
  await dragWithBand(0, 1, 80); // 下带 → after
  eq(saved.payloads[0]?.map((g) => g.id), ["g-b", "g-a", "g-c"], "下带 = 与目标同级排序（after）");
  eq(saved.payloads[0]?.find((g) => g.id === "g-a")?.parent, undefined, "同级排序不改 parent");
  await act(async () => activeRoot!.unmount());
  activeRoot = null;
}
{
  // 环拒绝：X 有子 Y，拖 X 放进 Y 的中带 → placeGroup no-op → 零写入。
  const saved: Saved = { payloads: [] };
  await mount(casBindings([
    { id: "g-x", title: "X", topicIds: [] },
    { id: "g-y", title: "Y", topicIds: [], parent: "g-x" },
  ], saved));
  await act(flush);
  await dragWithBand(0, 1, 45); // X into 自己的儿子 Y
  eq(saved.payloads.length, 0, "环拒绝：拖进自己的子树零写入");
  await act(async () => activeRoot!.unmount());
  activeRoot = null;
}
{
  // Alt+drop 保留 task-350 强制入组（中带无关，alt 优先）。
  const saved: Saved = { payloads: [] };
  await mount(casBindings([
    { id: "g-a", title: "A", topicIds: [] },
    { id: "g-b", title: "B", topicIds: [] },
  ], saved));
  await act(flush);
  await dragWithBand(1, 0, 10, { alt: true }); // 上带但按住 Alt → 仍入组
  eq(saved.payloads[0]?.find((g) => g.id === "g-b")?.parent, "g-a", "Alt+drop 无视 zone 强制入组（task-350 契约）");
  await act(async () => activeRoot!.unmount());
  activeRoot = null;
}
{
  // 零 rect 回退：jsdom 不 stub 时 hover → after（task-50 扁平契约不变）。
  const saved: Saved = { payloads: [] };
  await mount(casBindings([
    { id: "g-a", title: "A", topicIds: [] },
    { id: "g-b", title: "B", topicIds: [] },
  ], saved));
  await act(flush);
  await dragWithBand(0, 1, 0, { noStub: true }); // 零 rect → after
  eq(saved.payloads[0]?.map((g) => g.id), ["g-b", "g-a"], "零布局回退 after（兼容 task-50 机器）");
  await act(async () => activeRoot!.unmount());
  activeRoot = null;
}

process.stdout.write(`\nproject-tree-group-multilevel-drag: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
