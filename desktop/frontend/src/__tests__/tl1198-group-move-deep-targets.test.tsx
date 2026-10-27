// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1198-group-move-deep-targets.test.tsx
//
// tl 1198 验收逐条钉（分组右键「分组移动」无法移到深层，用户 20261025 实拍）：
//   ① 右键任一分组，「分组移动 ▸」二级列出全部其他合法目标，含 depth≥2 的深层组
//     （根因：旧菜单 nestTargets(...).slice(0, 4) 只取 roster 前 4 个合法目标，
//     深层组全被切掉；数据层 nestGroup 本就支持任意深度）；
//   ② 自身与自身子树不出现（防环，与 canNestUnder/管理工具同款规则）；
//   ③ 目标行带层级前缀（「4.reasonix / reasonix-for-ai」式），同名浅深层可区分；
//     选中深层目标后 groupPath 正确嵌套（CAS payload 的 parent 链可验证）；
//   ④ 空组树（无任何合法目标）时「分组移动」菜单项保留但禁用，不再整项消失；
//   ⑤ 现有浅层移动零回归 → 由 tl1149/tl1149b/hierarchy 既有断言族回归钉，
//     本文件只钉源码形态（slice(0,4) 不复活、disabled 逻辑、前缀 join）。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import type { Translator } from "../lib/i18n";
import {
  groupDepth,
  hierarchyMutationIsDisplayOnly,
  nestGroup,
  nestTargetRows,
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

// ── 纯 helper：nestTargetRows ─────────────────────────────────────────────────
console.log("\ntl1198 A: nestTargetRows — 全量任意深度 + 防环 + 前缀");
{
  // 树形：a, b（顶层）；b 下 c（depth 2）；c 下 d（depth 3）；孤儿 e；b 的当前子树 c/d。
  const roster: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
    { id: "g-c", title: "C", parent: "g-b" },
    { id: "g-d", title: "D", parent: "g-c" },
    { id: "g-orphan", title: "Orphan", parent: "dissolved" },
  ];

  // ① 对 a：全部其他组都是合法目标（含 depth≥2 的 c、d），按树形顺序 DFS。
  eq(nestTargetRows(roster, "g-a").map((r) => r.group.id), ["g-b", "g-c", "g-d"],
    "① a 的目标行含深层组 c(depth2)/d(depth3)，树形顺序 DFS，孤儿被排除");
  eq(nestTargetRows(roster, "g-a").map((r) => r.ancestorTitles), [[], ["B"], ["B", "C"]],
    "③ 前缀按深度递进：b=[]、c(depth2)=["+"B]、d(depth3)=[B, C]（根→直接父）");

  // ② 对 b：自身与自身子树（c、d）不出现，孤儿不出现，只剩 a。
  eq(nestTargetRows(roster, "g-b").map((r) => r.group.id), ["g-a"],
    "② b 的目标行排除自身与自身子树（c/d 防环）");
  // ② 对 c（parent=b）：当前父 b 是 no-op 不出现；b 的父链上的 a 合法；自身子树 d 排除。
  eq(nestTargetRows(roster, "g-c").map((r) => r.group.id), ["g-a"],
    "② c 的目标行排除当前父 b（no-op）与自身子树 d");

  // 孤儿组（断链父）作为被移动者是合法的（写回会替换 parent、修复其链），
  // 但它不能作为父目标出现——这里对合法成员求行时，孤儿不在目标里（上面 ① 的
  // 期望值已验证）；孤儿自己被移动时，其余全部合法目标仍然可用。
  eq(nestTargetRows(roster, "g-orphan").map((r) => r.group.id), ["g-a", "g-b", "g-c", "g-d"],
    "② 孤儿组自身可被移动：全部健康目标（含深层 c/d）仍在");

  // 坏指针环（a↔b 互指）：树走不进环成员（无根路径），调用必须终止且不含环成员。
  const cyclic: SessionGroup[] = [
    { id: "x", title: "X", parent: "y" },
    { id: "y", title: "Y", parent: "x" },
    { id: "extra", title: "Extra" },
  ];
  eq(nestTargetRows(cyclic, "extra").map((r) => r.group.id), [],
    "② 互指环成员不是合法目标（断链父链），遍历终止不悬挂");

  // ④ 空组树：roster 只有自身 → 无目标行（组件侧据此禁用菜单项）。
  eq(nestTargetRows([{ id: "only", title: "Only" }], "only"), [],
    "④ 唯一组自身 → 目标行为空（菜单项禁用态的数据源）");

  // ③ 移动后 groupPath 正确嵌套：b depth0 → c depth1 → d depth2；a 挂到 d 下 → depth 3；
  //    且 层级≠指挥权：只动 parent，id/标题/成员逐位不变。
  const after = nestGroup(roster, "g-a", "g-d");
  eq(groupDepth(after, "g-a"), 3, "③ 移动后 a 挂在 depth2 的 d 下 → groupDepth(a)=3");
  eq((after.find((g) => g.id === "g-a")?.parent), "g-d", "③ 移动后 parent 链指向深层目标 d");
  ok(hierarchyMutationIsDisplayOnly(roster, after), "GUARD 层级≠指挥权: 深层移动只动 parent");
}

