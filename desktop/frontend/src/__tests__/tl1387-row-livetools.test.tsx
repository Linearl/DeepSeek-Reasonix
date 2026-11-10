// Run: tsx src/__tests__/tl1387-row-livetools.test.tsx
// tl1387 三轮（刀1）contract under test: the DEFAULT (plan-C, detailEnabled
// off) running row's INLINE expansion carries the live tool-call stream —
// the same nested_sink projection the plan-A detail view already renders
// (lib/subagentDirectory.subagentChildTools, keyed by the dispatch call id).
// 验收②「点击可看工作过程」在默认开关下成立，不再要求打开
// experimentalSubagentDetail。分钉：
// 1. plan C 运行转录卡：行内展开渲染 data-subagents-livetools，嵌套工具逐条
//    在列（含运行中子工具的 spin 状态语义，同一 ChildToolRow 标记）；
// 2. registry job 行（二轮 callId 盖章）：转录里没有派发卡也能在行内展开里
//    看到工作过程——第一层 stall 形态的第二层补偿同样覆盖行内展开；
// 3. byte-for-byte 反向钉：ended 行展开永远无 livetools；备用视图（不传
//    transcriptItems）运行行展开无 livetools 且行本体照常展开（证明点击
//    通路未被破坏，不是死行）。
// 骨架与 tl1387-detail-running.test.tsx 同款（jsdom + zh 词典预载 + act 点击）。

import { createElement } from "react";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// 断言涉及 i18n 标签（subagentPanel.liveTools），预载 zh 词典保证首帧即有
// 中文（navigator.language 决定 detectLocale；部分 jsdom 版本不可配置时回
// 落 en——本钉不依赖词典文案本身，标签存在性经 data 属性断言，不受回落影响）。
try {
  Object.defineProperty(dom.window.navigator, "language", { value: "zh-CN", configurable: true });
} catch {
  /* fall back to en dict */
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

console.log("\ntl1387 row livetools (plan-C inline expansion shows the work process)");

// 1. plan C（detailEnabled=false）运行转录卡：行内展开渲染工具流——默认
//    开关下验收②成立。嵌套两条（done+running）逐条在列。
{
  const card = subagentTool({ id: "call-fg-1", subject: "前台核实子代理" });
  const nested: Item[] = [
    subagentTool({ id: "call-fg-1/fetch", name: "web_fetch", args: '{"url":"https://example.com"}', status: "done", parentId: "call-fg-1" }),
    subagentTool({ id: "call-fg-1/scan", name: "bash", args: '{"command":"grep -r tl1387"}', status: "running", parentId: "call-fg-1" }),
  ];
  const panel = await mountPanel({
    directory: buildSubagentDirectory([card]),
    detailEnabled: false,
    transcriptItems: [card, ...nested],
  });
  await panel.clickRow("前台核实子代理");
  ok(panel.container.querySelector('[data-subagents-livetools="true"]') !== null, "planC转录卡: 运行行内展开渲染实时工具流（刀1主钉）");
  eq(panel.container.querySelectorAll(".subagents-panel__livetool").length, 2, "planC转录卡: callId 下两条嵌套工具全部渲染");
  ok(panel.container.querySelector('[data-subagents-livetool-status="running"]') !== null, "planC转录卡: 运行中子工具带状态语义（同一 ChildToolRow 标记）");
  await panel.unmount();
}

// 2. registry job 行（callId 盖章、转录无派发卡）：第一层 stall 形态下行内
//    展开同样能看到工作过程。
{
  const empty = buildSubagentDirectory([]);
  const registry = registryRunningEntries({ jobs: [job({ id: "job-live", parentToolCallId: "call-bg-9" })] }, "tab-1");
  const nested: Item[] = [
    subagentTool({ id: "call-bg-9/read", name: "read_file", args: '{"path":"a.go"}', status: "done", parentId: "call-bg-9" }),
  ];
  const panel = await mountPanel({
    directory: empty,
    detailEnabled: false,
    registryRunning: registry,
    transcriptItems: nested,
  });
  await panel.clickRow("后台核实队列");
  ok(panel.container.querySelector('[data-subagents-livetools="true"]') !== null, "registryJob行: 行内展开渲染实时工具流");
  eq(panel.container.querySelectorAll(".subagents-panel__livetool").length, 1, "registryJob行: callId 戳对应的嵌套工具在列");
  await panel.unmount();
}

// 3a. ended 行：展开永远无工具流（byte-for-byte 反向钉）。
{
  const ended = subagentTool({ id: "call-ended-1", status: "done", subagentProgress: undefined, subject: "已结束子代理", summary: "核对完成" });
  const panel = await mountPanel({
    directory: buildSubagentDirectory([ended]),
    detailEnabled: false,
    transcriptItems: [ended],
  });
  await panel.clickRow("已结束子代理");
  ok(panel.container.querySelector(".subagents-panel__detail") !== null, "ended行: 行内展开照常（点击通路未破坏）");
  ok(panel.container.textContent?.includes("核对完成") ?? false, "ended行: summary 照常渲染");
  ok(panel.container.querySelector('[data-subagents-livetools="true"]') === null, "ended行: 展开无工具流（byte-for-byte）");
  await panel.unmount();
}

// 3b. 备用视图形态（不传 transcriptItems）：运行行展开无工具流，行本体照常。
{
  const card = subagentTool({ id: "call-bare-1", subject: "备用视图运行行" });
  const panel = await mountPanel({
    directory: buildSubagentDirectory([card]),
    detailEnabled: false,
  });
  await panel.clickRow("备用视图运行行");
  ok(panel.container.querySelector(".subagents-panel__detail") !== null, "备用视图: 运行行展开照常（不是死行）");
  ok(panel.container.querySelector('[data-subagents-livetools="true"]') === null, "备用视图: 无 transcriptItems 不渲染工具流（byte-for-byte）");
  await panel.unmount();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
