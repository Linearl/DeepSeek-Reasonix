// Run: tsx src/__tests__/capsule-panel.test.tsx
// Task 447 capsule: composer floating panel — pure helpers (elapsed format,
// job grouping), trigger states, running sections with stop wiring, the
// ended sub-agents directory (ListSubagentsByParent), and the transcript
// detail states (loading/failed via stubbed ReadSubagentSession; the ready
// branch mounts the shared Transcript and is asserted at source level,
// matching the lifecycle-test precedent).
// Task 447c2: the directory's event-driven refresh — a single job finishing
// re-pulls the ended directory while the panel stays open (value-keyed on
// the running identity); closed panels still never poll in the background.

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { LocaleProvider } from "../lib/i18n";
import type { BackgroundRuntimeView, HistoryMessage, JobView, SubagentArtifactView } from "../lib/types";

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

const { formatCapsuleElapsed, groupCapsuleJobs, mergeCapsuleWork, splitCapsuleEntries, CapsuleIndicator } = await import("../components/CapsulePanel");

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

function runtime(overrides: Partial<BackgroundRuntimeView> = {}): BackgroundRuntimeView {
  return {
    tabId: "tab-detached-1",
    title: "批十二C UI批量三件",
    detached: true,
    running: true,
    pendingPrompt: false,
    jobs: [],
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

async function renderIndicator(props: {
  jobs?: JobView[];
  runtimes?: BackgroundRuntimeView[];
  onCancelJob?: (jobID: string) => Promise<boolean>;
  onCancelRuntimeJob?: (tabId: string, jobID: string) => Promise<boolean>;
  sessionPath?: string;
}) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <CapsuleIndicator
          jobs={props.jobs ?? []}
          runtimes={props.runtimes ?? []}
          onCancelJob={props.onCancelJob}
          onCancelRuntimeJob={props.onCancelRuntimeJob}
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

// rerenderIndicator pushes a new jobs snapshot into the SAME mounted root —
// the way App feeds the panel on controller notice/turn_done events. Used by
// the 447c2 event-driven refresh tests.
async function rerenderIndicator(props: {
  jobs?: JobView[];
  runtimes?: BackgroundRuntimeView[];
  sessionPath?: string;
}) {
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <CapsuleIndicator
          jobs={props.jobs ?? []}
          runtimes={props.runtimes ?? []}
          sessionPath={props.sessionPath}
          onListSubagents={onListSubagents}
          onReadSubagent={onReadSubagent}
        />
      </LocaleProvider>,
    );
    await flush();
  });
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

// --- pure: mergeCapsuleWork / splitCapsuleEntries（任务 440 跨 tab 合并） ---

section("mergeCapsuleWork：跨 tab 合并去重 + 当前会话优先");
{
  const activeJobs = [
    job({ id: "bash-active", kind: "bash", label: "当前会话 serve" }),
    job({ id: "task-shared", kind: "task", label: "同源重复" }),
  ];
  const runtimes = [
    runtime({ tabId: "tab-a", title: "批十二C UI批量三件", jobs: [job({ id: "task-shared", kind: "task", label: "同源重复" }), job({ id: "task-a", kind: "task", label: "跨 tab 子代理" })] }),
    runtime({ tabId: "tab-b", title: "", jobs: [job({ id: "bash-b", kind: "bash", label: "分离会话命令" })] }),
  ];
  const merged = mergeCapsuleWork(activeJobs, runtimes, "未知任务");
  eq(merged.length, 4, "同 id 去重后共 4 条（2 当前 + 2 跨 tab）");
  ok(merged[0].tabId === "" && merged[0].job.id === "bash-active", "当前会话条目排最前（tabId 空标记）");
  ok(merged.some((entry) => entry.tabId === "" && entry.job.id === "task-shared"), "同源重复 job 以当前会话条目为准（App 已过滤 active tab 的 runtime，此处为防御性去重）");
  eq(merged.filter((entry) => entry.job.id === "task-shared").length, 1, "重复 id 只出现一次");
  const unlabeled = merged.find((entry) => entry.tabId === "tab-b");
  eq(unlabeled?.origin, "未知任务", "无标题 runtime 回退到未知任务文案");
  const split = splitCapsuleEntries(merged);
  eq(split.commands.length, 2, "bash 条目归命令节");
  eq(split.agents.length, 2, "task 条目归智能体节");
  eq(mergeCapsuleWork([], [], "x").length, 0, "空输入合并为空");
}

