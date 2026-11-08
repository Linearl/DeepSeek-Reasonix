// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/bash-job-output-detail.test.tsx
// tl1479 — capsule 后台 bash 输出详情：入口出现/缺席等价、视图数据通路、
// 跟随滚动开关、复制、自动刷新节流（in-flight 守卫 + 终态停轮询）、
// 大数据量输出性能钉（前端显示上限）。
// 反向验证：onJobOutput 未注入时 bash 行不出现「输出」入口（pre-1479 等价）；
// 组件渲染的文本是去噪后的（stripTerminalControlSequences），复制同一份。

import { JSDOM } from "jsdom";

import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import type { JobOutputView, JobView } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function section(title: string) {
  process.stdout.write(`\n${title}\n`);
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.SVGElement = dom.window.SVGElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis.window, "matchMedia", {
  configurable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }),
});

const { displaySlice, jobStatusLabel, BASH_OUTPUT_DISPLAY_MAX_CHARS, BashJobOutputDetail } = await import("../components/BashJobOutputDetail");
const { CapsuleIndicator } = await import("../components/CapsulePanel");

// --- helpers ---

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

async function flush(ms = 25) {
  await new Promise((resolve2) => setTimeout(resolve2, ms));
}

let activeRoot: Root | null = null;
let activeHost: HTMLElement | null = null;

async function mount(node: ReactNode) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(<LocaleProvider>{node}</LocaleProvider>);
    await flush();
  });
}

async function unmount() {
  if (activeRoot) {
    await act(async () => activeRoot?.unmount());
  }
  activeHost?.remove();
  activeRoot = null;
  activeHost = null;
}

