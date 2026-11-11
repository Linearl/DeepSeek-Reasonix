// Run: tsx src/__tests__/tl1548-dock-bash-output.test.tsx
// tl1548 — 右栏「后台命令」区 + bash 输出详情（用户视角后台任务输出视图）。
// 通道边界（任务书硬要求）：子代理面板工具流（tl1387fe liveTools / tl1537
// 转录轮询）与后台 bash 输出（tl1479 App.JobOutputForTab 非消费快照读）是
// 两通道——本件只给右栏补 bash 通道的 UI 面，复用 BashJobOutputDetail 原组件
// 与同一输出桥，不碰子代理通道。Contract under test:
// - 反向：bashJobs/onJobOutput 未注入 → 无「后台命令」段，面板 byte-for-byte
//   pre-1548（空态/行/计数原样）；bashJobs 有而 onJobOutput 缺 → 整段不渲染
//   （数据与出口同源注入，无死行）；
// - 正向：注入后「后台命令」段出现（数据源=active controller bash job），
//   行点击开输出详情（非分屏=面板体替换；分屏=右栏内容面），数据经同一
//   onJobOutput 桥注入并去噪（ANSI 剥离）；
// - 空态：无输出任务的详情渲染空态（tl1479 的 empty 态原样复用）；
// - 计数边界：子代理「运行中」计数不含 bash（bash 段自带口径）；bash-only
//   时不再误读「暂无子代理」空态；
// - 互斥：bash 详情与子代理详情同屏只有一张（打开一方关闭另一方）。

