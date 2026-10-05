// Run: tsx src/__tests__/subagent-panel-dock.test.tsx
// Task 495: the subagent panel package behind ONE switch
// (experimental_subagent_panel, default off). Contract under test:
// - dock gate: the "subagents" RightDockMode answers to its own switch
//   (third gate argument, optional so the task-259 two-arg calls keep their
//   meaning) and falls back to "files" while the switch is off;
// - tab ids: "subagents" joins the hideable set and counts as renderable only
//   under its own switch (the keep-one-tab guard math);
// - transcript half: with the flag off, deep mode keeps the legacy
//   unconditional expand (ended subagent cards included); with it on, ENDED
//   subagent cards default collapsed (live terminal phase or hydrated
//   name+status), running cards keep every existing expand path, and
//   non-subagent cards are untouched;
// - directory model: running/ended classification (terminal phase wins over a
//   stale running status; hydrated cards classify by status), newest first;
// - panel: two sections, rows folded, ended list reveals 20 at a time via the
//   show-more button (requirement ④).

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

const { resolveToolCardDefaultOpen } = await import("../lib/transcriptRowGeometry");
const labFlags = await import("../lib/labFlags");
const layout = await import("../store/layout");
const dockTabs = await import("../lib/dockTabs");
const { buildSubagentDirectory, SUBAGENT_DIRECTORY_PAGE_SIZE } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider } = await import("../lib/i18n");
const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";

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

console.log("\nsubagent panel dock (task 495)");

// 1. Dock mode gate: own switch, legacy two-arg calls unchanged.
{
  eq(layout.dockModeWithinSidebarGates("subagents", false), "files", "gate: subagents falls back to files when its switch is omitted");
  eq(layout.dockModeWithinSidebarGates("subagents", false, false), "files", "gate: subagents falls back to files when its switch is off");
  eq(layout.dockModeWithinSidebarGates("subagents", false, true), "subagents", "gate: subagents passes when its switch is on (todo family off)");
  eq(layout.dockModeWithinSidebarGates("subagents", true, true), "subagents", "gate: subagents passes with both switches on");
  eq(layout.dockModeWithinSidebarGates("todos", false), "files", "gate 259 unchanged: todos falls back when todo switch off");
  eq(layout.dockModeWithinSidebarGates("todos", true), "todos", "gate 259 unchanged: todos passes when todo switch on");
  eq(layout.dockModeWithinSidebarGates("todos", true, false), "todos", "gate: subagents switch off must NOT gate the todo family");
}

// 2. Tab ids + renderable set (the keep-one-tab guard math).
{
  eq(dockTabs.DOCK_TAB_IDS.includes("subagents"), true, "tab ids: subagents is hideable");
  const off = dockTabs.renderableDockTabs({ todoSidebar: true, remoteAvailable: true, creation: false });
  eq(off.includes("subagents"), false, "renderable: subagents absent without its switch");
  const on = dockTabs.renderableDockTabs({ todoSidebar: true, remoteAvailable: true, creation: false, subagentsPanel: true });
  eq(on.includes("subagents"), true, "renderable: subagents present with its switch on");
  eq(dockTabs.renderableDockTabs({ todoSidebar: false, remoteAvailable: true, creation: false, subagentsPanel: true }).length, 0, "renderable: todo switch off keeps the empty set (legacy literals path)");
}

// 3. Directory model: classification + ordering.
{
  const running = subagentTool({
    id: "t-running", status: "running", startedAt: 3_000,
    subagentProgress: { phase: "tool", reasoning: "", text: "", notice: "", lastActivityAt: 3_500, truncated: false, startedAt: 3_000 },
  });
  const endedNewest = subagentTool({
    id: "t-ended-new", status: "done", startedAt: 2_000, durationMs: 4_000,
    subagentProgress: { phase: "completed", reasoning: "", text: "", notice: "", lastActivityAt: 2_000, truncated: false, startedAt: 2_000, durationMs: 4_000 },
  });
  const endedHydrated = subagentTool({ id: "t-ended-old", name: "explore", status: "done", startedAt: 1_000 });
  const plainBash = subagentTool({ id: "t-bash", name: "bash", status: "done", startedAt: 1_500 });
  const staleRunning = subagentTool({
    id: "t-stale", status: "running", startedAt: 2_500,
    subagentProgress: { phase: "cancelled", reasoning: "", text: "", notice: "", lastActivityAt: 2_500, truncated: false, startedAt: 2_500 },
  });
  const directory = buildSubagentDirectory([plainBash, endedNewest, running, endedHydrated, staleRunning]);
  eq(directory.running.map((entry) => entry.item.id).join(","), "t-running", "directory: only the live card is running");
  eq(directory.ended.length, 3, "directory: ended = terminal-phase + hydrated + stale-status cards");
  eq(directory.ended.map((entry) => entry.item.id).join(","), "t-stale,t-ended-new,t-ended-old", "directory: ended list is newest first (stale startedAt 2500 > 2000 > 1000)");
  eq(directory.ended.some((entry) => entry.item.id === plainBash.id), false, "directory: plain bash never enters the directory");
}

