// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1200-project-menu-order.test.tsx
//
// tl 1200 验收逐条钉（项目右键菜单布局重组——用户已拍板的两项；第三项待确认）:
//   ① 「在文件资源管理器中显示」「移动到分组」上移至颜色区之前（一级可见）；
//   ② 9 色平铺收拢为一级「颜色 ▸」submenu + tl 1168 浮动二级面板（hover 停留
//      180ms 展开/移出关闭/键盘 → 进），叶子 9 项、key 与 setProjectColor 落盘
//      链原样保留（零功能丢失）；
//   ③ 三语不新增死键（projectTree.color、projectTree.projectAlreadyInGroup）；
//   ④ tl 1149 契约保持：projectGroupMoveItems 恰好两处使用（classic+workbench）；
//      workbench 变体菜单本轮不动（无颜色区，重组不适用）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextMenu, CONTEXT_MENU_HOVER_OPEN_MS, type ContextMenuItem } from "../components/ContextMenu";
import { PROJECT_COLOR_OPTIONS } from "../lib/projectColors";

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

// ── classic 项目菜单切片（workbench 变体单独核对） ─────────────────────────────
const menuStart = treeSource.indexOf("const projectMenuItems: ContextMenuItem[] = [");
const menuEnd = treeSource.indexOf("const workbenchProjectMenuItems: ContextMenuItem[] = [");
ok(menuStart >= 0 && menuEnd > menuStart, "the classic project menu block is locatable");
const menu = treeSource.slice(menuStart, menuEnd);

function idx(needle: string): number {
  const at = menu.indexOf(needle);
  if (at < 0) throw new Error(`anchor not found in classic menu slice: ${needle}`);
  return at;
}

console.log("\ntl1200 ① 两项上移至颜色区之前");
{
  // 一级序：… rename → ─── → reveal → move-to-group ▸ → ─── → 颜色 ▸ → ─── → copy-path → …
  ok(idx('{ type: "separator" as const, key: "color-separator" }') < idx('key: "reveal"'), "reveal sits right after the rename section separator");
  ok(idx('key: "reveal"') < idx('key: "move-to-project-group"'), "reveal comes before move-to-group (the two promoted rows stay adjacent)");
  ok(idx('key: "move-to-project-group"') < idx('{ type: "separator" as const, key: "color-menu-separator" }'), "move-to-group sits before the color section separator");
  ok(idx('{ type: "separator" as const, key: "color-menu-separator" }') < idx('key: "color",'), "the color entry follows its separator (one section above = before the old color zone)");
  ok(idx('key: "color",') < idx('{ type: "separator" as const, key: "path-separator" }'), "the color section still precedes the path separator");
  ok(idx('{ type: "separator" as const, key: "path-separator" }') < idx('key: "copy-path"'), "copy-path keeps its original tail position (untouched by the promoted rows)");
  ok(idx('key: "copy-path"') < idx('key: "relocate-project"'), "relocate stays after copy-path");
  ok(idx('key: "topic-versions"') < idx('{ type: "separator" as const, key: "remove-separator" }'), "versions stays before the remove separator");
  // 移动后的旧位不再有第二个 move-to-project-group（classic 菜单内恰好一次）。
  eq(menu.split('key: "move-to-project-group"').length - 1, 1, "the classic menu carries exactly ONE move-to-group row (no duplicate at the old position)");
}

console.log("\ntl1200 ② 9 色收拢一级「颜色 ▸」+ tl 1168 二级面板");
{
  eq(menu.split('key: `color-${option.key || "default"}`').length - 1, 1, "the 9-color leaf builder appears exactly once (inside the submenu, not flat in level 1)");
  const colorRow = menu.slice(idx('{ type: "separator" as const, key: "color-menu-separator" }'), idx('{ type: "separator" as const, key: "path-separator" }'));
  ok(/type: "submenu" as const/.test(colorRow), "the color entry is a submenu (gets the tl 1168 floating flyout: hover dwell / tap / keyboard)");
  ok(colorRow.includes("PROJECT_COLOR_OPTIONS.map"), "the leaves derive from the same PROJECT_COLOR_OPTIONS (9 options, no second source)");
  ok(colorRow.includes("colorMenuLabel(projectColorLabel(t, option.key)"), "the leaf labels keep the swatch+check rendering");
  ok(colorRow.includes("void setProjectColor(colorTargetRoot, option.key)"), "the leaf onSelect keeps the setProjectColor write chain (zero function loss)");
  eq(PROJECT_COLOR_OPTIONS.length, 9, "the option set is still the 9 colors");
  ok(colorRow.includes('label: t("projectTree.color")'), "the entry label is the new color wording key");
}

