// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1252-project-manage-submenu.test.tsx
//
// tl 1252 验收逐条钉（1200 遗留第三项：项目右键低频管理项收拢「项目管理 ▸」
// 二级菜单——用户已拍板做）:
//   ① classic 菜单：relocate / topic-versions / remove 三项收拢为一级
//      「项目管理 ▸」submenu（tl 1168 浮动二级面板基建，与「颜色 ▸」同形态），
//      三叶 key、落盘链（relocateProject / requestTopicVersions /
//      removeProject+confirm 双击）、remove 的 danger 标注原样保留；
//   ② 一级不再平铺三项——relocate-project / remove-separator / remove 各恰出
//      现一次且都在 submenu 内；copy-path 之后紧跟项目管理入口（1200 已交付
//      形态基础上收拢，其余序不动）；
//   ③ workbench 变体同步收拢（同 key 同语义；remove 短文案键保持专属）；
//   ④ 三语 projectTree.projectManage 齐备（无死键）；submenu 单层约束
//      （叶子无嵌套 submenu，1168 nested 契约）；
//   ⑤ 行为冒烟：与 ProjectTree 同构的「项目管理 ▸」入口——hover 停留展开
//      二级、三叶齐全、点叶触发回调、danger 叶样式保留、点叶整链关闭
//      （1168 契约，remove 确认态需重开菜单续点，见回执）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextMenu, CONTEXT_MENU_HOVER_OPEN_MS, type ContextMenuItem } from "../components/ContextMenu";

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

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "../..");
const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");

// ── classic 项目菜单切片 ──────────────────────────────────────────────────────
const menuStart = treeSource.indexOf("const projectMenuItems: ContextMenuItem[] = [");
const menuEnd = treeSource.indexOf("const workbenchProjectMenuItems: ContextMenuItem[] = [");
ok(menuStart >= 0 && menuEnd > menuStart, "the classic project menu block is locatable");
const menu = treeSource.slice(menuStart, menuEnd);
const workbench = treeSource.slice(menuEnd);

function idx(needle: string, haystack: string = menu): number {
  const at = haystack.indexOf(needle);
  if (at < 0) throw new Error(`anchor not found in slice: ${needle}`);
  return at;
}

// 「项目管理 ▸」submenu 切片（classic）：自入口 key 起（入口 type 声明在其前）。
const manageStart = menu.indexOf('key: "project-manage"');

console.log("\ntl1252 ① classic 三项收拢为「项目管理 ▸」submenu");
{
  ok(manageStart > 0, "the classic menu carries the project-manage entry");
  // 入口的 type 声明在其 key 之前——用相邻行正则钉 submenu 形态。
  ok(/type: "submenu" as const,\s*key: "project-manage",/.test(menu),
    "the project-manage entry is a submenu (gets the tl 1168 floating flyout)");
  const manageSlice = menu.slice(manageStart);
  ok(manageSlice.includes('label: t("projectTree.projectManage")'), "the entry label is the project-manage wording key");
  ok(manageSlice.includes('key: "relocate-project"'), "relocate leaf key preserved");
  ok(manageSlice.includes("void relocateProject(projectPath ?? node.root)"), "relocate leaf keeps the RelocateProject write chain");
  ok(manageSlice.includes('key: "topic-versions"'), "topic-versions leaf key preserved");
  ok(manageSlice.includes("requestTopicVersions({"), "topic-versions leaf keeps the requestTopicVersions entry chain");
  ok(manageSlice.includes('key: "remove"') && manageSlice.includes("danger: true"), "remove leaf keeps danger styling");
  ok(manageSlice.includes("t(\"projectTree.confirmRemoveProject\")") && manageSlice.includes("setConfirmRemoveProject(key)"),
    "remove leaf keeps the two-step confirm arm/execute semantics");
  ok(manageSlice.includes("void removeProject(projectPath)"), "remove leaf keeps the removeProject write chain");
}

