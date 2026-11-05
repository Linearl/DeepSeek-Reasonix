// Run: tsx src/__tests__/tl1387-detail-running.test.tsx
// tl1387 二轮（包 74 装机实测打回）contract under test:
// - 判定修正（本件主修）：RUNNING entry 永不命中 detailNoPreview——「暂无预览
//   内容：重启后仅保留名称与状态」描述的是重启水合的 ended 卡，运行中子代理
//   命中它就是语义错误（用户实测：子代理 11:16 启动、app 10:52 起未重启，详情
//   却显示重启文案）。running 空内容时显示诚实运行注记（新键
//   subagentPanel.runningNoPreview），ref 在场时保持「完成后可读」pending
//   note，工具流在场时渲染工具流；
// - 用户场景全链（线2 收口后的端到端）：后台 job 行（callId 盖章）+ 转录中的
//   嵌套工具事件 ⇒ 详情渲染实时工具流、无任何兜底/注记文案；
// - 重启语义保留（验收③反向钉）：ended 卡无 summary/error/preview 仍走
//   detailNoPreview；带 summary 的 ended 卡不渲染任何兜底文案。

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// 本件钉断言的是词典文案本身（重启兜底 vs 诚实运行注记），必须钉在 zh 词典上：
// navigator.language 决定 detectLocale，预载保证首帧渲染即有 zh 词典（否则
// translate 回落 en，缺席断言全部退化为平凡通过）。
try {
  Object.defineProperty(dom.window.navigator, "language", { value: "zh-CN", configurable: true });
} catch {
  // jsdom 某些版本 language 不可配置——此时回退 EN 词典断言（RESTART_COPY_EN）。
}

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

const { buildSubagentDirectory, registryRunningEntries } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { JobView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

function job(overrides: Partial<JobView> = {}): JobView {
  return {
    id: "job-bg-1",
    kind: "task",
    label: "后台核实队列",
    status: "running",
    startedAt: 1_000,
    ...overrides,
  };
}

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "tool-card",
    name: "task",
    args: '{"prompt":"work"}',
    readOnly: false,
    status: "running",
    startedAt: 2_000,
    ...overrides,
  };
}

async function mountPanel(props: Record<string, unknown>) {
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

const RESTART_COPY = "重启后仅保留名称与状态";

console.log("\ntl1387 detail running (running entries never read the restart copy)");

// 1. 用户场景全链：后台 job 行（二轮 callId 盖章）+ 转录嵌套工具事件 ⇒ 详情
//    渲染实时工具流，无重启文案、无占位注记。
{
  const empty = buildSubagentDirectory([]);
  const registry = registryRunningEntries({ jobs: [job({ id: "job-live", parentToolCallId: "call-bg-1" })] }, "tab-1");
  const nested: Item[] = [
    subagentTool({ id: "call-bg-1/fetch", name: "web_fetch", args: '{"url":"https://example.com"}', status: "done", parentId: "call-bg-1" }),
    subagentTool({ id: "call-bg-1/scan", name: "bash", args: '{"command":"grep -r tl1387"}', status: "running", parentId: "call-bg-1" }),
  ];
  const panel = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registry, transcriptItems: nested });
  await panel.clickRow("后台核实队列");
  ok(panel.container.querySelector('[data-subagents-livetools="true"]') !== null, "用户场景: 后台 job 行详情渲染实时工具流（二轮主钉）");
  eq((panel.container.querySelectorAll(".subagents-panel__livetool")).length, 2, "用户场景: callId 下两条嵌套工具全部渲染");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "用户场景: 运行中详情不出现重启兜底文案");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') === null, "用户场景: 工具流在场时不渲染运行占位注记");
  await panel.unmount();
}

// 2. 运行中无内容可显（job 行无 callId 的老遥测形态）：不命中重启文案，
//    渲染诚实运行注记。
{
  const empty = buildSubagentDirectory([]);
  const registry = registryRunningEntries({ jobs: [job({ id: "job-legacy" })] }, "tab-1");
  const panel = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registry, transcriptItems: [] });
  await panel.clickRow("后台核实队列");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "运行空态: 老 job 行详情不出现重启兜底文案（修复主钉）");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') !== null, "运行空态: 渲染诚实运行注记（runningNoPreview）");
  ok(panel.container.querySelector('[data-subagents-live-pending="true"]') === null, "运行空态: 无 ref 不渲染完成后可读 pending note");
  await panel.unmount();
}

