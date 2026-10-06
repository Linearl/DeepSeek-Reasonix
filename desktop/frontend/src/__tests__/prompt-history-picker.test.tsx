// Run: tsx src/__tests__/prompt-history-picker.test.tsx
// Task 261 (upstream #10425): composer history-navigation safety behind the
// experimental prompt-history-picker switch (default off).
//
// Contract under test:
// - canUsePromptHistory: default keeps the legacy trigger byte for byte
//   (caret-at-position-0 starts history); with upStartsOnlyFromEmpty on,
//   plain ArrowUp only STARTS history from an empty composer or while already
//   browsing - editing multi-line text and pressing ArrowUp just moves the
//   caret. ArrowDown semantics are untouched in both modes.
// - PromptHistoryPicker panel: lists entries, first-line summary with
//   ellipsis, empty state, load-more wiring, pick returns the entry text,
//   Escape closes. Picking inserts at the caret (host side) and never
//   replaces the unsent draft.
// - 任务529 host chain: the clock button click MUST toggle the panel open
//   (never a silent no-op); the panel renders through AnchoredPopover (portaled
//   to body — .composer-meta has overflow:hidden and clipped the old
//   absolutely-positioned panel into invisibility); empty history still shows
//   a visible empty-state hint.
// - Wiring (source-shape): Composer gates the trigger + the button on
//   historyPickerEnabled; App passes the boot-snapshot flag; SettingsPanel
//   flips the config through the new bridge method; locales carry the keys
//   in all three languages.

import { readFileSync } from "node:fs";
import { createElement } from "react";
import { useState, useRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
// jsdom ships no matchMedia; AnchoredPopover consults prefers-reduced-motion.
(dom.window as unknown as { matchMedia: (query: string) => MediaQueryList }).matchMedia = (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
}) as MediaQueryList;
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// AnchoredPopover's outside-close guard does `target instanceof Node` — jsdom
// type globals must be exposed for the test process.
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { Element: unknown }).Element = dom.window.Element;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const { canUsePromptHistory } = await import("../lib/composerKeyboard");
const { PromptHistoryPicker } = await import("../components/PromptHistoryPicker");
const { AnchoredPopover } = await import("../components/AnchoredPopover");
const { LocaleProvider } = await import("../lib/i18n");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

// 任务529 host probe: the exact wiring Composer uses — clock button toggles
// open, the panel renders inside an AnchoredPopover portaled to document.body.
function HistoryHostProbe({ entries }: { entries: readonly { text: string; at: number }[] }) {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLButtonElement>(null);
  return createElement(LocaleProvider, { locale: "en" },
    createElement("div", null,
      createElement("button", { id: "probe-clock", ref: anchorRef, onClick: () => setOpen((v) => !v) }, "clock"),
      createElement(AnchoredPopover, {
        open,
        anchorRef,
        onClose: () => setOpen(false),
        className: "composer-history-menu composer-menu-surface",
        align: "end",
      },
        createElement(PromptHistoryPicker, {
          entries, hasMore: false, loading: false,
          onPick: () => {}, onLoadMore: () => {}, onClose: () => setOpen(false),
          t: (key: string) => key,
        } as never)),
    ),
  );
}

const base = {
  direction: "up" as const,
  menuOpen: false,
  composing: false,
  altKey: false,
  ctrlKey: false,
  metaKey: false,
  shiftKey: false,
  fnKey: false,
  historyIndex: -1,
};

function eligibility(overrides: Partial<typeof base & { value: string; selectionStart: number | null; selectionEnd: number | null; upStartsOnlyFromEmpty?: boolean }>) {
  const { upStartsOnlyFromEmpty, ...rest } = overrides;
  return canUsePromptHistory({ ...base, ...rest, upStartsOnlyFromEmpty } as never) as boolean;
}

console.log("\nprompt history picker (task 261)");

