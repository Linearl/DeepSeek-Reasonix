// Run: tsx src/__tests__/context-menu-submenu.test.tsx
//
// Task 720: the ContextMenu submenu type — the second-level section a topic's
// "move to group" entry is built on. Covers expand (click and hover), disabled
// leaves, nested selection closing the whole menu, and re-expansion after a
// close so a stale expansion cannot leak into the next open.

import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextMenu, type ContextMenuItem } from "../components/ContextMenu";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
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

let alphaSelected = false;
let twoSelected = false;
let closeCount = 0;
const items: ContextMenuItem[] = [
  { key: "alpha", label: "Alpha", onSelect: () => { alphaSelected = true; } },
  {
    type: "submenu",
    key: "move",
    label: "Move",
    items: [
      { key: "one", label: "One", disabled: true, onSelect: () => { twoSelected = true; } },
      { key: "two", label: "Two", onSelect: () => { twoSelected = true; } },
    ],
  },
];

function menu(): HTMLElement {
  return document.body.querySelector(".context-menu")!;
}
function row(label: string): HTMLButtonElement | null {
  return Array.from(menu().querySelectorAll<HTMLButtonElement>("button[role=\"menuitem\"]"))
    .find((button) => button.textContent?.includes(label)) ?? null;
}

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(<StrictMode>
    <ContextMenu
      open
      point={{ left: 12, top: 12 }}
      items={items}
      onClose={() => { closeCount += 1; }}
    />
  </StrictMode>);
});

ok(Boolean(menu()), "open renders the menu");
ok(row("Two") === null, "no second-level rows before the section expands");

// Click expands too (keyboard shares this path via Enter).
await act(async () => { row("Move")!.click(); });
ok(row("Two") !== null, "clicking the submenu row expands its leaves in place");
ok(row("One")!.disabled, "a disabled leaf renders disabled");
ok(!row("Two")!.disabled, "the selectable leaf is enabled");

await act(async () => { row("One")!.click(); });
ok(!twoSelected, "clicking a disabled leaf does nothing");
ok(row("Two") !== null, "the section stays expanded after a disabled click");

await act(async () => { row("Two")!.click(); });
ok(twoSelected, "selecting an expanded leaf fires its onSelect");
ok(closeCount === 1, "selecting an expanded leaf closes the whole menu");
ok(row("Two") === null, "the expansion is reset with the close");

// Re-expansion after the reset, then toggle: a second click on the section row
// collapses it without closing the menu.
await act(async () => { row("Move")!.click(); });
ok(row("Two") !== null, "the section re-expands after the reset");
await act(async () => { row("Move")!.click(); });
ok(row("Two") === null, "clicking the expanded section row collapses it");
ok(closeCount === 1, "toggling the section does not close the menu");

await act(async () => { row("Alpha")!.click(); });
ok(alphaSelected, "top-level rows keep working");
ok(closeCount === 1, "a top-level selection does not close the menu by itself");

await act(async () => root.unmount());
dom.window.close();

process.stdout.write(`\ncontext-menu-submenu: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
