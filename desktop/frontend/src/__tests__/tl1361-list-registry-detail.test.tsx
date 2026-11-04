// Run: tsx src/__tests__/tl1361-list-registry-detail.test.tsx
// tl1361 列表层衔接（tl1387 覆盖度验证后的收口件）contract under test:
// - 两层 running 可见性（对齐协调线验收①）：同步子代理（foreground registry
//   行）与后台子代理（job 行）各自在 dock 面板 running 区可见、空态被抑制；
// - callId 维度（本件修复）：
//   · 投影——foreground 行把 lifecycle 遥测的 parentToolCallId（task 667，
//     即真实派发 call id）盖章为 entry.callId；job 行无该数据源、不盖章；
//   · 合并——registry 双胞的 callId 与转录卡 item.id 碰撞即去重（转录卡
//     丢 progress-ref 的水合窗形态不再产出两行）；
//   · 详情——registry 前台行详情的运行中工具流按 callId 查转录（修复前查
//     合成 id registry:fg:<ref> 恒不匹配 → 失卡场景详情永远纯预览）；
//     job 行维持预览行（无 callId、无终止桥）；callId 缺席的老遥测行不崩、
//     保持 pending note；
// - 原语义不回归：转录投影行（无 callId 字段）详情仍按 item.id 命中。

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
import type { JobView, ForegroundSubagentView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

function job(overrides: Partial<JobView> = {}): JobView {
  return {
    id: "job-bg-1",
    kind: "task",
    label: "后台抓取队列",
    status: "running",
    startedAt: 1_000,
    ...overrides,
  };
}

function fgEntry(overrides: Partial<ForegroundSubagentView> = {}): ForegroundSubagentView {
  return {
    tabId: "tab-1",
    ref: "child-fg-1",
    name: "task",
    description: "同步核对报告",
    startedAt: 3_000,
    parentToolCallId: "call-dispatch-1",
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
    ...overrides,
  };
}

type MountedPanel = {
  container: HTMLDivElement;
  clickRow: (rowText: string) => Promise<void>;
  unmount: () => Promise<void>;
};

async function mountPanel(props: Record<string, unknown>): Promise<MountedPanel> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, props as never)));
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

console.log("\ntl1361 list-layer registry detail (two-source visibility + callId tool stream)");

// 1. 投影：foreground 行盖章 callId；job 行不盖章。
{
  const entries = registryRunningEntries({ jobs: [job()], foreground: [fgEntry(), fgEntry({ ref: "child-fg-2", parentToolCallId: undefined })] }, "tab-1");
  eq(entries.length, 3, "projection: 1 job + 2 foreground rows");
  const fgRow = entries.find((entry) => entry.item.id === "registry:fg:child-fg-1");
  ok(fgRow !== undefined, "projection: foreground row present");
  eq(fgRow?.callId, "call-dispatch-1", "tl1361: foreground row stamps parentToolCallId as callId");
  eq(fgRow?.ref, "child-fg-1", "projection: foreground row keeps its transcript ref");
  const fgNoCall = entries.find((entry) => entry.item.id === "registry:fg:child-fg-2");
  ok(fgNoCall !== undefined && fgNoCall.callId === undefined, "tl1361: foreground row without parentToolCallId carries no callId (old telemetry)");
  const jobRow = entries.find((entry) => entry.item.id === "registry:job:job-bg-1");
  ok(jobRow !== undefined && jobRow.callId === undefined, "tl1361: job rows carry no callId (jobs.View has no call id)");
}

// 2. 合并：callId 碰撞去重（水合窗丢 ref 的双胞形态）。
{
  // The hydration-window shape: the transcript card exists with the real call
  // id but LOST its progress-preview ref, so ref-based dedupe cannot fire.
  const card = subagentTool({ id: "call-dispatch-1", status: "running", subagentProgress: undefined });
  const directory = buildSubagentDirectory([card]);
  ok(directory.running[0]?.ref === undefined, "merge fixture: the hydrated card carries no ref");
  const twin = registryRunningEntries({ foreground: [fgEntry()] }, "tab-1");
  const merged = mergeRegistryRunningSubagents(directory, twin);
  eq(merged.running.length, 1, "tl1361: callId collision dedupes the ref-less registry twin (one row per child)");
  eq(merged.running[0]?.item.id, "call-dispatch-1", "tl1361: the transcript card survives the callId collision");

  // Distinct callId → still appended, with callId preserved for the detail.
  const other = registryRunningEntries({ foreground: [fgEntry({ ref: "child-fg-9", parentToolCallId: "call-dispatch-2", description: "另一核对" })] }, "tab-1");
  const merged2 = mergeRegistryRunningSubagents(directory, other);
  eq(merged2.running.length, 2, "merge: non-colliding callId row still joins");
  eq(merged2.running.find((entry) => entry.item.id === "registry:fg:child-fg-9")?.callId, "call-dispatch-2", "merge: appended row keeps its callId");
}