console.log("\ntl1252 ② 一级平铺退场 + 菜单序保持");
{
  // 三项不再平铺在一级：relocate-project / remove-separator / remove 在
  // classic 菜单切片内各恰出现一次，且都在 project-manage submenu 内部
  // （出现位置晚于入口 key）。
  for (const anchor of ['key: "relocate-project"', 'key: "remove-separator"', 'key: "remove",']) {
    eq(menu.split(anchor).length - 1, 1, `classic menu carries exactly ONE ${anchor} (folded, not duplicated at level 1)`);
    ok(menu.indexOf(anchor) > manageStart, `${anchor} sits inside the project-manage submenu`);
  }
  // 1200 已交付形态基础上收拢：copy-path 之后紧跟项目管理入口，其余序不动。
  ok(idx('key: "copy-path"') < idx('key: "project-manage"'), "project-manage follows copy-path (tail position of the 1200 form)");
  ok(idx('key: "color",') < idx('{ type: "separator" as const, key: "path-separator" }'), "the 1200 color section order is untouched");
  ok(idx('key: "reveal"') < idx('key: "move-to-project-group"'), "the 1200 promoted rows order is untouched");
}

console.log("\ntl1252 ③ workbench 变体同步收拢");
{
  const wbManage = workbench.indexOf('key: "project-manage"');
  ok(wbManage > 0, "the workbench menu carries the project-manage entry");
  ok(/type: "submenu" as const,\s*key: "project-manage",/.test(workbench),
    "the workbench entry is a submenu too");
  const wbSlice = workbench.slice(wbManage);
  ok(wbSlice.includes('key: "relocate-project"') && wbSlice.includes('key: "topic-versions"') && wbSlice.includes('key: "remove"'),
    "workbench folds the same three leaves");
  ok(wbSlice.includes("void relocateProject(projectPath ?? node.root)") && wbSlice.includes("requestTopicVersions({"),
    "workbench leaves keep the same write chains");
  ok(wbSlice.includes("t(\"projectTree.confirmRemoveProjectShort\")") && wbSlice.includes("t(\"projectTree.removeProjectShort\")"),
    "workbench remove keeps its short-copy confirm semantics");
  // classic + workbench 各一份入口（同 key 不复用旧平铺位）。
  eq(treeSource.split('key: "project-manage"').length - 1, 2, "project-manage appears exactly once per menu variant");
}

console.log("\ntl1252 ④ 三语键 + 1168 单层契约");
{
  const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
  const en = (await import("../locales/en")).en as unknown as Record<string, string>;
  const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
  eq(zh["projectTree.projectManage"], "项目管理", "zh project-manage = 项目管理");
  eq(zhTw["projectTree.projectManage"], "專案管理", "zh-TW project-manage = 專案管理");
  eq(en["projectTree.projectManage"], "Project management", "en project-manage = Project management");
  // 1168 契约：submenu 内叶子是普通 item，无嵌套 submenu（nested 单层约束）。
  // manageSlice 自入口 key 起切（入口自身的 type 声明在其之前，不计入）。
  const manageSlice = menu.slice(manageStart);
  const nestedSubmenuCount = (manageSlice.match(/type: "submenu" as const/g) ?? []).length;
  eq(nestedSubmenuCount, 0, "the project-manage submenu carries no nested submenu (single-level constraint)");
}

// ── 行为冒烟：与 ProjectTree 同构的「项目管理 ▸」入口 ─────────────────────────
console.log("\ntl1252 行为冒烟：project-manage submenu 的 1168 面板行为");
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
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: false, media: "(prefers-reduced-motion: reduce)", onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
    dispatchEvent: () => false,
  }),
});

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}
async function dwell() {
  await new Promise((resolve) => setTimeout(resolve, CONTEXT_MENU_HOVER_OPEN_MS + 80));
}
function menuButtons(): HTMLElement[] {
  return [...document.querySelectorAll("[role=\"menuitem\"]")].filter((el): el is HTMLElement => el instanceof HTMLElement);
}
function findMenuItem(text: string): HTMLElement | undefined {
  return menuButtons().find((el) => (el.textContent ?? "").includes(text));
}
function flyoutPanel(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".context-menu--flyout");
}
function hoverEnter(el: HTMLElement) {
  el.dispatchEvent(new window.MouseEvent("mouseover", { bubbles: true, cancelable: true }));
}