// 1. Legacy behaviour preserved when the flag is off/absent.
{
  eq(eligibility({ value: "draft text", selectionStart: 0, selectionEnd: 0 }), true, "legacy: caret at 0 starts history (bug reproduced)");
  eq(eligibility({ value: "", selectionStart: 0, selectionEnd: 0 }), true, "legacy: empty composer starts history");
  eq(eligibility({ value: "draft", selectionStart: 3, selectionEnd: 3 }), false, "legacy: caret mid-text never starts history");
  eq(eligibility({ direction: "down", value: "draft", selectionStart: 5, selectionEnd: 5, historyIndex: 1 }), true, "legacy: down continues while browsing at the end");
  eq(eligibility({ direction: "down", value: "draft", selectionStart: 1, selectionEnd: 1, historyIndex: 1 }), false, "legacy: down needs caret at the end");
}
// 2. Narrowed trigger while the flag is on.
{
  eq(eligibility({ value: "multi\nline draft", selectionStart: 0, selectionEnd: 0, upStartsOnlyFromEmpty: true }), false, "narrow: editing multi-line text, ArrowUp only moves the caret");
  eq(eligibility({ value: "x", selectionStart: 0, selectionEnd: 0, upStartsOnlyFromEmpty: true }), false, "narrow: non-empty composer never starts history");
  eq(eligibility({ value: "", selectionStart: 0, selectionEnd: 0, upStartsOnlyFromEmpty: true }), true, "narrow: empty composer still starts history");
  eq(eligibility({ value: "browsed", selectionStart: 0, selectionEnd: 0, historyIndex: 2, upStartsOnlyFromEmpty: true }), true, "narrow: already browsing keeps ArrowUp navigation");
  eq(eligibility({ direction: "down", value: "draft", selectionStart: 5, selectionEnd: 5, historyIndex: 1, upStartsOnlyFromEmpty: true }), true, "narrow: down semantics unchanged");
  eq(eligibility({ value: "draft", selectionStart: 0, selectionEnd: 0, upStartsOnlyFromEmpty: false }), true, "narrow: explicit off = legacy");
}
// 3. IME/menu guards stay in force in both modes.
{
  eq(eligibility({ value: "", selectionStart: 0, selectionEnd: 0, composing: true, upStartsOnlyFromEmpty: true }), false, "guard: composing never switches history");
  eq(eligibility({ value: "", selectionStart: 0, selectionEnd: 0, menuOpen: true, upStartsOnlyFromEmpty: true }), false, "guard: open menu never switches history");
}

// 4. Panel rendering.
const longLine = "A".repeat(120);
const entries = [
  { text: "most recent prompt", at: new Date(2026, 8, 29, 12, 30).getTime() },
  { text: `${longLine}\nwith a second line`, at: new Date(2026, 8, 29, 9, 5).getTime() },
];
{
  const t = (key: string) => key;
  const markup = renderToStaticMarkup(createElement(LocaleProvider, { locale: "en" }, createElement(PromptHistoryPicker, {
    entries, hasMore: true, loading: false,
    onPick: () => {}, onLoadMore: () => {}, onClose: () => {}, t,
  } as never)));
  eq(markup.includes("most recent prompt"), true, "panel: lists the newest entry");
  eq(markup.includes(`${"A".repeat(96)}…`), true, "panel: long first lines truncate with an ellipsis");
  eq(markup.includes(`>${"A".repeat(96)}…</span>`), true, "panel: the truncation is the visible summary span (full text lives only in the title tooltip)");
  eq(markup.includes("composer.historyPickerMore"), true, "panel: load-more button renders when more pages exist");
}
{
  const t = (key: string) => key;
  const markup = renderToStaticMarkup(createElement(LocaleProvider, { locale: "en" }, createElement(PromptHistoryPicker, {
    entries: [], hasMore: false, loading: false,
    onPick: () => {}, onLoadMore: () => {}, onClose: () => {}, t,
  } as never)));
  eq(markup.includes("composer.historyPickerEmpty"), true, "panel: friendly empty state");
  eq(markup.includes('class="composer-history-menu__empty"'), true, "panel: the empty state is a visible dedicated row (任务529)");
  eq(markup.includes("composer.historyPickerMore"), false, "panel: no load-more when the tape is exhausted");
}

