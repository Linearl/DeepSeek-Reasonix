// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1766-subagent-live-flicker.test.tsx
// tl1766: the running subagent detail's 工作过程 area flickered every 2s —
// the live poll (tl1537 chain) reset the state to loading on EVERY pull
// whenever the reads still came back empty/pending, and the projection gate
// excluded live.loading, so each cycle unmounted the live tool-call stream
// (列表消失 → 正在读取历史… → pending+投影重现). Contract under test:
// - the loading note is a FIRST-READ-ONLY state (initial mount / retry bump /
//   running→ended re-subscribe); refresh polls keep whatever is on screen;
// - the memory projection (data-subagents-livetools) stays mounted while the
//   transcript has no content — including while a read is in flight;
// - the pending note and the loading note never alternate across polls;
// - a successful-but-empty mid-run result cannot wipe already-loaded
//   content (the autosave transcript only grows while the child runs);
// - recovery to the ready state (transcript takes over, projection yields)
//   and the follow hint are unchanged.

import assert from "node:assert/strict";
import { createElement } from "react";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
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
function ok(value: unknown, label: string) {
  eq(Boolean(value), true, label);
}

const { buildSubagentDirectory } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel, setDockLivePollMsForTests } = await import("../components/SubagentsDockPanel");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { HistoryMessage } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

let nextId = 0;
function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  nextId += 1;
  return {
    kind: "tool",
    id: `tool-${nextId}`,
    name: "task",
    args: '{"prompt":"研究一下"}',
    readOnly: false,
    status: "done",
    ...overrides,
  };
}

console.log("\ntl1766 running-detail live poll: no per-poll loading flicker");

// Deferred read bridge: each pull parks in `inflight` until the test
// resolves it, so the "read in flight" window — exactly where the old code
// flashed the loading note — is deterministic and sampleable.
type DeferredRead = {
  promise: Promise<HistoryMessage[]>;
  resolve: (messages: HistoryMessage[]) => void;
};
const inflight: DeferredRead[] = [];
const readRefs: string[] = [];
const onReadSubagent = (_sessionPath: string, ref: string) => {
  readRefs.push(ref);
  let resolve!: (messages: HistoryMessage[]) => void;
  const promise = new Promise<HistoryMessage[]>((res) => { resolve = res; });
  inflight.push({ promise, resolve });
  return promise;
};
// Strictly sequential: the busy guard keeps at most one read in flight, so
// "readRefs.length > settledReads && inflight.length > 0" pins exactly the
// next read, parked. Sample assertions run BEFORE settling — that parked
// window is where the old code flashed the loading note.
let settledReads = 0;
async function waitForReadInFlight(label: string) {
  await waitFor(`${label}: a read is in flight`, () => readRefs.length > settledReads && inflight.length > 0);
}
async function settleOldest(messages: HistoryMessage[]) {
  const next = inflight.shift();
  assert.ok(next, "settleOldest: an in-flight read exists");
  await act(async () => { next.resolve(messages); });
  settledReads += 1;
}
async function settleNextRead(messages: HistoryMessage[], label: string) {
  await waitForReadInFlight(label);
  await settleOldest(messages);
}

const flush = async (ms = 15) => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms));
  });
};
async function waitFor(label: string, cond: () => boolean, timeoutMs = 2000) {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) throw new Error(`waitFor timeout: ${label}`);
    await flush(10);
  }
}