// ── 源码钉：slice(0,4) 不复活 / 禁用态 / 层级前缀 ─────────────────────────────
console.log("\ntl1198 B: 源码钉 — 旧 cap 不复活、禁用态、层级前缀");
{
  const orgSource = readFileSync(new URL("../components/ProjectTreeOrganization.tsx", import.meta.url), "utf8");
  const nestIntoSlice = orgSource.slice(orgSource.indexOf("const nestInto"), orgSource.indexOf("const groupMoveItems"));
  ok(!nestIntoSlice.includes(".slice("), "旧 slice(0, 4) cap 不复活：nestInto 计算无任何截断");
  ok(nestIntoSlice.includes("nestTargetRows"), "菜单数据源是树形枚举 helper（nestTargetRows）");
  ok(orgSource.includes("disabled: groupMoveItems.length === 0"), "④ 空目标时「分组移动」submenu 走 disabled 态");
  ok(!orgSource.includes("groupMoveItems.length > 0 ? [{"), "④ 旧「空目标整项消失」的条件展开已移除");
  ok(orgSource.includes('ancestorTitles.join(" / ")'), "③ 目标行拼接层级前缀（祖先链 / 自身）");
}

// ── jsdom 渲染链：真实右键 → 展开二级 → 断言/点击 ─────────────────────────────
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

function DeepHarness({ bindings, revision, children }: {
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
    root.render(<StrictMode><DeepHarness bindings={bindings} revision={revision} children={children} /></StrictMode>);
    await flush();
  });
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