function clickButton(el: Element | null | undefined) {
  if (!el) throw new Error("click target missing");
  act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

// --- section 1: pure helpers ---

section("纯函数：displaySlice 显示上限 / jobStatusLabel 状态映射");

{
  const { shown, truncated } = displaySlice("short output");
  eq(shown, "short output", "短文本原样返回");
  eq(truncated, false, "短文本不标记截断");

  const big = "A".repeat(2_000_000) + "TAIL-MARKER";
  const capped = displaySlice(big);
  eq(capped.shown.length, BASH_OUTPUT_DISPLAY_MAX_CHARS, "两百万字符截到显示上限");
  eq(capped.truncated, true, "超限标记截断");
  ok(capped.shown.endsWith("TAIL-MARKER"), "截断保留尾部（最新输出）");

  const zh = jobStatusLabel("running", (key) => String(key));
  eq(zh, "subagent.phase.running", "running 映射到既有键");
  eq(jobStatusLabel("killed", (key) => String(key)), "composer.bashOutputStatusKilled", "killed 映射到新键");
  eq(jobStatusLabel("mystery", (key) => String(key)), "mystery", "未知状态回退原文");
}

// --- section 2: 反向验证 — absent prop keeps pre-1479 rows ---

section("反向验证：无 onJobOutput 时 bash 行不出现输出入口");

{
  await mount(<CapsuleIndicator jobs={[bashJob()]} />);
  clickButton(document.querySelector(".capsule__trigger"));
  await flush();
  const row = document.querySelector('[data-capsule-job-id="bash-1"]');
  ok(row !== null, "bash 行照常渲染");
  eq(document.querySelector(".capsule-panel__output"), null, "无数据出口则无「输出」按钮（pre-1479 等价）");
  eq(document.querySelector("[data-capsule-bash-detail]"), null, "无输出详情视图");
  await unmount();
}

// --- section 3: 入口出现 + 数据通路 + 去噪 ---

section("入口出现：点击「输出」打开详情，数据经 onJobOutput 注入并去噪");

{
  const calls: Array<[string, string]> = [];
  const onJobOutput = (tabId: string, jobID: string): Promise<JobOutputView> => {
    calls.push([tabId, jobID]);
    return Promise.resolve({ text: "\x1b[31mbuild failed\x1b[0m\nexit 1\n", status: "running", found: true });
  };
  await mount(<CapsuleIndicator jobs={[bashJob()]} onJobOutput={onJobOutput} />);
  clickButton(document.querySelector(".capsule__trigger"));
  await flush();
  const openButton = document.querySelector<HTMLButtonElement>('[data-capsule-job-id="bash-1"] .capsule-panel__output');
  ok(openButton !== null, "bash 行出现「输出」入口");
  clickButton(openButton);
  await flush(60);
  const detail = document.querySelector("[data-capsule-bash-detail]");
  ok(detail !== null, "详情视图打开");
  eq(detail?.getAttribute("data-capsule-bash-detail"), "bash-1", "详情视图带 job id");
  eq(calls[0]?.[0], "", "首拉 tabId 为空（激活控制器）");
  eq(calls[0]?.[1], "bash-1", "首拉 jobID 正确");
  const pre = document.querySelector("[data-capsule-bash-detail-text]");
  eq(pre?.getAttribute("data-capsule-bash-detail-text"), "ready", "首拉完成进入 ready 态");
  eq(pre?.textContent, "build failed\nexit 1\n", "渲染去噪文本（ANSI 已剥离）");
  eq(document.querySelector(".capsule-panel__list"), null, "详情态隐藏列表");

  // 复制：写入剪贴板的是去噪后的同一份文本。
  const written: string[] = [];
  Object.defineProperty(globalThis.navigator, "clipboard", {
    configurable: true,
    value: { writeText: async (s: string) => { written.push(s); } },
  });
  clickButton(document.querySelector("[data-capsule-bash-copy]"));
  await flush(30);
  eq(written.length, 1, "复制写入剪贴板一次");
  eq(written[0], "build failed\nexit 1\n", "复制内容为去噪全文");

  // 返回列表。
  clickButton(document.querySelector(".capsule-panel__back"));
  await flush();
  ok(document.querySelector("[data-capsule-bash-detail]") === null, "返回后详情关闭");
  ok(document.querySelector(".capsule-panel__list") !== null, "返回后列表恢复");
  await unmount();
}

// --- section 4: 失败 / missing 态 ---

section("异常态：读取失败与 job 消失");

{
  let shouldFail = true;
  const onJobOutput = (): Promise<JobOutputView> =>
    shouldFail ? Promise.reject(new Error("boom")) : Promise.resolve({ text: "", status: "done", found: false });
  await mount(<BashJobOutputDetail job={bashJob()} tabId="" onJobOutput={onJobOutput} onBack={() => {}} refreshMs={40} />);
  await flush(60);
  const pre = document.querySelector("[data-capsule-bash-detail-text]");
  eq(pre?.getAttribute("data-capsule-bash-detail-text"), "failed", "读取失败进入 failed 态");

  shouldFail = false;
  // 手动触发一次新一轮询：等 2 个节拍。
  await flush(120);
  eq(document.querySelector("[data-capsule-bash-detail]")?.getAttribute("data-capsule-bash-state"), "missing", "job 消失进入 missing 态");
  await unmount();
}

// --- section 5: 跟随滚动开关 ---

section("跟随滚动：默认跟随、可关闭、关闭后视口不动");

{
  let value: JobOutputView = { text: "chunk-1\n", status: "running", found: true };
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve(value);
  await mount(<BashJobOutputDetail job={bashJob()} tabId="" onJobOutput={onJobOutput} onBack={() => {}} refreshMs={40} />);
  await flush(60);
  const pre = document.querySelector<HTMLElement>("[data-capsule-bash-detail-text]");
  ok(pre !== null, "文本视口渲染");
  Object.defineProperty(pre, "scrollHeight", { configurable: true, value: 1000 });
  const toggle = document.querySelector<HTMLButtonElement>("[data-capsule-bash-follow]");
  eq(toggle?.getAttribute("data-capsule-bash-follow"), "on", "跟随默认开启");
  eq(toggle?.getAttribute("aria-pressed"), "true", "跟随 aria-pressed=true");

  // 新内容到达 → 钉底。
  value = { text: "chunk-1\nchunk-2\n", status: "running", found: true };
  await flush(120);
  eq(pre.scrollTop, 1000, "跟随开启时新内容钉底");

  // 关闭跟随 → 新内容不再移动视口。
  clickButton(toggle);
  await flush();
  eq(toggle?.getAttribute("data-capsule-bash-follow"), "off", "关闭后标记 off");
  pre.scrollTop = 0;
  value = { text: "chunk-1\nchunk-2\nchunk-3\n", status: "running", found: true };
  await flush(120);
  eq(pre.scrollTop, 0, "跟随关闭时视口不动");
  await unmount();
}

// --- section 6: 自动刷新节流 ---

section("刷新节流：in-flight 跳过、终态停轮询");

{
  let calls = 0;
  let release: (() => void) | null = null;
  let gate: Promise<void> = new Promise<void>((resolve) => { release = resolve; });
  let value: JobOutputView = { text: "slow-first\n", status: "running", found: true };
  const onJobOutput = (): Promise<JobOutputView> => {
    calls += 1;
    return gate.then(() => value);
  };
  await mount(<BashJobOutputDetail job={bashJob()} tabId="" onJobOutput={onJobOutput} onBack={() => {}} refreshMs={40} />);
  await flush(10);
  eq(calls, 1, "首拉立即发起");

  // 首拉挂起 3 个节拍：in-flight 守卫把后续节拍全部跳过。
  await flush(150);
  eq(calls, 1, "上一跳未返回则后续节拍跳过（节流）");

  release?.();
  await flush(60);
  const duringRunning = calls;
  ok(duringRunning >= 2, `恢复后继续轮询（calls=${duringRunning}）`);

  // 终态：最后一次拉取返回 done 后停止轮询。
  value = { text: "slow-first\nfinal\n", status: "done", found: true };
  await flush(120);
  const atTerminal = calls;
  ok(atTerminal >= duringRunning, "终态前轮询继续");
  await flush(200);
  eq(calls, atTerminal, "终态后不再轮询");
  await unmount();
}

// --- section 7: 大数据量输出性能钉 ---

section("性能钉：两百万字符输出 — 渲染封顶且在预算内完成");

{
  const big = "\x1b[32m" + ("line-of-build-log\n".repeat(180_000)) + "\x1b[0m";
  const onJobOutput = (): Promise<JobOutputView> => Promise.resolve({ text: big, status: "done", found: true });
  const startedAt = Date.now();
  await mount(<BashJobOutputDetail job={bashJob()} tabId="" onJobOutput={onJobOutput} onBack={() => {}} refreshMs={40} />);
  await flush(80);
  const elapsed = Date.now() - startedAt;
  const pre = document.querySelector("[data-capsule-bash-detail-text]");
  const rendered = pre?.textContent ?? "";
  ok(rendered.length <= BASH_OUTPUT_DISPLAY_MAX_CHARS, `渲染长度封顶（${rendered.length}）`);
  ok(rendered.endsWith("\n") && rendered.includes("line-of-build-log"), "渲染保留尾部内容");
  ok(document.querySelector("[data-capsule-bash-truncated]") !== null, "截断提示可见");
  ok(elapsed < 5000, `挂载+首拉在预算内完成（${elapsed}ms < 5000ms）`);
  await unmount();
}

// --- summary ---

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
