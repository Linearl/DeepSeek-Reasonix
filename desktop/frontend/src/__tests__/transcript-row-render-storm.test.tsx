// Run: tsx src/__tests__/transcript-row-render-storm.test.tsx
// Task 735 (issue #43): the delete-topic unmount storm logged one 958ms long
// task whose top frames were transcript geometry callbacks (39 samples) plus
// passive unmount (7 samples). Two memo contracts keep untouched rows out of
// re-render storms: block/row views skip when their row data is unchanged
// (renderRow travels by context, no children-prop trap), and UserMessage is
// memoized like AssistantMessage so a re-rendered row skips the parser chain.

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { document: Document }).document = dom.window.document;
(globalThis as unknown as { window: Window }).window = dom.window as unknown as Window;
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
if (!("navigator" in globalThis)) {
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
}

import { UserMessage } from "../components/Message";
import { TranscriptBlockView } from "../components/TranscriptBlockView";
import { TranscriptRowRendererProvider, type TranscriptRowRenderer } from "../components/TranscriptRowRendererContext";
import { LocaleProvider } from "../lib/i18n";
import type { TranscriptRow } from "../lib/transcriptRows";
import type { TimelineBlock } from "../lib/transcriptTimeline";

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

function makeRow(key: string, text: string): TranscriptRow {
  return {
    kind: "user",
    key,
    item: { kind: "user", id: key.slice(2), text },
    turn: 0,
    layoutVariant: "text-flow",
  } as unknown as TranscriptRow;
}

function makeBlock(rows: TranscriptRow[], key = "b1"): TimelineBlock {
  return { key, phase: "completed", rows, contentRevision: 1, measurementRevision: "1" };
}

const BASE_ROWS = [makeRow("u:1", "first"), makeRow("u:2", "second"), makeRow("u:3", "third")];

function rowRenderer(calls: Map<string, number>): TranscriptRowRenderer {
  return (row) => {
    calls.set(String(row.key), (calls.get(String(row.key)) ?? 0) + 1);
    return <span>{String(row.key)}</span>;
  };
}

async function renderHost(block: TimelineBlock, renderRow: TranscriptRowRenderer) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <TranscriptRowRendererProvider renderRow={renderRow}>
        <TranscriptBlockView block={block} />
      </TranscriptRowRendererProvider>,
    );
  });
  return {
    async update(nextBlock: TimelineBlock, nextRenderer: TranscriptRowRenderer) {
      await act(async () => {
        root.render(
          <TranscriptRowRendererProvider renderRow={nextRenderer}>
            <TranscriptBlockView block={nextBlock} />
          </TranscriptRowRendererProvider>,
        );
      });
    },
    async unmount() {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

async function main() {
  // --- contract 1: parent re-render, same block + same renderer → no row re-render ---
  {
    const calls = new Map<string, number>();
    const renderer = rowRenderer(calls);
    const host = await renderHost(makeBlock(BASE_ROWS), renderer);
    eq([...calls.values()].reduce((a, b) => a + b, 0), 3, "initial mount renders every row once");
    await host.update(makeBlock(BASE_ROWS), renderer);
    eq([...calls.values()].reduce((a, b) => a + b, 0), 3, "same block + same renderer skips every row");
    await host.unmount();
  }

  // --- contract 2: rebuilt block object, same row objects → row memo still holds ---
  {
    const calls = new Map<string, number>();
    const renderer = rowRenderer(calls);
    const host = await renderHost(makeBlock(BASE_ROWS), renderer);
    await host.update(makeBlock([...BASE_ROWS]), renderer);
    eq([...calls.values()].reduce((a, b) => a + b, 0), 3, "rebuilt block keeps untouched rows memoized");
    await host.unmount();
  }

  // --- contract 3: a changed row re-renders, and only that row ---
  {
    const calls = new Map<string, number>();
    const renderer = rowRenderer(calls);
    const host = await renderHost(makeBlock(BASE_ROWS), renderer);
    const edited = [BASE_ROWS[0], makeRow("u:2", "second (edited)"), BASE_ROWS[2]];
    await host.update(makeBlock(edited), renderer);
    eq(calls.get("u:2"), 2, "edited row renders again");
    eq(calls.get("u:1"), 1, "untouched sibling row skipped");
    eq(calls.get("u:3"), 1, "untouched sibling row skipped");
    await host.unmount();
  }

  // --- contract 4: UserMessage is memoized (issue #43 recommendation, mirrors AssistantMessage) ---
  eq((UserMessage as unknown as { $$typeof?: symbol }).$$typeof, Symbol.for("react.memo"), "UserMessage is a memo component");

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