// --- component ---

section("空态：用户点开空面板保持打开并显示空态（任务 440 ③）");
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
    await flush(300);
  });
  const panel = document.querySelector(".capsule-panel");
  ok(panel !== null, "用户明确点开的空面板保持打开（不自动收起）");
  ok(document.querySelector("[data-capsule-empty]") !== null, "面板显示空态文案");
  await clickTrigger(220);
  await act(async () => {
    await flush(200);
  });
  ok(document.querySelector(".capsule-panel") === null, "用户再次点击可关闭空面板");
  await cleanup();
}

section("排空自动收起：打开时有内容、全部结束且目录为空才自动关（保留 447 防噪音）");
{
  listCalls.length = 0;
  const jobsStart = [job({ id: "task-drain", kind: "task", label: "即将结束" })];
  listResult = Promise.resolve([]);
  await renderIndicator({ jobs: jobsStart, sessionPath: "s.jsonl" });
  await clickTrigger(30);
  ok(document.querySelector(".capsule-panel") !== null, "面板已打开且有运行条目");
  await rerenderIndicator({ jobs: [], runtimes: [], sessionPath: "s.jsonl" });
  await act(async () => {
    await flush(300); // drain auto-close fires; popover enters its 140ms closing phase
  });
  await act(async () => {
    await flush(200);
  });
  ok(document.querySelector(".capsule-panel") === null, "内容排空且目录为空时面板自动收起");
  await cleanup();
}

section("全部运行面：跨 tab/分离会话条目入列 + 徽标计数 + 来源标签");
{
  listResult = Promise.resolve([]);
  const activeJobs = [job({ id: "bash-active", kind: "bash", label: "当前会话 serve" })];
  const runtimes = [
    runtime({ tabId: "tab-a", title: "批十二C UI批量三件", jobs: [job({ id: "task-a", kind: "task", label: "跨 tab 子代理", startedAt: Date.now() - 95 * 60_000 })] }),
    runtime({ tabId: "tab-b", title: "", jobs: [job({ id: "bash-b", kind: "bash", label: "分离会话命令" })] }),
  ];
  await renderIndicator({ jobs: activeJobs, runtimes, sessionPath: "s.jsonl" });
  const trigger = triggerButton();
  ok(trigger.classList.contains("capsule__trigger--active"), "任一来源有任务时入口高亮");
  eq(trigger.querySelector(".capsule__badge")?.textContent, "3", "徽标计数含跨 tab 条目（1 当前 + 2 跨 tab）");
  await clickTrigger(30);
  eq(document.querySelectorAll(".capsule-panel__row").length, 3, "面板列出全部来源的运行条目");
  const crossRow = document.querySelector('[data-capsule-job-id="task-a"]');
  ok(crossRow !== null, "跨 tab 条目渲染");
  eq(crossRow?.getAttribute("data-capsule-job-tab"), "tab-a", "跨 tab 条目带来源 tab 标记");
  ok((crossRow?.querySelector(".capsule-panel__origin")?.textContent ?? "").includes("批十二C UI批量三件"), "跨 tab 条目显示来源标题");
  const currentRow = document.querySelector('[data-capsule-job-id="bash-active"]');
  eq(currentRow?.querySelector(".capsule-panel__origin"), null, "当前会话条目无来源标签");
  const unknownRow = document.querySelector('[data-capsule-job-id="bash-b"]');
  ok((unknownRow?.querySelector(".capsule-panel__origin")?.textContent ?? "").length > 0, "无标题来源回退为未知任务文案");
  await cleanup();
}