function menuItems(): HTMLElement[] {
  return [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

async function openGroupMenu(title: string) {
  const index = headers().findIndex((el) => el.getAttribute("title") === title);
  if (index < 0) return;
  await act(async () => {
    headers()[index]!.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
}

async function expandGroupMoveSubmenu() {
  const trigger = menuItems().find((el) => (el.textContent ?? "").includes("projectTree.groupMove")) as HTMLButtonElement | undefined;
  if (trigger) await act(async () => { trigger.click(); await flush(); });
  return trigger;
}

function rowByText(text: string): HTMLButtonElement | undefined {
  return menuItems().find((el) => (el.textContent ?? "").includes(text)) as HTMLButtonElement | undefined;
}

// 验收 ①：右键顶层组 a，二级含 depth≥2 深层组 c、d，且 c/d 行带层级前缀。
console.log("\ntl1198 C: 渲染链 — 深层目标进二级 + 前缀 + 点击落盘");
{
  const roster: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
    { id: "g-c", title: "C", parent: "g-b" },
    { id: "g-d", title: "D", parent: "g-c" },
  ];
  const saved: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(roster, saved), [topicNode("t1", "idle")]);

  await openGroupMenu("A"); // 右键 A
  const trigger = await expandGroupMoveSubmenu();
  ok(Boolean(trigger) && trigger!.disabled === false, "「分组移动 ▸」存在且可点（合法目标非空）");
  const rows = menuItems().map((el) => el.textContent ?? "");
  ok(rows.some((text) => text.includes("projectTree.nestGroupUnder:B")), "① 浅层目标 B 仍在二级");
  ok(rows.some((text) => text.includes("projectTree.nestGroupUnder:B / C")), "① depth2 目标 C 进二级，行带前缀「B / C」");
  ok(rows.some((text) => text.includes("projectTree.nestGroupUnder:B / C / D")), "① depth3 目标 D 进二级，行带前缀「B / C / D」");

  // ③ 点击深层目标 D：CAS payload 的 parent 链落盘正确（groupPath 嵌套）。
  const deepRow = rowByText("projectTree.nestGroupUnder:B / C / D");
  ok(Boolean(deepRow) && deepRow!.disabled === false, "③ 深层目标行可点（非禁用）");
  if (deepRow) {
    await act(async () => { deepRow.click(); await flush(); });
    await flush();
    eq(saved.payloads.length, 1, "深层移动写恰好一次 CAS payload");
    const payload = saved.payloads[0] ?? [];
    eq(payload.find((g) => g.id === "g-a")?.parent, "g-d", "③ 落盘 parent=g-d（深层目标，groupPath 嵌套正确）");
    eq(payload.find((g) => g.id === "g-a")?.topicIds, ["t1"], "GUARD 层级≠指挥权: 成员集不动");
  }
  await cleanup();
}

// 验收 ②：右键 b（有子树 c/d），二级不出现自身子树；右键 c，二级不出现当前父 b。
console.log("\ntl1198 D: 渲染链 — 自身子树/当前父排除（防环）");
{
  const roster: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
    { id: "g-c", title: "C", parent: "g-b" },
    { id: "g-d", title: "D", parent: "g-c" },
  ];
  const saved: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(roster, saved), [topicNode("t1", "idle")]);

  // 右键 B（有子树 c/d）。
  await openGroupMenu("B");
  await expandGroupMoveSubmenu();
  const rowsForB = menuItems().map((el) => el.textContent ?? "");
  ok(rowsForB.some((text) => text.includes("projectTree.nestGroupUnder:A")), "② B 的二级含 A");
  ok(!rowsForB.some((text) => text.includes("projectTree.nestGroupUnder:C")), "② B 的二级不含自身子树成员 C");
  ok(!rowsForB.some((text) => text.includes("projectTree.nestGroupUnder:D")), "② B 的二级不含自身子树成员 D");
  await cleanup();

  const roster2: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
    { id: "g-c", title: "C", parent: "g-b" },
  ];
  await mount(makeBindings(roster2, saved), [topicNode("t1", "idle")]);
  await openGroupMenu("C");
  const triggerC = await expandGroupMoveSubmenu();
  ok(Boolean(triggerC), "嵌套组 C 的「分组移动」仍可用");
  const rowsForC = menuItems().map((el) => el.textContent ?? "");
  ok(!rowsForC.some((text) => text.includes("projectTree.nestGroupUnder:B")), "② C 的二级不含当前父 B（no-op 不出现）");
  ok(rowsForC.some((text) => text.includes("projectTree.moveGroupToTopLevel")), "嵌套组 C 保留「移到顶层」逃生口");
  await cleanup();
}

// 验收 ④：唯一组（无任何合法目标）时「分组移动」保留但禁用，不再整项消失。
console.log("\ntl1198 E: 渲染链 — 空目标禁用态");
{
  const roster: SessionGroup[] = [{ id: "g-only", title: "Only", topicIds: ["t1"] }];
  const saved: SavedState = { payloads: [], revisions: [] };
  await mount(makeBindings(roster, saved), [topicNode("t1", "idle")]);
  await openGroupMenu("Only");
  const trigger = menuItems().find((el) => (el.textContent ?? "").includes("projectTree.groupMove")) as HTMLButtonElement | undefined;
  ok(Boolean(trigger), "④ 唯一组菜单里「分组移动」入口仍在（不整项消失）");
  ok(Boolean(trigger) && trigger!.disabled === true, "④ 无合法目标时该入口为禁用态（点不开）");
  await act(async () => { trigger!.click(); await flush(); });
  const flyoutRows = menuItems().filter((el) => (el.textContent ?? "").includes("projectTree.nestGroupUnder"));
  eq(flyoutRows.length, 0, "④ 禁用态点击不展开目标面板");
  await cleanup();
}

console.log(`\ntl1198: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
