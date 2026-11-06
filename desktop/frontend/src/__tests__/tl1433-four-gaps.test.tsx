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
// ===== GAP 3 =====
section("③ 子代理层级：工作流派发的运行中子代理缩进挂在 run 行下 + 「子代理」徽标");
{
  const { attributeWorkflowChildren } = await import("../lib/workflowRunRows");
  const { sidecarOnlyRunningEntries, hasJobTwin, JOB_TWIN_WINDOW_MS, mergePersistedRunningSubagents, registryRunningEntries } = await import("../lib/subagentDirectory");
  const probe = (child: { ref?: string; startedAt?: number }) => child;
  const dispatching = makeRun({ runId: "wf-a", startedAt: NOW - 120_000, nodes: [node("read-facts", "subagent", "running")] });
  const other = makeRun({ runId: "wf-b", startedAt: NOW - 100_000, nodes: [node("plan", "subagent", "running")] });
  const child = { ref: "sa_triage", startedAt: NOW - 60_000 };

  let result = attributeWorkflowChildren([child], [dispatching], probe);
  eq(result.byRun.get("wf-a")?.length ?? 0, 1, "归属②：唯一在派子代理的 run 收下 sidecar-only 子代理");
  eq(result.flat.length, 0, "归属②：归上的不再留顶层");
  result = attributeWorkflowChildren([child], [dispatching, other], probe);
  eq(result.flat.length, 1, "歧义：两个 run 同时在派子代理 → 不猜，留顶层（宁可平铺不错挂）");
  const otherWithRef = { ...other, nodes: [...other.nodes, node("report", "subagent", "completed", { subagentRefs: ["sa_triage"] })] };
  result = attributeWorkflowChildren([child], [dispatching, otherWithRef], probe);
  eq(result.byRun.get("wf-b")?.length ?? 0, 1, "归属①：ref 命中某 run 的 subagentRefs → 精确归它（并发也不歧义）");
  result = attributeWorkflowChildren([{ ref: "sa_old", startedAt: NOW - 600_000 }], [dispatching], probe);
  eq(result.flat.length, 1, "保守：派发时刻早于该 run 起点的不归它");
  result = attributeWorkflowChildren([child], [makeRun({ runId: "wf-g", nodes: [node("gate-tests", "gate", "running")] })], probe);
  eq(result.flat.length, 1, "保守：run 只在跑门节点（没有子代理在跑）且 ref 不中 → 留顶层");
  result = attributeWorkflowChildren([child], [makeRun({ state: "completed", endedAt: NOW })], probe);
  eq(result.flat.length, 1, "保守：没有运行中的 run → 全部留顶层");

  // 候选面：只取「仅 sidecar 可见」的运行中条目；会话自己派发的孪生排除。
  const persistedDir = mergePersistedRunningSubagents(buildSubagentDirectory([]), [
    sidecar({ ref: "sa_triage" }),
    sidecar({ ref: "sa_fgtwin", description: "前台孪生" }),
    sidecar({ ref: "sa_jobtwin", description: "后台孪生", createdAt: NOW - 50_000 }),
  ]);
  const registry = registryRunningEntries({ jobs: [job({ id: "job-1", kind: "task", startedAt: NOW - 50_000 + 800 })], foreground: [fg({ ref: "sa_fgtwin" })] }, "");
  eq(sidecarOnlyRunningEntries(persistedDir.running, registry).map((entry) => entry.ref).join(","), "sa_triage",
    "候选：只剩真正仅 sidecar 可见的（前台 ref 孪生、后台 job 时刻孪生都排除）");
  const transcriptEntry = { item: { kind: "tool" as const, id: "call_1", name: "task", args: "", readOnly: true, status: "running" as const }, running: true, status: "running" as const, ref: "sa_card" };
  eq(sidecarOnlyRunningEntries([transcriptEntry], []).length, 0, "候选：会话转录卡片投影的条目不算（非 persisted: 投影）");
  ok(hasJobTwin(NOW, [NOW + JOB_TWIN_WINDOW_MS]), "job 孪生：窗口边界内算孪生");
  ok(!hasJobTwin(NOW, [NOW + JOB_TWIN_WINDOW_MS + 1]), "job 孪生：窗口外不算");
  ok(!hasJobTwin(undefined, [NOW]), "job 孪生：无派发时刻不判孪生");

  // 任务面板（右侧）：分诊子代理缩进挂到 run 行下。
  const runA = makeRun({ nodes: [node("plan", "subagent", "completed"), node("read-facts", "subagent", "running")] });
  const triage = sidecar({ ref: "sa_triage", description: "分诊" });
  const dock = (extra: Record<string, unknown>) => (
    <LocaleProvider>
      <SubagentsDockPanel
        directory={buildSubagentDirectory([])}
        sessionPath="sess-1433"
        onListPersisted={async () => [triage]}
        onTerminateSubagent={async () => true}
        {...extra}
      />
    </LocaleProvider>
  );
  await mount(dock({ workflowRuns: [runA], onOpenWorkflowRun: () => {} }));
  const nested = host?.querySelector('[data-wf-run-children="wf-1433"] [data-wf-child="sa_triage"]') ?? null;
  ok(nested !== null, "任务面板层级：分诊子代理缩进挂在工作流 run 行之下");
  ok(SUBAGENT_BADGE_TEXT.includes(nested?.querySelector(".subagents-panel__sub-badge")?.textContent ?? ""), "任务面板徽标：嵌套行标「子代理」（复用 workflowPanel.kind.subagent 三语键）");
  ok((nested?.textContent ?? "").includes("分诊"), "任务面板：嵌套行保留人话名（sidecar description）");
  const topList = host?.querySelector(".subagents-panel__list");
  eq(topList ? topList.children.length : -1, 1, "任务面板层级：顶层只剩 run 行（子代理不再与工作流平铺同级）");
  ok((host?.querySelectorAll(".subagents-panel__section-title")[0]?.textContent ?? "").includes("2"), "任务面板计数：运行分区仍计 run + 子代理（嵌套只换位置不改口径）");
  ok(nested?.querySelector('[data-subagent-terminate="sa_triage"]') !== null, "能力保留：嵌套行仍带终止按钮（tl1209 不因换位丢失）");

  await mount(dock({ workflowRuns: [runA], onOpenWorkflowRun: () => {}, detailEnabled: true }));
  ok(host?.querySelector('[data-wf-run-children="wf-1433"] [data-wf-child="sa_triage"]') !== null, "任务面板 split 形态同样嵌套");

  await mount(dock({ workflowRuns: [runA] }));
  eq(host?.querySelector("[data-wf-child]") ?? null, null, "无跳转出口（run 行不渲染）→ 不归属");
  ok([...(host?.querySelectorAll(".subagents-panel__row-title") ?? [])].some((el) => el.textContent === "分诊"), "无出口时子代理仍在顶层可见（不随不渲染的 run 行消失）");

  await mount(dock({ workflowRuns: [runA, makeRun({ runId: "wf-b", workflow: "wf-beta", nodes: [node("plan", "subagent", "running")] })], onOpenWorkflowRun: () => {} }));
  eq(host?.querySelectorAll("[data-workflow-run]").length ?? 0, 2, "并发前提：两个 run 行都在列");
  eq(host?.querySelector("[data-wf-child]") ?? null, null, "任务面板：两个 run 同时在派子代理 → 不猜，留顶层");
  ok([...(host?.querySelectorAll(".subagents-panel__row-title") ?? [])].some((el) => el.textContent === "分诊"), "并发时子代理仍在顶层可见");

  // 胶囊（左侧）：同一归属规则，嵌套后不再在「运行中子代理」平铺组重复出现。
  listResult = [triage];
  await mount(capsule({ workflowRuns: [runA], onOpenWorkflowRun: () => {} }));
  await openCapsule();
  const capsuleNested = document.querySelector('[data-capsule-wf-children="wf-1433"] [data-capsule-wf-child="sa_triage"]');
  ok(capsuleNested !== null, "胶囊层级：分诊子代理缩进挂在工作流 run 行下");
  ok(SUBAGENT_BADGE_TEXT.includes(capsuleNested?.querySelector(".capsule-panel__sub-badge")?.textContent ?? ""), "胶囊徽标：嵌套行标「子代理」");
  eq(document.querySelector('[data-capsule-group="running-subagents"] [data-capsule-running-id="sa_triage"]'), null, "胶囊：嵌套后不在平铺组重复出现");

  await mount(capsule({ subagents: [fg({ ref: "sa_triage" })], workflowRuns: [runA], onOpenWorkflowRun: () => {} }));
  await openCapsule();
  ok(document.querySelector('[data-capsule-workflow-run="wf-1433"]') !== null, "孪生前提：工作流 run 行在列");
  eq(document.querySelector("[data-capsule-wf-child]"), null, "胶囊：前台登记表有同 ref 孪生 → 不归工作流");

  await mount(capsule({ jobs: [job({ id: "task-9", kind: "task", label: "后台", startedAt: triage.createdAt + 500 })], workflowRuns: [runA], onOpenWorkflowRun: () => {} }));
  await openCapsule();
  eq(document.querySelector("[data-capsule-wf-child]"), null, "胶囊：后台 job 时刻孪生 → 不归工作流");
  ok(document.querySelector('[data-capsule-group="running-subagents"] [data-capsule-running-id="sa_triage"]') !== null, "胶囊：孪生留在原平铺组（不丢行）");
  listResult = [];
  await unmount();

  const dockSrc = read("src/components/SubagentsDockPanel.tsx");
  ok(dockSrc.includes("sidecarOnlyRunningEntries(running, registryRunning)") && dockSrc.includes("attributeWorkflowChildren("), "接线：任务面板走共享候选面 + 共享归属规则");
  ok(dockSrc.includes('t("workflowPanel.kind.subagent")'), "文案：任务面板徽标复用既有三语键（不新增文案）");
  const capsuleSrc = read("src/components/CapsulePanel.tsx");
  ok(capsuleSrc.includes("attributeWorkflowChildren(candidates") && capsuleSrc.includes("hasJobTwin(view.createdAt, jobStarts)"), "接线：胶囊走同一归属规则 + 同一 job 孪生判定");
  ok(capsuleSrc.includes('t("workflowPanel.kind.subagent")'), "文案：胶囊徽标复用既有三语键");
  const css = read("src/styles.css");
  ok(css.includes(".subagents-panel__wf-children {") && css.includes(".capsule-panel__wf-children {") && css.includes(".subagents-panel__sub-badge,\n.capsule-panel__sub-badge {"),
    "样式：嵌套容器与「子代理」徽标有定义（两处共用一条徽标规则）");
  const hierarchyRules = [
    /\.subagents-panel__wf-children\s*\{[^}]*\}/,
    /\.subagents-panel__sub-badge,\s*\.capsule-panel__sub-badge\s*\{[^}]*\}/,
    /\.capsule-panel__wf-children\s*\{[^}]*\}/,
  ].map((re) => css.match(re)?.[0] ?? "");
  ok(hierarchyRules.every((rule) => rule.length > 0 && rule.includes("var(--") && !/#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/.test(rule)),
    "样式：层级三条规则只用主题 token（无硬编码颜色）");
}
// ===== SUMMARY =====

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
