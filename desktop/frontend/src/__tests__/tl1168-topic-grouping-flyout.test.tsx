// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1168-topic-grouping-flyout.test.tsx
//
// tl 1168 返修验收（包30 用户二次反馈）:
//   ①「希望在分组选项上停留后自动展开二级菜单，而不是把分组移动的多个选项
//      也放一级菜单」→ ContextMenu 的 submenu 从 inline accordion（叶子在展开
//      后把一级菜单往下撑长）升级为浮动二级面板：hover 停留 CONTEXT_MENU_HOVER_
//      OPEN_MS 展开、点击/触屏同样可开、键盘 → 进入 ← 返回、Esc 关整条链、
//      视口右溢翻左 + 垂直贴合。单层约束保留（flyout 内嵌套 submenu 照旧 disabled）。
//   ②「没有体现移动到的语义」→ 入口文案改「移动到分组」（共用项目行入口的
//      projectGroup.moveInto 三语键；死键 projectTree.grouping 删除）。
//   钉五层:
//   D. 一级单项入口——菜单里分组相关只有「移动到分组」一个 submenu 项。
//   E. hover 停留展开（含划过即走不展开的防误触）。
//   F. 键盘 → 进入（焦点落首叶）/ ← 返回（焦点回触发钮）/ Esc 关整链。
//   G. 触屏/无 hover 设备点击可开。
//   H. 移动全链不回归（flyout 内点目标组 → CAS 落盘；当前组禁用；「移出分组」
//      垫分隔线居末）+ 定位在 jsdom（rect=0）下的冒烟与源码钉。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ContextMenu, CONTEXT_MENU_HOVER_OPEN_MS, type ContextMenuItem } from "../components/ContextMenu";
import { useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
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
globalThis.KeyboardEvent = dom.window.KeyboardEvent as unknown as typeof KeyboardEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const t = ((key: string, vars?: Record<string, string>) => (vars?.title ? `${key}:${vars.title}` : key)) as unknown as Translator;

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}
async function dwell() {
  // The hover-open delay plus a small scheduler margin.
  await new Promise((resolve) => setTimeout(resolve, CONTEXT_MENU_HOVER_OPEN_MS + 80));
}

function menuButtons(): HTMLElement[] {
  return [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}
function findMenuItem(text: string): HTMLElement | undefined {
  return menuButtons().find((el) => (el.textContent ?? "").includes(text));
}
function rootPanel(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".context-menu:not(.context-menu--flyout)");
}
function flyoutPanel(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".context-menu--flyout");
}
function hoverEnter(el: HTMLElement) {
  el.dispatchEvent(new window.MouseEvent("mouseover", { bubbles: true, cancelable: true }));
}
function hoverLeave(el: HTMLElement) {
  el.dispatchEvent(new window.MouseEvent("mouseout", { bubbles: true, cancelable: true }));
}
function pressKey(el: HTMLElement, key: string) {
  el.dispatchEvent(new window.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
}

interface SavedState { payloads: SessionGroup[][] }

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
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
}

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };
const topicNode: ProjectNode = { key: "project-/repo", kind: "topic", label: "T1", root: "/repo", topicId: "t1", children: [] };

// 与 ProjectTree.tsx 会话菜单同构的入口组装（tl 1168 后的形态）。
function FlyoutHarness({ bindings, onItems, onClose }: {
  bindings: ProjectTreeOrganizationBindings;
  onItems: (items: ContextMenuItem[]) => void;
  onClose: () => void;
}) {
  const organization = useProjectTreeOrganization({ tree: [folder], refresh: async () => {}, bindings });
  const organizationItems = organization.topicMenuItems(topicNode, t);
  // 与真实会话菜单同构：grouping 入口前有一条普通行（重命名）——E 段用它验证
  // 「hover 一级普通行收起 flyout」。
  const menuItems: ContextMenuItem[] = [
    { key: "rename", label: t("projectTree.renameTopic"), onSelect: () => {} },
    ...(organizationItems.length > 0 ? [{
      type: "submenu" as const,
      key: "grouping",
      label: t("projectGroup.moveInto"),
      items: organizationItems,
    }] : []),
  ];
  onItems(menuItems);
  return <ContextMenu open point={{ left: 10, top: 10 }} minWidth={178} ariaLabel="topicActions" items={menuItems} onClose={onClose} />;
}

const roster: SessionGroup[] = [
  { id: "holding", title: "Holding", topicIds: ["t1"] },
  { id: "target", title: "Target", topicIds: [] },
];