section("跨 tab 停止路由：按来源走 onCancelRuntimeJob(tabId, jobId)");
{
  const cancelledActive: string[] = [];
  const cancelledRuntime: Array<[string, string]> = [];
  let releaseRuntime: (() => void) | null = null;
  const runtimes = [
    runtime({ tabId: "tab-a", title: "批十二C UI批量三件", jobs: [job({ id: "task-a", kind: "task", label: "跨 tab 子代理" })] }),
  ];
  listResult = Promise.resolve([]);
  await renderIndicator({
    jobs: [job({ id: "bash-active", kind: "bash", label: "当前会话 serve" })],
    runtimes,
    onCancelJob: (id) => new Promise<boolean>((resolve) => { cancelledActive.push(id); resolve(true); }),
    onCancelRuntimeJob: (tabId, id) => new Promise<boolean>((resolve) => { cancelledRuntime.push([tabId, id]); releaseRuntime = () => resolve(true); }),
    sessionPath: "s.jsonl",
  });
  await clickTrigger();
  const crossStop = document.querySelector<HTMLButtonElement>('[data-capsule-job-id="task-a"] .capsule-panel__stop');
  const activeStop = document.querySelector<HTMLButtonElement>('[data-capsule-job-id="bash-active"] .capsule-panel__stop');
  await act(async () => {
    crossStop?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush();
  });
  eq(cancelledRuntime.length, 1, "跨 tab 停止走 per-tab 链");
  eq(cancelledRuntime[0]?.[0], "tab-a", "per-tab 停止携带来源 tabId");
  eq(cancelledRuntime[0]?.[1], "task-a", "per-tab 停止携带 job id");
  eq(crossStop?.disabled, true, "停止中按钮禁用防重复");
  eq(cancelledActive.length, 0, "跨 tab 停止不误触 active 链");
  await act(async () => {
    releaseRuntime?.();
    await flush();
  });
  await act(async () => {
    activeStop?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush();
  });
  eq(cancelledActive.length, 1, "当前会话条目仍走 onCancelJob 链");
  eq(cancelledActive[0], "bash-active", "active 停止携带本tab job id");
  await cleanup();
}

section("未接 per-tab 停止链时：跨 tab 条目不渲染说谎的停止按钮");
{
  listResult = Promise.resolve([]);
  await renderIndicator({
    jobs: [job({ id: "bash-active", kind: "bash", label: "当前会话 serve" })],
    runtimes: [runtime({ tabId: "tab-a", title: "别的会话", jobs: [job({ id: "task-a", kind: "task" })] })],
    onCancelJob: () => Promise.resolve(true),
    sessionPath: "s.jsonl",
  });
  await clickTrigger();
  eq(document.querySelector('[data-capsule-job-id="task-a"] .capsule-panel__stop'), null, "无 per-tab 链的跨 tab 行无停止按钮（不渲染无效按钮）");
  ok(document.querySelector('[data-capsule-job-id="bash-active"] .capsule-panel__stop') !== null, "当前会话行停止按钮不受影响");
  await cleanup();
}

section("运行区空态行：目录有条目但无运行任务时明确显示空态（任务 440 ③）");
{
  listResult = Promise.resolve([ended({ ref: "sa_only", name: "已结束件" })]);
  await renderIndicator({ jobs: [], sessionPath: "s.jsonl" });
  await clickTrigger(30);
  ok(document.querySelector("[data-capsule-running-empty]") !== null, "无运行任务时运行区显示空态行");
  ok(document.querySelector('[data-capsule-group="ended"]') !== null, "已结束目录同时可见");
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
  eq(triggerButton().querySelector(".capsule__badge"), null, "开合一次后空闲（无运行任务）无徽标：已结束数不回填徽标（任务 497）");
  await cleanup();
}

