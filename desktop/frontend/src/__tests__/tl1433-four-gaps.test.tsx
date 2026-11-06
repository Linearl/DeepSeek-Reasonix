// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1433-four-gaps.test.tsx
// tl1433 追加四缺口 harness（主体「run 行并入任务面板 + 点击跳转选中」的 56 案在
// tl1433-workflow-run-in-tasks；本件只验装机实测报出的四个缺口）：
//  ① 左侧「任务与子代理」胶囊弹层并入工作流 run 行——与右侧任务面板同一喂单
//     （App 订阅 workflowRunFeed 单例一次，running 子集下发）+ 同一跳转出口；
//  ② 心跳胶囊计数面补工作流子代理（557 同族）——run 计 1 + 其正在跑的子代理型
//     节点（subagent/confirm），取自喂单节点态：关闭态不需要后台轮询（447c2 规则
//     保持），不再「工作流在跑、胶囊置灰」；
//  ③ 子代理层级——工作流派发的运行中子代理（仅 sidecar 可见的那类，tl1435 把它
//     们平铺在顶层）缩进挂到所属 run 行下 + 「子代理」徽标（任务面板与胶囊两处）。
//     归属保守：精确 ref / 唯一在派子代理的 run，歧义与孪生（前台 ref、后台 job
//     时刻）都留顶层——宁可平铺，不错挂；
//  ④ 历史入口——run 结束后进「最近工作流」段（终态、30 分钟、≤3 条），点击走同一
//     出口；最近段不计入运行计数，弹层不因 run 跑完而排空自关。