// ── D. 一级单项入口：菜单里分组相关只有一个「移动到分组」submenu 项，且带
//      aria-haspopup（可点开的显式标记——触屏路径的可达性前提）。
console.log("\ntl1168 D: 一级单项入口（不再平铺分组列表）");
let latestItems: ContextMenuItem[] = [];
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={(items) => { latestItems = items; }} onClose={() => {}} /></StrictMode>);
    await flush();
  });

  const submenus = latestItems.filter((item) => item.type === "submenu");
  eq(submenus.length, 1, "exactly ONE grouping entry at the top level (no flattened group rows)");
  eq(submenus[0]?.type === "submenu" ? submenus[0].key : "", "grouping", "the single entry is the grouping submenu");
  eq(submenus[0]?.type === "submenu" ? submenus[0].label : "", "projectGroup.moveInto", "the entry label is the action wording 「移动到分组」(moveInto key)");

  const trigger = findMenuItem("projectGroup.moveInto");
  ok(Boolean(trigger) && trigger!.getAttribute("aria-haspopup") === "menu", "the entry row carries aria-haspopup=menu (openable without hover)");

  // 未展开时根菜单里没有任何分组叶子——一级不再被撑长。
  const rootBefore = rootPanel();
  ok(Boolean(rootBefore) && ![...(rootBefore?.querySelectorAll("button") ?? [])].some((b) => (b.textContent ?? "").includes("Target")),
    "no group leaf renders inside the level-1 panel before expansion");
  await act(async () => root.unmount());
}

// ── E. hover 停留展开：mouseover 停留 CONTEXT_MENU_HOVER_OPEN_MS 后 flyout
//      面板出现、叶子在第二级面板里；划过即走不展开。
console.log("\ntl1168 E: hover 停留展开 + 划过防误触");
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={() => {}} onClose={() => {}} /></StrictMode>);
    await flush();
  });
  const trigger = findMenuItem("projectGroup.moveInto")!;

  // 划过即走：进入后立刻离开，停留计时器必须被取消。
  await act(async () => {
    hoverEnter(trigger);
    hoverLeave(trigger);
    await dwell();
  });
  ok(flyoutPanel() === null, "a pointer passing through does NOT open the flyout (dwell timer cancelled on leave)");

  // 停留展开。
  await act(async () => {
    hoverEnter(trigger);
    await dwell();
  });
  const panel = flyoutPanel();
  ok(Boolean(panel), "dwelling on the entry opens the flyout panel (hover, no click)");
  ok(Boolean(panel?.querySelector(".context-menu--flyout") === null) && panel!.className.includes("context-menu"),
    "the flyout is its own panel (second level), not inline rows in the level-1 menu");
  ok(Boolean(panel?.textContent?.includes("Target")), "the destination groups render inside the flyout panel");
  ok(!rootPanel()!.textContent!.includes("Target"), "the level-1 panel stays short (no flattened group rows)");

  // hover 展开不得抢焦点。
  ok(document.activeElement !== menuButtons().find((b) => (b.textContent ?? "").includes("Target")),
    "hover-open does not steal focus");

  // 移到一级普通行（悬停非 submenu 行）→ flyout 收起。
  const plainRow = findMenuItem("projectTree.renameTopic")!;
  await act(async () => {
    hoverEnter(plainRow);
    await new Promise((resolve) => setTimeout(resolve, 380));
  });
  ok(flyoutPanel() === null, "hovering a level-1 sibling row folds the flyout (dwell-close)");

  await act(async () => root.unmount());
}

// ── F. 键盘：→ 进入（焦点落首叶）/ ← 返回（焦点回触发钮）/ Esc 关整链。
console.log("\ntl1168 F: 键盘可达（→ 进入 / ← 返回 / Esc 关整链）");
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  let closeCount = 0;
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={() => {}} onClose={() => { closeCount += 1; }} /></StrictMode>);
    await flush();
  });
  const trigger = findMenuItem("projectGroup.moveInto")!;

  await act(async () => {
    trigger.focus();
    pressKey(trigger, "ArrowRight");
    await flush();
  });
  const panel = flyoutPanel();
  ok(Boolean(panel), "ArrowRight on the entry opens the flyout");
  const firstLeaf = panel?.querySelector<HTMLButtonElement>("button[role=\"menuitem\"]:not(:disabled)");
  ok(Boolean(firstLeaf) && document.activeElement === firstLeaf, "focus lands on the first enabled leaf (keyboard entry)");

  await act(async () => {
    pressKey(firstLeaf!, "ArrowLeft");
    await flush();
  });
  ok(flyoutPanel() === null, "ArrowLeft folds the flyout");
  ok(document.activeElement === trigger, "focus returns to the entry row");

  await act(async () => {
    trigger.click();
    await flush();
  });
  ok(Boolean(flyoutPanel()), "reopened via click for the escape test");
  const leafAgain = flyoutPanel()!.querySelector<HTMLButtonElement>("button[role=\"menuitem\"]")!;
  await act(async () => {
    leafAgain.focus();
    pressKey(leafAgain, "Escape");
    await flush();
  });
  eq(closeCount, 1, "Escape closes the whole chain (single onClose)");
  ok(flyoutPanel() === null, "the flyout is gone after Escape");

  await act(async () => root.unmount());
}

// ── G. 触屏/无 hover 设备：点击（tap）即可开面板。
console.log("\ntl1168 G: 触屏可点开（click 无 hover 依赖）");
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={() => {}} onClose={() => {}} /></StrictMode>);
    await flush();
  });
  const trigger = findMenuItem("projectGroup.moveInto")!;
  ok(Boolean(trigger) && trigger!.disabled === false, "the entry row is a real button (tap target)");
  await act(async () => { trigger.click(); await flush(); });
  ok(Boolean(flyoutPanel()?.textContent?.includes("Target")), "a tap (click) opens the flyout — touch needs no hover");

  await act(async () => root.unmount());
}

