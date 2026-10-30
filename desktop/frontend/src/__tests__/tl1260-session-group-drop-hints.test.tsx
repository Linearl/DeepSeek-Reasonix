// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1260-session-group-drop-hints.test.tsx
//
// tl 1260 验收钉（会话分组拖拽 drop 提示补齐：1225 漏面——项目分组有提示，
// Global 区会话分组（1.常用/2.不常用 类）拖拽无虚影无放置指示）:
//   ① 视觉统一：会话分组头加入 tl1225 accent 范式；源行拖拽变暗权重对齐
//      项目侧 0.55。
//   ② 特异性是载荷：双写类选择器 (0,2,0) 压过同文件靠前的
//      .project-tree__group-main:hover (0,2,0)——pointer 拖拽期间指针必然悬停
//      在目标头上，单类 (0,1,0) 的旧背景被 8% hover 底色静默替换（=「看着没
//      提示」的机制之一）；tl1225b 双写先例同款手法。
//   ③ 状态链与落盘语义：className 组装点、hover 带判定、dropGroup →
//      placeGroup 纯函数链（提示层不干扰提交链）。
//   ④ 机器绘类（jsdom）：拖拽中目标头按 hover 带正确绘制提示，释放后清零
//      且落盘照常。
//
// tl 1267 返修演进（用户否决 1260 的 zone 盲类现状后标准化）：三分带升级为
// 业界四分位（VS Code ListViewTargetSector，25/50/25，展开分组例外——展开项
// 无 after，下带归 into）；before/after 从 zone 盲的 reorder-target 环洗拆为
// 各自的 2px accent 插入线（--drop-before/--drop-after）；新增拖到自己/子孙
// 的 blocked 防呆。本文件随新契约演进：插入线的完整契约钉在
// tl1267-quadrisector-drop-hints.test.tsx，这里保留 hover-beat 双写、nest
// 容器高亮、源行变暗、灰退场与落盘链的哨兵。

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

