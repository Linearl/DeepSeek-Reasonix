// Run: tsx src/__tests__/tl1387-subagent-registry.test.tsx
// tl1387: the dock panel's running section gains a second, backend-owned
// source (the zcode form). Contract under test:
// - projection: bash jobs never enter; task/fleet jobs project as preview-only
//   rows (registry:job:<id>, no ref — a job id is not a transcript ref, so no
//   terminate/read bridge may aim at it); foreground registry rows project
//   with their real ref and description-first label; foreign-tab foreground
//   rows stay out of this session's directory;
// - merge: pure no-op on an empty registry; transcript entries win on ref
//   collisions (richer live state stays authoritative); non-colliding
//   registry rows are prepended;
// - panel: registry-only running rows render the running section and suppress
//   the 「暂无子代理」 empty state (the tl1387 第一层 symptom — a live child
//   visible to the capsule while the panel read 「暂无」); the legacy empty
//   state (no transcript, no registry) is byte-for-byte unchanged;
// - panel: a transcript running card dedupes against its registry twin.

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

const { buildSubagentDirectory, registryRunningEntries, mergeRegistryRunningSubagents } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { JobView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

function job(overrides: Partial<JobView> = {}): JobView {
  return {
    id: "job-1",
    kind: "task",
    label: "后台研究",
    status: "running",
    startedAt: 1_000,
    ...overrides,
  };
}

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "tool-transcript",
    name: "task",
    args: '{"run_in_background":true,"prompt":"work"}',
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

console.log("\ntl1387 subagent registry (dock second running source)");

// 1. registryRunningEntries projection.
{
  const entries = registryRunningEntries(
    { jobs: [job(), job({ id: "job-2", kind: "bash", label: "ls" })], foreground: [{ tabId: "tab-1", ref: "child-ref-9", name: "task", description: "核对任务", startedAt: 3_000 }] },
    "tab-1",
  );
  eq(entries.length, 2, "projection: bash jobs dropped, task job + same-tab foreground row kept");
  const jobRow = entries.find((entry) => entry.item.id === "registry:job:job-1");
  ok(jobRow !== undefined, "projection: task job projects under registry:job:<id>");
  ok(jobRow ? jobRow.ref === undefined : false, "projection: job rows carry no ref (job id is not a transcript ref)");
  eq(jobRow?.item.subject, "后台研究", "projection: job row label prefers the job label");
  eq(jobRow?.running, true, "projection: job rows are running entries");
  const fgRow = entries.find((entry) => entry.item.id === "registry:fg:child-ref-9");
  ok(fgRow !== undefined, "projection: foreground row projects under registry:fg:<ref>");
  eq(fgRow?.ref, "child-ref-9", "projection: foreground rows carry the real child ref");
  eq(fgRow?.item.subject, "核对任务", "projection: foreground label prefers the dispatch description (tl1209)");

  const foreign = registryRunningEntries(
    { foreground: [{ tabId: "tab-other", ref: "ref-x", name: "task", startedAt: 1_000 }] },
    "tab-1",
  );
  eq(foreign.length, 0, "projection: foreign-tab foreground rows stay out of this session");

  eq(registryRunningEntries(undefined, "tab-1").length, 0, "projection: undefined source yields no rows");
}

// 2. mergeRegistryRunningSubagents.
{
  const directory = buildSubagentDirectory([subagentTool()]);
  eq(directory.running.length, 1, "merge fixture: transcript running card present");
  const noop = mergeRegistryRunningSubagents(directory, []);
  ok(noop === directory, "merge: empty registry is a pure no-op (same reference)");

  const twin = { item: { ...directory.running[0]!.item, id: "registry:twin" }, running: true, status: "running" as const, ref: "child-ref-1" };
  const deduped = mergeRegistryRunningSubagents(directory, [twin]);
  eq(deduped.running.length, 1, "merge: ref collision drops the registry twin (transcript wins)");
  eq(deduped.running[0]!.item.id, "tool-transcript", "merge: the transcript entry survives the collision");

  const extra = { item: { kind: "tool", id: "registry:fg:ref-2", name: "ask", args: "", readOnly: true, status: "running" as const, subject: "补充核查" }, running: true, status: "running" as const, ref: "ref-2" };
  const merged = mergeRegistryRunningSubagents(directory, [extra]);
  eq(merged.running.length, 2, "merge: non-colliding registry row joins the running list");
  eq(merged.running[0]!.item.id, "registry:fg:ref-2", "merge: registry rows render first (newest-source-first)");
  ok(merged.ended === directory.ended, "merge: ended section untouched");
}

// 3. Panel: registry-only running rows suppress the empty state (线1/线2 钉).
{
  const empty = buildSubagentDirectory([]);
  const legacy = renderPanel({ directory: empty });
  ok(legacy.includes("subagents-panel__empty"), "panel: legacy empty state (no registry prop) unchanged");
  ok(!legacy.includes("subagents-panel__row"), "panel: legacy empty state renders no rows");

  const registry = registryRunningEntries(
    { jobs: [job()], foreground: [{ tabId: "", ref: "fg-ref-1", name: "task", description: "同步核对", startedAt: 3_000 }] },
    "tab-1",
  );
  const noopTerminate = async () => true;
  const markup = renderPanel({ directory: empty, registryRunning: registry, onTerminateSubagent: noopTerminate });
  ok(!markup.includes("subagents-panel__empty"), "panel: registry rows suppress 「暂无子代理」");
  ok(markup.includes("后台研究"), "panel: background job row visible (线2 钉)");
  ok(markup.includes("同步核对"), "panel: foreground (synchronous) child row visible (线1 钉)");
  ok(markup.includes("subagents-panel__row-wrap"), "panel: rows render in the running section");

  // A ref-bearing foreground row is terminable (the tl1209 bridge takes the
  // child ref); the ref-less job row must not render a terminate button.
  ok(markup.includes('data-subagent-terminate="fg-ref-1"'), "panel: foreground registry row carries the terminate bridge");
  ok(!markup.includes('data-subagent-terminate="registry:job:job-1"'), "panel: job rows never aim terminate at a job id");
}

// 4. Panel: transcript card + registry twin dedupe in the render.
{
  const directory = buildSubagentDirectory([subagentTool()]);
  const twin = registryRunningEntries(
    { foreground: [{ tabId: "", ref: "child-ref-1", name: "task", description: "card 描述", startedAt: 2_000 }] },
    "tab-1",
  );
  const markup = renderPanel({ directory, registryRunning: twin });
  eq((markup.match(/subagents-panel__row-wrap/g) ?? []).length, 1, "panel: one row for the transcript card (registry twin deduped)");
  ok(markup.includes("work") || markup.includes("task"), "panel: the transcript row's own title survives");
  ok(!markup.includes("card 描述"), "panel: the registry twin's label does not render");
}

// 5. App-level projection shape: the exact source App passes (active tab
// jobs + this tab's foreground rows) projects to the merge-ready entries.
{
  const source = {
    jobs: [job({ id: "j1" }), job({ id: "j2", kind: "bash" })],
    foreground: [{ tabId: "tab-1", ref: "r1", name: "task", startedAt: 5_000 }, { tabId: "tab-2", ref: "r2", name: "task", startedAt: 6_000 }],
  };
  const entries = registryRunningEntries(source, "tab-1");
  eq(entries.length, 2, "app: job j1 + own-tab foreground r1 (bash + foreign tab filtered)");
  ok(entries.every((entry) => entry.running), "app: every projected entry is running");

  const directory = mergeRegistryRunningSubagents(buildSubagentDirectory([]), entries);
  eq(directory.running.length, 2, "app: projection feeds the merge directly");
}

let code = 0;
if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  code = 1;
} else {
  process.stdout.write(`\n${passed} passed, 0 failed\n`);
}
process.exit(code);
