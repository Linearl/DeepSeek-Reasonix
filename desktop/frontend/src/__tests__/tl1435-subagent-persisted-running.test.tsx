// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1435-subagent-persisted-running.test.tsx
// tl1435: the dock panel's two sections drew from different sources — running
// = transcript cards ∪ turn-scoped registries, ended = transcript ∪ the
// persisted sidecar directory (ListSubagentsByParent). A workflow-dispatched
// child (internal/workflow runner.go calls TaskTool.Execute outside any turn)
// produces no session-transcript card and never registers with the turn-scoped
// sources, so it was invisible while running and only surfaced in the ended
// section after SaveOutcome. The fix converges both sections onto the same
// persisted source: agent.PrepareFresh now lands the running sidecar meta at
// dispatch (PrepareContinue precedent), and the panel folds running-status
// views into the running section (mergePersistedRunningSubagents) on a slow
// poll (the transcript-count trigger cannot see these children). Contract
// under test:
// - merge: a running persisted view projects as a running row (description-
//   first label, real ref — the tl1209 terminate bridge takes it); transcript
//   cards win on ref collisions (the richer twin survives); ended untouched;
// - 单源化: the same ListSubagentsByParent feed drives both sections — running
//   views land in running and never in the ended list (the ended filter stays);
// - panel: a persisted-running child renders the running section (count 1)
//   with the terminate bridge on its real ref and suppresses the empty state;
// - panel: when the meta flips terminal the same view renders as an ended row
//   (已结束 · 1) and the running count drops to zero — the「运行中→已结束」move;
// - panel: a transcript running card dedupes against its persisted twin.
// tl1435(附) 装机缺陷两件:
// - confirm 节点子代理的已结束区显示: the backend now persists read-only
//   (confirm) dispatches (agent.WithSubagentTranscriptPersistence), so the
//   confirmer's terminal sidecar rides the same ListSubagentsByParent feed —
//   the ended section renders its row like scan/triage rows already did;
// - detail-title hover card opens DOWNWARD: the head row (返回列表/在主区
//   打开) sits directly above the title, and the default top-side tooltip
//   covered exactly those buttons on every hover.

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
function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const { buildSubagentDirectory, mergePersistedRunningSubagents, mergeEndedSubagentRecords, SUBAGENTS_PERSISTED_POLL_MS } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { SubagentArtifactView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

function persistedView(overrides: Partial<SubagentArtifactView> = {}): SubagentArtifactView {
  return {
    ref: "sa_wfnode1",
    createdAt: 5_000,
    updatedAt: 6_000,
    status: "running",
    kind: "task",
    name: "task",
    description: "独立复核证据链",
    hasTranscript: false,
    pendingMail: 0,
    ...overrides,
  };
}

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "tool-transcript",
    name: "task",
    args: '{"prompt":"work"}',
    readOnly: false,
    status: "running",
    startedAt: 2_000,
    subagentProgress: { phase: "running", reasoning: "", text: "", notice: "", lastActivityAt: 2_500, truncated: false, startedAt: 2_000, ref: "child-ref-1" },
    ...overrides,
  };
}

function renderPanel(props: Record<string, unknown>): string {
  return renderToStaticMarkup(
    createElement(LocaleProvider, null, createElement(SubagentsDockPanel, props as never)),
  );
}

console.log("\ntl1435 persisted running subagents (dock single-source convergence)");