function ruleBody(selector: string): string {
  const index = css.indexOf(`${selector} {`);
  if (index < 0) return "";
  const open = css.indexOf("{", index);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

const REORDER_RING = "box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--accent) 55%, transparent);";
const NEST_RING = REORDER_RING;

console.log("\ntl1260 ① session-group hints join the tl1225 accent paradigm (CSS contract)");

// ── ① CSS 契约：双写选择器 + nest 容器高亮 + 插入线双标识（1267 演进） ────────
{
  // tl 1267: the zone-blind reorder ring/wash retired — before/after are now
  // the separate 2px accent insertion lines (contract pinned in tl1267).
  const nest = ruleBody(".project-tree__group-main.project-tree__group-main--nest-target");
  ok(
    !css.includes("project-tree__group-main--reorder-target"),
    "the zone-blind reorder-target class stays retired (TL1267 two-treatment paradigm)",
  );
  ok(
    nest.includes(NEST_RING) && nest.includes("background: color-mix(in srgb, var(--accent) 16%, transparent);"),
    "the into hint is the accent twin of the project side's drop-into (16% wash + 2px ring)",
  );
  ok(
    ruleBody(".project-tree__pgroup-main--drop-into").includes("background: color-mix(in srgb, var(--accent) 16%, transparent);")
      && ruleBody(".project-tree__pgroup-main--drop-into").includes(REORDER_RING),
    "the project-side drop-into reference treatment is intact (same colour family both trees)",
  );
  ok(
    ruleBody(".project-tree__group-main--dragging").includes("opacity: 0.55;"),
    "the source dim follows the project side's dragging weight (0.55)",
  );
  // ② 特异性载荷：单类形式必须已被双写形式替换（否则 hover 底色继续吞背景）。
  ok(
    !css.includes("\n.project-tree__group-main--nest-target {")
      && !css.includes("\n.project-tree__group-main--drop-before {")
      && !css.includes("\n.project-tree__group-main--drop-after {")
      && !css.includes("\n.project-tree__group-main--drop-blocked {"),
    "the plain (0,1,0) target selectors are gone — the hover-beat doubled form is the only definition",
  );
}

// ── ③ 本域外置细线清零（回归即红） ────────────────────────────────────────────
{
  const retired = [
    ".project-tree__group-main::before",
    ".project-tree__group-main::after",
    ".project-tree__group-main--reorder-target::before",
    ".project-tree__group-main--reorder-target::after",
    ".project-tree__group-main--nest-target::before",
    ".project-tree__group-main--nest-target::after",
  ];
  ok(
    retired.every((selector) => !css.includes(selector)),
    "no outset hairline pseudo-element selectors in the session-group domain (0 occurrences, regression = red)",
  );
  // 灰色退场钉收窄在本域：group-main 的任何规则体不再出现中性灰 overlay
  // （styles.css 其他域的 rgba(127,...) 是合法既有用色，不在此钉范围内）。
  const grayGroupRules = css
    .split("}")
    .filter((chunk) => chunk.includes("project-tree__group-main") && chunk.includes("rgba(127, 127, 127"));
  eq(grayGroupRules.length, 0, "no neutral-gray hint overlays remain on any group-main rule (accent paradigm only)");
}

console.log("\ntl1260 ④ state chain and write semantics untouched (hint layer = CSS only)");

// ── ④ 类名组装点 / hover 四分位 / 落盘链（1267 新契约） ──────────────────────
{
  ok(
    org.includes("groupDropHintClass(group.id)"),
    "the className assembly routes the hovered header through the four-state hint helper",
  );
  ok(
    org.includes('zone = rel < 1 / 4 ? "before" : rel >= 3 / 4 && organization.groupCollapsed(key, id) ? "after" : "into";'),
    "the hover band is the quadrisector with the collapsed gate on the after band (tl1267)",
  );
  ok(
    org.includes("organization.dropGroup(key, from, to, zone)"),
    "the release commit still runs the dropGroup chain with the hovered zone",
  );
  ok(
    org.includes("import { canNestUnder, childGroups, descendantGroupIDs, inheritedMemberIDs, nestGroup as nestInRoster, nestTargetRows, placeGroup, topLevelGroups, unnestGroup as unnestInRoster, type GroupDropZone } from \"../lib/sessionGroupTree\";"),
    "the pure placeGroup/nestGroup helpers are wired (落盘语义零改动; descendantGroupIDs feeds the tl1267 block guard)",
  );
  ok(
    org.includes('zone: "blocked"') && org.includes("descendantGroupIDs(groups, draggingGroup).includes(id)"),
    "the self/subtree hover is blocked before it can ever reach the commit chain (tl1267 防呆)",
  );
}

// ── ⑤ 机器绘类（jsdom）：拖拽中目标头按三分带绘制两类提示 ─────────────────────
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

async function releasePointer() {
  await act(async () => { window.dispatchEvent(new window.MouseEvent("pointerup")); await flush(); });
}

// hover 三分带的量尺：header 盒子 top=100 height=30（jsdom 的真实 rect 全 0，
// hoverGroup 对无盒子兜底 "after"——这里钉的是三分带本身，必须给可测的盒子）。
function measureHeader(header: HTMLElement) {
  header.getBoundingClientRect = () => ({
    top: 100, bottom: 130, left: 0, right: 200, width: 200, height: 30, x: 0, y: 100,
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

console.log("\ntl1260 ⑤ the pointer machine paints the target hints (jsdom)");

{
  const state = { groups: [groupA, groupB], revision: 5 };
  const saves: SessionGroup[][] = [];
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><GroupHarness bindings={casBindings(state, saves)} revision={state.revision} /></StrictMode>);
    await flush();
  });
  await waitFor("initial load", () => document.querySelectorAll(".project-tree__group").length === 2);
  measureHeader(headerOf(1));

  ok(
    !headerOf(1).className.includes("--drop-before") && !headerOf(1).className.includes("--nest-target")
      && !headerOf(1).className.includes("--drop-blocked"),
    "no target hint paints while nothing is being dragged",
  );

  // 按住 A（鼠标路径立即入机）→ 悬停 B 上 25%（before → 插入其上方的插入线）。
  await pointerOn(titleSpanOf(0), "pointerdown");
  await pointerOn(headerOf(1), "pointermove", 105);
  ok(
    headerOf(1).className.includes("project-tree__group-main--drop-before"),
    "hovering the top quarter paints the before insertion line on the target header",
  );
  ok(
    !headerOf(1).className.includes("project-tree__group-main--nest-target"),
    "the into hint stays off in the sibling band (two meanings stay distinguishable)",
  );
  ok(
    headerOf(0).className.includes("project-tree__group-main--dragging"),
    "the source header dims while the drag is in motion",
  );

  // 中 50%（into → 落入子树的容器高亮）。
  await pointerOn(headerOf(1), "pointermove", 115);
  ok(
    headerOf(1).className.includes("project-tree__group-main--nest-target")
      && !headerOf(1).className.includes("project-tree__group-main--drop-before"),
    "hovering the middle half swaps the hint to the container treatment",
  );

  // 下 25%，B 展开 → 展开项无 after（Atlassian 规则，tl1267）→ 容器高亮保持。
  await pointerOn(headerOf(1), "pointermove", 125);
  ok(
    headerOf(1).className.includes("project-tree__group-main--nest-target")
      && !headerOf(1).className.includes("--drop-after"),
    "hovering the bottom quarter of an EXPANDED group keeps the container hint (no after band)",
  );

  // 释放：提示清零，落盘照常（提示层不干扰提交链）。
  await releasePointer();
  await waitFor("CAS save", () => saves.length === 1);
  eq(saves[0].map((group) => group.id), ["g-b", "g-a"], "the release still lands the reorder through the CAS chain");
  ok(
    !headerOf(0).className.includes("--dragging") && !headerOf(1).className.includes("--drop-before")
      && !headerOf(1).className.includes("--nest-target"),
    "all drag hints clear on release",
  );
  await act(async () => root.unmount());
}

console.log(`\ntl1260: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
dom.window.close();
