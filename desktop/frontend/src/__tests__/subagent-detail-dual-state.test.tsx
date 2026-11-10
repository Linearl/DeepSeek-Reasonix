// Run: tsx src/__tests__/subagent-detail-dual-state.test.tsx
// 任务 507: the subagent detail view dual state behind ONE switch
// (experimental_subagent_detail, default off). Contract under test:
// - widen clamp math (plan C): target 620, available width always wins,
//   dock minimum floors the result, already-wider widths stay untouched;
// - lab flags: the new gate ships off and flips live via applyLabFlags;
// - OFF state (plan C fallback, 行为等价): a row click expands the exact 495
//   inline preview (no detail view, no back button), the widen toolbar only
//   renders where the width commands are wired, and clicking it toggles;
// - ON state (plan A): a row click opens the read-only in-dock detail view
//   (back button + read-only notice, zero input elements), running entries
//   show the in-memory progress preview, ended hydrated entries fall back to
//   the name+status note, back returns to the list, and nothing survives a
//   remount (不持久化).

import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
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

const layout = await import("../store/layout");
const labFlags = await import("../lib/labFlags");
const { buildSubagentDirectory } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
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

console.log("\nsubagent detail dual state (task 507)");

// 1. Widen clamp math (plan C): pure, exported, directly testable.
{
  const { clampSubagentsWideWidth, SUBAGENTS_WIDE_TARGET_WIDTH, RIGHT_DOCK_TREE_MIN_WIDTH } = layout;
  eq(SUBAGENTS_WIDE_TARGET_WIDTH, 620, "widen: reading-width target is 620");
  eq(RIGHT_DOCK_TREE_MIN_WIDTH, 246, "widen: the dock minimum follows tl1108 的窄态地板 246");
  eq(clampSubagentsWideWidth(246, 1200), 620, "widen: roomy viewport hits the 620 target");
  eq(clampSubagentsWideWidth(246, 450), 450, "widen: a narrow viewport caps at the available width");
  eq(clampSubagentsWideWidth(246, 200), 246, "widen: a tiny viewport floors at the dock minimum");
  eq(clampSubagentsWideWidth(700, 1200), 700, "widen: an already-wider dock stays untouched");
}

// 2. Lab flags: ships off, flips live.
{
  labFlags.applyLabFlags({});
  eq(labFlags.labFlagEnabled("subagentDetail"), false, "flags: subagentDetail ships off");
  labFlags.applyLabFlags({ subagentDetail: true });
  eq(labFlags.labFlagEnabled("subagentDetail"), true, "flags: subagentDetail flips live");
  labFlags.applyLabFlags({});
  eq(labFlags.labFlagEnabled("subagentDetail"), false, "flags: default restored after reset");
}

const running = subagentTool({
  id: "s-running",
  status: "running",
  startedAt: 10_000,
  subagentProgress: {
    phase: "responding",
    reasoning: "先查目录结构",
    text: "正在汇总",
    notice: "",
    lastActivityAt: 11_000,
    truncated: false,
    startedAt: 10_000,
  },
});
const endedWithSummary = subagentTool({
  id: "s-ended",
  status: "done",
  startedAt: 5_000,
  durationMs: 8_000,
  summary: "已写入 3 个文件",
  subagentProgress: {
    phase: "completed",
    reasoning: "",
    text: "最终答复",
    notice: "",
    lastActivityAt: 5_000,
    truncated: false,
    startedAt: 5_000,
  },
});
const endedHydrated = subagentTool({
  id: "s-hydrated",
  name: "explore",
  status: "done",
  startedAt: 1_000,
});
const directory = buildSubagentDirectory([running, endedWithSummary, endedHydrated]);

const markupFor = (element: ReturnType<typeof createElement>) =>
  renderToStaticMarkup(createElement(LocaleProvider, null, element));