// ── H. 移动全链不回归 + 出口顺序：flyout 内点目标组 → CAS 落盘；当前组禁用；
//      「移出分组」垫分隔线居末。
console.log("\ntl1168 H: 移动全链（CAS 落盘）+ 移出分组居末");
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={(items) => { latestItems = items; }} onClose={() => {}} /></StrictMode>);
    await flush();
  });
  const groupingEntry = latestItems.find((item): item is Extract<ContextMenuItem, { type: "submenu" }> => item.type === "submenu" && item.key === "grouping");
  const moveItems = groupingEntry?.items ?? [];
  const lastKey = moveItems.length > 0 ? (moveItems[moveItems.length - 1] as { key?: string }).key : "";
  eq(lastKey, "remove-from-group", "「移出分组」sits at the END of the submenu items");
  ok(moveItems[moveItems.length - 2]?.type === "separator", "a separator divides the destinations from the escape hatch");
  const holdingLeaf = moveItems.find((item) => "key" in item && item.key === "move:holding") as { disabled?: boolean } | undefined;
  eq(holdingLeaf?.disabled, true, "the current group renders disabled (single-group constraint)");

  const trigger = findMenuItem("projectGroup.moveInto")!;
  await act(async () => {
    trigger.click();
    await flush();
  });
  const targetRow = findMenuItem("Target")!;
  ok(targetRow.disabled === false, "the destination group is clickable inside the flyout");
  await act(async () => { targetRow.click(); await flush(); });
  await flush();
  eq(saved.payloads.length, 1, "exactly one CAS save");
  const payload = saved.payloads[0] ?? [];
  eq(payload.find((g) => g.id === "holding")?.topicIds, [], "the old group drops the topic");
  eq(payload.find((g) => g.id === "target")?.topicIds, ["t1"], "the target gains the topic at the tail");

  // 叶子点击后二级面板自收（ContextMenu 契约：nested leaf closes the menu）；
  // 根面板的卸载归调用方（onClose → open=false），harness 里 open 恒真。
  await act(async () => { await flush(); });
  ok(flyoutPanel() === null, "picking a leaf folds the flyout (chain close handed to the caller via onClose)");

  await act(async () => root.unmount());
}

// ── I. 定位：jsdom（rect=0）下展开不炸且钳到视口内（top ≥ EDGE_GAP）；翻转与
//      贴合逻辑在源码里钉住。
console.log("\ntl1168 I: 定位翻转/贴合（jsdom 冒烟 + 源码钉）");
{
  const saved: SavedState = { payloads: [] };
  const bindings = makeBindings(roster, saved);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><FlyoutHarness bindings={bindings} onItems={() => {}} onClose={() => {}} /></StrictMode>);
    await flush();
  });
  const trigger = findMenuItem("projectGroup.moveInto")!;
  await act(async () => {
    trigger.click();
    await flush();
  });
  const panel = flyoutPanel();
  ok(Boolean(panel), "the flyout mounts under jsdom (zero rects do not break positioning)");
  ok(panel!.style.top === "8px", `vertically clamped to the viewport edge (top=${panel!.style.top})`);

  await act(async () => root.unmount());

  const source = readFileSync(new URL("../components/ContextMenu.tsx", import.meta.url), "utf8");
  ok(source.includes("const left = rightFlows || !leftFlows ? tr.right : tr.left - fr.width;"),
    "source: the flyout flips to the LEFT edge when the right side has no room");
  ok(source.includes("Math.min(tr.top, window.innerHeight - fr.height - EDGE_GAP)"),
    "source: the flyout clamps vertically inside the viewport");
  ok(source.includes("flyoutRef.current?.contains(target)"),
    "source: clicks inside the flyout do not count as outside-pointer closes");
}

// ── J. 三语文案：入口 =「移动到分组 / Move to group / 移動到分組」；死键
//      projectTree.grouping 三语已删。
console.log("\ntl1168 J: 三语文案（移动到分组）");
{
  const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
  const en = (await import("../locales/en")).en as unknown as Record<string, string>;
  const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
  eq(zh["projectGroup.moveInto"], "移动到分组", "zh entry = 移动到分组");
  eq(en["projectGroup.moveInto"], "Move to group", "en entry = Move to group");
  eq(zhTw["projectGroup.moveInto"], "移動到分組", "zh-TW entry = 移動到分組");
  ok(zh["projectTree.grouping"] === undefined && en["projectTree.grouping"] === undefined && zhTw["projectTree.grouping"] === undefined,
    "the dead noun key projectTree.grouping is removed from all three locales");
  const treeSource = readFileSync(new URL("../components/ProjectTree.tsx", import.meta.url), "utf8");
  ok(!treeSource.includes("projectTree.grouping"), "ProjectTree no longer references the dead key");
}

console.log(`\ntl1168-topic-grouping-flyout: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
