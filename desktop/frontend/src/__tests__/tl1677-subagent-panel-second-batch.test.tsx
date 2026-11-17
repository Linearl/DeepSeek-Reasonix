// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1677-subagent-panel-second-batch.test.tsx
// tl1677 任务面板四缺陷第二批 contract under test:
// a) 名字深化：task 无 description 时回退 prompt——但 subject 是 one-liner，
//    全文 flooding 改为首个非空行（subjectOf + streamingToolSubject 同纪律）；
// b) 实时刷新：运行行时长接面板墙钟（15s 一跳，事件间隙不再冻结读数）；
//    sidecar 轮询 5s→2s 对齐 dock live 节奏（SUBAGENTS_PERSISTED_POLL_MS）。
//    选型=保持轮询（ListSubagentsByParent 全量投影 re-pull 即增量，零新接口；
//    事件推送需新 Wails 通道面大收益同）——tl1537/tl1434 同一论证。
// c) 任务书/预览大块：详情面无上限 pre clamp（6 行+渐隐）+「展开全文」portal
//    悬浮卡（fixed+遮罩），转录回放与列表行不再被顶走；
// d) 状态单一生真源：已结束行残留的运行中相位不再显示「运行中」——
//    statusLabelKey 仅运行行信任相位；buildSubagentDirectory 对已结束行清掉
//    非终态相位（图标同归 status 口径）。

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
function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const { buildSubagentDirectory, SUBAGENTS_PERSISTED_POLL_MS } = await import("../lib/subagentDirectory");
const { subjectOf, streamingToolSubject } = await import("../lib/tools");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "call-dispatch-1",
    name: "task",
    args: '{"description":"调研任务","prompt":"深入调研"}',
    readOnly: false,
    status: "running",
    startedAt: 2_000,
    ...overrides,
  };
}

async function mount(element: ReturnType<typeof createElement>) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, element));
  });
  return {
    container,
    clickRow: async (rowText: string) => {
      const row = Array.from(container.querySelectorAll<HTMLButtonElement>(".subagents-panel__row"))
        .find((el) => el.textContent?.includes(rowText));
      if (!row) throw new Error(`row not found: ${rowText}`);
      await act(async () => { row.click(); });
    },
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

console.log("\ntl1677 subagent panel second batch: prompt-first-line title, wall-clock duration, clamp popover, status single truth");

// a) prompt 首行（名字深化）。
{
  eq(
    subjectOf("task", '{"description":"调研任务","prompt":"深入调研 X"}'),
    "调研任务",
    "a: the dispatch description still wins",
  );
  eq(
    subjectOf("task", '{"description":"","prompt":"深入调研 X 并核对证据链\\n第二行还有更多"}'),
    "深入调研 X 并核对证据链",
    "a: a description-less task falls back to the prompt's FIRST non-empty line (no full-text flooding)",
  );
  eq(
    subjectOf("task", '{"prompt":"\\n\\n首行之前是空行"}'),
    "首行之前是空行",
    "a: leading blank lines are skipped",
  );
  eq(
    streamingToolSubject("task", '{"description":"","prompt":"流式任务书第一行\\n流式中的更多内容'),
    "流式任务书第一行",
    "a: a still-streaming prompt subject is first-line too",
  );
}

// b) 实时刷新（节奏 + 墙钟时长）。
{
  eq(SUBAGENTS_PERSISTED_POLL_MS, 2000, "b: the persisted poll rides the dock live cadence (2s)");
}

// b) 墙钟时长：运行行按 now 折算，事件间隙不再冻结（DOM：展开行 meta）。
{
  // startedAt is a real unix-ms stamp (useController sets Date.now() at
  // dispatch); 60s ago + the 15s wall clock renders 1m 0s.
  const startedAt = Date.now() - 60_000;
  const card = subagentTool({ startedAt, status: "running", subagentProgress: { phase: "running", reasoning: "", text: "", notice: "", lastActivityAt: startedAt + 500, truncated: false, startedAt } });
  const panel = await mount(createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([card]) } as never));
  await panel.clickRow("调研任务");
  const meta = panel.container.querySelector(".subagents-panel__detail-meta");
  ok(meta !== null && /1m/.test(meta.textContent ?? ""), "b: the running row's elapsed time follows the panel wall clock (60s → 1m 0s)");
  await panel.unmount();
}

