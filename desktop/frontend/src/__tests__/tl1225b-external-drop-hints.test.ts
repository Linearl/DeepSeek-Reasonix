// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1225b-external-drop-hints.test.ts
//
// tl 1225b 验收钉（1225 扩面：侧栏外三处「外置细线」drop 提示统一为 inset 实心条）:
//   ① 状态栏设置行（.status-bar-item-row--drop-*，StatusBarItemsEditor 域）
//   ② 侧停靠标签条（.side-dock-tab--drop-*，SideDockTabStrip 域）
//   ③ 经典模式页签（.tabbar__tab--drop-*，TabBar 域，含 theme 层特异性分支）
//   三处与 tl1225 的对话行/项目树同机制：inset 2px 实心条画在行框内最近边，
//   旧「画在行框外的伪元素细线」清零（回归即红）。
//   类名组装、拖拽状态与落盘链零改动（提示层只动 CSS，不碰 TSX）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(here, "../styles.css"), "utf8");
const classic = readFileSync(resolve(here, "../styles/mode-classic.css"), "utf8");
const statusBar = readFileSync(resolve(here, "../components/StatusBarItemsEditor.tsx"), "utf8");
const sideDock = readFileSync(resolve(here, "../components/SideDockTabStrip.tsx"), "utf8");
const tabBar = readFileSync(resolve(here, "../components/TabBar.tsx"), "utf8");

// 取「选择器组（可含逗号分组）规则」的声明体：定位选择器文本后取其后第一个 {..}。
function ruleBody(source: string, selector: string): string {
  const index = source.indexOf(selector);
  if (index < 0) return "";
  const open = source.indexOf("{", index);
  const close = source.indexOf("}", open);
  return source.slice(open + 1, close);
}

function countOccurrences(source: string, needle: string): number {
  return source.split(needle).length - 1;
}

console.log("\ntl1225b ① status-bar rows: inset bars (tl1225 mechanism)");

{
  ok(
    ruleBody(css, ".status-bar-item-row--drop-before {").includes("box-shadow: inset 0 2px 0 var(--accent);")
      && ruleBody(css, ".status-bar-item-row--drop-after {").includes("box-shadow: inset 0 -2px 0 var(--accent);"),
    "status-bar row before/after hints are inset bars INSIDE the row box",
  );
}

console.log("\ntl1225b ② side-dock tabs: inset bars (tl1225 mechanism)");

{
  ok(
    ruleBody(css, ".side-dock-tab--drop-before {").includes("box-shadow: inset 2px 0 0 var(--accent);")
      && ruleBody(css, ".side-dock-tab--drop-after {").includes("box-shadow: inset -2px 0 0 var(--accent);"),
    "side-dock tab before/after hints are inset bars INSIDE the tab box",
  );
}

console.log("\ntl1225b ③ classic tabbar tabs: inset bars, both default and theme layers");

{
  ok(
    ruleBody(classic, ".tabbar__tab--drop-before,").includes("box-shadow: inset 2px 0 0 var(--accent);")
      && ruleBody(classic, ".tabbar__tab--drop-after,").includes("box-shadow: inset -2px 0 0 var(--accent);"),
    "tabbar before/after hints are inset bars (grouped rule, default layer)",
  );
  ok(
    classic.includes(":root[data-theme-style] .app-chrome--tabs .tabbar__tab--drop-before {")
      && classic.includes(":root[data-theme-style] .app-chrome--tabs .tabbar__tab--drop-after {"),
    "the theme-scoped branch (0,4,0) is present to out-rank `:root[data-theme-style] .tabbar__tab { box-shadow: none }` and the themed --active shadow",
  );
}

console.log("\ntl1225b ④ retired outset hairline selectors stay gone (regression = red)");

{
  const retiredStyles = [
    ".status-bar-item-row--drop-before::before",
    ".status-bar-item-row--drop-after::after",
    ".side-dock-tab--drop-before::before",
    ".side-dock-tab--drop-after::after",
  ];
  ok(
    retiredStyles.every((selector) => !css.includes(selector)),
    "styles.css: status-bar/side-dock outset hairline selectors are 0-occurrence",
  );
  ok(
    !classic.includes(".tabbar__tab--drop-before::before")
      && !classic.includes(".tabbar__tab--drop-after::after"),
    "mode-classic.css: tabbar drop hairline pseudo-element selectors are 0-occurrence",
  );
  ok(
    classic.includes(".tabbar__tab + .tabbar__tab:not(.tabbar__tab--drop-before)::before"),
    "the tab separator's :not(--drop-before) guard stays untouched (pinned by app-chrome-tabs.test.ts)",
  );
}

console.log("\ntl1225b ⑤ class composition and drag states untouched (hint layer = CSS only)");

{
  ok(
    statusBar.includes('targetPlacement === "before" ? "status-bar-item-row--drop-before" : ""')
      && statusBar.includes('targetPlacement === "after" ? "status-bar-item-row--drop-after" : ""'),
    "StatusBarItemsEditor still composes both drop-position hint classes",
  );
  ok(
    sideDock.includes('? "side-dock-tab--drop-before"') && sideDock.includes(': "side-dock-tab--drop-after"')
      && sideDock.includes("event.clientX < rect.left + rect.width / 2"),
    "SideDockTabStrip still composes the before/after hint via the pointer-half split",
  );
  ok(
    tabBar.includes("tabbar__tab--drop-${dropTarget.side}"),
    "TabBar still composes the drop-side hint class from dropTarget.side",
  );
}

console.log(`\ntl1225b: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
