// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1664-subagent-ghost-row-and-replay.test.tsx
// tl1664 任务面板双缺陷 contract under test:
// - 幽灵行（运行中计数虚高）：派发卡在「dispatch → 首条 progress 事件」窗口
//   内没有 progress ref，sidecar running 行此前仅按 ref 配对 → 同一子代理
//   双计两行，子代理结束（卡拿到 outcome ref）才归一。修复链：
//   · Go ListSubagentsByParent 投影补 parentToolCallId（desktop/subagents_app.go）；
//   · sidecar 运行行盖章 callId，merge 与转录卡 item.id（= 派发 call id）
//     配对去重；
//   · mergeRegistryRunningSubagents 的 known 集合补 callId——sidecar 行与
//     job/foreground 双胞（job 行无 ref，ref 检查永不命中）不再双计。
// - 名字（裸 task）：裸工具名（task/read_only_task/parallel_tasks/fleet）不再
//   充当行名——无名行渲染「子代理（HH:MM）」兜底（可辨识，不伪造细节）；
//   description / 非裸 profile 名照旧优先。
// - 「在主区打开」：原 handler 只读一次并弹 toast（半成品）；修复后打开主区
//   只读回放面（SubagentReplaySurface）。本件钉 surface 组件：只读徽章、
//   只读脚注、终态单读 / 运行中轮询（复用 dock detail 读桥）、失败可重试。

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

const {
  buildSubagentDirectory,
  mergePersistedRunningSubagents,
  mergeRegistryRunningSubagents,
  isBareSubagentToolName,
  subagentRowLabel,
} = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { SubagentReplaySurface } = await import("../components/SubagentReplaySurface");
const { LocaleProvider } = await import("../lib/i18n");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { SubagentArtifactView, HistoryMessage } from "../lib/types";
import type { SubagentReplayTarget } from "../components/SubagentReplaySurface";

type ToolItem = Extract<Item, { kind: "tool" }>;

function persistedView(overrides: Partial<SubagentArtifactView> = {}): SubagentArtifactView {
  return {
    ref: "sa_ghost1",
    createdAt: 5_000,
    updatedAt: 6_000,
    status: "running",
    kind: "task",
    name: "task",
    description: "",
    hasTranscript: false,
    pendingMail: 0,
    ...overrides,
  };
}

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "call-dispatch-1",
    name: "task",
    args: '{"prompt":"work"}',
    readOnly: false,
    status: "running",
    startedAt: 2_000,
    ...overrides,
  };
}

type Mounted = {
  container: HTMLDivElement;
  flush: () => Promise<void>;
  unmount: () => Promise<void>;
};

async function mount(element: ReturnType<typeof createElement>): Promise<Mounted> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, element));
  });
  return {
    container,
    // The persisted-directory effect resolves through a promise chain; one
    // extra act tick lets the settled state reach the DOM.
    flush: async () => {
      await act(async () => {});
    },
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

console.log("\ntl1664 subagent panel: ghost-row dedupe, unnamed fallback, open-in-main replay");

// 1. 幽灵行主钉：无 ref 卡 × 带 parentToolCallId 的 sidecar 行 → 一行。
{
  const card = subagentTool({ id: "call-dispatch-1", status: "running", subagentProgress: undefined });
  const directory = buildSubagentDirectory([card]);
  ok(directory.running[0]?.ref === undefined, "fixture: the dispatched card carries no progress ref yet (the ghost window)");
  const view = persistedView({ ref: "sa_ghost1", parentToolCallId: "call-dispatch-1" });
  const merged = mergePersistedRunningSubagents(directory, [view]);
  eq(merged.running.length, 1, "ghost-row: the sidecar twin dedupes against the ref-less card by dispatch call id (one row per child)");

  // 无 parentToolCallId 的旧记录保持原语义（ref 配对不变）。
  const legacy = mergePersistedRunningSubagents(directory, [persistedView({ ref: "sa_legacy" })]);
  eq(legacy.running.length, 2, "ghost-row: a legacy record without parentToolCallId still joins (ref-only pairing unchanged)");

  // 不同派发的 sidecar 行照常加入。
  const other = mergePersistedRunningSubagents(directory, [persistedView({ ref: "sa_other", parentToolCallId: "call-dispatch-2", description: "另一调研" })]);
  eq(other.running.length, 2, "ghost-row: a genuinely distinct child still joins the running list");
}

// 2. 投影钉：sidecar 运行行盖章 callId。
{
  const directory = buildSubagentDirectory([]);
  const merged = mergePersistedRunningSubagents(directory, [persistedView({ parentToolCallId: "call-77" })]);
  eq(merged.running[0]?.callId, "call-77", "projection: the persisted running row stamps parentToolCallId as callId");
}

// 3. registry 双胞：sidecar 行（callId 在场）× 无 ref 的 job 行（同 callId）→ 一行。
{
  const directory = buildSubagentDirectory([]);
  const withSidecar = mergePersistedRunningSubagents(directory, [persistedView({ ref: "sa_bg1", parentToolCallId: "call-bg-9", description: "后台核查" })]);
  eq(withSidecar.running.length, 1, "fixture: the sidecar row alone");
  const { registryRunningEntries } = await import("../lib/subagentDirectory");
  const jobTwin = registryRunningEntries(
    { jobs: [{ id: "job-9", kind: "task", label: "后台核查", status: "running", startedAt: 5_500, parentToolCallId: "call-bg-9" }] },
    "tab-1",
  );
  const merged = mergeRegistryRunningSubagents(withSidecar, jobTwin);
  eq(merged.running.length, 1, "registry: the ref-less job twin dedupes against the sidecar row by callId (known-set fix)");
}

// 4. 裸工具名判定与行名回退链。
{
  ok(isBareSubagentToolName("task") && isBareSubagentToolName("read_only_task") && isBareSubagentToolName("parallel_tasks") && isBareSubagentToolName("fleet"), "bare: dispatch tool names are bare");
  ok(!isBareSubagentToolName("research") && !isBareSubagentToolName("explore"), "bare: named profiles stay informative");
  eq(subagentRowLabel("", "task", "task"), "", "label: a bare profile name yields no label (unnamed fallback takes over)");
  eq(subagentRowLabel("调研任务", "task"), "调研任务", "label: the dispatch description wins");
  eq(subagentRowLabel(undefined, "research"), "research", "label: a named profile is kept");
}

// 5. 面板 DOM：无名行渲染「子代理（HH:MM）/ Subagent (HH:MM)」兜底；description 行照旧。
// （JSDOM 的 navigator 是 en-US，LocaleProvider 走 en 字典——断言双语言匹配。）
{
  const UNNAMED = /子代理（|Subagent \(/;
  const view = persistedView({ ref: "sa_anon", description: "", name: "task" });
  const panel = await mount(createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([]), onListPersisted: async () => [view], sessionPath: "/tmp/session.jsonl" } as never));
  await panel.flush();
  await panel.flush();
  const rows = Array.from(panel.container.querySelectorAll(".subagents-panel__row"));
  ok(rows.some((el) => UNNAMED.test(el.textContent ?? "")), "unnamed: the label-less sidecar row renders the unnamed fallback");
  await panel.unmount();

  const named = await mount(createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([]), onListPersisted: async () => [persistedView({ ref: "sa_named", description: "独立复核证据链" })], sessionPath: "/tmp/session.jsonl" } as never));
  await named.flush();
  await named.flush();
  ok(Array.from(named.container.querySelectorAll(".subagents-panel__row-title")).some((el) => (el.textContent ?? "").includes("独立复核证据链")), "unnamed: a described row keeps its description title");
  await named.unmount();
}