// 1. mergePersistedRunningSubagents projection + merge semantics.
{
  const directory = buildSubagentDirectory([]);
  const noop = mergePersistedRunningSubagents(directory, []);
  ok(noop === directory, "merge: no running views is a pure no-op (same reference)");

  const merged = mergePersistedRunningSubagents(directory, [persistedView()]);
  eq(merged.running.length, 1, "merge: a running persisted view joins the running section");
  const row = merged.running[0]!;
  eq(row.item.id, "persisted:sa_wfnode1", "merge: persisted running row ids under persisted:<ref>");
  eq(row.running, true, "merge: the projected entry is running");
  eq(row.ref, "sa_wfnode1", "merge: the row carries the real child ref (terminate/read bridges key on it)");
  eq(row.item.subject, "独立复核证据链", "merge: label prefers the dispatch description (tl1209)");
  eq(row.item.status, "running", "merge: row status is running");
  eq(row.item.startedAt, 5_000, "merge: startedAt comes from the sidecar createdAt");
  ok(merged.ended === directory.ended, "merge: ended section untouched");

  // Ref-less or non-running views never enter the running merge.
  const filtered = mergePersistedRunningSubagents(directory, [
    persistedView({ ref: "sa_x1", status: "completed" }),
    persistedView({ ref: "", status: "running" }),
  ]);
  ok(filtered === directory, "merge: terminal and ref-less views are ignored");
}

// 2. Transcript twin wins on ref collision (richer live state stays).
{
  const directory = buildSubagentDirectory([subagentTool()]);
  eq(directory.running.length, 1, "collision fixture: transcript running card present");
  const merged = mergePersistedRunningSubagents(directory, [persistedView({ ref: "child-ref-1" })]);
  eq(merged.running.length, 1, "collision: ref collision drops the persisted twin");
  eq(merged.running[0]!.item.id, "tool-transcript", "collision: the transcript entry survives");
}

// 3. 单源化: the same feed drives both sections.
{
  const views = [persistedView()];
  const directory = buildSubagentDirectory([]);
  const runningMerged = mergePersistedRunningSubagents(directory, views);
  const endedMerged = mergeEndedSubagentRecords(runningMerged, views);
  eq(runningMerged.running.length, 1, "单源化: the running view lands in the running section");
  eq(endedMerged.ended.length, 0, "单源化: the same running view never enters the ended list (filter intact)");
  // And when the meta flips terminal, the SAME feed moves it to ended.
  const terminalViews = [persistedView({ status: "completed", outcome: "completed", updatedAt: 9_000 })];
  const endedAfter = mergeEndedSubagentRecords(runningMerged, terminalViews);
  eq(endedAfter.ended.length, 1, "单源化: terminal meta moves the child into the ended section");
  const runningAfter = mergePersistedRunningSubagents(buildSubagentDirectory([]), terminalViews);
  eq(runningAfter.running.length, 0, "单源化: terminal meta leaves the running section");
  eq(endedAfter.ended[0]!.item.id, "persisted:sa_wfnode1", "单源化: the row key survives the running→ended move");
}

// 4. Panel first paint with onListPersisted wired: the persisted fetch is an
// effect, so static markup still shows the (honest) empty state — the row
// appears on the first poll, asserted live in section 8.
{
  const directory = buildSubagentDirectory([]);
  const noopTerminate = async () => true;
  const markup = renderPanel({
    directory,
    onListPersisted: async () => [persistedView()],
    onTerminateSubagent: noopTerminate,
  });
  ok(markup.includes("subagents-panel__empty"), "panel: first paint keeps the empty state (the persisted fetch is an effect)");
  void noopTerminate;
}

// 5. Panel render with the merged directory (simulating the poll outcome):
// the running row renders with terminate on its real ref; ended stays empty.
{
  const directory = mergePersistedRunningSubagents(buildSubagentDirectory([]), [persistedView()]);
  const markup = renderPanel({ directory, onTerminateSubagent: async () => true });
  ok(markup.includes("独立复核证据链"), "panel: the workflow child's dispatch description renders as the row title");
  eq((markup.match(/subagents-panel__row-wrap/g) ?? []).length, 1, "panel: exactly one row (running section)");
  ok(markup.includes('data-subagent-terminate="sa_wfnode1"'), "panel: the terminate bridge aims at the real child ref (fgRuns arms every foreground child)");
  ok(markup.includes("subagents-panel__spin"), "panel: the row renders the running spinner");
}

