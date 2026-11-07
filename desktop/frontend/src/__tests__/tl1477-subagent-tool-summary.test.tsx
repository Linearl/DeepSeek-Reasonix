// Run: tsx src/__tests__/tl1477-subagent-tool-summary.test.tsx
// tl1477 子代理工具摘要行 contract under test:
// - 单行摘要：工具调用行 = 工具名 + 关键参数（完整 args 才提取；MCP resolvedName
//   优先展示）；参数提取不出时后端盖章 subject 兜底（权威元数据，非推断值）；
// - 只读详情 tab：行点击展开只读详情块（参数 pretty JSON / 输出 / 错误），
//   无任何输入面；再点收起；默认收起；
// - 半截 JSON 不猜默认值（本件主钉）：JSON.parse 失败 = 截断态——行出截断
//   标记，详情按原文逐字显示，绝不从残片解析/补默认值；流式中 argChars-only
//   = 接收态（显示已收字符数）；空 args 且无流式痕迹 = 无参数。
// 分界（与 tl1475）：tl1475 管主转录 ToolCard 流式期间的参数预览；本件管
// SubagentsDockPanel 详情内工具调用行的完成后摘要 + 只读详情 + 截断态。

import { createElement } from "react";
import { JSDOM } from "jsdom";

// jsdom 先于 react-dom（harness 纪律）：先建 DOM 再加载 React 渲染器。
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// 词典文案断言钉在 zh 词典上（tl1387 同款预载，首帧即 zh）。
try {
  Object.defineProperty(dom.window.navigator, "language", { value: "zh-CN", configurable: true });
} catch {
  // jsdom 某些版本 language 不可配置——此时文案断言退化为结构断言。
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

const { toolCallArgsState, toolCallSummaryLine, toolCallArgsDetailText, formatToolArgChars } = await import("../lib/toolCallSummary");
const { buildSubagentDirectory, registryRunningEntries } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { ForegroundSubagentView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

console.log("\ntl1477 subagent tool-call summary line + read-only detail + truncation states");

// ─────────────────────────────────────────────────────────────────────────────
// 1. 纯函数层：四种参数态 + 摘要行构成。
{
  // complete：可解析 → 摘要 = 工具名 · 关键参数；详情 = pretty JSON。
  const st = toolCallArgsState('{"url":"https://example.com/report"}');
  eq(st.kind, "complete", "纯函数: 完整 JSON → complete");
  eq(toolCallSummaryLine({ name: "web_fetch", args: '{"url":"https://example.com/report"}' }), "web_fetch · https://example.com/report", "纯函数: 摘要 = 工具名 · 关键参数");
  ok(toolCallArgsDetailText(st).includes('"url": "https://example.com/report"'), "纯函数: 详情 = pretty JSON（冒号带空格）");
  eq(toolCallArgsDetailText(toolCallArgsState('"hi"')), '"hi"', "纯函数: 标量 JSON 也是 complete（原样 pretty）");

  // truncated：半截 JSON → 截断态；摘要绝不出残片参数。
  const raw = '{"command":"git sta';
  const tr = toolCallArgsState(raw);
  eq(tr.kind, "truncated", "纯函数: 半截 JSON → truncated");
  eq(toolCallArgsDetailText(tr), raw, "纯函数: 截断详情 = 原文逐字（一个字符不改）");
  eq(toolCallSummaryLine({ name: "bash", args: raw }), "bash", "纯函数: 截断摘要只出工具名（残片不变参数）");
  eq(toolCallSummaryLine({ name: "bash", args: raw, subject: "核对提交历史" }), "bash · 核对提交历史", "纯函数: 截断+盖章 subject → subject 兜底（权威元数据）");
  ok(!toolCallSummaryLine({ name: "bash", args: raw }).includes("git sta"), "纯函数: 反向——截断摘要不含残片文本");

  // receiving：args 空 + argChars>0 → 接收态（只有字符数可知）。
  const rc = toolCallArgsState("", 5120);
  eq(rc.kind, "receiving", "纯函数: 空 args + argChars → receiving");
  eq(toolCallSummaryLine({ name: "write_file", args: "", argChars: 5120 }), "write_file", "纯函数: 接收态摘要只出工具名");
  eq(formatToolArgChars(5120), "5.1k", "纯函数: 字符数紧凑格式（与 ToolCard 同语义）");
  eq(formatToolArgChars(42), "42", "纯函数: 小字符数原样");

  // empty：无参工具如实「无参数」。
  eq(toolCallArgsState("").kind, "empty", "纯函数: 空 args 无痕迹 → empty");
  eq(toolCallSummaryLine({ name: "todo_write", args: "" }), "todo_write", "纯函数: 无参摘要 = 工具名");

  // MCP resolvedName 优先展示。
  eq(toolCallSummaryLine({ name: "proxy_call", resolvedName: "mcp-server/tool", args: '{"path":"a.ts"}' }), "mcp-server/tool · a.ts", "纯函数: resolvedName 优先展示");
  eq(toolCallArgsState(undefined).kind, "empty", "纯函数: undefined args → empty（防御面）");
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. DOM 层：面板详情内工具调用行全链。
function fgEntry(overrides: Partial<ForegroundSubagentView> = {}): ForegroundSubagentView {
  return {
    tabId: "tab-1",
    ref: "child-fg-1477",
    name: "task",
    description: "摘要行核对子代理",
    startedAt: 3_000,
    parentToolCallId: "call-1477",
    ...overrides,
  };
}

function subagentTool(overrides: Partial<ToolItem> = {}): ToolItem {
  return {
    kind: "tool",
    id: "tool-x",
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

{
  const empty = buildSubagentDirectory([]);
  const registry = registryRunningEntries({ foreground: [fgEntry()] }, "tab-1");
  const childTools: Item[] = [
    subagentTool({ id: "call-1477/fetch", name: "web_fetch", args: '{"url":"https://example.com/report"}', status: "done", parentId: "call-1477", output: "Report body line" }),
    subagentTool({ id: "call-1477/scan", name: "bash", args: '{"command":"pwd"}', status: "running", parentId: "call-1477" }),
    subagentTool({ id: "call-1477/half", name: "bash", args: '{"command":"git sta', status: "running", parentId: "call-1477" }),
    subagentTool({ id: "call-1477/recv", name: "write_file", args: "", argChars: 5120, status: "running", parentId: "call-1477" }),
    subagentTool({ id: "call-1477/bare", name: "todo_write", args: "", status: "done", parentId: "call-1477" }),
  ];
  const panel = await mountPanel({ directory: empty, detailEnabled: true, registryRunning: registry, transcriptItems: childTools });
  await panel.clickRow("摘要行核对子代理");

  const rows = Array.from(panel.container.querySelectorAll(".subagents-panel__livetool"));
  eq(rows.length, 5, "DOM: 五条子工具行全部渲染（既有行结构不回归）");
  eq(rows.map((el) => el.getAttribute("data-subagents-livetool-status")).join(","), "done,running,running,running,done", "DOM: 每行状态标记保留");

  // 验收①：单行摘要 = 工具名 + 关键参数。
  ok((rows[0]?.textContent ?? "").includes("web_fetch · https://example.com/report"), "DOM: 摘要 = 工具名 · 参数（web_fetch url）");
  ok((rows[1]?.textContent ?? "").includes("bash · pwd"), "DOM: 摘要 = 工具名 · 参数（bash command）");
  ok((rows[4]?.textContent ?? "").includes("todo_write"), "DOM: 无参行摘要 = 工具名");

  // 默认收起：详情块不存在。
  eq(panel.container.querySelector('[data-subagents-livetool-detail="true"]') !== null, false, "DOM 反向: 默认不展开详情");

  // 验收②：点开只读详情 tab（参数 pretty JSON + 输出块），无输入面。
  const toggle0 = rows[0]?.querySelector<HTMLButtonElement>(".subagents-panel__livetool-toggle");
  ok(toggle0 !== null && toggle0.getAttribute("aria-expanded") === "false", "DOM: 行是可展开按钮（aria-expanded=false）");
  await act(async () => { toggle0!.click(); });
  eq(toggle0?.getAttribute("aria-expanded"), "true", "DOM: 点击后 aria-expanded=true");
  const detail0 = rows[0]?.querySelector('[data-subagents-livetool-detail="true"]');
  ok(detail0 !== null, "DOM: 点击展开只读详情块");
  ok((detail0?.textContent ?? "").includes("参数"), "DOM: 详情带参数标签（zh 词典）");
  ok((detail0?.textContent ?? "").includes('"url": "https://example.com/report"'), "DOM: 详情展示 pretty JSON");
  ok((detail0?.textContent ?? "").includes("输出") && (detail0?.textContent ?? "").includes("Report body line"), "DOM: 完成调用详情带输出块");
  eq(detail0?.querySelector("input, textarea, select, button:not(.subagents-panel__livetool-toggle)") !== null, false, "DOM 反向: 详情内无任何输入面（只读）");
  await act(async () => { toggle0!.click(); });
  eq(rows[0]?.querySelector('[data-subagents-livetool-detail="true"]') !== null, false, "DOM: 再点收起详情");

  // 验收③：半截 JSON 行 = 截断标记 + 原文详情 + 不猜值。
  const halfRow = rows[2];
  ok(halfRow?.querySelector('[data-subagents-livetool-truncated="true"]') !== null, "DOM: 半截 JSON 行带截断标记");
  ok(!(halfRow?.textContent ?? "").includes("git sta"), "DOM 反向: 截断行摘要不含残片文本");
  await act(async () => { halfRow?.querySelector<HTMLButtonElement>(".subagents-panel__livetool-toggle")!.click(); });
  const halfDetail = halfRow?.querySelector('[data-subagents-livetool-detail="true"]');
  ok(halfDetail !== null, "DOM: 截断行可展开详情");
  ok((halfDetail?.querySelector('p[data-subagents-livetool-truncated="true"]')?.textContent ?? "").includes("参数不完整"), "DOM: 截断详情带截断注记（不推断取值）");
  const halfPre = halfDetail?.querySelector("pre");
  eq(halfPre?.textContent, '{"command":"git sta', "DOM: 截断详情 = 原文逐字");
  eq(halfPre?.textContent?.includes('"command": "git sta'), false, "DOM 反向: 不出现 pretty 化残片（无补全/猜值）");
  await act(async () => { halfRow?.querySelector<HTMLButtonElement>(".subagents-panel__livetool-toggle")!.click(); });

  // 接收态：argChars-only 行 = 接收标记 + 字符数，不出参数块。
  const recvRow = rows[3];
  const recvFlag = recvRow?.querySelector('[data-subagents-livetool-receiving="true"]');
  ok(recvFlag !== null, "DOM: 流式中行带接收标记");
  ok((recvFlag?.textContent ?? "").includes("5.1k"), "DOM: 接收标记带已收字符数（5.1k）");
  await act(async () => { recvRow?.querySelector<HTMLButtonElement>(".subagents-panel__livetool-toggle")!.click(); });
  const recvDetail = recvRow?.querySelector('[data-subagents-livetool-detail="true"]');
  ok((recvDetail?.querySelector('p[data-subagents-livetool-receiving="true"]')?.textContent ?? "").includes("参数接收中"), "DOM: 接收详情带接收注记");
  eq(recvDetail?.querySelector("pre") !== null, false, "DOM 反向: 接收态不出参数块（没有的参数不显示）");

  // 无参工具：如实「无参数」。
  const bareRow = rows[4];
  await act(async () => { bareRow?.querySelector<HTMLButtonElement>(".subagents-panel__livetool-toggle")!.click(); });
  ok(bareRow?.querySelector('p[data-subagents-livetool-empty="true"]')?.textContent?.includes("无参数") ?? false, "DOM: 无参工具详情如实「无参数」");
  eq(bareRow?.querySelector("pre") !== null, false, "DOM 反向: 无参工具不出参数块");

  await panel.unmount();
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
