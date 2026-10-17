// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/settings-select-flip.test.tsx
// 940 返修：权限下拉框在按钮贴近窗口底部时必须向上翻转，菜单完整收在视口内。

import { JSDOM } from "jsdom";
import { useRef } from "react";
import type { ReactElement } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { AnchoredPopover } from "../components/AnchoredPopover";
import { SettingsSelect } from "../components/SettingsSelect";

type RectParts = Pick<DOMRect, "left" | "top" | "right" | "bottom" | "width" | "height">;

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    ok(true, label);
  } else {
    ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

function rect({ left, top, right, bottom, width, height }: RectParts): DOMRect {
  return {
    left,
    top,
    right,
    bottom,
    width,
    height,
    x: left,
    y: top,
    toJSON: () => ({}),
  } as DOMRect;
}

async function nextFrame() {
  await act(async () => {
    await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
  });
}

function Harness({ placement }: { placement: "auto" | "bottom" | "flip" }) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  return (
    <>
      <button ref={anchorRef} data-testid="anchor">Anchor</button>
      <AnchoredPopover open anchorRef={anchorRef} onClose={() => {}} className="test-popover" placement={placement} offset={4}>
        <div>Menu</div>
      </AnchoredPopover>
    </>
  );
}

console.log("\nsettings select flip positioning");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as never;

Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });

// Mutable geometry: the tests reposition the anchor / resize the menu per case.
let anchorRect = rect({ left: 60, top: 100, right: 260, bottom: 130, width: 200, height: 30 });
let menuRect = rect({ left: 0, top: 0, right: 240, bottom: 120, width: 240, height: 120 });
const originalGetBoundingClientRect = dom.window.HTMLElement.prototype.getBoundingClientRect;
dom.window.HTMLElement.prototype.getBoundingClientRect = function getBoundingClientRect() {
  if (this instanceof dom.window.HTMLElement && this.dataset.testid === "anchor") return anchorRect;
  if (this instanceof dom.window.HTMLElement && this.classList.contains("settings-select")) return anchorRect;
  if (this instanceof dom.window.HTMLElement && this.dataset.anchoredPopover === "active") return menuRect;
  return originalGetBoundingClientRect.call(this);
};

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
let root = createRoot(rootEl);

async function mount(node: ReactElement) {
  await act(async () => root.render(node));
  await nextFrame();
}

async function reset() {
  await act(async () => root.unmount());
  root = createRoot(rootEl);
}

function activePopover(): HTMLElement {
  const node = document.querySelector<HTMLElement>("[data-anchored-popover='active']");
  if (!node) throw new Error("popover did not render");
  return node;
}

await mount(<Harness placement="flip" />);
let popover = activePopover();

eq(popover.style.top, "134px", "flip: space below holds the menu, it opens below the anchor");

anchorRect = rect({ left: 60, top: 520, right: 260, bottom: 550, width: 200, height: 30 });
menuRect = rect({ left: 0, top: 0, right: 240, bottom: 200, width: 240, height: 200 });
await act(async () => window.dispatchEvent(new Event("scroll", { bubbles: true })));
await nextFrame();

eq(popover.style.top, "316px", "flip: no space below triggers the upward flip (menu bottom = anchor.top - offset)");
eq(316 + 200, 520 - 4, "flip: flipped menu bottom sits exactly at the anchor top minus offset");

anchorRect = rect({ left: 60, top: 100, right: 260, bottom: 130, width: 200, height: 30 });
menuRect = rect({ left: 0, top: 0, right: 240, bottom: 458, width: 240, height: 458 });
await act(async () => window.dispatchEvent(new Event("scroll", { bubbles: true })));
await nextFrame();

eq(popover.style.top, "134px", "flip: menu bottom exactly reaching the viewport edge stays below the anchor");

anchorRect = rect({ left: 60, top: 40, right: 260, bottom: 70, width: 200, height: 30 });
menuRect = rect({ left: 0, top: 0, right: 240, bottom: 560, width: 240, height: 560 });
await act(async () => window.dispatchEvent(new Event("scroll", { bubbles: true })));
await nextFrame();

eq(popover.style.top, "32px", "flip: neither side fits falls back to the clamped below position");
eq(32 + 560, 600 - 8, "flip: oversized menu stays pinned inside the viewport bottom edge");

anchorRect = rect({ left: 60, top: 300, right: 260, bottom: 330, width: 200, height: 30 });
menuRect = rect({ left: 0, top: 0, right: 240, bottom: 100, width: 240, height: 100 });

// The legacy "auto" mode keeps its prefer-above semantics for other callers.
await reset();
await mount(<Harness placement="auto" />);
popover = activePopover();
eq(popover.style.top, "196px", "auto: prefers opening above the anchor when the space holds");

// Integration: the settings dropdown near the window bottom flips up instead
// of spilling past the button and the viewport bottom edge.
async function mountSelect(trigger: RectParts) {
  await reset();
  anchorRect = rect(trigger);
  menuRect = rect({ left: 0, top: 0, right: 200, bottom: 200, width: 200, height: 200 });
  await mount(
    <SettingsSelect aria-label="Permission" value="ask" onValueChange={() => {}}
      options={[{ value: "ask", label: "Ask" }, { value: "auto", label: "Auto" }]} />,
  );
  const button = document.querySelector<HTMLButtonElement>(".settings-select");
  if (!button) throw new Error("settings select did not render");
  await act(async () => { button.click(); });
  await nextFrame();
}

await mountSelect({ left: 60, top: 520, right: 260, bottom: 556, width: 200, height: 36 });
popover = activePopover();
eq(popover.style.top, "316px", "settings select near the window bottom flips the menu above the trigger");
ok(316 + 200 <= 520 && 316 >= 8, "settings select flipped menu stays fully inside the viewport above the trigger");

await mountSelect({ left: 60, top: 200, right: 260, bottom: 236, width: 200, height: 36 });
popover = activePopover();
eq(popover.style.top, "240px", "settings select with room below keeps opening under the trigger");
ok(240 + 200 <= 600 - 8, "settings select menu below the trigger stays inside the viewport");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