try {
  setDockLivePollMsForTests(25);

  // A tool-heavy running child: the progress preview is empty, the live
  // tool-call stream (memory projection) is the visible work process.
  const runningCard = subagentTool({
    id: "call-live",
    status: "running",
    startedAt: 10_000,
    subject: "调研员3",
    subagentProgress: {
      phase: "tool",
      reasoning: "",
      text: "",
      notice: "",
      lastActivityAt: 11_000,
      truncated: false,
      startedAt: 10_000,
      ref: "sa_1766",
    },
  });
  const childA = subagentTool({ id: "call-live/web-a", name: "web_fetch", args: '{"url":"https://example.com/a"}', status: "done", parentId: "call-live" });
  const childB = subagentTool({ id: "call-live/web-b", name: "bash", args: '{"command":"ls"}', status: "running", parentId: "call-live" });
  const liveDirectory = buildSubagentDirectory([runningCard]);
  const transcriptItems: Item[] = [runningCard, childA, childB];

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: liveDirectory,
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
      transcriptItems,
    })));
  });
  await act(async () => {
    (container.querySelector(".subagents-panel__list .subagents-panel__row") as HTMLButtonElement).click();
  });

  // First read in flight: the loading note is allowed exactly here (首读窗口),
  // and — tl1766 fix B — the projection must NOT be unmounted by it.
  await waitFor("first read fires", () => readRefs.length >= 1);
  eq(readRefs[0], "sa_1766", "live: the read fires with the child ref");
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "loading",
    "live: the first in-flight read shows the loading state (首读窗口允许一次)");
  ok(container.querySelector('[data-subagents-livetools="true"]') !== null,
    "live: the projection stays mounted even while the first read is in flight (tl1766 B)");

  // First read lands empty (pre-first-snapshot): the pending note replaces the
  // loading note and the projection holds.
  await settleNextRead([], "empty first read");
  await flush();
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "empty",
    "live: an empty first read lands on the pending state");
  ok(container.querySelector("[data-subagents-live-pending]") !== null,
    "live: the empty window carries the pending note");
  ok(container.querySelector('[data-subagents-livetools="true"]') !== null,
    "live: the projection holds through the empty landing");

  // ≥3 further poll cycles: at EVERY sampled in-flight window the state must
  // stay the pending shape — never back to loading, never unmounting the
  // projection, never swapping the pending note for the loading note. This is
  // the user-visible flicker, made deterministic via the deferred bridge.
  for (let cycle = 1; cycle <= 3; cycle += 1) {
    await waitForReadInFlight(`cycle ${cycle}`);
    eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "empty",
      `live: cycle ${cycle} — a refresh poll never resets to the loading state (tl1766 A)`);
    ok(container.querySelector("[data-subagents-live-pending]") !== null,
      `live: cycle ${cycle} — the pending note is not swapped for the loading note`);
    ok(container.querySelector('[data-subagents-livetools="true"]') !== null,
      `live: cycle ${cycle} — the projection stays mounted while the read is in flight`);
    await settleOldest([]);
    await flush();
    eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "empty",
      `live: cycle ${cycle} — the empty result keeps the pending shape`);
  }

  // The first snapshot lands: the poll heals to the ready state with the
  // follow hint, and the projection yields to the authoritative transcript.
  await settleNextRead([
    { role: "user", content: "child prompt" },
    { role: "assistant", content: "child working" },
  ] as HistoryMessage[], "first snapshot");
  await flush();
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: a polled snapshot reaches the ready state");
  ok(container.querySelector("[data-subagents-live-follow]") !== null,
    "live: the ready running view carries the live-follow hint");
  ok(container.querySelector('[data-subagents-livetools="true"]') === null,
    "live: once content renders, the projection yields to the transcript");

  // Amplifier guard: a successful-but-empty mid-run result must not wipe the
  // already-loaded content — the autosave transcript only grows while the
  // child runs, so an empty read after content is a transient race.
  await waitForReadInFlight("refresh after content");
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: a refresh poll keeps the loaded content on screen while in flight");
  await settleOldest([] as HistoryMessage[]);
  await flush();
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: an empty mid-run result cannot wipe already-loaded content (tl1766 amplifier)");
  ok(container.querySelector("[data-subagents-live-pending]") === null,
    "live: the wiped-content shape shows no pending note");

  // The next real snapshot heals normally after the guarded race.
  await settleNextRead([
    { role: "user", content: "child prompt" },
    { role: "assistant", content: "child working harder" },
  ] as HistoryMessage[], "post-race snapshot");
  await flush();
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: the poll keeps healing to ready after the guarded race");

  await act(async () => { root.unmount(); });
  container.remove();
} finally {
  setDockLivePollMsForTests(2000);
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
