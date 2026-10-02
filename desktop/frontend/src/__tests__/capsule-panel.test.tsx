// Run: tsx src/__tests__/capsule-panel.test.tsx
// Task 447 capsule: composer floating panel — pure helpers (elapsed format,
// job grouping), trigger states, running sections with stop wiring, the
// ended sub-agents directory (ListSubagentsByParent), and the transcript
// detail states (loading/failed via stubbed ReadSubagentSession; the ready
// branch mounts the shared Transcript and is asserted at source level,
// matching the lifecycle-test precedent).

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { LocaleProvider } from "../lib/i18n";
import type { HistoryMessage, JobView, SubagentArtifactView } from "../lib/types";

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

// Bridge calls are injected props (same pattern as onCancelJob), so the stubs
// here never load the bridge module graph.
const listCalls: string[] = [];
const readCalls: Array<[string, string]> = [];
let listResult: Promise<SubagentArtifactView[]> = Promise.resolve([]);
let readResult: Promise<unknown> = Promise.resolve([]);
const onListSubagents = (sessionPath: string) => {
  listCalls.push(sessionPath);
  return listResult;
};
const onReadSubagent = (sessionPath: string, ref: string) => {
  readCalls.push([sessionPath, ref]);
  return readResult as Promise<HistoryMessage[]>;
};

const { formatCapsuleElapsed, groupCapsuleJobs, CapsuleIndicator } = await import("../components/CapsulePanel");

// --- helpers ---

function job(overrides: Partial<JobView> = {}): JobView {
  return {
    id: "bash-1",
    kind: "bash",
    label: "后台拉起 serve (8787)",
    status: "running",
    startedAt: Date.now() - 60_000,
    ...overrides,
  };
}

function ended(overrides: Partial<SubagentArtifactView> = {}): SubagentArtifactView {
  return {
    ref: "sa_20261002_100000_000000000_000000000001",
    createdAt: Date.now() - 30_000,
    updatedAt: Date.now() - 20_000,
    status: "completed",
    kind: "task",
    name: "调研子代理",
    model: "test/model",
    parentSession: "capsule-parent",
    hasTranscript: true,
    ...overrides,
  };
}

let activeRoot: Root | null = null;
let activeHost: HTMLElement | null = null;

async function flush(ms = 10) {
  await new Promise((resolve2) => setTimeout(resolve2, ms));
}

async function renderIndicator(props: { jobs?: JobView[]; onCancelJob?: (jobID: string) => Promise<boolean>; sessionPath?: string }) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <CapsuleIndicator
          jobs={props.jobs ?? []}
          onCancelJob={props.onCancelJob}
          sessionPath={props.sessionPath}
          onListSubagents={onListSubagents}
          onReadSubagent={onReadSubagent}
        />
      </LocaleProvider>,
    );
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) {
    await act(async () => activeRoot?.unmount());
  }
  activeHost?.remove();
  activeRoot = null;
  activeHost = null;
}

function triggerButton(): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(".capsule__trigger");
  if (!button) throw new Error("missing capsule trigger");
  return button;
}

async function clickTrigger(waitMs = 10) {
  await act(async () => {
    triggerButton().dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush(waitMs);
  });
}

// --- pure: formatCapsuleElapsed ---

section("formatCapsuleElapsed");
const t0 = 1_700_000_000_000;
eq(formatCapsuleElapsed(t0, t0), "0s", "刚启动显示 0s");
eq(formatCapsuleElapsed(t0, t0 + 59_000), "59s", "59s 保持秒格式");
eq(formatCapsuleElapsed(t0, t0 + 60_000), "1m00s", "60s 进位为 1m00s");
eq(formatCapsuleElapsed(t0, t0 + 95 * 60_000 + 14_000), "1h35m", "95m14s 进位为 1h35m");
eq(formatCapsuleElapsed(t0 + 5_000, t0), "0s", "时钟偏差钳制为 0s");

// --- pure: groupCapsuleJobs ---