// 5. Panel interactions: pick + load-more + Escape.
{
  let picked = "";
  let loadedMore = 0;
  let closed = 0;
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  const t = (key: string) => key;
  await act(async () => {
    root.render(createElement(LocaleProvider, { locale: "en" }, createElement(PromptHistoryPicker, {
      entries, hasMore: true, loading: false,
      onPick: (text: string) => { picked = text; },
      onLoadMore: () => { loadedMore += 1; },
      onClose: () => { closed += 1; },
      t,
    } as never)));
  });
  const items = Array.from(container.querySelectorAll<HTMLButtonElement>(".composer-history-menu__item"));
  eq(items.length, 2, "interact: two history rows render");
  await act(async () => { items[1]!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  eq(picked, `${longLine}\nwith a second line`, "interact: pick returns the FULL entry text (newlines included)");
  const more = container.querySelector<HTMLButtonElement>(".composer-history-menu__more");
  await act(async () => { more!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  eq(loadedMore, 1, "interact: load-more fires the host loader");
  const dialog = container.querySelector<HTMLDivElement>(".composer-history-menu");
  await act(async () => { dialog!.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
  eq(closed, 1, "interact: Escape closes the picker");
  await act(async () => root.unmount());
  container.remove();
}

// 6. 任务529 host chain: click opens the portaled panel, outside click closes,
//    and EMPTY history still opens with a visible hint (never a silent no-op).
{
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => { root.render(createElement(HistoryHostProbe, { entries } as never)); });
  eq(dom.window.document.querySelector(".composer-history-menu"), null, "host: panel absent before any click");

  const clock = container.querySelector<HTMLButtonElement>("#probe-clock")!;
  await act(async () => { clock.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  const opened = dom.window.document.querySelector(".composer-history-menu");
  eq(opened !== null, true, "host: clock click opens the panel (portaled to body, outside overflow clips)");
  eq(opened?.textContent?.includes("most recent prompt") ?? false, true, "host: opened panel lists the history entries");

  await act(async () => { dom.window.document.body.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 220)); });
  eq(dom.window.document.querySelector(".composer-history-menu"), null, "host: outside click closes the panel");
  await act(async () => root.unmount());
  container.remove();
}
{
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => { root.render(createElement(HistoryHostProbe, { entries: [] } as never)); });
  await act(async () => { container.querySelector<HTMLButtonElement>("#probe-clock")!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  const opened = dom.window.document.querySelector(".composer-history-menu");
  eq(opened !== null, true, "host: click with EMPTY history still opens the panel (no silent no-op)");
  eq(opened?.querySelector(".composer-history-menu__empty") !== null, true, "host: empty panel renders the dedicated empty-state row");
  eq(opened?.textContent?.includes("composer.historyPickerEmpty") ?? false, true, "host: empty-state row carries the localized hint");
  await act(async () => root.unmount());
  container.remove();
}

// 7. Wiring (source-shape; Composer is too heavy to mount here).
{
  const composer = readFileSync(new URL("../components/Composer.tsx", import.meta.url), "utf8");
  eq(composer.includes("upStartsOnlyFromEmpty: historyPickerEnabled"), true, "wiring: ArrowUp trigger follows the switch");
  eq(composer.includes("historyPickerEnabled && ("), true, "wiring: the picker button/popup render only while the switch is on");
  eq(composer.includes("<PromptHistoryPicker"), true, "wiring: the popup is the shared panel component");
  eq(composer.includes("textRef.current = next;\n    setText(next);"), true, "wiring: pick path goes through the draft setters");
  eq(/pickHistoryEntry[\s\S]{0,900}setHistoryPickerOpen\(false\)/.test(composer), true, "wiring: picking closes the popup");
  eq(composer.includes("const closeHistoryPicker"), true, "wiring: close helper exists");
  eq(/const openHistoryPicker = \(\) => \{\s*if \(!historyPickerEnabled\) return;\s*setHistoryPickerOpen\(true\);/.test(composer), true, "wiring: the click handler sets the panel open (任务529 钉死)");
  eq(composer.includes("onClick={() => (historyPickerOpen ? closeHistoryPicker() : openHistoryPicker())}"), true, "wiring: the clock button toggles open/close");
  eq(composer.includes("ref={historyPickerAnchorRef}"), true, "wiring: the clock button is the popover anchor");
  eq(composer.includes("<AnchoredPopover"), true, "wiring: the panel renders through AnchoredPopover (portaled past overflow:hidden)");
  eq(composer.includes("className=\"composer-history-menu composer-menu-surface\""), true, "wiring: the surface spec rides the portal container");
  eq(composer.includes("composer-history-picker__backdrop"), false, "wiring: the clipped-era backdrop is gone (AnchoredPopover owns outside-close)");
  eq(composer.includes("historyPickerEnabled = false"), true, "wiring: the prop defaults to OFF (legacy behaviour)");

  const app = readFileSync(new URL("../App.tsx", import.meta.url), "utf8");
  eq(app.includes("historyPickerEnabled={promptHistoryPickerEnabled}"), true, "wiring: App passes the boot-snapshot flag to Composer");
  eq(app.includes("setPromptHistoryPickerEnabled(Boolean(settings.experimentalPromptHistoryPicker))"), true, "wiring: App reads the flag from the settings view");

  const settings = readFileSync(new URL("../components/SettingsPanel.tsx", import.meta.url), "utf8");
  eq(settings.includes('id: "promptHistoryPicker"'), true, "wiring: the lab shelf lists the switch");
  eq(settings.includes("app.SetExperimentalPromptHistoryPicker(on)"), true, "wiring: the toggle persists through the new bridge method");
  eq(settings.includes("setRestartNeeded(true)"), true, "wiring: the boot-snapshot switch raises the restart banner");

  const bridge = readFileSync(new URL("../lib/bridge.ts", import.meta.url), "utf8");
  eq(bridge.includes("SetExperimentalPromptHistoryPicker(enabled: boolean): Promise<void>;"), true, "wiring: bridge interface declares the setter");
  eq(bridge.includes("async SetExperimentalPromptHistoryPicker() {}"), true, "wiring: dev mock implements the setter");

  const keyboard = readFileSync(new URL("../lib/composerKeyboard.ts", import.meta.url), "utf8");
  eq(keyboard.includes("upStartsOnlyFromEmpty?: boolean;"), true, "wiring: the eligibility option is optional (callers untouched)");
}

// 8. Locales: every new key exists in all three languages.
{
  const keys = [
    '"settings.promptHistoryPicker"',
    '"settings.promptHistoryPickerHint"',
    '"composer.historyPicker"',
    '"composer.historyPickerEmpty"',
    '"composer.historyPickerMore"',
  ];
  for (const file of ["zh", "en", "zh-TW"]) {
    const src = readFileSync(new URL(`../locales/${file}.ts`, import.meta.url), "utf8");
    for (const key of keys) {
      eq(src.includes(key), true, `locales/${file}: ${key} present`);
    }
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
