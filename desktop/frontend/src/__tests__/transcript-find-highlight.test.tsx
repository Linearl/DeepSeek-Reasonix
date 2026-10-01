// Run: tsx src/__tests__/transcript-find-highlight.test.tsx
// Task 399: find highlight rides context down to rows — hit set + active row
// render as data attributes/classes; closed bar (null) renders neither.

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { document: Document }).document = dom.window.document;
(globalThis as unknown as { window: Window }).window = dom.window as unknown as Window;
if (!("navigator" in globalThis)) {
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
}

import { TranscriptBlockView } from "../components/TranscriptBlockView";
import { TranscriptFindContext } from "../components/TranscriptFindContext";
import type { TranscriptRow } from "../lib/transcriptRows";
import type { TimelineBlock } from "../lib/transcriptTimeline";
import type { TranscriptFindHighlight } from "../lib/transcriptFind";

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

const rows = [
  { kind: "user", key: "u:1", item: { kind: "user", id: "1", text: "first" }, turn: 0, layoutVariant: "text-flow" },
  { kind: "answer", key: "a:1", item: { kind: "assistant", id: "1", text: "second", reasoning: "", streaming: false }, layoutVariant: "text-flow" },
  { kind: "answer", key: "a:2", item: { kind: "assistant", id: "2", text: "third", reasoning: "", streaming: false }, layoutVariant: "text-flow" },
] as unknown as TranscriptRow[];

const block: TimelineBlock = {
  key: "b1",
  phase: "completed",
  rows: rows as TimelineBlock["rows"],
  contentRevision: 1,
  measurementRevision: "1",
};

async function render(find: TranscriptFindHighlight) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <TranscriptFindContext.Provider value={find}>
        <TranscriptBlockView block={block} renderRow={(row) => <span>{row.kind}</span>} />
      </TranscriptFindContext.Provider>,
    );
  });
  return {
    container,
    unmount: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
    rowAttrs: (key: string) => {
      const el = container.querySelector(`[data-row-key="${key}"]`);
      return {
        found: el !== null,
        hit: el?.getAttribute("data-find-hit") ?? null,
        active: el?.getAttribute("data-find-active") ?? null,
        className: el?.getAttribute("className") ?? el?.className ?? "",
      };
    },
  };
}

console.log("\ntranscript find highlight (task 399)");

// 1. Bar closed → no highlight attributes anywhere
{
  const h = await render(null);
  eq(h.rowAttrs("u:1").hit, null, "closed bar: first row has no hit marker");
  eq(h.rowAttrs("a:1").active, null, "closed bar: no active marker");
  eq(String(h.rowAttrs("u:1").className).includes("transcript__row--find"), false, "closed bar: no find class");
  await h.unmount();
}

// 2. Active hit is also a hit; non-active hits are hits only
{
  const h = await render({ hits: new Set(["u:1", "a:2"]), active: "a:2" });
  eq(h.rowAttrs("u:1").hit, "true", "hit row carries data-find-hit");
  eq(h.rowAttrs("u:1").active, null, "hit row is not active");
  eq(String(h.rowAttrs("u:1").className).includes("transcript__row--find"), true, "hit row gets the find class");
  eq(h.rowAttrs("a:2").hit, "true", "active row also counts as a hit");
  eq(h.rowAttrs("a:2").active, "true", "active row carries data-find-active");
  eq(String(h.rowAttrs("a:2").className).includes("transcript__row--find-active"), true, "active row gets the active class");
  eq(h.rowAttrs("a:1").hit, null, "miss row stays untouched");
  await h.unmount();
}

// 3. Hit set with no active row (cursor between queries)
{
  const h = await render({ hits: new Set(["a:1"]), active: null });
  eq(h.rowAttrs("a:1").hit, "true", "hit still renders without an active row");
  eq(h.rowAttrs("a:1").active, null, "no active marker when active is null");
  await h.unmount();
}

console.log(`\n${passed}/${passed + failed} passed`);
if (failed > 0) process.exit(1);
