// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1267-quadrisector-drop-hints.test.tsx
//
// tl 1267 验收钉（会话树拖拽放置提示标准化：1260 返修——用户否决 zone 盲类，
// 给出业界三源一致的精确预期：VS Code ListViewTargetSector 四分位 +
// React Aria between/default 双 variant）:
//   ① 四分位模型：分组行 = 上 25% 插其上方 / 中 50% 放入内部 / 下 25% 插其
//      下方；展开分组例外（Atlassian 规则）：展开项无 after——其下 25% 归
//      into（插入线会读成其首个子项之前）；折叠分组保留下 25% after。
//      会话行（叶子）= 上半插上 / 下半插下（topicRow 对半判定，原样）。
//   ② 双视觉标识：插入 = 2px accent 细线（--drop-before 顶沿 / --drop-after
//      底沿，行框内 inset 伪元素，--project-tree-drop-inset 内联变量从目标
//      层级缩进起笔）；放入内部 = 整行容器高亮（nest-target：accent 16% 底光
//      + 2px accent 环，1260 契约保留）。两棵机器一个语义：pointer 机的 into
//      带与 HTML5 拖会话悬停分组头（drop 恒 into）画同一容器高亮。
//   ③ 防呆：拖分组到自己或子孙上 = 阻断（--drop-blocked：not-allowed 光标 +
//      灰罩；hoverGroup 把指针目标置回原位，release 不触发放置，零写入）；
//      拖到原位 = no-op 不发请求（from === to 拦截 + blocked 视觉）。
//   ④ 落盘链零改动：release 仍走 organization.dropGroup → placeGroup 纯函数
//      （cycle-safe），CAS 落盘照常；提示层不干扰提交链。

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

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(here, "../styles.css"), "utf8");
const org = readFileSync(resolve(here, "../components/ProjectTreeOrganization.tsx"), "utf8");
const treeTsx = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