// d) 状态单一生真源：已结束卡残留运行中相位 → 文案与图标归 status 口径。
{
  const card = subagentTool({
    id: "call-ended",
    status: "done",
    durationMs: 60_000,
    output: "Subagent outcome: completed sa_x",
    subagentProgress: { phase: "running", reasoning: "", text: "", notice: "", lastActivityAt: 3_000, truncated: false, startedAt: 2_000 },
  });
  const directory = buildSubagentDirectory([card]);
  eq(directory.ended.length, 1, "d fixture: the settled card lands in the ended section");
  eq(directory.ended[0]!.phase, undefined, "d: the stale running phase is dropped from the ended entry");
  const panel = await mount(createElement(SubagentsDockPanel, { directory } as never));
  const rows = Array.from(panel.container.querySelectorAll(".subagents-panel__row"));
  ok(rows.length === 1, "d fixture: exactly one ended row");
  ok(!(rows[0]!.textContent ?? "").includes("running"), "d: the ended row no longer reads running");
  ok((rows[0]!.textContent ?? "").includes("completed"), "d: the ended row reads its terminal status");
  await panel.unmount();
}

// d 补钉：终态相位在已结束行保留（图标仍能区分 failed/cancelled）。
{
  const card = subagentTool({
    id: "call-cancelled",
    status: "stopped",
    output: "Subagent outcome: cancelled sa_y",
    subagentProgress: { phase: "cancelled", reasoning: "", text: "", notice: "", lastActivityAt: 3_000, truncated: false, startedAt: 2_000 },
  });
  const directory = buildSubagentDirectory([card]);
  eq(directory.ended[0]!.phase, "cancelled", "d: a terminal phase survives on the ended entry (icon semantics keep it)");
}

// c) 大块 clamp + portal 悬浮卡。
{
  const longSummary = "证据链一\n".repeat(120);
  const card = subagentTool({
    id: "call-ended-long",
    status: "done",
    output: `Subagent outcome: completed sa_long\n${longSummary}`,
    summary: longSummary,
    durationMs: 60_000,
    subagentProgress: { phase: "completed", reasoning: "", text: "", notice: "", lastActivityAt: 3_000, truncated: false, startedAt: 2_000 },
  });
  const directory = buildSubagentDirectory([card]);
  const panel = await mount(createElement(SubagentsDockPanel, { directory, detailEnabled: true, sessionPath: "", onReadSubagent: undefined } as never));
  await panel.clickRow("调研任务");
  const block = panel.container.querySelector('[data-clamped-text="true"]');
  ok(block !== null, "c: an oversized summary block renders clamped");
  ok(block?.querySelector(".subagents-panel__detailview-text--clamped") !== null, "c: the clamped pre is capped (6 lines + fade)");
  const expand = block?.querySelector<HTMLButtonElement>('[data-subagents-clamp-expand="true"]');
  ok(expand !== null, "c: the expand button is present");
  await act(async () => { expand!.click(); });
  const popover = document.body.querySelector('[data-subagents-popover="true"]');
  ok(popover !== null, "c: expand opens the portal popover (fixed, not in the panel flow)");
  ok((popover?.querySelector(".subagent-popover__text")?.textContent ?? "").includes("证据链一"), "c: the popover carries the full text");
  await act(async () => {
    (popover as HTMLElement).click();
  });
  eq(document.body.querySelector('[data-subagents-popover="true"]') === null, true, "c: clicking the overlay closes the popover");
  await panel.unmount();
}

// c 反钉：短文本不 clamp（byte-for-byte 原块）。
{
  const card = subagentTool({
    id: "call-ended-short",
    status: "done",
    summary: "很短的结论",
    durationMs: 1_000,
    output: "Subagent outcome: completed sa_short",
    subagentProgress: { phase: "completed", reasoning: "", text: "", notice: "", lastActivityAt: 3_000, truncated: false, startedAt: 2_000 },
  });
  const panel = await mount(createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([card]), detailEnabled: true } as never));
  await panel.clickRow("调研任务");
  ok(panel.container.querySelector('[data-clamped-text="true"]') === null, "c: a short summary renders the plain block (no clamp)");
  await panel.unmount();
}

console.log(`\n  tl1677: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