import { createElement } from "react";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
try {
  Object.defineProperty(dom.window.navigator, "language", { value: "zh-CN", configurable: true });
} catch {
  // jsdom 某些版本 language 不可配置——词典回落 en，仅影响文案断言强度。
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

const { buildSubagentDirectory } = await import("../lib/subagentDirectory");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
const { useLayoutStore } = await import("../store/layout");
await preloadLocale("zh");
await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");

import type { Item } from "../lib/useController";
import type { JobOutputView, JobView } from "../lib/types";

type ToolItem = Extract<Item, { kind: "tool" }>;

function flushMs(ms = 60): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function bashJob(overrides: Partial<JobView> = {}): JobView {
  return {
    id: "bash-1",
    kind: "bash",
    label: "后台构建 (make)",
    status: "running",
    startedAt: Date.now() - 60_000,
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
    subject: "核对任务",
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
    click: async (selector: string) => {
      const el = container.querySelector(selector);
      if (!el) throw new Error(`click target missing: ${selector}`);
      await act(async () => { (el as HTMLElement).click(); });
    },
    clickRowByText: async (text: string) => {
      const row = Array.from(container.querySelectorAll<HTMLButtonElement>(".subagents-panel__row"))
        .find((el) => el.textContent?.includes(text));
      if (!row) throw new Error(`row not found: ${text}`);
      await act(async () => { row.click(); });
    },
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

console.log("\ntl1548 dock bash output (background command section + output detail)");

// 1. 反向：未注入 → byte-for-byte pre-1548。
{
  const empty = buildSubagentDirectory([]);
  const idle = await mountPanel({ directory: empty });
  const markup = idle.container.innerHTML;
  ok(markup.includes("subagents-panel__empty"), "反向: 未注入时空态照常（pre-1548 等价）");
  ok(!markup.includes("data-subagents-bash"), "反向: 未注入时无「后台命令」段");
  ok(!markup.includes("data-capsule-bash-detail"), "反向: 未注入时无输出详情视图");
  await idle.unmount();

  // 子代理在场时同样无 bash 段（props 缺席 = 面板原样）。
  const withSub = await mountPanel({ directory: buildSubagentDirectory([subagentTool()]) });
  ok(withSub.container.querySelector("[data-subagents-bash]") === null, "反向: 子代理在场且未注入时无 bash 段");
  await withSub.unmount();
}

// 2. 反向：数据在而出口缺 → 整段不渲染（无死行）。
{
  const panel = await mountPanel({ directory: buildSubagentDirectory([]), bashJobs: [bashJob()] });
  ok(panel.container.querySelector("[data-subagents-bash]") === null, "反向: 缺 onJobOutput 时段不渲染（无死行）");
  ok(panel.container.innerHTML.includes("subagents-panel__empty"), "反向: 缺出口时空态保持");
  await panel.unmount();
}

// 3. 正向：bash-only → 段出现、空态退位、行渲染。
{
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve({ text: "", status: "running", found: true });
  const panel = await mountPanel({ directory: buildSubagentDirectory([]), bashJobs: [bashJob()], onJobOutput });
  const section = panel.container.querySelector("[data-subagents-bash]");
  ok(section !== null, "正向: bash-only 时「后台命令」段出现");
  eq(section?.querySelectorAll("[data-subagents-bash-job]").length, 1, "正向: 段内一行（job 一行）");
  ok(panel.container.querySelector('[data-subagents-bash-job="bash-1"]') !== null, "正向: 行带 job id");
  ok(!panel.container.innerHTML.includes("subagents-panel__empty"), "正向: bash-only 不再误读「暂无子代理」空态");
  await panel.unmount();
}

// 4. 计数边界：子代理「运行中」计数不含 bash（bash 段自带口径）。
{
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve({ text: "", status: "running", found: true });
  const panel = await mountPanel({
    directory: buildSubagentDirectory([subagentTool()]),
    bashJobs: [bashJob()],
    onJobOutput,
  });
  const sections = panel.container.querySelectorAll(".subagents-panel__section");
  const runningTitle = sections[0]?.querySelector("h3")?.textContent ?? "";
  ok(runningTitle.includes("1") && !runningTitle.includes("2"), `计数边界: 运行中计数不含 bash（${JSON.stringify(runningTitle)}）`);
  eq(sections[0]?.querySelectorAll("li").length, 1, "计数边界: 运行段仍只 1 行（子代理）");
  eq(panel.container.querySelector("[data-subagents-bash]")?.querySelectorAll("li").length, 1, "计数边界: bash 段 1 行");
  await panel.unmount();
}

// 5. 非分屏：行点击 → 面板体切换输出详情，数据经同一桥注入并去噪；返回回列表。
{
  const calls: Array<[string, string]> = [];
  const onJobOutput = (tabId: string, jobID: string): Promise<JobOutputView> => {
    calls.push([tabId, jobID]);
    return Promise.resolve({ text: "\x1b[31mbuild failed\x1b[0m\nexit 1\n", status: "running", found: true });
  };
  const panel = await mountPanel({
    directory: buildSubagentDirectory([subagentTool()]),
    bashJobs: [bashJob()],
    onJobOutput,
  });
  await panel.click('[data-subagents-bash-job="bash-1"]');
  await flushMs();
  const detail = panel.container.querySelector("[data-capsule-bash-detail]");
  ok(detail !== null, "非分屏: 详情打开");
  eq(detail?.getAttribute("data-capsule-bash-detail"), "bash-1", "非分屏: 详情带 job id");
  eq(calls[0]?.[0], "", "非分屏: 首拉 tabId 为空（激活控制器）");
  eq(calls[0]?.[1], "bash-1", "非分屏: 首拉 jobID 正确");
  await flushMs();
  const pre = panel.container.querySelector("[data-capsule-bash-detail-text]");
  eq(pre?.getAttribute("data-capsule-bash-detail-text"), "ready", "非分屏: 首拉完成 ready 态");
  eq(pre?.textContent, "build failed\nexit 1\n", "非分屏: 渲染去噪文本（ANSI 已剥离）");
  // 返回 → 列表恢复、段恢复。
  await panel.click(".capsule-panel__back");
  await flushMs();
  ok(panel.container.querySelector("[data-capsule-bash-detail]") === null, "非分屏: 返回后详情关闭");
  ok(panel.container.querySelector("[data-subagents-bash]") !== null, "非分屏: 返回后列表恢复");
  await panel.unmount();
}

// 6. 空态：无输出任务详情渲染空态（tl1479 empty 态原样）。
{
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve({ text: "", status: "running", found: true });
  const panel = await mountPanel({ directory: buildSubagentDirectory([]), bashJobs: [bashJob()], onJobOutput });
  await panel.click('[data-subagents-bash-job="bash-1"]');
  await flushMs();
  eq(
    panel.container.querySelector("[data-capsule-bash-detail-text]")?.getAttribute("data-capsule-bash-detail-text"),
    "empty",
    "空态: 无输出任务进入 empty 态",
  );
  await panel.unmount();
}

// 7. 分屏互斥：bash 详情与子代理详情同屏只有一张。
{
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve({ text: "hello-output\n", status: "running", found: true });
  const panel = await mountPanel({
    directory: buildSubagentDirectory([subagentTool()]),
    bashJobs: [bashJob()],
    onJobOutput,
    detailEnabled: true,
  });
  // bash 行点击 → 右栏出现 bash 详情（左列列表保留）。
  await panel.click('[data-subagents-bash-job="bash-1"]');
  await flushMs();
  ok(panel.container.querySelector("[data-subagents-bash-detail]") !== null, "分屏: 右栏出现 bash 详情面");
  ok(panel.container.querySelector("[data-capsule-bash-detail]") !== null, "分屏: 详情视图挂载");
  ok(panel.container.textContent?.includes("hello-output"), "分屏: 输出文本渲染");
  ok(panel.container.querySelector("[data-subagents-bash]") !== null, "分屏: 左列后台命令段保留");
  // tl1548 抬宽位：bash 详情与子代理详情同一张内容面 → 同据抬宽（tl1426
  // 四级链的 subagentsDetailWideActive；关详情回落）。
  eq(useLayoutStore.getState().subagentsDetailWideActive, true, "抬宽位: bash 详情打开时上报来源标识");
  await panel.click(".capsule-panel__back");
  await flushMs();
  eq(useLayoutStore.getState().subagentsDetailWideActive, false, "抬宽位: 返回后回落");
  // 子代理行点击 → 详情切换为子代理面（互斥：bash 详情关）。
  await panel.clickRowByText("核对任务");
  ok(panel.container.querySelector("[data-capsule-bash-detail]") === null, "分屏: 打开子代理详情后 bash 详情关闭");
  ok(panel.container.querySelector(".subagents-panel__detailview") !== null, "分屏: 子代理详情面在");
  // bash 行再点 → 切回 bash 详情。
  await panel.click('[data-subagents-bash-job="bash-1"]');
  await flushMs();
  ok(panel.container.querySelector("[data-capsule-bash-detail]") !== null, "分屏: 切回 bash 详情");
  ok(panel.container.querySelector("[data-subagents-bash-detail]") !== null, "分屏: bash 详情面在右栏");
  await panel.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