// 3. 两层可见性 + 详情工具流全链（面板 DOM）。
{
  const empty = buildSubagentDirectory([]);
  // The stall fixture: NO transcript cards at all (the tl1387 blind panel
  // shape), but the transcript DOES hold the child's re-parented tool events
  // under the dispatch call id (nested_sink.go: Tool.ParentID = call id) —
  // exactly the partial-loss shape the callId stamp exists for.
  const childTools: Item[] = [
    subagentTool({ id: "call-dispatch-1/web-a", name: "web_fetch", args: '{"url":"https://example.com/report"}', status: "done", parentId: "call-dispatch-1" }),
    subagentTool({ id: "call-dispatch-1/web-b", name: "bash", args: '{"command":"ls"}', status: "running", parentId: "call-dispatch-1" }),
    subagentTool({ id: "call-dispatch-1/web-c", name: "write_file", args: '{"path":"out/report.md"}', status: "error", parentId: "call-dispatch-1" }),
  ];
  const registry = registryRunningEntries({ jobs: [job()], foreground: [fgEntry()] }, "tab-1");

  // 3a. 两层行各自可见，空态被抑制（本文件自足复钉；tl1387 钉有基线版）。
  const noopTerminate = async () => true;
  const listMarkup = renderToStaticMarkup(
    createElement(LocaleProvider, null, createElement(SubagentsDockPanel, { directory: empty, registryRunning: registry, onTerminateSubagent: noopTerminate } as never)),
  );
  ok(listMarkup.includes("后台抓取队列"), "两层可见性: 后台子代理 job 行可见（线2）");
  ok(listMarkup.includes("同步核对报告"), "两层可见性: 同步子代理 foreground 行可见（线1）");
  ok(!listMarkup.includes("subagents-panel__empty"), "两层可见性: registry 行在场即不显示「暂无子代理」");
  ok(listMarkup.includes('data-subagent-terminate="child-fg-1"'), "两层可见性: foreground 行带终止桥（ref 键）");
  ok(!listMarkup.includes('data-subagent-terminate="registry:job:job-bg-1"'), "两层可见性: job 行无终止桥（job id 非 ref）");

  // 3b. 修复主钉：registry 前台行详情按 callId 命中转录工具流。
  //     修复前查询键是合成 id（registry:fg:child-fg-1），永不匹配
  //     parentId=call-dispatch-1 的事件 → livetools 恒缺、详情纯预览。
  const panel = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registry, transcriptItems: childTools });
  await panel.clickRow("同步核对报告");
  const live = panel.container.querySelector('[data-subagents-livetools="true"]');
  ok(live !== null, "详情工具流: registry 前台行详情渲染运行中工具流（修复主钉）");
  const rows = Array.from(panel.container.querySelectorAll(".subagents-panel__livetool"));
  eq(rows.length, 3, "详情工具流: callId 下三条嵌套子工具全部渲染");
  eq(rows.map((el) => el.getAttribute("data-subagents-livetool-status")).join(","), "done,running,error", "详情工具流: 每行带工具状态标记");
  ok((rows[0]?.textContent ?? "").includes("https://example.com/report"), "详情工具流: web_fetch 行带参数主题");
  ok((rows[2]?.textContent ?? "").includes("out/report.md"), "详情工具流: write_file 行带参数主题");
  ok(panel.container.querySelector('[data-subagents-live-pending="true"]') !== null, "详情工具流: 有 ref 的运行中行保持「完成后可读」pending note");
  // 反向断言：合成 id 从不是重挂键（修复前的错误查询键形状）。
  ok(childTools.every((item) => (item as ToolItem).parentId !== "registry:fg:child-fg-1"), "详情工具流: 合成 id 从不是重挂键");
  await panel.unmount();

  // 3c. callId 缺席（老遥测）与 job 行：详情不崩、维持预览行形状。
  const registryOld = registryRunningEntries(
    {
      jobs: [job({ id: "job-bg-2", label: "后台构建" })],
      foreground: [fgEntry({ ref: "child-fg-3", parentToolCallId: undefined, description: "旧遥测同步行" })],
    },
    "tab-1",
  );
  const panelOld = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registryOld, transcriptItems: childTools });
  await panelOld.clickRow("旧遥测同步行");
  eq(panelOld.container.querySelector('[data-subagents-livetools="true"]') !== null, false, "详情工具流: 无 callId 的 foreground 行不渲染工具流（查 item.id 无匹配）");
  ok(panelOld.container.querySelector('[data-subagents-live-pending="true"]') !== null, "详情工具流: 无 callId 有 ref 行保持 pending note（不空白）");
  await panelOld.unmount();

  const panelJob = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registryOld, transcriptItems: childTools });
  await panelJob.clickRow("后台构建");
  eq(panelJob.container.querySelector('[data-subagents-livetools="true"]') !== null, false, "详情工具流: job 行详情无工具流（预览行设计不变）");
  ok(panelJob.container.textContent?.includes("后台构建") ?? false, "详情工具流: job 行详情标题在场（不空白不崩）");
  await panelJob.unmount();
}

// 4. 转录投影行原语义不回归：无 callId → item.id 仍是查询键。
{
  const card = subagentTool({
    id: "call-native-1", status: "running", subject: "转录卡子代理",
    subagentProgress: { phase: "tool", reasoning: "", text: "", notice: "", lastActivityAt: 4_000, truncated: false, startedAt: 2_000, ref: "sub-native" },
  });
  const nested = subagentTool({ id: "call-native-1/x", name: "bash", args: '{"command":"pwd"}', status: "running", parentId: "call-native-1" });
  const panel = await mountPanel({ directory: buildSubagentDirectory([card]), detailEnabled: true, transcriptItems: [card, nested] });
  await panel.clickRow("转录卡子代理");
  const rows = Array.from(panel.container.querySelectorAll(".subagents-panel__livetool"));
  eq(rows.length, 1, "原语义: 转录投影行按 item.id 命中嵌套工具（无 callId 字段时行为逐字节不变）");
  await panel.unmount();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
