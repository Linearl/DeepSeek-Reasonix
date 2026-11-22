// Run: tsx src/__tests__/tl1745-compact-focus-hint.test.tsx
//
// tl1745: /compact accepts a free-form focus argument (tl1723 wiring), but the
// slash surface never said so — the menu description was a bare "压缩上下文"
// and nothing hinted at the parameter while typing. This pins:
//
// 1. the three locales carry the focus hint copy (the menu description's
//    "[聚焦词]" marker lives in the Go i18n catalogs, asserted over there);
// 2. slashMenuRows rides the non-pickable hint row at the bottom whenever the
//    compact command is reachable — visible and enabled;
// 3. no compact row (filtered out or disabled) means no hint row, and the
//    rendered menu shrinks accordingly (sizer height, not per-row mounts —
//    the jsdom virtualizer mounts no rows, a pre-existing quirk shared with
//    virtual-menu-identity.test.tsx).

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { slashMenuRows, SlashMenu } from "../components/SlashMenu";
import { LocaleProvider } from "../lib/i18n";
import { en, type DictKey } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import type { CommandInfo } from "../lib/types";

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
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// Locale copy: three languages stay in sync (type-level parity is enforced by
// DictKey; these pin the exact sentences).
eq(en["slash.compactFocusHint"], "Optionally add a focus hint to steer what compact keeps", "en carries the compact focus hint copy");
eq(zh["slash.compactFocusHint"], "可加聚焦词，指定压缩保留的重点", "zh carries the compact focus hint copy");
eq(zhTW["slash.compactFocusHint"], "可加聚焦詞，指定壓縮保留的重點", "zh-TW carries the compact focus hint copy");

const enT = (key: DictKey) => en[key];

const withCompact: CommandInfo[] = [
  { name: "clear", description: "discard current context", kind: "builtin", group: "actions" },
  { name: "compact", description: "compact context [focus]", kind: "builtin", group: "actions" },
];
const withoutCompact: CommandInfo[] = [withCompact[0]!];

console.log("\ncompact focus hint row layout");

const rowsWith = slashMenuRows(withCompact, enT);
const hintRow = rowsWith.find((row) => row.type === "hint");
ok(Boolean(hintRow), "menu rows with compact include the hint row");
eq(hintRow && "label" in hintRow ? hintRow.label : null, en["slash.compactFocusHint"], "hint row renders the localized copy");
eq(rowsWith[rowsWith.length - 1]?.type, "hint", "hint row rides at the bottom of the menu");
eq(rowsWith.filter((row) => row.type === "command").length, 2, "hint row adds no command row");

const rowsWithout = slashMenuRows(withoutCompact, enT);
eq(rowsWithout.filter((row) => row.type === "hint").length, 0, "menu rows without compact show no hint row");

const rowsDisabled = slashMenuRows(withCompact, enT, (c) => c.name === "compact");
eq(rowsDisabled.filter((row) => row.type === "hint").length, 0, "disabled compact shows no hint row");

// DOM check: the hint row participates in the menu's virtualized layout. jsdom
// mounts no virtual rows (see header), but the sizer's height sums the row
// estimates — 26 (group) + 34 + 34 (commands) + 26 (hint) vs 94 without.
class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
function rect(height: number): DOMRect {
  return {
    left: 0,
    top: 0,
    right: 600,
    bottom: height,
    width: 600,
    height,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  } as DOMRect;
}
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Node = dom.window.Node;
globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
dom.window.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(dom.window.HTMLElement.prototype, "getBoundingClientRect", {
  configurable: true,
  value(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return rect(360);
    return rect(0);
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", {
  configurable: true,
  get() {
    return 600;
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 360;
    return 0;
  },
});

function Menu({ items }: { items: CommandInfo[] }) {
  return (
    <LocaleProvider>
      <SlashMenu items={items} activeIndex={0} onPick={() => {}} onHover={() => {}} />
    </LocaleProvider>
  );
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);

await act(async () => {
  root.render(<Menu items={withCompact} />);
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const sizerWith = document.querySelector<HTMLElement>(".slashmenu__sizer");
eq(sizerWith?.style.height, "120px", "menu with compact sizes in the hint row (120px)");

await act(async () => {
  root.render(<Menu items={withoutCompact} />);
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const sizerWithout = document.querySelector<HTMLElement>(".slashmenu__sizer");
eq(sizerWithout?.style.height, "60px", "menu without compact drops the hint row (60px)");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