section("groupCapsuleJobs");
{
  const jobs = [
    job({ id: "task-2", kind: "task", label: "调研" }),
    job({ id: "bash-1", kind: "bash", label: "serve" }),
    job({ id: "fleet-3", kind: "fleet", label: "fleet(3)" }),
    job({ id: "bash-2", kind: "bash", label: "build" }),
  ];
  const groups = groupCapsuleJobs(jobs);
  eq(groups.agents.length, 2, "task/fleet 归智能体组");
  eq(groups.commands.length, 2, "bash 归命令组");
  ok(groups.agents.every((item) => item.kind !== "bash") && groups.commands.every((item) => item.kind === "bash"), "两组互不混 kind");
  eq(groupCapsuleJobs([]).agents.length + groupCapsuleJobs([]).commands.length, 0, "空输入两组皆空");
}

// --- component ---

section("空态：打开即自动收起（无运行且目录为空）");
{
  let resolveList: (views: SubagentArtifactView[]) => void = () => {};
  listResult = new Promise((res) => { resolveList = res; });
  await renderIndicator({ jobs: [], sessionPath: "s.jsonl" });
  const trigger = triggerButton();
  ok(trigger.classList.contains("capsule__trigger--idle"), "无任务时入口灰显");
  ok(trigger.querySelector(".capsule__badge") === null, "无任务且目录未加载时无徽标");
  ok(document.querySelector(".capsule-panel") === null, "初始不渲染面板");
  await clickTrigger();
  ok(document.querySelector(".capsule-panel") !== null, "打开后渲染面板");
  await act(async () => {
    resolveList([]);
    await flush(200); // auto-close fires; popover enters its 140ms closing phase
  });
  await act(async () => {
    await flush(200); // closing phase elapses; the portal unmounts
  });
  ok(document.querySelector(".capsule-panel") === null, "运行与目录皆空时面板自动收起");
  await cleanup();
}

section("运行分组与停止接线");
{
  const cancelled: string[] = [];
  let releaseCancel: (() => void) | null = null;
  const jobs = [
    job({ id: "task-2", kind: "task", label: "批十二C UI批量三件", startedAt: Date.now() - 5_000 }),
    job({ id: "bash-1", kind: "bash", label: "后台拉起 serve (8787)", startedAt: Date.now() - 61_000 }),
  ];
  listResult = Promise.resolve([]);
  await renderIndicator({
    jobs,
    onCancelJob: (id) => new Promise<boolean>((resolve) => { cancelled.push(id); releaseCancel = () => resolve(true); }),
    sessionPath: "s.jsonl",
  });
  const trigger = triggerButton();
  ok(trigger.classList.contains("capsule__trigger--active"), "有任务时入口高亮");
  eq(trigger.querySelector(".capsule__badge")?.textContent, "2", "徽标计数为运行任务总数");
  await clickTrigger();
  ok(document.querySelector('[data-capsule-group="agent"]') !== null, "智能体节渲染");
  ok(document.querySelector('[data-capsule-group="terminal"]') !== null, "命令节渲染");
  eq(document.querySelectorAll(".capsule-panel__stop").length, 2, "每行都有停止按钮");
  const stopButton = document.querySelector<HTMLButtonElement>('[data-capsule-job-id="task-2"] .capsule-panel__stop');
  await act(async () => {
    stopButton?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush();
  });
  ok(cancelled.includes("task-2"), "停止按钮走 onCancelJob 链");
  eq(stopButton?.disabled, true, "停止中按钮禁用防重复");
  await act(async () => {
    releaseCancel?.();
    await flush();
  });
  await cleanup();
}