function ruleBody(selector: string): string {
  const index = css.indexOf(`${selector} {`);
  if (index < 0) return "";
  const open = css.indexOf("{", index);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

// 「最后一次出现」版：合并选择器列表的第二个选择器行（`xxx,\nyyy {`）会先被
// ruleBody 命中（返回共享声明体）；单独定位块（top/bottom）写在文件更后，
// 用 lastIndexOf 才能取到。
function ruleBodyLast(selector: string): string {
  const index = css.lastIndexOf(`${selector} {`);
  if (index < 0) return "";
  const open = css.indexOf("{", index);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

// tl 1267 的 drop 线规则用合并选择器列表（before,after 共享一套声明）——
// ruleBody 只认「单选择器 + {」。ruleBodyAny 找到 selector 第一次作为
// 选择器（而非声明值）出现的位置，返回其所属规则的声明体。
function ruleBodyAny(selector: string): string {
  let from = 0;
  for (;;) {
    const at = css.indexOf(selector, from);
    if (at < 0) return "";
    const open = css.indexOf("{", at);
    const close = css.indexOf("}", open);
    const prevClose = css.lastIndexOf("}", at);
    if (open >= 0 && close > open && (prevClose < 0 || prevClose < at)) {
      return css.slice(open + 1, close);
    }
    from = at + selector.length;
  }
}

console.log("\ntl1267 ① CSS: the two-treatment paradigm (insertion line vs container)");

// ── ① 分组头插入线（before/after 分离，2px accent，缩进起笔，行框内） ─────────
{
  const beforeRule = ruleBodyAny(".project-tree__group-main.project-tree__group-main--drop-before::before");
  const afterRule = ruleBodyAny(".project-tree__group-main.project-tree__group-main--drop-after::before");
  ok(
    beforeRule.includes("height: 2px;") && beforeRule.includes("background: var(--accent);"),
    "the group before-hint is a 2px accent insertion line (not a ring, not currentColor)",
  );
  ok(
    afterRule.includes("height: 2px;") && afterRule.includes("background: var(--accent);"),
    "the group after-hint is the same accent line on its own pseudo-element",
  );
  ok(
    beforeRule.includes("left: var(--project-tree-drop-inset, 8px);") && afterRule.includes("left: var(--project-tree-drop-inset, 8px);"),
    "both lines start at the target row's hierarchy indent (--project-tree-drop-inset)",
  );
  ok(
    ruleBodyLast(".project-tree__group-main.project-tree__group-main--drop-before::before").includes("top: 0;")
      && ruleBodyLast(".project-tree__group-main.project-tree__group-main--drop-after::before").includes("bottom: 0;"),
    "before draws at the top edge, after at the bottom edge (separable mid-drag)",
  );
  // tl 1225 教训：线必须画在行框内——负偏移（外置细线）会被相邻行的
  // contain:layout stacking context 盖住、被滚动容器在首末行裁剪。
  ok(
    !beforeRule.includes("top: -") && !beforeRule.includes("bottom: -")
      && !afterRule.includes("top: -") && !afterRule.includes("bottom: -"),
    "no outset (negative-offset) hairline is back in the group domain (regression = red)",
  );
  ok(
    ruleBody(".project-tree__group-main.project-tree__group-main--drop-before,").length > 0
      || css.includes(".project-tree__group-main.project-tree__group-main--drop-before,\n.project-tree__group-main.project-tree__group-main--drop-after {"),
    "the drop line rules are doubled-class (0,2,0) — the hover-beat load-bearing form",
  );
  ok(
    !css.includes("\n.project-tree__group-main--drop-before {") && !css.includes("\n.project-tree__group-main--drop-after {")
      && !css.includes("\n.project-tree__group-main--drop-blocked {"),
    "no plain (0,1,0) target selectors exist — the doubled form is the only definition",
  );
  // 双标识的另一半：放入内部 = 整行容器高亮（1260 契约原样保留）。
  const nest = ruleBody(".project-tree__group-main.project-tree__group-main--nest-target");
  ok(
    nest.includes("background: color-mix(in srgb, var(--accent) 16%, transparent);")
      && nest.includes("box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--accent) 55%, transparent);"),
    "the into treatment stays the container highlight (accent 16% wash + 2px ring)",
  );
  ok(
    !css.includes(".project-tree__group-main--reorder-target"),
    "the zone-blind --reorder-target class is retired everywhere (TSX and CSS)",
  );
}

// ── ② blocked 防呆视觉 + 会话行插入线 ────────────────────────────────────────
{
  const blocked = ruleBody(".project-tree__group-main.project-tree__group-main--drop-blocked");
  ok(
    blocked.includes("cursor: not-allowed;"),
    "the blocked hover paints the not-allowed cursor",
  );
  ok(
    blocked.includes("background: color-mix(in srgb, var(--fg) 5%, transparent);"),
    "the blocked hover carries a flat gray wash (distinct from both accents)",
  );
  const topicBefore = ruleBodyAny(".project-tree__topic.project-tree__topic--drop-before::after");
  const topicAfter = ruleBodyAny(".project-tree__topic.project-tree__topic--drop-after::after");
  ok(
    topicBefore.includes("height: 2px;") && topicBefore.includes("background: var(--accent);")
      && ruleBodyLast(".project-tree__topic.project-tree__topic--drop-before::after").includes("top: 0;"),
    "the topic-row before-hint is a 2px accent line at the top edge (accent, not currentColor)",
  );
  ok(
    topicAfter.includes("height: 2px;")
      && ruleBodyLast(".project-tree__topic.project-tree__topic--drop-after::after").includes("bottom: 0;"),
    "the topic-row after-hint is the accent line at the bottom edge",
  );
  ok(
    topicBefore.includes("left: var(--project-tree-drop-inset, 8px);")
      && topicAfter.includes("left: var(--project-tree-drop-inset, 8px);"),
    "topic lines start at the row's hierarchy indent too",
  );
  ok(
    !ruleBody(".project-tree__topic--drop-before").includes("currentColor")
      && !ruleBody(".project-tree__topic--drop-after").includes("currentColor"),
    "the old currentColor inset bars are gone from the topic domain",
  );
  ok(
    !topicBefore.includes("top: -") && !topicAfter.includes("bottom: -"),
    "no outset hairline in the topic domain either (regression = red)",
  );
  // 伪元素车道分离：topic ::before 是 --active 竖条（主题层 (0,4,0) 会压过
  // 同伪元素的 drop 声明），drop 线必须走 ::after。
  ok(
    ruleBody(".project-tree__topic--active::before").includes("width: 4px;")
      && topicBefore.length > 0
      && css.indexOf(".project-tree__topic--drop-before::after") > css.indexOf(".project-tree__topic--active::before"),
    "topic drop lines ride ::after — the ::before lane stays the active marker's",
  );
}

console.log("\ntl1267 ③ TSX: quadrisector zone machine, block guard, indent wiring");

// ── ③ 源码链钉（新契约） ─────────────────────────────────────────────────────
{
  ok(
    org.includes('zone = rel < 1 / 4 ? "before" : rel >= 3 / 4 && organization.groupCollapsed(key, id) ? "after" : "into";'),
    "the hover zone is the quadrisector (25/50/25) with the collapsed-gate on the after band",
  );
  ok(
    org.includes("descendantGroupIDs(groups, draggingGroup).includes(id)") && org.includes('zone: "blocked"'),
    "a hover over self/subtree is recorded as blocked (not-allowed, never committed)",
  );
  ok(
    org.includes("groupDropHintClass(group.id)")
      && org.includes('" project-tree__group-main--drop-blocked"')
      && org.includes('" project-tree__group-main--drop-before"')
      && org.includes('" project-tree__group-main--drop-after"')
      && org.includes('" project-tree__group-main--nest-target"'),
    "the className assembly maps the four hint states (before/after/nest/blocked)",
  );
  ok(
    org.includes("organization.dropGroup(key, from, to, zone)"),
    "the release commit still runs the dropGroup → placeGroup chain untouched",
  );
  ok(
    org.includes("--project-tree-drop-inset") && org.includes("${8 + (depth - 1) * 16 + level * 16}px"),
    "the group header feeds its own indent into the drop-inset variable",
  );
  ok(
    treeTsx.includes('"--project-tree-drop-inset"') && treeTsx.includes("${14 + depth * 16}px"),
    "the topic row feeds its indent (14 + depth*16, the topic-main pad) into the variable",
  );
  ok(
    org.includes("setTopicHoverGroup") && org.includes("event.currentTarget.contains(event.relatedTarget as Node | null)"),
    "the HTML5 session-hover paints the container hint (relatedTarget-guarded leave)",
  );
  ok(
    org.includes('window.addEventListener("dragend", clear);')
      && org.includes('window.addEventListener("drop", clear);')
      && org.includes('window.addEventListener("blur", clear);'),
    "the HTML5 hover hint clears on the same dragend/drop/blur points as the hook's own drag",
  );
}

console.log("\ntl1267 ④ the pointer machine paints the quadrisector hints (jsdom)");

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
const groupX: SessionGroup = { id: "g-x", title: "X", topicIds: [] };
const groupY: SessionGroup = { id: "g-y", title: "Y", topicIds: [], parent: "g-x" };

const COLLAPSED_KEY = "projectTree:sessionGroupCollapsed";

function GroupHarness({ bindings, revision, renderNode, childrenNodes }: {
  bindings: ProjectTreeOrganizationBindings;
  revision: number;
  renderNode?: Parameters<typeof ProjectTreeGroupRows>[0]["renderNode"];
  childrenNodes?: ProjectNode[];
}) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: revision,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={childrenNodes ?? []}
    depth={1}
    section="pinned"
    visible
    organization={organization}
    renderNode={renderNode ?? (() => null)}
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

async function pointerOn(el: Element, type: string, clientY?: number) {
  const event = new window.MouseEvent(type, { bubbles: true, button: 0, clientY });
  Object.defineProperty(event, "pointerType", { value: "mouse" });
  await act(async () => { el.dispatchEvent(event); await flush(); });
}

async function releasePointer(opts: { alt?: boolean } = {}) {
  await act(async () => {
    window.dispatchEvent(new window.MouseEvent("pointerup", opts.alt ? { altKey: true } : {}));
    await flush();
  });
}

// 四分位量尺：header 盒子 top=100 height=40 → 上 25% = 100..110（105），
// 中 50% = 110..130（120），下 25% = 130..140（135）。jsdom 的真实 rect 全 0，
// hoverGroup 对无盒子兜底 "after"——这里钉的是四分位本身，必须给可测的盒子。
function measureHeader(header: HTMLElement) {
  header.getBoundingClientRect = () => ({
    top: 100, bottom: 140, left: 0, right: 200, width: 200, height: 40, x: 0, y: 100,
    toJSON: () => ({}),
  } as DOMRect);
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

let activeRoot: Root | null = null;

async function mount(bindings: ProjectTreeOrganizationBindings, revision: number, opts: { renderNode?: Parameters<typeof ProjectTreeGroupRows>[0]["renderNode"]; childrenNodes?: ProjectNode[] } = {}) {
  activeRoot = createRoot(document.getElementById("root")!);
  await act(async () => {
    activeRoot!.render(<StrictMode><GroupHarness bindings={bindings} revision={revision} renderNode={opts.renderNode} childrenNodes={opts.childrenNodes} /></StrictMode>);
    await flush();
  });
}

async function unmount() {
  if (!activeRoot) return;
  const root = activeRoot;
  activeRoot = null;
  await act(async () => root.unmount());
}

// ── ④a 四分位：展开分组的三个带 ──────────────────────────────────────────────
{
  window.localStorage.clear();
  const state = { groups: [groupA, groupB], revision: 5 };
  const saves: SessionGroup[][] = [];
  await mount(casBindings(state, saves), state.revision);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  measureHeader(headerOf(1));

  ok(
    !headerOf(1).className.includes("--drop-before") && !headerOf(1).className.includes("--nest-target") && !headerOf(1).className.includes("--drop-blocked"),
    "no hint paints while nothing is being dragged",
  );

  // 按住 A → 悬停展开的 B 上 25%（before → 插入其上方）。
  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(1), "pointermove", 105);
  ok(
    headerOf(1).className.includes("project-tree__group-main--drop-before"),
    "the top quarter paints the before insertion line on the target header",
  );
  ok(
    !headerOf(1).className.includes("--nest-target") && !headerOf(1).className.includes("--drop-blocked"),
    "the top quarter carries neither the container nor the blocked hint",
  );
  ok(
    headerOf(0).className.includes("project-tree__group-main--dragging"),
    "the source header dims while the drag is in motion",
  );

  // 中 50%（into → 容器高亮）。
  await pointerOn(headerOf(1), "pointermove", 120);
  ok(
    headerOf(1).className.includes("project-tree__group-main--nest-target")
      && !headerOf(1).className.includes("--drop-before"),
    "the middle half swaps to the container highlight (into)",
  );

  // 下 25%，B 展开 → 展开项无 after（Atlassian 规则）→ 仍是容器高亮。
  await pointerOn(headerOf(1), "pointermove", 135);
  ok(
    headerOf(1).className.includes("project-tree__group-main--nest-target")
      && !headerOf(1).className.includes("--drop-after"),
    "the bottom quarter of an EXPANDED group stays the container hint (no after band)",
  );

  // 释放（into）：提示清零，落盘照常。
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the release still lands the into-reorder through the CAS chain");
  ok(
    !headerOf(0).className.includes("--dragging") && !headerOf(1).className.includes("--nest-target"),
    "all drag hints clear on release",
  );
  await unmount();
}

// ── ④b 折叠分组例外：下 25% 恢复 after 插入线 ────────────────────────────────
{
  window.localStorage.clear();
  window.localStorage.setItem(COLLAPSED_KEY, JSON.stringify(["project|/repo|g-b"]));
  const state = { groups: [groupA, groupB], revision: 5 };
  const saves: SessionGroup[][] = [];
  await mount(casBindings(state, saves), state.revision);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  ok(
    headerOf(1).closest(".project-tree__group")?.className.includes("--collapsed") === true,
    "the collapsed fixture is actually collapsed (persisted set drives the fold)",
  );
  measureHeader(headerOf(1));

  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(1), "pointermove", 135);
  ok(
    headerOf(1).className.includes("project-tree__group-main--drop-after"),
    "the bottom quarter of a COLLAPSED group paints the after insertion line",
  );
  ok(
    !headerOf(1).className.includes("--nest-target"),
    "the collapsed after-band carries no container hint",
  );
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the collapsed after-band still lands the same-level reorder");
  await unmount();
}

// ── ④c 防呆：拖到自己或子孙上 = blocked，零写入 ─────────────────────────────
{
  window.localStorage.clear();
  const state = { groups: [groupX, groupY], revision: 5 };
  const saves: SessionGroup[][] = [];
  await mount(casBindings(state, saves), state.revision);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  measureHeader(headerOf(1));

  // 拖 X → 悬停自己的子组 Y（任意带）→ blocked。
  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(1), "pointermove", 120);
  ok(
    headerOf(1).className.includes("project-tree__group-main--drop-blocked"),
    "hovering one's own descendant paints the blocked hint",
  );
  ok(
    !headerOf(1).className.includes("--nest-target") && !headerOf(1).className.includes("--drop-before") && !headerOf(1).className.includes("--drop-after"),
    "the blocked target paints no placement hint (the feedback must not lie)",
  );
  await releasePointer();
  eq(saves.length, 0, "a blocked release commits nothing (zero CAS writes)");

  // 拖 X → 悬停 X 自己（原位）→ blocked + 零写入。
  measureHeader(headerOf(0));
  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(0), "pointermove", 120);
  ok(
    headerOf(0).className.includes("project-tree__group-main--drop-blocked"),
    "hovering the source row itself paints the blocked hint too",
  );
  await releasePointer();
  eq(saves.length, 0, "an in-place release stays a no-op (no request)");

  // 对照：同一拖拽悬停合法目标（X → Y 之外没有别的组；改 roster 再验）。
  await unmount();
}
{
  window.localStorage.clear();
  const state = { groups: [groupA, groupB], revision: 5 };
  const saves: SessionGroup[][] = [];
  await mount(casBindings(state, saves), state.revision);
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  measureHeader(headerOf(1));
  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(1), "pointermove", 105);
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);
  eq(saves[0].map((group) => group.id), ["g-a", "g-b"], "a legal before-release still commits (blocked never fires on legal targets)");
  await unmount();
}