console.log("\ntl1200 ③④ 三语键 + tl 1149 契约");
{
  const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
  const en = (await import("../locales/en")).en as unknown as Record<string, string>;
  const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
  eq(zh["projectTree.color"], "颜色", "zh color = 颜色");
  eq(en["projectTree.color"], "Color", "en color = Color");
  eq(zhTw["projectTree.color"], "顏色", "zh-TW color = 顏色");
  ok(Boolean(zh["projectTree.projectAlreadyInGroup"]) && Boolean(en["projectTree.projectAlreadyInGroup"]) && Boolean(zhTw["projectTree.projectAlreadyInGroup"]),
    "alreadyInGroup present in all three locales (tl 1199 shared key, no dead key)");
  // tl 1149 契约：两份项目菜单共用同一 submenu builder；workbench 变体不动。
  eq(treeSource.split("items: projectGroupMoveItems(projectPath ?? node.root)").length - 1, 2, "tl 1149 contract: both classic and workbench menus use the same submenu builder");
  const workbench = treeSource.slice(menuEnd);
  ok(workbench.includes('key: "move-to-project-group"'), "the workbench variant keeps its own move-to-group row (untouched this round)");
  ok(!workbench.includes('key: "color",'), "the workbench variant has no color section (restructure not applicable there)");
}

// ── 行为冒烟：与 ProjectTree 同构的「颜色 ▸」入口——hover 停留展开二级、
//    9 叶齐全、点叶触发落盘回调、键盘 → 可进。 ─────────────────────────────────
console.log("\ntl1200 行为冒烟：颜色 submenu 的 1168 面板行为");
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
  // 与 ProjectTree.tsx 的颜色行同构：一级一个 submenu，叶子来自 PROJECT_COLOR_OPTIONS，
  // onSelect 走回调（这里以 pick 记录代替 setProjectColor）。
  const picked: string[] = [];
  const colorItems: ContextMenuItem[] = PROJECT_COLOR_OPTIONS.map((option) => ({
    key: `color-${option.key || "default"}`,
    label: option.key || "default",
    onSelect: () => { picked.push(option.key || "default"); },
  }));
  const menuItems: ContextMenuItem[] = [
    { key: "reveal", label: "reveal", onSelect: () => {} },
    {
      type: "submenu" as const,
      key: "color",
      label: "color",
      items: colorItems,
    },
    { key: "copy-path", label: "copy-path", onSelect: () => {} },
  ];
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><ContextMenu open point={{ left: 10, top: 10 }} ariaLabel="projectMenu" items={menuItems} onClose={() => {}} /></StrictMode>);
    await flush();
  });

  // 未展开时一级面板里没有任何颜色叶——一级只有 3 行。
  eq(root_cooked_rootPanelRowCount(), 3, "before expansion the level-1 panel holds only its own rows (no flattened colors)");

  // hover 停留展开。
  const trigger = findMenuItem("color")!;
  await act(async () => {
    hoverEnter(trigger);
    await dwell();
  });
  const panel = flyoutPanel();
  ok(Boolean(panel), "dwelling on 「颜色 ▸」 opens the floating flyout (hover, no click)");
  eq(panel?.querySelectorAll("button[role=\"menuitem\"]").length, 9, "the flyout carries all 9 color leaves");

  // 点叶触发落盘回调（flyout 内叶子点击 = 全链关闭）。
  const blueLeaf = [...(panel?.querySelectorAll<HTMLButtonElement>("button[role=\"menuitem\"]") ?? [])]
    .find((b) => b.textContent === "blue");
  await act(async () => { blueLeaf?.click(); await flush(); });
  eq(picked, ["blue"], "clicking a color leaf fires the setProjectColor callback chain");

  // 键盘可进：→ 在触发钮上打开面板、焦点落首叶。
  const triggerAgain = findMenuItem("color")!;
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

function root_cooked_rootPanelRowCount(): number {
  const rootPanel = document.querySelector<HTMLElement>(".context-menu:not(.context-menu--flyout)");
  return rootPanel?.querySelectorAll("button[role=\"menuitem\"]").length ?? 0;
}

process.stdout.write(`\ntl1200: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
dom.window.close();