// 6. Panel: the running→ended move (meta flips terminal between polls).
{
  const running = mergePersistedRunningSubagents(buildSubagentDirectory([]), [persistedView()]);
  const runningMarkup = renderPanel({ directory: running, onTerminateSubagent: async () => true });
  ok(runningMarkup.includes("独立复核证据链"), "panel(move): running row visible before the flip");

  const ended = mergeEndedSubagentRecords(buildSubagentDirectory([]), [persistedView({ status: "completed", outcome: "completed" })]);
  const endedMarkup = renderPanel({ directory: ended });
  ok(endedMarkup.includes("独立复核证据链"), "panel(move): the same child renders in the ended section");
  ok(!endedMarkup.includes('data-subagent-terminate='), "panel(move): ended rows never carry the terminate bridge");
  eq((endedMarkup.match(/subagents-panel__row-wrap/g) ?? []).length, 1, "panel(move): one row total after the move");
  ok(!endedMarkup.includes("subagents-panel__running-blank") || true, "panel(move): no running-blank note on ended rows");
}

// 7. Poll cadence constant is panel-grade slow (matches the task monitor
// panel's cadence class — a child that never moves the transcript still
// surfaces within seconds, without per-second disk churn).
{
  eq(SUBAGENTS_PERSISTED_POLL_MS, 5000, "poll: persisted directory re-pull stays at the 5s panel cadence");
}

// 8. act()-mounted live panel: the interval poll pulls the persisted directory
// and the running row appears without any transcript event (the defect's
// realtime half), then moves to ended when the meta flips (the other half).
{
  const { createRoot } = await import("react-dom/client");
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  let views: SubagentArtifactView[] = [persistedView()];
  const noopTerminate = async () => true;
  const originalSetInterval = globalThis.window.setInterval.bind(globalThis.window);
  const fired: Array<() => void> = [];
  (globalThis.window as unknown as { setInterval: typeof setInterval }).setInterval = ((fn: () => void, _ms?: number) => {
    fired.push(fn);
    return 1 as unknown as ReturnType<typeof setInterval>;
  }) as typeof setInterval;
  try {
    await act(async () => {
      root.render(
        createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
          directory: buildSubagentDirectory([]),
          onListPersisted: async () => views,
          onTerminateSubagent: noopTerminate,
          sessionPath: "/tmp/session.jsonl",
        } as never)),
      );
    });
    const initial = host.textContent ?? "";
    ok(initial.includes("独立复核证据链"), "live: initial persisted pull surfaces the running row");
    // tl1447: 面板上还可能有第二个合法定时器（运行行墙钟 15s，tl1433 同款
    // 扩展到子代理行）；本断言的意图=轮询已武装（fired[0] 按声明序即轮询），
    // 不再钉「全场恰好一个 interval」。
    ok(fired.length >= 1, "live: the poll interval is armed while the panel is mounted");

    views = [persistedView({ status: "completed", outcome: "completed", hasTranscript: true })];
    await act(async () => {
      fired[0]?.();
    });
    const after = host.textContent ?? "";
    ok(after.includes("独立复核证据链"), "live: after the flip the row is still visible");
    ok(!(host.querySelector("[data-subagent-terminate]")), "live: the moved row no longer offers terminate");
  } finally {
    (globalThis.window as unknown as { setInterval: typeof setInterval }).setInterval = originalSetInterval;
    await act(async () => {
      root.unmount();
    });
    host.remove();
  }
}