section("已结束子代理目录：过滤运行中、缺失转录禁点");
{
  listCalls.length = 0;
  readCalls.length = 0;
  const views = [
    ended({ ref: "sa_1", name: "已完成", status: "completed" }),
    ended({ ref: "sa_2", name: "失败件", status: "failed", outcome: "partial" }),
    ended({ ref: "sa_3", name: "仍在跑", status: "running" }),
    ended({ ref: "sa_4", name: "缺转录", status: "completed", hasTranscript: false }),
  ];
  listResult = Promise.resolve(views);
  await renderIndicator({ jobs: [], sessionPath: "capsule-parent.jsonl" });
  const trigger = triggerButton();
  eq(trigger.querySelector(".capsule__badge"), null, "目录只在打开后加载，未开无徽标");
  await clickTrigger(30);
  ok(document.querySelector('[data-capsule-group="ended"]') !== null, "已结束节渲染");
  const rows = document.querySelectorAll<HTMLButtonElement>("[data-capsule-ended-id]");
  eq(rows.length, 3, "目录只列非 running 条目");
  const missing = document.querySelector<HTMLButtonElement>('[data-capsule-ended-id="sa_4"]');
  eq(missing?.disabled, true, "缺转录条目禁用不可点");
  eq(missing?.dataset.capsuleEndedOpenable, "false", "缺转录条目标记 openable=false");
  eq(document.querySelector('[data-capsule-ended-id="sa_1"]')?.getAttribute("data-capsule-ended-status"), "completed", "条目带状态标记");
  ok(listCalls.length > 0, "目录查询已发起");
  eq(listCalls[0], "capsule-parent.jsonl", "目录查询携带会话路径");
  await clickTrigger(220);
  await act(async () => {
    await flush(200);
  });
  eq(triggerButton().querySelector(".capsule__badge")?.textContent, "3", "开合一次后空闲徽标显示已结束数（过滤运行中）");
  await cleanup();
}

section("子代理历史：加载态与失败态");
{
  listCalls.length = 0;
  readCalls.length = 0;
  let rejectRead: ((reason: unknown) => void) | null = null;
  listResult = Promise.resolve([ended({ ref: "sa_load", name: "历史件" })]);
  readResult = new Promise((_resolve, reject) => { rejectRead = reject; });
  await renderIndicator({ jobs: [], sessionPath: "capsule-parent.jsonl" });
  await clickTrigger();
  const row = document.querySelector<HTMLButtonElement>('[data-capsule-ended-id="sa_load"]');
  await act(async () => {
    row?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush();
  });
  const history = document.querySelector("[data-capsule-history]");
  eq(history?.getAttribute("data-capsule-history"), "sa_load", "详情容器带 ref 标记");
  eq(history?.getAttribute("data-capsule-history-state"), "loading", "点击后先进入加载态");
  eq(readCalls[readCalls.length - 1]?.[0], "capsule-parent.jsonl", "读取调用携带会话路径");
  eq(readCalls[readCalls.length - 1]?.[1], "sa_load", "读取调用携带子代理 ref");
  ok(document.querySelector(".capsule-panel--detail") !== null, "详情态面板加宽类生效");
  await act(async () => {
    rejectRead?.(new Error("boom"));
    await flush();
  });
  eq(document.querySelector("[data-capsule-history]")?.getAttribute("data-capsule-history-state"), "failed", "读取失败进入失败态");
  ok((document.querySelector(".capsule-panel__history-empty")?.textContent ?? "").length > 0, "失败态有用户可读文案");
  const back = document.querySelector<HTMLButtonElement>(".capsule-panel__back");
  await act(async () => {
    back?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush();
  });
  ok(document.querySelector("[data-capsule-history]") === null, "返回按钮退出详情视图");
  ok(document.querySelector('[data-capsule-ended-id="sa_load"]') !== null, "返回后目录仍在");
  await cleanup();
}

section("详情就绪分支：源级契约断言（Transcript 挂载）");
{
  const here = dirname(fileURLToPath(import.meta.url));
  const source = readFileSync(resolve(here, "../components/CapsulePanel.tsx"), "utf8");
  ok(source.includes("<LazyTranscript items={detailItems} onPrompt={() => {}} questionNavigator={false} rewindDisabled />"), "就绪分支以只读参数挂载共享 Transcript（惰性）");
  ok(source.includes('lazy(async () => ({ default: (await import("./Transcript")).Transcript }))'), "Transcript 走惰性导入，不拖入完整转录模块图");
  ok(source.includes('historyMessagesToItems(detail.messages, "capsule")'), "历史消息走既有 historyMessagesToItems 管道");
  ok(source.includes("onReadSubagent?.(sessionPath ?? \"\", view.ref)"), "读取按 (sessionPath, ref) 走注入的 Wails 方法");
  ok(source.includes('view.status !== "running"'), "目录过滤 running 条目");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