import { JSDOM } from "jsdom";
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { LocaleProvider } from "../lib/i18n";
import type { WireRunNode, WireWorkflowRun } from "../lib/workflowTypes";
import type { BackgroundRuntimeView, ForegroundSubagentView, HistoryMessage, JobView, SubagentArtifactView } from "../lib/types";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`); failed += 1; }
}
function section(title: string) {
  process.stdout.write(`\n${title}\n`);
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.SVGElement = dom.window.SVGElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis.window, "matchMedia", {
  configurable: true,
  value: (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false,
  }),
});

const here = fileURLToPath(new URL(".", import.meta.url));
const read = (rel: string) => readFileSync(`${here}../../${rel}`, "utf8");

const NOW = Date.now();
const RUNNING_TEXT = ["运行中", "執行中", "Running"];
const SUBAGENT_BADGE_TEXT = ["子代理", "Subagent"];

function makeRun(overrides: Partial<WireWorkflowRun> = {}): WireWorkflowRun {
  return {
    runId: "wf-1433",
    workflow: "migration-wf",
    state: "running",
    startedAt: NOW - 120_000,
    nodes: [],
    dag: {
      id: "migration-wf",
      name: "migration-wf",
      phases: ["survey", "deliver"],
      nodes: [
        { id: "plan", name: "调研计划", kind: "subagent", phase: "survey" },
        { id: "read-facts", name: "读事实", kind: "subagent", phase: "survey" },
        { id: "gate-tests", name: "测试门", kind: "gate", phase: "deliver" },
        { id: "confirm", name: "复现确认", kind: "confirm", phase: "deliver" },
        { id: "report", name: "报告", kind: "subagent", phase: "deliver" },
      ],
      edges: [{ from: "plan", to: "read-facts" }],
    },
    ...overrides,
  };
}

function node(nodeId: string, nodeType: string, state: WireRunNode["state"], extra: Partial<WireRunNode> = {}): WireRunNode {
  return { nodeId, nodeType, state, changedAt: NOW - 1_000, ...extra };
}

// 一条运行中的 sidecar 记录（ListSubagentsByParent；工作流派发的子代理只在这里可见）。
function sidecar(overrides: Partial<SubagentArtifactView> = {}): SubagentArtifactView {
  return {
    ref: "sa_triage",
    createdAt: NOW - 60_000,
    updatedAt: NOW - 30_000,
    status: "running",
    kind: "task",
    name: "task",
    description: "分诊",
    hasTranscript: false,
    pendingMail: 0,
    ...overrides,
  };
}

function job(overrides: Partial<JobView> = {}): JobView {
  return { id: "bash-1", kind: "bash", label: "后台拉起 serve", status: "running", startedAt: NOW - 60_000, ...overrides };
}

function fg(overrides: Partial<ForegroundSubagentView> = {}): ForegroundSubagentView {
  return { tabId: "", title: "", ref: "sa_fg", name: "前台子代理", startedAt: NOW - 90_000, ...overrides };
}

async function flush(ms = 10) {
  await new Promise((resolve) => setTimeout(resolve, ms));
}

let root: Root | null = null;
let host: HTMLElement | null = null;
async function mount(element: ReactElement) {
  await unmount();
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root?.render(element);
    await flush(20);
  });
  await act(async () => { await flush(20); });
}
async function rerender(element: ReactElement) {
  await act(async () => {
    root?.render(element);
    await flush(20);
  });
}
async function unmount() {
  if (root) await act(async () => root?.unmount());
  host?.remove();
  root = null;
  host = null;
}

// 胶囊的两个只读桥按注入 prop 走（同 capsule-panel harness 惯例，不拉 bridge 模块图）。
const listCalls: string[] = [];
let listResult: SubagentArtifactView[] = [];
const onListSubagents = (sessionPath: string) => {
  listCalls.push(sessionPath);
  return Promise.resolve(listResult);
};
const onReadSubagent = () => Promise.resolve([] as HistoryMessage[]);

const { CapsuleIndicator } = await import("../components/CapsulePanel");
const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
const { buildSubagentDirectory } = await import("../lib/subagentDirectory");

interface CapsuleProps {
  jobs?: JobView[];
  runtimes?: BackgroundRuntimeView[];
  subagents?: ForegroundSubagentView[];
  sessionPath?: string;
  workflowRuns?: WireWorkflowRun[];
  workflowRecentRuns?: WireWorkflowRun[];
  onOpenWorkflowRun?: (runId: string) => void;
}
function capsule(props: CapsuleProps): ReactElement {
  return (
    <LocaleProvider>
      <CapsuleIndicator
        jobs={props.jobs ?? []}
        runtimes={props.runtimes ?? []}
        subagents={props.subagents ?? []}
        sessionPath={props.sessionPath ?? "sess-1433"}
        onListSubagents={onListSubagents}
        onReadSubagent={onReadSubagent}
        workflowRuns={props.workflowRuns}
        workflowRecentRuns={props.workflowRecentRuns}
        onOpenWorkflowRun={props.onOpenWorkflowRun}
      />
    </LocaleProvider>
  );
}
function trigger(): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(".capsule__trigger");
  if (!button) throw new Error("capsule trigger not rendered");
  return button;
}
async function openCapsule() {
  await act(async () => {
    trigger().click();
    await flush(20);
  });
  await act(async () => { await flush(20); });
}
function capsuleCount(): string | null {
  return document.querySelector(".capsule")?.getAttribute("data-capsule-count") ?? null;
}

console.log("\ntl1433 four gaps: 胶囊弹层 run 入列 / 计数面 / 子代理层级 / 历史入口");

// ===== GAP 1 =====
section("① 左侧胶囊弹层：工作流 run 入列（同一喂单 running 子集 + 同一跳转出口）");
{
  listResult = [];
  const opened: string[] = [];
  const run = makeRun({
    nodes: [
      node("plan", "subagent", "completed"),
      node("read-facts", "subagent", "completed"),
      node("gate-tests", "gate", "running"),
    ],
  });
  await mount(capsule({ jobs: [job()], workflowRuns: [run], onOpenWorkflowRun: (runId) => { opened.push(runId); } }));
  await openCapsule();
  const group = document.querySelector('[data-capsule-group="workflow"]');
  ok(group !== null, "入列：弹层出现「工作流」组");
  eq(document.querySelectorAll("[data-capsule-group]")[0]?.getAttribute("data-capsule-group"), "workflow", "入列：工作流组排在运行组最前（编排在前，与任务面板同序）");
  const row = document.querySelector('[data-capsule-workflow-run="wf-1433"]');
  ok(row !== null, "入列：运行中的 run 以行出现在弹层");
  ok((row?.textContent ?? "").includes("migration-wf"), "字段：工作流名");
  const meta = document.querySelector('[data-capsule-wf-meta="wf-1433"]')?.textContent ?? "";
  ok(meta.includes("测试门"), "字段：当前节点显示 DAG 人话名");
  ok(meta.includes("2/5"), "字段：已完成/总节点（2/5）");
  ok(/\d+s|\d+m/.test(meta), "字段：运行时长（胶囊同款时钟格式）");
  ok(RUNNING_TEXT.includes(row?.querySelector(".capsule-panel__outcome")?.textContent ?? ""), "字段：状态胶囊=运行中（三语之一）");
  eq(document.querySelector("[data-capsule-running-empty]"), null, "入列：有 run 在跑时不读「暂无运行任务」");
  ok(trigger().classList.contains("capsule__trigger--active"), "入列：run 在列即触发器 active（行在列不得置灰）");
  await act(async () => { row?.querySelector<HTMLButtonElement>("button.capsule-panel__row-main")?.click(); });
  eq(opened.join(","), "wf-1433", "跳转：点击 run 行触发同一出口且带 runId");

  // 数据与出口同源：只有数据没有出口 → 不渲染、不计数（不产生点了没反应的死行）。
  await mount(capsule({ workflowRuns: [run] }));
  eq(capsuleCount(), "0", "同源：无跳转出口时 run 不入计数");
  await openCapsule();
  eq(document.querySelector('[data-capsule-group="workflow"]'), null, "同源：无跳转出口时不渲染工作流组");

  // 防御：非运行 run 落进 running 下发也不渲染成运行行。
  await mount(capsule({ workflowRuns: [makeRun({ state: "completed", endedAt: NOW })], onOpenWorkflowRun: () => {} }));
  await openCapsule();
  eq(document.querySelector("[data-capsule-workflow-run]"), null, "防御：completed 落进 running 下发不渲染成运行行");

  // 不传 workflow props = pre-1433 胶囊逐字节（无工作流组、计数不变）。
  await mount(capsule({}));
  eq(capsuleCount(), "0", "兼容：不传 workflow props 计数不变");
  ok(trigger().classList.contains("capsule__trigger--idle"), "兼容：不传 workflow props 触发器仍 idle");
  await unmount();

  const capsuleSrc = read("src/components/CapsulePanel.tsx");
  ok(!/from\s+["'][^"']*workflowRunFeed["']/.test(capsuleSrc), "接线：胶囊不自订阅喂单（不 import workflowRunFeed——数据源仍是 App 订阅的单例，经 props 注入，不新造）");
  ok(capsuleSrc.includes("runningWorkflowRuns(workflowRuns ?? []).map(workflowRunRowView)"), "接线：胶囊行视图走与任务面板同一纯投影（workflowRunRows）");
  ok(capsuleSrc.includes('t("workflowPanel.title")') && capsuleSrc.includes('t("workflowPanel.run.running")') && capsuleSrc.includes('t("workflowPanel.progress"'),
    "文案：胶囊 run 行复用既有三语键（类型/状态/进度）");
  const app = read("src/App.tsx");
  ok(app.includes("capsuleWorkflowRuns={workflowRunningRows}") && app.includes("onCapsuleOpenWorkflowRun={openWorkflowRunFromTasks}"),
    "接线：App 把右侧任务面板同一份 running 子集与同一跳转出口下发给胶囊");
  const composer = read("src/components/Composer.tsx");
  ok(composer.includes("workflowRuns={capsuleWorkflowRuns}") && composer.includes("onOpenWorkflowRun={onCapsuleOpenWorkflowRun}"), "接线：Composer 原样透传给 CapsuleIndicator");
  const css = read("src/styles.css");
  ok(css.includes(".capsule-panel__wf {") && css.includes(".capsule-panel__row--workflow"), "样式：胶囊工作流行类有定义（类契约）");
}
// ===== GAP 2 =====
section("② 胶囊计数面补工作流子代理（557 同族；关闭态即计数、不轮询）");
{
  const { runningSubagentNodeCount, workflowRunRowView } = await import("../lib/workflowRunRows");
  const busy = makeRun({
    nodes: [
      node("plan", "subagent", "completed"),
      node("read-facts", "subagent", "running"),
      node("confirm", "confirm", "running"),
      node("gate-tests", "gate", "running"),
    ],
  });
  eq(runningSubagentNodeCount(busy), 2, "计数：running 的 subagent + confirm 节点计入，gate 不计");
  eq(workflowRunRowView(busy).runningSubagents, 2, "投影：行视图携带同一计数");
  eq(runningSubagentNodeCount(makeRun({ nodes: [node("plan", "subagent", "running"), node("plan", "subagent", "completed")] })), 0,
    "计数：节点表后见覆盖——同节点跑完即不计");
  eq(runningSubagentNodeCount(makeRun({ nodes: [node("report", "", "running")] })), 1, "计数：种类以 DAG 定义为准（节点事件就地点亮的 nodeType 为空也认）");
  eq(runningSubagentNodeCount(makeRun({ dag: undefined, nodes: [node("x", "subagent", "running"), node("g", "gate", "running")] })), 1, "计数：缺 DAG 退节点表 nodeType");

  listCalls.length = 0;
  await mount(capsule({ workflowRuns: [busy], onOpenWorkflowRun: () => {} }));
  eq(capsuleCount(), "3", "计数面：关闭态即计入（run 1 + 正在跑的子代理型节点 2）");
  ok(trigger().classList.contains("capsule__trigger--active"), "置灰修复：工作流子代理在跑时触发器 active 而非 idle");
  eq(document.querySelector('[data-capsule-badge="running"]')?.textContent ?? null, "3", "徽标：显示计数面总数");
  eq(listCalls.length, 0, "关闭态不后台轮询 sidecar（447c2 规则保持——计数取自喂单节点态）");

  await mount(capsule({ jobs: [job({ id: "task-1", kind: "task", label: "后台调研" })], subagents: [fg()], workflowRuns: [busy], onOpenWorkflowRun: () => {} }));
  eq(capsuleCount(), "5", "计数面：三源（job 1 + 前台子代理 1）+ 工作流（1 + 2）合计 5，互不双计");

  await mount(capsule({ workflowRuns: [makeRun({ nodes: [node("gate-tests", "gate", "running")] })], onOpenWorkflowRun: () => {} }));
  eq(capsuleCount(), "1", "计数面：run 在跑门节点（无子代理在跑）只计 run 本身");

  await mount(capsule({ jobs: [job()] }));
  eq(capsuleCount(), "1", "兼容：无工作流时计数面 = 原三源");
  await unmount();

  const capsuleSrc = read("src/components/CapsulePanel.tsx");
  ok(capsuleSrc.includes("sum + 1 + view.runningSubagents") && capsuleSrc.includes("runningEntries.length + workflowRunningCount"),
    "接线：计数面 = 三源 + Σ(run 1 + 子代理型节点)");
  ok(capsuleSrc.includes('data-capsule-badge="running"') && !capsuleSrc.includes("data-capsule-badge={"), "兼容：徽标仍只以 running 态渲染（任务 497 契约不动）");
}
// ===== SUMMARY =====

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