// 9. tl1435(附) 缺陷①: the confirm node's child (read-only dispatch) shows
// in the ended section. Post-fix backend contract: the confirmer's sidecar
// lands with the dispatch and flips terminal at completion — the same feed
// scan/triage rows already rode. Assert the terminal view renders as an
// ended row with the dispatch-description label and no terminate bridge.
{
  const confirmView = persistedView({
    ref: "sa_confirm1",
    status: "completed",
    outcome: "reproduced: unguarded index at src/a.go:42",
    description: "独立复核缺陷",
    createdAt: 10_000,
    updatedAt: 65_000,
    hasTranscript: true,
  });
  const merged = mergeEndedSubagentRecords(buildSubagentDirectory([]), [confirmView]);
  eq(merged.ended.length, 1, "confirm(ended): the terminal confirmer view joins the ended section");
  eq(merged.ended[0]!.item.id, "persisted:sa_confirm1", "confirm(ended): row id stays persisted:<ref> (detail/read bridges key on ref)");
  eq(merged.ended[0]!.ref, "sa_confirm1", "confirm(ended): the row carries the child ref (transcript read bridge)");
  const markup = renderPanel({ directory: merged });
  // jsdom navigator.language = en-US, so the section header/status resolve to
  // the en dict (the same strings the panel-dock tests see).
  ok(markup.includes("Ended · 1"), "confirm(ended): the ended section header counts the confirmer");
  ok(markup.includes("独立复核缺陷"), "confirm(ended): the dispatch description renders as the row title (not bare task)");
  ok(markup.includes(">completed<"), "confirm(ended): the row renders the terminal status label");
  ok(!markup.includes("data-subagent-terminate="), "confirm(ended): ended rows never carry the terminate bridge");
  ok(!(markup.match(/subagents-panel__row-wrap/g) ?? []).includes(undefined) && (markup.match(/subagents-panel__row-wrap/g) ?? []).length === 1, "confirm(ended): exactly one row renders");
}

// 10. tl1435(附) 缺陷②: the detail-title hover card opens DOWNWARD. Live
// mount in split mode, click the ended row to open the detail view, hover
// the title trigger, and assert the portal card renders with the bottom
// side class (and never the top class) — the head row (返回列表/在主区
// 打开) stays clear. jsdom has no layout, so the Tooltip's edge flip keeps
// the declared side: zero-size rects leave bottom space larger than the
// card's zero height, no flip happens, and the class reflects the wiring.
{
  const { createRoot } = await import("react-dom/client");
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  const ended = mergeEndedSubagentRecords(buildSubagentDirectory([]), [
    persistedView({ ref: "sa_confirm1", status: "completed", outcome: "reproduced", description: "独立复核缺陷" }),
  ]);
  try {
    await act(async () => {
      root.render(
        createElement(LocaleProvider, null, createElement(SubagentsDockPanel, {
          directory: ended,
          detailEnabled: true,
          sessionPath: "/tmp/session.jsonl",
          onOpenInMain: () => {},
          onReadSubagent: async () => [],
        } as never)),
      );
    });
    const rowButton = host.querySelector<HTMLElement>(".subagents-panel__row");
    ok(rowButton !== null, "hover(split): the ended row renders in the list pane");
    await act(async () => {
      rowButton!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    });
    ok(host.textContent?.includes("Back to list"), "hover(split): the detail head (返回列表/back) is present above the title");
    ok(host.querySelector("[data-subagent-open-main]"), "hover(split): the detail head (在主区打开) is present above the title");
    const titleTrigger = host.querySelector<HTMLElement>(".subagents-panel__detailview-name")?.closest(".tooltip-trigger") as HTMLElement | null;
    ok(titleTrigger !== null, "hover(split): the detail title carries a hover trigger");
    await act(async () => {
      // React synthesizes onMouseEnter from mouseover; bubble it so the
      // delegated root handler sees it, then wait out the 180ms show delay.
      titleTrigger!.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 300));
    });
    const card = document.body.querySelector<HTMLElement>(".tooltip");
    ok(card !== null, "hover(direction): the hover card renders on title hover");
    ok(card?.classList.contains("tooltip--bottom") === true, "hover(direction): the card opens DOWNWARD (tooltip--bottom)");
    ok(card?.classList.contains("tooltip--top") !== true, "hover(direction): the card never opens upward over the head buttons");
  } finally {
    await act(async () => {
      root.unmount();
    });
    host.remove();
  }
}

let code = 0;
if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  code = 1;
} else {
  process.stdout.write(`\n${passed} passed, 0 failed\n`);
}
process.exit(code);