{
  // 与 ProjectTree.tsx 的项目管理行同构：一级一个 submenu，三叶同 key，
  // onSelect 走回调（这里以记录数组代替 relocate/versions/remove 落盘）。
  const fired: string[] = [];
  let closeCount = 0;
  const manageItems: ContextMenuItem[] = [
    { key: "relocate-project", label: "relocate", onSelect: () => { fired.push("relocate"); } },
    { key: "topic-versions", label: "versions", onSelect: () => { fired.push("versions"); } },
    { type: "separator" as const, key: "remove-separator" },
    { key: "remove", label: "remove", danger: true, onSelect: () => { fired.push("remove"); } },
  ];
  const menuItems: ContextMenuItem[] = [
    { key: "copy-path", label: "copy-path", onSelect: () => {} },
    {
      type: "submenu" as const,
      key: "project-manage",
      label: "project-manage",
      items: manageItems,
    },
  ];
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><ContextMenu open point={{ left: 10, top: 10 }} ariaLabel="projectMenu" items={menuItems} onClose={() => { closeCount += 1; }} /></StrictMode>);
    await flush();
  });

  // 未展开时一级面板只有自己的行（copy-path + 项目管理入口），无平铺叶。
  const rootPanel = document.querySelector<HTMLElement>(".context-menu:not(.context-menu--flyout)");
  eq(rootPanel?.querySelectorAll("button[role=\"menuitem\"]").length, 2, "level 1 holds only its own rows (no flattened leaves)");

  // hover 停留展开。
  const trigger = findMenuItem("project-manage")!;
  await act(async () => {
    hoverEnter(trigger);
    await dwell();
  });
  const panel = flyoutPanel();
  ok(Boolean(panel), "dwelling on 「项目管理 ▸」 opens the floating flyout (hover, no click)");
  eq(panel?.querySelectorAll("button[role=\"menuitem\"]").length, 3, "the flyout carries all three leaves (separator is not a button)");
  const removeLeaf = [...(panel?.querySelectorAll<HTMLButtonElement>("button[role=\"menuitem\"]") ?? [])]
    .find((b) => b.textContent === "remove");
  ok(removeLeaf?.className.includes("context-menu__item--danger") === true, "the remove leaf renders with the danger class");

  // 点叶触发回调；1168 契约——flyout 叶子点击即整链关闭。
  const relocateLeaf = [...(panel?.querySelectorAll<HTMLButtonElement>("button[role=\"menuitem\"]") ?? [])]
    .find((b) => b.textContent === "relocate");
  await act(async () => { relocateLeaf?.click(); await flush(); });
  eq(fired, ["relocate"], "clicking a leaf fires its callback chain");
  ok(closeCount >= 1, "a flyout leaf click closes the whole menu chain (confirm arm needs a reopen, per receipt)");

  // 键盘可进：→ 在触发钮上打开面板、焦点落首叶。
  const triggerAgain = findMenuItem("project-manage")!;
  await act(async () => {
    triggerAgain.focus();
    triggerAgain.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true }));
    await flush();
  });
  const panelAgain = flyoutPanel();
  ok(Boolean(panelAgain), "ArrowRight opens the flyout (keyboard reachable)");
  const firstLeaf = panelAgain?.querySelector<HTMLButtonElement>("button[role=\"menuitem\"]:not(:disabled)");
  ok(Boolean(firstLeaf) && document.activeElement === firstLeaf, "focus lands on the first enabled leaf");

  await act(async () => root.unmount());
}

process.stdout.write(`\ntl1252: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
dom.window.close();