// 3. 运行中转录卡丢 ref（水合窗形态）且子工具未产生：同样不命中重启文案。
{
  const card = subagentTool({ id: "call-hydr-1", status: "running", subagentProgress: undefined, subject: "失卡转录行" });
  const panel = await mountPanel({ directory: buildSubagentDirectory([card]), detailEnabled: true, transcriptItems: [card] });
  await panel.clickRow("失卡转录行");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "运行空态: 丢 ref 转录卡详情不出现重启兜底文案");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') !== null, "运行空态: 丢 ref 转录卡渲染诚实运行注记");
  await panel.unmount();
}

// 4. 运行中 + 有 ref + 无嵌套工具：保持「完成后可读」pending note（tl1202
//    形态不回归），不渲染运行占位注记。
{
  const card = subagentTool({
    id: "call-ref-1", status: "running", subject: "有ref运行行",
    subagentProgress: { phase: "running", reasoning: "", text: "", notice: "", lastActivityAt: 2_500, truncated: false, startedAt: 2_000, ref: "child-ref-1" },
  });
  const panel = await mountPanel({ directory: buildSubagentDirectory([card]), detailEnabled: true, transcriptItems: [card] });
  await panel.clickRow("有ref运行行");
  ok(panel.container.querySelector('[data-subagents-live-pending="true"]') !== null, "有ref运行行: pending note 保持");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') === null, "有ref运行行: 不重复渲染运行占位注记");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "有ref运行行: 不出现重启兜底文案");
  await panel.unmount();
}

// 5. 验收③反向钉：重启后已结束卡（无 summary/error/preview）仍走重启兜底
//    文案——detailNoPreview 语义原样保留。
{
  const ended = subagentTool({ id: "call-ended-1", status: "done", subagentProgress: undefined, subject: "重启旧卡" });
  const directory = buildSubagentDirectory([ended]);
  eq(directory.ended.length, 1, "重启语义 fixture: 无进度卡归入 ended");
  const panel = await mountPanel({ directory, detailEnabled: true });
  await panel.clickRow("重启旧卡");
  ok(panel.container.textContent?.includes(RESTART_COPY) ?? false, "重启语义: ended 空卡仍显示重启兜底文案（原样保留）");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') === null, "重启语义: ended 卡不渲染运行占位注记");
  await panel.unmount();
}

// 6. ended 卡带 summary：无任何兜底文案（原有形状不回归）。
{
  const ended = subagentTool({ id: "call-ended-2", status: "done", subagentProgress: undefined, subject: "有结果旧卡", summary: "核对完成，输出见报告" });
  const panel = await mountPanel({ directory: buildSubagentDirectory([ended]), detailEnabled: true });
  await panel.clickRow("有结果旧卡");
  ok(panel.container.textContent?.includes("核对完成，输出见报告") ?? false, "ended带summary: summary 块照常渲染");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "ended带summary: 不出现兜底文案");
  await panel.unmount();
}

// 7. 运行中卡带 preview text：预览块照常渲染，无占位注记、无兜底。
{
  const card = subagentTool({
    id: "call-prev-1", status: "running", subject: "带预览运行行",
    subagentProgress: { phase: "text", reasoning: "", text: "正在整理核对结论……", notice: "", lastActivityAt: 2_500, truncated: false, startedAt: 2_000, ref: "child-ref-2" },
  });
  const panel = await mountPanel({ directory: buildSubagentDirectory([card]), detailEnabled: true, transcriptItems: [card] });
  await panel.clickRow("带预览运行行");
  ok(panel.container.textContent?.includes("正在整理核对结论……") ?? false, "带预览运行行: 预览块照常渲染");
  ok(panel.container.querySelector('[data-subagents-running-blank="true"]') === null, "带预览运行行: 有预览不渲染运行占位注记");
  ok(!panel.container.textContent?.includes(RESTART_COPY), "带预览运行行: 不出现重启兜底文案");
  await panel.unmount();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