// 6. 无名兜底对裸卡（无 args 信息）同样生效。
{
  const card = subagentTool({ id: "call-bare", name: "task", args: "", status: "running", startedAt: 43_200_000 });
  const registry = undefined;
  const panel = await mount(createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([card]), registryRunning: registry } as never));
  ok(Array.from(panel.container.querySelectorAll(".subagents-panel__row")).some((el) => /子代理（|Subagent \(/.test(el.textContent ?? "")), "unnamed: a bare task card without args info falls back too");
  await panel.unmount();
}

// 7. 回放面：终态单读渲染只读徽章 + 转录 + 只读脚注；失败可重试。
{
  const target: SubagentReplayTarget = { sessionPath: "/tmp/session.db", ref: "sa_r1", title: "调研任务", running: false };
  const messages: HistoryMessage[] = [
    { role: "user", content: "调研 X" },
    { role: "assistant", content: "调研结论……" },
  ] as unknown as HistoryMessage[];
  let reads = 0;
  const surface = await mount(createElement(SubagentReplaySurface, {
    target,
    onReadSubagent: async () => {
      reads += 1;
      return messages;
    },
    onClose: () => {},
  }));
  const body = surface.container.querySelector(".subagent-replay__body");
  eq(body?.getAttribute("data-subagent-replay-state"), "ready", "replay: an ended child reads once and renders");
  eq(reads, 1, "replay: ended children read exactly once (the transcript is final)");
  ok(surface.container.querySelector('[data-subagent-replay-badge="true"]') !== null, "replay: the read-only badge is present");
  ok((surface.container.querySelector(".subagent-replay__readonly")?.textContent ?? "").length > 0, "replay: the read-only footnote is present");
  ok((surface.container.querySelector("[data-subagent-replay-title]")?.textContent ?? "").includes("调研任务"), "replay: the header shows the entry title");
  await surface.unmount();

  const failedSurface = await mount(createElement(SubagentReplaySurface, {
    target,
    onReadSubagent: async () => { throw new Error("boom"); },
    onClose: () => {},
  }));
  eq(failedSurface.container.querySelector(".subagent-replay__body")?.getAttribute("data-subagent-replay-state"), "failed", "replay: a failed read shows the failed state");
  ok(failedSurface.container.querySelector(".subagents-panel__retry") !== null, "replay: a failed read offers the retry button");
  await failedSurface.unmount();
}

// 8. 回放面：运行中子代理 pending 哨兵降级（不红脸），继续轮询。
{
  const target: SubagentReplayTarget = { sessionPath: "/tmp/session.db", ref: "sa_r2", title: "s", running: true };
  const pendingErr = Object.assign(new Error('transcript "sa_r2.jsonl" has not been saved yet'), {});
  const surface = await mount(createElement(SubagentReplaySurface, {
    target,
    onReadSubagent: async () => { throw pendingErr; },
    onClose: () => {},
  }));
  eq(surface.container.querySelector(".subagent-replay__body")?.getAttribute("data-subagent-replay-state"), "pending", "replay: the pre-first-snapshot window degrades to the pending note");
  await surface.unmount();
}

console.log(`\n  tl1664: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