section("目录事件驱动刷新：面板常开期间单个任务结束即重拉（447c2）");
{
  listCalls.length = 0;
  readCalls.length = 0;
  const jobsTwo = [
    job({ id: "task-2", kind: "task", label: "调研" }),
    job({ id: "bash-1", kind: "bash", label: "serve" }),
  ];
  listResult = Promise.resolve([ended({ ref: "sa_old", name: "旧条目" })]);
  await renderIndicator({ jobs: jobsTwo, sessionPath: "s.jsonl" });
  await clickTrigger(30);
  const callsAfterOpen = listCalls.length;
  ok(callsAfterOpen > 0, "打开时目录已拉取");
  ok(document.querySelector('[data-capsule-ended-id="sa_old"]') !== null, "既有条目在目录中");
  // 部分排空：一个子代理任务结束（快照 2→1），面板保持打开。
  // 完成事件会把 jobs 快照换成新数组——目录应当随之重拉。
  listResult = Promise.resolve([
    ended({ ref: "sa_old", name: "旧条目" }),
    ended({ ref: "sa_new", name: "新条目" }),
  ]);
  await rerenderIndicator({ jobs: [jobsTwo[1]], sessionPath: "s.jsonl" });
  eq(listCalls.length, callsAfterOpen + 1, "单个任务结束后目录自动重拉（无需重开面板）");
  ok(document.querySelector('[data-capsule-ended-id="sa_new"]') !== null, "新结束的子代理即时进入目录");
  ok(document.querySelector('[data-capsule-ended-id="sa_old"]') !== null, "既有条目保持");
  // 值等价快照（新数组、同 id 集）不触发重拉：防控制器同集重渲染造成抖动。
  await rerenderIndicator({ jobs: [{ ...jobsTwo[1] }], sessionPath: "s.jsonl" });
  eq(listCalls.length, callsAfterOpen + 1, "值等价快照（新数组同 id）不触发重拉");
  await cleanup();
}

section("面板关闭时不做后台目录轮询（一期口径保持，447c2 不越界）");
{
  listCalls.length = 0;
  listResult = Promise.resolve([ended({ ref: "sa_idle", name: "空闲件" })]);
  await renderIndicator({ jobs: [], sessionPath: "s.jsonl" });
  await clickTrigger(30);
  ok(document.querySelector(".capsule-panel") !== null, "面板已打开");
  const callsOpen = listCalls.length;
  ok(callsOpen > 0, "打开期间目录已拉取");
  await clickTrigger(220); // 用户关闭面板
  await act(async () => {
    await flush(200); // closing phase elapses; the portal unmounts
  });
  ok(document.querySelector(".capsule-panel") === null, "面板已关闭");
  const callsClosed = listCalls.length;
  await rerenderIndicator({ jobs: [job({ id: "task-9", kind: "task" })], sessionPath: "s.jsonl" });
  eq(listCalls.length, callsClosed, "关闭期间 jobs 变化不触发目录拉取（不后台轮询）");
  await cleanup();
}

section("任务 497：徽标只计运行中（混合态计数正确 + 全部结束归零）");
{
  listCalls.length = 0;
  const jobsMixed = [
    job({ id: "task-r1", kind: "task", label: "运行中调研" }),
    job({ id: "bash-r1", kind: "bash", label: "运行中命令" }),
  ];
  listResult = Promise.resolve([
    ended({ ref: "sa_e1", name: "已完成件", status: "completed" }),
    ended({ ref: "sa_e2", name: "失败件", status: "failed" }),
    ended({ ref: "sa_e3", name: "中断件", status: "interrupted" }),
  ]);
  await renderIndicator({ jobs: jobsMixed, sessionPath: "s.jsonl" });
  const badge = triggerButton().querySelector(".capsule__badge");
  eq(badge?.textContent, "2", "混合态徽标只计运行中（2 运行 + 3 已结束 → 2）");
  eq(badge?.getAttribute("data-capsule-badge"), "running", "徽标只呈现 running 态（ended 态已移除）");
  await clickTrigger(30);
  const endedTitle = document.querySelector('[data-capsule-group="ended"] .capsule-panel__group-title')?.textContent ?? "";
  ok(endedTitle.includes("3"), "面板内已结束节标题仍如实计已结束数（3）");
  // 全部结束：运行快照排空，3 条进已结束目录；徽标必须归零消失，不回填已结束数。
  await rerenderIndicator({ jobs: [], sessionPath: "s.jsonl" });
  await act(async () => {
    await flush(300); // directory re-pull settles while the panel stays open
  });
  await clickTrigger(220); // 用户关闭面板
  await act(async () => {
    await flush(200);
  });
  eq(triggerButton().querySelector(".capsule__badge"), null, "全部结束归零：无运行任务即无徽标");
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
  ok(source.includes('data-capsule-badge="running"'), "徽标只以 running 态渲染（任务 497）");
  ok(!source.includes("data-capsule-badge={"), "徽标不再按运行/结束二态切换取值");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