// 4. Transcript half: deep-mode default open around the switch.
{
  const endedCard = subagentTool({
    id: "g-ended", status: "done",
    subagentProgress: { phase: "completed", reasoning: "", text: "", notice: "", lastActivityAt: 1, truncated: false, startedAt: 1 },
  });
  const runningCard = subagentTool({
    id: "g-running", status: "running",
    subagentProgress: { phase: "tool", reasoning: "", text: "", notice: "", lastActivityAt: 1, truncated: false, startedAt: 1 },
  });
  const hydratedEnded = subagentTool({ id: "g-hydrated", name: "explore", status: "done" });
  const plainCard = subagentTool({ id: "g-bash", name: "bash", status: "done" });

  labFlags.applyLabFlags({ subagentPanel: false });
  eq(resolveToolCardDefaultOpen(endedCard, 0, "deep"), true, "off: legacy deep keeps the ended subagent card expanded");
  eq(resolveToolCardDefaultOpen(hydratedEnded, 0, "deep"), true, "off: legacy deep keeps a hydrated subagent card expanded");
  eq(resolveToolCardDefaultOpen(endedCard, 0, "standard"), false, "off: standard mode stays collapsed (unchanged)");

  labFlags.applyLabFlags({ subagentPanel: true });
  eq(resolveToolCardDefaultOpen(endedCard, 0, "deep"), false, "on: deep collapses the ended subagent card (terminal phase)");
  eq(resolveToolCardDefaultOpen(hydratedEnded, 0, "deep"), false, "on: deep collapses the hydrated ended card (name+status)");
  eq(resolveToolCardDefaultOpen(runningCard, 0, "deep"), true, "on: deep still auto-expands the running subagent card");
  eq(resolveToolCardDefaultOpen(plainCard, 0, "deep"), true, "on: non-subagent deep expand untouched");
  eq(resolveToolCardDefaultOpen(endedCard, 1, "deep"), false, "on: ended card stays collapsed even with nested subcalls");

  labFlags.applyLabFlags({});
  eq(resolveToolCardDefaultOpen(endedCard, 0, "deep"), true, "default off restored after reset");
}

// 5. Panel: sections, folded rows, 20-per-page reveal.
//    Locale note: the panel translates through useT (real strings, locale
//    dependent), so assertions are structural (classes/counts), not textual.
{
  const ended21 = Array.from({ length: 21 }, (_, index) =>
    subagentTool({ id: `p-${index}`, status: "done", startedAt: 1_000 + index, subject: `子代理 ${index + 1}` }));
  const runningCard = subagentTool({
    id: "p-running", status: "running", startedAt: 99_000,
    subagentProgress: { phase: "reasoning", reasoning: "", text: "", notice: "", lastActivityAt: 99_000, truncated: false, startedAt: 99_000 },
  });

  const markupFor = (element: ReturnType<typeof createElement>) =>
    renderToStaticMarkup(createElement(LocaleProvider, null, element));

  const emptyMarkup = markupFor(createElement(SubagentsDockPanel, { directory: { running: [], ended: [] } }));
  eq(emptyMarkup.includes("subagents-panel__empty"), true, "panel: both-empty renders the single empty state");
  eq(emptyMarkup.includes("subagents-panel__row"), false, "panel: empty state renders no rows");

  const full = buildSubagentDirectory([runningCard, ...ended21]);
  eq(full.running.length, 1, "panel fixture: one running card");
  eq(full.ended.length, 21, "panel fixture: 21 ended cards");
  eq(SUBAGENT_DIRECTORY_PAGE_SIZE, 20, "panel: page size is 20");

  const markup = markupFor(createElement(SubagentsDockPanel, { directory: full }));
  eq((markup.match(/subagents-panel__section-title/g) ?? []).length, 2, "panel: two section headings render");
  eq((markup.match(/subagents-panel__row"/g) ?? []).length, 21, "panel: static first page = 1 running row + 20 ended rows");
  eq((markup.match(/subagents-panel__foot/g) ?? []).length, 1, "panel: 21 ended rows show exactly one more-button foot");

  // Interaction: the foot button reveals the next page of the ended list.
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, { directory: full })));
  });
  const countRows = () => document.querySelectorAll(".subagents-panel__row").length;
  eq(countRows(), 21, "panel: DOM first page = 1 running row + 20 ended rows");
  const moreButton = container.querySelector(".subagents-panel__foot button");
  eq(moreButton !== null, true, "panel: show-more button rendered");
  await act(async () => { (moreButton as HTMLButtonElement).click(); });
  eq(countRows(), 22, "panel: one click reveals the remaining page (+1 here)");
  eq(container.querySelectorAll(".subagents-panel__foot button").length, 0, "panel: foot disappears when everything is revealed");
  await act(async () => { root.unmount(); });
  container.remove();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