console.log("\ntl1267 ⑤ the HTML5 machine paints the container hint on a header (jsdom)");

// ── ⑤ 拖会话悬停分组头：drop 恒 into → 容器高亮；drop 后落盘照常 ─────────────
{
  window.localStorage.clear();
  const state = { groups: [groupA, { ...groupB, topicIds: [] }], revision: 5 };
  const saves: SessionGroup[][] = [];
  const topicNode: ProjectNode = {
    key: "project-/repo|t1",
    kind: "project",
    label: "T1",
    root: "/repo",
    topicId: "t1",
    children: [],
  };
  const renderNode = (node: ProjectNode) => {
    const drag = organizationRef!.topicRow(node, false);
    return <div className={`project-tree__topic${drag.className}`} {...drag.props}>{node.topicId}</div>;
  };
  let organizationRef: ReturnType<typeof useProjectTreeOrganization> | null = null;
  const ProbeHarness = ({ bindings, revision }: { bindings: ProjectTreeOrganizationBindings; revision: number }) => {
    const organization = useProjectTreeOrganization({
      tree: [folder],
      refresh: async () => {},
      organizationRevision: revision,
      bindings,
    });
    organizationRef = organization;
    return <ProjectTreeGroupRows
      folder={folder}
      children={[topicNode]}
      depth={1}
      section="pinned"
      visible
      organization={organization}
      renderNode={renderNode}
      t={t}
    />;
  };
  activeRoot = createRoot(document.getElementById("root")!);
  await act(async () => {
    activeRoot!.render(<StrictMode><ProbeHarness bindings={casBindings(state, saves)} revision={state.revision} /></StrictMode>);
    await flush();
  });
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  const topicRow = document.querySelector(".project-tree__topic");
  ok(topicRow instanceof HTMLElement, "the topic fixture row renders (the HTML5 drag source)");

  function dragEvent(type: string): Event {
    const event = new window.MouseEvent(type, { bubbles: true, cancelable: true });
    Object.defineProperty(event, "dataTransfer", {
      value: {
        setData: () => {},
        getData: () => "",
        effectAllowed: "",
        dropEffect: "",
      },
    });
    return event;
  }

  const headerB = headerOf(1);
  await act(async () => { (topicRow as HTMLElement).dispatchEvent(dragEvent("dragstart")); await flush(); });
  await act(async () => { headerB.dispatchEvent(dragEvent("dragover")); await flush(); });
  ok(
    headerB.className.includes("project-tree__group-main--nest-target"),
    "an HTML5 session drag hovering a group header paints the container hint (drop is always into)",
  );
  ok(
    !headerB.className.includes("--drop-before") && !headerB.className.includes("--drop-after"),
    "the HTML5 hover never paints insertion lines (its drop has no between meaning)",
  );

  // 离开（relatedTarget 为 null = 真离开）→ 高亮清除。
  await act(async () => { headerB.dispatchEvent(dragEvent("dragleave")); await flush(); });
  ok(
    !headerB.className.includes("--nest-target"),
    "the container hint clears on a real dragleave",
  );

  // 再悬停 + 释放：dropTopicInto 落盘（t1 加入 g-b），提示清零。
  await act(async () => { headerB.dispatchEvent(dragEvent("dragover")); await flush(); });
  await act(async () => { headerB.dispatchEvent(dragEvent("drop")); await flush(); });
  await waitFor("CAS save", () => saves.length === 1);
  eq(saves[0].find((group) => group.id === "g-b")?.topicIds, ["t1"], "the drop still files the session into the group through the CAS chain");
  ok(
    !headerB.className.includes("--nest-target"),
    "the hover hint clears after the drop",
  );
  await act(async () => { window.dispatchEvent(dragEvent("dragend")); await flush(); });
  await unmount();
}

console.log(`\ntl1267: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
dom.window.close();
