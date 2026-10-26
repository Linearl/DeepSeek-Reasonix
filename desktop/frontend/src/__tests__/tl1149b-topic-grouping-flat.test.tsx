// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1149b-topic-grouping-flat.test.tsx
//
// tl 1149b 返修验收（包30 用户实测「对话→分组→移动到分组」点不动）:
//   根因: 侧栏 ContextMenu 只展开一层 submenu——renderItem 对 nested 的
//   submenu 一律 disabled（"one level is the deal"）。tl 1103 把 task 720 的
//   「移动到分组 ▸」flyout 收进会话菜单「分组 ▸」二级后，它成了嵌套 submenu，
//   渲染即禁用；tl 1149 与其同包交付，被记为 1149 缺陷。
//   返修: topicMenuItems 平铺——分组行就是「分组 ▸」的本层叶子。
//   本文件钉三层:
//   A. 会话节点全链（真实 ContextMenu + 真实 controller + CAS 落盘）:
//      「分组 ▸」可展开 → 分组叶子（当前组禁用/目标组可选）→ 点击 →
//      handler 收到正确 topicId+groupId（CAS payload 逐字断言）。
//   B. 分组节点对照: 分组头右键「分组移动 ▸」占一级菜单的第一层（合法），
//      展开 → 点「移到 B 子层」→ nestGroup 落盘（层级≠指挥权，成员不动）。
//   C. 根因钉: ContextMenu 的 nested-disabled 语义仍在源码里——若将来放开
//      多层展开，须回头重审 topicMenuItems 的平铺假设与该语义的消费者。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ContextMenu, type ContextMenuItem } from "../components/ContextMenu";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import type { Translator } from "../lib/i18n";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "../lib/types";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent as unknown as typeof MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const t = ((key: string, vars?: Record<string, string>) => (vars?.title ? `${key}:${vars.title}` : key)) as unknown as Translator;

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function menuButtons(): HTMLElement[] {
  return [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}
function findMenuItem(text: string): HTMLElement | undefined {
  return menuButtons().find((el) => (el.textContent ?? "").includes(text));
}

interface SavedState { payloads: SessionGroup[][]; calls: Array<{ scope: string; root: string }> }

function makeBindings(initial: SessionGroup[], saved: SavedState): ProjectTreeOrganizationBindings {
  let revision = 1;
  let groups = initial.map((g) => ({ ...g }));
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (scope: string, root: string, expected: number, next: SessionGroup[]) => {
      if (expected !== revision) return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      saved.payloads.push(next.map((g) => ({ ...g })));
      saved.calls.push({ scope, root });
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
}

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

// ── A. 会话节点全链：与 ProjectTree.tsx 完全相同的「分组 ▸」组装（1764-1770），
//      挂真实 ContextMenu，重放用户路径「右键对话 → 分组 ▸ → 点目标分组」。
const topicNode: ProjectNode = { key: "project-/repo", kind: "topic", label: "T1", root: "/repo", topicId: "t1", children: [] };

function TopicGroupingHarness({ bindings, onItems }: {
  bindings: ProjectTreeOrganizationBindings;
  onItems: (items: ContextMenuItem[]) => void;
}) {
  const organization = useProjectTreeOrganization({ tree: [folder], refresh: async () => {}, bindings });
  // 与 ProjectTree.tsx 会话菜单同构：organizationItems.length > 0 才渲染二级。
  const organizationItems = organization.topicMenuItems(topicNode, t);
  onItems(organizationItems);
  return <ContextMenu
    open
    point={{ left: 10, top: 10 }}
    minWidth={178}
    ariaLabel="topicActions"
    items={organizationItems.length > 0 ? [{
      type: "submenu" as const,
      key: "grouping",
      label: t("projectTree.grouping"),
      items: organizationItems,
    }] : []}
    onClose={() => {}}
  />;
}

console.log("\ntl1149b A: 会话节点「对话→分组→移动到分组」全链（真实 ContextMenu + CAS 落盘）");
{
  const roster: SessionGroup[] = [
    { id: "holding", title: "Holding", topicIds: ["t1"] },
    { id: "target", title: "Target", topicIds: [] },
  ];
  const saved: SavedState = { payloads: [], calls: [] };
  const bindings = makeBindings(roster, saved);
  let latestItems: ContextMenuItem[] = [];
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><TopicGroupingHarness bindings={bindings} onItems={(items) => { latestItems = items; }} /></StrictMode>);
    await flush();
  });

  // 结构前提：平铺——「分组 ▸」的 items 里没有任何嵌套 submenu（根因不复入）。
  ok(latestItems.every((item) => item.type !== "submenu"), "the grouping section items are flat leaves (no nested submenu)");

  const groupingTrigger = findMenuItem("projectTree.grouping");
  ok(Boolean(groupingTrigger) && groupingTrigger!.disabled === false, "「分组 ▸」second-level entry is present and clickable");
  await act(async () => { groupingTrigger!.click(); await flush(); });

  const holdingRow = findMenuItem("Holding");
  const targetRow = findMenuItem("Target");
  ok(Boolean(holdingRow) && holdingRow!.disabled === true, "the group the topic already sits in renders DISABLED (single-group constraint)");
  ok(Boolean(targetRow) && targetRow!.disabled === false, "the destination group renders clickable");

  // 点击 = 用户点「移动到分组」的目标分组行。断言 handler 收到正确
  // topicId+groupId：CAS payload 必须是 t1 离 holding、进 target 尾部。
  await act(async () => { targetRow!.click(); await flush(); });
  await flush();
  eq(saved.payloads.length, 1, "exactly one CAS save");
  const payload = saved.payloads[0] ?? [];
  eq(payload.find((g) => g.id === "holding")?.topicIds, [], "handler got groupId=target: the old group drops the topic");
  eq(payload.find((g) => g.id === "target")?.topicIds, ["t1"], "handler got topicId=t1: the target gains the topic at the tail (moveTopicInRoster append)");
  eq(saved.calls[0], { scope: "project", root: "/repo" }, "the save rides the right scope/root key");

  // 点完后的乐观翻转（同一挂载内的 roster 状态）。
  const afterItems = latestItems;
  ok(afterItems.find((item) => "key" in item && item.key === "move:target")?.disabled === true
    && afterItems.find((item) => "key" in item && item.key === "move:holding")?.disabled === false,
    "after the move the disabled pair flips (optimistic roster)");

  // 移出分组行仍随在组内状态出现（task 144 出口）。
  ok(afterItems.some((item) => "key" in item && item.key === "remove-from-group"), "「移出分组」escape hatch stays");

  await act(async () => root.unmount());
}