async function mountPanel(props: Record<string, unknown>) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, { directory, ...props })));
  });
  return {
    container,
    async click(selector: string) {
      const el = container.querySelector(selector) as HTMLButtonElement | null;
      assert.ok(el, `click target missing: ${selector}`);
      await act(async () => { el.click(); });
    },
    async unmount() {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

// 3. OFF state (plan C fallback): 行为等价 + widen toolbar contract.
{
  eq(markupFor(createElement(SubagentsDockPanel, { directory })).includes("subagents-panel__toolbar"), false,
    "off: no widen toolbar without the width commands");
  eq(markupFor(createElement(SubagentsDockPanel, { directory })).includes("subagents-panel__detailview"), false,
    "off: static render has no detail view");

  const panel = await mountPanel({ onToggleWide: () => {} });
  eq(panel.container.querySelector(".subagents-panel__toolbar") !== null, true,
    "off: the widen toolbar renders where the commands are wired");
  eq(panel.container.querySelectorAll(".subagents-panel__row").length, 3,
    "off: all three rows render");
  const widen = panel.container.querySelector(".subagents-panel__wide") as HTMLButtonElement;
  eq(widen.getAttribute("aria-pressed"), "false", "off: widen starts not-pressed");

  // Row click: the exact 495 inline preview — never the detail view.
  await panel.click(".subagents-panel__row");
  eq(panel.container.querySelector(".subagents-panel__detail") !== null, true,
    "off: row click expands the inline preview");
  eq(panel.container.querySelector(".subagents-panel__detailview") === null, true,
    "off: row click never opens the detail view");
  eq(panel.container.querySelector(".subagents-panel__back") === null, true,
    "off: no back button in the fallback state");
  await panel.unmount();
}

// 4. OFF state: widen toggle round-trips through the callback.
{
  let lastNext: boolean | null = null;
  const panel = await mountPanel({ onToggleWide: (next: boolean) => { lastNext = next; } });
  await panel.click(".subagents-panel__wide");
  eq(lastNext, true, "off: widen click asks for wide");
  await panel.unmount();
  const widePanel = await mountPanel({ wide: true, onToggleWide: (next: boolean) => { lastNext = next; } });
  await widePanel.click(".subagents-panel__wide");
  eq(lastNext, false, "off: a second click asks to restore");
  await widePanel.unmount();
}

// 5. ON state (plan A): row click → read-only detail view; running vs ended.
{
  const panel = await mountPanel({ detailEnabled: true });
  // Running entry first: detail shows the in-memory progress preview.
  await panel.click(".subagents-panel__list .subagents-panel__row");
  eq(panel.container.querySelector(".subagents-panel__detailview") !== null, true,
    "on: row click opens the detail view");
  eq(panel.container.querySelector(".subagents-panel__back") !== null, true,
    "on: the detail view carries a back button");
  eq(panel.container.querySelector(".subagents-panel__readonly") !== null, true,
    "on: the detail view carries the read-only notice");
  eq(panel.container.querySelectorAll("input, textarea").length, 0,
    "on: the detail view renders zero input elements (硬边界)");
  eq(panel.container.querySelectorAll(".subagents-panel__detailview-text").length >= 2, true,
    "on: running entry shows the progress preview blocks");

  // Back returns to the pick-entry state (tl1109 同屏: 列表常驻，内容区回提示态).
  await panel.click(".subagents-panel__back");
  eq(panel.container.querySelector(".subagents-panel__detailview-note") !== null, true,
    "on: back lands on the pick-entry note (same-screen)");
  eq(panel.container.querySelectorAll(".subagents-panel__row").length, 3,
    "on: the list is intact after back");

  // Ended hydrated entry (name+status only): the no-preview fallback note.
  const rows = panel.container.querySelectorAll(".subagents-panel__list .subagents-panel__row");
  const endedRow = rows[rows.length - 1] as HTMLButtonElement;
  await act(async () => { endedRow.click(); });
  eq(panel.container.querySelector(".subagents-panel__detailview") !== null, true,
    "on: an ended hydrated row also opens the detail view");
  eq(panel.container.querySelectorAll(".subagents-panel__detailview-text").length, 0,
    "on: a hydrated entry has no preview blocks");
  eq(panel.container.querySelector(".subagents-panel__detailview-note") !== null, true,
    "on: hydrated entry shows the no-preview note (name+status only)");

  // The ended-with-summary entry: summary block renders.
  await panel.click(".subagents-panel__back");
  const firstEndedRow = panel.container.querySelectorAll(".subagents-panel__list .subagents-panel__row")[1] as HTMLButtonElement;
  await act(async () => { firstEndedRow.click(); });
  const detailText = panel.container.querySelector(".subagents-panel__detailview-text");
  eq(detailText !== null && detailText.textContent === "已写入 3 个文件", true,
    "on: ended entry shows its summary block");
  await panel.unmount();
}

// 6. ON state: selection is component state — a remount lands back on the
//    pick-entry state (不持久化; the preview itself is memory-only).
{
  const panel = await mountPanel({ detailEnabled: true });
  await panel.click(".subagents-panel__list .subagents-panel__row");
  eq(panel.container.querySelector(".subagents-panel__detailview-note") === null, true,
    "persist: detail open before remount");
  await panel.unmount();
  const fresh = await mountPanel({ detailEnabled: true });
  eq(fresh.container.querySelector(".subagents-panel__detailview-note") !== null, true,
    "persist: a remount lands back on the pick-entry note");
  await fresh.unmount();
}

// 7. Gate isolation: with the switch off, even repeated row clicks never
//    produce the detail view — the click keeps toggling the inline preview
//    (open → closed), byte-for-byte the plan-C fallback.
{
  const panel = await mountPanel({ detailEnabled: false });
  await panel.click(".subagents-panel__row");
  eq(panel.container.querySelector(".subagents-panel__detail") !== null, true,
    "gate: first click expands the inline preview");
  await panel.click(".subagents-panel__row");
  eq(panel.container.querySelector(".subagents-panel__detail") === null, true,
    "gate: second click collapses it (toggle still works)");
  eq(panel.container.querySelector(".subagents-panel__detailview") === null, true,
    "gate: repeated clicks never render the detail view");
  await panel.unmount();
}

// 8. 任务440 (as amended by tl1202, then tl1537): ordinary dispatches now
//    snapshot the transcript mid-run (backend write side), so a RUNNING
//    ref-bearing entry polls the same read bridge — already-written work
//    renders live with a follow hint; before the first snapshot lands the
//    pending-sentinel read degrades to the pending note (never the red
//    读取失败 note) and the poll heals it without manual action; the
//    running→ended transition performs the one final read and stops the
//    polling; a failed ended-read surfaces copy plus a retry button; a
//    ref-less entry stays byte-for-byte the 507 preview with zero reads.
{
  const { SUBAGENT_DOCK_LIVE_POLL_MS, setDockLivePollMsForTests } = await import("../components/SubagentsDockPanel");
  eq(SUBAGENT_DOCK_LIVE_POLL_MS, 2000, "live: production poll cadence is the 2s named constant");
  setDockLivePollMsForTests(25);
  const restorePoll = () => setDockLivePollMsForTests(2000);

  const flush = async (ms = 20) => {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, ms));
    });
  };

  const readCalls: Array<[string, string]> = [];
  let readResult: Promise<HistoryMessage[]> = Promise.resolve([]);
  const onReadSubagent = (sessionPath: string, ref: string) => {
    readCalls.push([sessionPath, ref]);
    return readResult;
  };

  const runningWithRef = subagentTool({
    id: "s-live",
    status: "running",
    startedAt: 10_000,
    subagentProgress: {
      phase: "responding",
      reasoning: "先查目录",
      text: "",
      notice: "",
      lastActivityAt: 11_000,
      truncated: false,
      startedAt: 10_000,
      ref: "sa_live_440",
    },
  });
  const liveDirectory = buildSubagentDirectory([runningWithRef]);

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: liveDirectory,
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
    })));
  });
  await flush();

  // Open the running entry: the live container mounts (tl1537) and the first
  // read fires immediately; with no snapshot on disk yet (the bridge resolves
  // empty) the detail shows the pending note — not the preview blocks, not a
  // red failure.
  await act(async () => {
    (container.querySelector(".subagents-panel__row") as HTMLButtonElement).click();
  });
  await flush();
  const liveEl = container.querySelector(".subagents-panel__live") as HTMLElement | null;
  eq(liveEl !== null, true,
    "live: a running entry mounts the transcript read container (tl1537)");
  eq(liveEl!.getAttribute("data-subagents-live"), "running",
    "live: the running container carries the running shape marker");
  eq(readCalls.length >= 1 && readCalls[0][0] === "dock-parent.jsonl" && readCalls[0][1] === "sa_live_440", true,
    "live: the running entry fires the read with the session path and the child ref");
  eq(liveEl!.getAttribute("data-subagents-live-state"), "empty",
    "live: an empty mid-run projection reads as the empty state");
  eq(container.querySelector("[data-subagents-live-pending]") !== null, true,
    "live: the pre-first-snapshot running detail carries the pending note");
  eq((container.querySelector("[data-subagents-live-pending]") as HTMLElement).textContent!.length > 0, true,
    "live: the pending note carries user-readable copy");
  eq(container.querySelectorAll(".subagents-panel__detailview-text").length, 0,
    "live: the running detail swaps the memory preview blocks for the live container");

  // Polling follows the snapshots: once the bridge returns content, the next
  // tick reaches the ready state with the follow hint, and the read count
  // keeps growing while the child runs.
  readResult = Promise.resolve([
    { role: "user", content: "child prompt" },
    { role: "assistant", content: "child working" },
  ] as HistoryMessage[]);
  await flush(120);
  const readyEl = container.querySelector(".subagents-panel__live") as HTMLElement;
  eq(readyEl.getAttribute("data-subagents-live-state"), "ready",
    "live: a polled snapshot reaches the ready state while still running");
  eq(container.querySelector("[data-subagents-live-follow]") !== null, true,
    "live: the ready running view carries the live-follow hint");
  eq(readCalls.length >= 3, true,
    "live: the poll keeps re-pulling while the entry runs (readCalls=" + readCalls.length + ")");

  // The child ends: the parent re-render flips running→ended, the effect
  // re-subscribes and performs exactly one final read; with the poll interval
  // parked far away, no further reads may fire afterwards.
  setDockLivePollMsForTests(600_000);
  const readsBeforeEnd = readCalls.length;
  const endedWithRef = { ...runningWithRef, status: "done" as const, subagentProgress: { ...runningWithRef.subagentProgress!, phase: "completed" as const } };
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: buildSubagentDirectory([endedWithRef]),
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
    })));
  });
  await flush();
  eq(readCalls.length, readsBeforeEnd + 1,
    "live: the ended transition fires exactly one final read");
  eq((container.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live"), "ended",
    "live: the ended container flips to the ended shape marker");
  await flush(60);
  eq(readCalls.length, readsBeforeEnd + 1,
    "live: polling stops once the entry has ended (no further reads)");
  await act(async () => { root.unmount(); });
  container.remove();
  restorePoll();

  // A pending-sentinel read while running degrades to the pending note (the
  // backend bridge's not-yet-saved error), and the poll heals it to ready
  // without any manual action. Fresh mount: every early read rejects pending.
  setDockLivePollMsForTests(25);
  readCalls.length = 0;
  readResult = Promise.reject(new Error('subagent transcript "sa_live_440" has not been saved yet'));
  readResult.catch(() => {});
  const pendingContainer = document.createElement("div");
  document.body.appendChild(pendingContainer);
  const pendingRoot = createRoot(pendingContainer);
  await act(async () => {
    pendingRoot.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: liveDirectory,
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
    })));
  });
  await act(async () => {
    (pendingContainer.querySelector(".subagents-panel__row") as HTMLButtonElement).click();
  });
  await flush(60);
  const pendingEl = pendingContainer.querySelector(".subagents-panel__live") as HTMLElement;
  eq(readCalls.length >= 2, true,
    "live: the pending-sentinel window keeps polling (readCalls=" + readCalls.length + ")");
  eq(pendingEl.getAttribute("data-subagents-live-state"), "empty",
    "live: a pending-sentinel error while running never reaches the failed state");
  eq(pendingContainer.querySelector("[data-subagents-live-pending]") !== null, true,
    "live: the pending-sentinel error renders the pending note");
  eq(pendingContainer.querySelector("[data-subagents-retry]") === null, true,
    "live: the pending window offers no retry button (nothing to retry — the poll heals)");
  readResult = Promise.resolve([
    { role: "user", content: "child prompt" },
    { role: "assistant", content: "child working" },
  ] as HistoryMessage[]);
  await flush(120);
  eq((pendingContainer.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: the poll recovers the pending window to ready without manual action");
  await act(async () => { pendingRoot.unmount(); });
  pendingContainer.remove();
  restorePoll();

  // A rejected ended-read surfaces the failed state — with a retry button, not
  // a dead red note; clicking retry re-reads and recovers (tl1202 form
  // unchanged). Fresh mount: the entry's first (and only automatic) read
  // rejects — ended entries never poll.
  setDockLivePollMsForTests(600_000);
  readCalls.length = 0;
  readResult = Promise.reject(new Error("transcript read boom"));
  readResult.catch(() => {});
  const failedContainer = document.createElement("div");
  document.body.appendChild(failedContainer);
  const failedRoot = createRoot(failedContainer);
  await act(async () => {
    failedRoot.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: buildSubagentDirectory([endedWithRef]),
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
    })));
  });
  await act(async () => {
    (failedContainer.querySelector(".subagents-panel__list .subagents-panel__row") as HTMLButtonElement).click();
  });
  await flush();
  eq(readCalls.length, 1, "live: the failed ended entry's automatic read fires once");
  await flush(60);
  eq(readCalls.length, 1, "live: an ended entry never polls (exactly one read)");
  eq((failedContainer.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "failed",
    "live: a failed read reaches the failed state");
  eq(((failedContainer.querySelector(".subagents-panel__live") as HTMLElement).querySelector(".subagents-panel__detailview-note") as HTMLElement | null)?.textContent!.length! > 0, true,
    "live: the failed state carries user-readable copy");
  const retryBtn = failedContainer.querySelector("[data-subagents-retry]") as HTMLButtonElement | null;
  eq(retryBtn !== null, true, "live: the failed state offers a retry button (tl1202)");
  readResult = Promise.resolve([
    { role: "user", content: "child prompt" },
    { role: "assistant", content: "child working" },
  ] as HistoryMessage[]);
  await act(async () => { retryBtn!.click(); });
  await flush();
  eq(readCalls.length, 2, "live: clicking retry fires another read");
  eq((failedContainer.querySelector(".subagents-panel__live") as HTMLElement).getAttribute("data-subagents-live-state"), "ready",
    "live: a retried read recovers to the ready state");

  await act(async () => { failedRoot.unmount(); });
  failedContainer.remove();
  restorePoll();

  // A ref-less running entry (ephemeral run) keeps the 507 preview blocks —
  // no pending note either (without a ref there is no transcript to await).
  const reflessRunning = subagentTool({
    id: "s-preview",
    status: "running",
    startedAt: 12_000,
    subagentProgress: {
      phase: "responding",
      reasoning: "预览内容",
      text: "",
      notice: "",
      lastActivityAt: 12_500,
      truncated: false,
      startedAt: 12_000,
    },
  });
  const reflessDirectory = buildSubagentDirectory([reflessRunning]);
  const reflessContainer = document.createElement("div");
  document.body.appendChild(reflessContainer);
  const reflessRoot = createRoot(reflessContainer);
  readCalls.length = 0;
  await act(async () => {
    reflessRoot.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
      directory: reflessDirectory,
      detailEnabled: true,
      sessionPath: "dock-parent.jsonl",
      onReadSubagent,
    })));
  });
  await act(async () => {
    (reflessContainer.querySelector(".subagents-panel__row") as HTMLButtonElement).click();
  });
  await flush();
  eq(reflessContainer.querySelector(".subagents-panel__live") === null, true,
    "live: a ref-less entry keeps the preview-only fallback");
  eq(reflessContainer.querySelectorAll(".subagents-panel__detailview-text").length >= 1, true,
    "live: the ref-less entry still shows its preview blocks");
  eq(reflessContainer.querySelector("[data-subagents-live-pending]") === null, true,
    "live: a ref-less entry shows no pending note (nothing to await)");
  eq(readCalls.length, 0, "live: a ref-less entry never fires a transcript read");
  await act(async () => { reflessRoot.unmount(); });
  reflessContainer.remove();
  restorePoll();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