// ── B. 分组节点对照：分组头右键菜单的「分组移动 ▸」占一级菜单的第一层
//      submenu（合法形态），展开后叶子可点、走 nestGroup 落盘。
console.log("\ntl1149b B: 分组节点对照——「分组移动 ▸」一级菜单内正常展开（第一层合法）");
{
  const roster: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: ["t2"] },
  ];
  const saved: SavedState = { payloads: [], calls: [] };
  const bindings = makeBindings(roster, saved);

  function GroupRowsHarness() {
    const organization = useProjectTreeOrganization({ tree: [folder], refresh: async () => {}, bindings });
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

  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><GroupRowsHarness /></StrictMode>);
    await flush();
  });
  const headers = () => [...document.querySelectorAll(".project-tree__group-main")].filter((el): el is HTMLElement => el instanceof HTMLElement);
  eq(headers().map((h) => h.getAttribute("title")), ["A", "B"], "both group headers render");

  await act(async () => {
    headers()[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const groupMoveTrigger = findMenuItem("projectTree.groupMove");
  ok(Boolean(groupMoveTrigger) && groupMoveTrigger!.disabled === false, "「分组移动 ▸」is present and clickable in the group-header menu (first-level submenu)");
  await act(async () => { groupMoveTrigger!.click(); await flush(); });

  const nestRow = findMenuItem("projectTree.nestGroupUnder:B");
  ok(Boolean(nestRow) && nestRow!.disabled === false, "「移到 B 子层」leaf is clickable");
  await act(async () => { nestRow!.click(); await flush(); });
  await flush();
  eq(saved.payloads.length, 1, "the nest move writes exactly one CAS payload");
  const payload = saved.payloads[0] ?? [];
  eq(payload.find((g) => g.id === "g-a")?.parent, "g-b", "handler got (childID=g-a, parentID=g-b)");
  eq(payload.find((g) => g.id === "g-a")?.topicIds, ["t1"], "GUARD 层级≠指挥权: membership untouched");

  await act(async () => root.unmount());
}

// ── C. 根因钉：ContextMenu 的 nested-disabled 语义仍在——这是 topicMenuItems
//      必须平铺的原因。放开多层展开前必须重审该假设。
console.log("\ntl1149b C: 根因钉——ContextMenu 单层展开语义仍在源码");
{
  const contextMenuSource = readFileSync(new URL("../components/ContextMenu.tsx", import.meta.url), "utf8");
  ok(/const unusable = item\.disabled \|\| item\.items\.length === 0 \|\| nested;/.test(contextMenuSource),
    "ContextMenu still renders nested submenus disabled (the constraint topicMenuItems flattens for)");
  ok(contextMenuSource.includes("One level is all the sidebar needs") || contextMenuSource.includes("deeper nesting stays disabled"),
    "the one-level design note survives for the next reader");
}

console.log(`\ntl1149b-topic-grouping-flat: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
