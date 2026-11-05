// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1433-workflow-run-in-tasks.test.tsx
// tl1433 验收 harness —— 工作流运行可感知：任务面板（任务/子代理 tab）的
// 「运行中」列表并入工作流 run 行 + 点击跳转工作流面板选中该 run。
//  ① 纯投影面（lib/workflowRunRows）：running 过滤；字段投影——工作流名/
//     当前节点（DAG 名映射，未知 id 回退节点 id）/已完成数（completed 与
//     gate_passed 同口径）/总数（DAG 定义优先，缺 DAG 退节点表）/起点；
//     节点表按 nodeId 后见覆盖（门重跑只计最终态）。
//  ② 入列面（React）：模拟运行中 run → 出现在任务面板「运行中」列表（字段
//     显示断言：名称/当前节点/进度/状态徽章）；分区计数计入 run 行；空态不
//     误显；点击行 → 出口回调带 runId；非运行 run 不入列；不传 props 的
//     备用视图 byte-for-byte（无 run 行）。
//  ③ 跳转锚定面（React）：WorkflowPanel 收 focusRunId → 运行选择器选中该
//     run + 工具条落点高亮（class + data 标记）。
//  ④ 接线源码钉：App 订阅既有喂单（workflowRunFeed，不造新机制）并把
//     running 子集与出口下发给任务面板；跳转=setFocus+自动开面板；面板收
//     focus props；三语键齐备；CSS 主题 token。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { workflowRunRowView, runningWorkflowRuns } from "../lib/workflowRunRows";
import type { WireWorkflowRun } from "../lib/workflowTypes";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");

function makeRun(overrides: Partial<WireWorkflowRun> = {}): WireWorkflowRun {
  return {
    runId: "wf-1433",
    workflow: "migration-wf",
    state: "running",
    startedAt: Date.now() - 120_000,
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

console.log("\ntl1433 workflow run in tasks panel: run 行入列 + 点击跳转选中");

// ── ① 纯投影面 ──────────────────────────────────────────────────────
{
  const running = makeRun();
  const completed = makeRun({ runId: "wf-done", state: "completed", endedAt: Date.now() });
  const interrupted = makeRun({ runId: "wf-cut", state: "interrupted", resumable: true });
  eq(runningWorkflowRuns([running, completed, interrupted]).map((r) => r.runId).join(","),
    "wf-1433", "running 过滤：只有运行中的 run 入列（completed/interrupted 不并）");
  eq(runningWorkflowRuns([]).length, 0, "running 过滤：空表安全");

  const view = workflowRunRowView(makeRun({
    nodes: [
      { nodeId: "plan", nodeType: "subagent", state: "completed", changedAt: 1 },
      { nodeId: "read-facts", nodeType: "subagent", state: "gate_passed", changedAt: 2 },
      { nodeId: "gate-tests", nodeType: "gate", state: "running", changedAt: 3 },
    ],
  }));
  eq(view.workflow, "migration-wf", "投影：工作流名直传");
  eq(view.currentNode, "测试门", "投影：当前节点映射 DAG 人话名（非内部 id）");
  eq(view.doneCount, 2, "投影：completed 与 gate_passed 都计已完成");
  eq(view.totalCount, 5, "投影：总数取 DAG 定义（未见态节点的诚实总数）");
  ok(view.startedAt !== undefined, "投影：起点携带（行内时长折算用）");

  const gap = workflowRunRowView(makeRun({
    nodes: [
      { nodeId: "plan", nodeType: "subagent", state: "completed", changedAt: 1 },
    ],
  }));
  eq(gap.currentNode, "", "投影：无运行中节点（节点间隙/全排队）→ 当前节点空");

  const unknownId = workflowRunRowView(makeRun({
    nodes: [{ nodeId: "ghost", nodeType: "phase", state: "running", changedAt: 1 }],
  }));
  eq(unknownId.currentNode, "ghost", "投影：DAG 未知名回退节点 id（不显示空行）");

  const retried = workflowRunRowView(makeRun({
    nodes: [
      { nodeId: "gate-tests", nodeType: "gate", state: "failed", changedAt: 1 },
      { nodeId: "gate-tests", nodeType: "gate", state: "running", changedAt: 2 },
      { nodeId: "gate-tests", nodeType: "gate", state: "gate_passed", changedAt: 3 },
    ],
  }));
  eq(retried.doneCount, 1, "投影：门重跑多次变迁按最终态计一次（后见覆盖）");
  eq(retried.currentNode, "", "投影：重跑后终态不再算当前节点");

  const noDag = workflowRunRowView(makeRun({
    dag: undefined,
    nodes: [
      { nodeId: "a", nodeType: "subagent", state: "completed", changedAt: 1 },
      { nodeId: "b", nodeType: "gate", state: "running", changedAt: 2 },
    ],
  }));
  eq(noDag.totalCount, 2, "投影：缺 DAG 退节点表长度（降级不炸）");
  eq(noDag.currentNode, "b", "投影：缺 DAG 时当前节点回退节点 id");
}

// ── ② 入列面（React）────────────────────────────────────────────────
{
  const dom = new (await import("jsdom")).JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  // useMountTransition（WorkflowPanel 挂载动画）用裸 requestAnimationFrame——
  // jsdom 的 rAF 停在自己的 window 上，挂到 globalThis 供裸引用解析。
  Object.defineProperty(globalThis, "requestAnimationFrame", { configurable: true, writable: true, value: dom.window.requestAnimationFrame.bind(dom.window) });
  Object.defineProperty(globalThis, "cancelAnimationFrame", { configurable: true, writable: true, value: dom.window.cancelAnimationFrame.bind(dom.window) });

  const { createElement } = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = await import("react");
  const { LocaleProvider } = await import("../lib/i18n");
  const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");
  const { buildSubagentDirectory } = await import("../lib/subagentDirectory");

  const run = makeRun({
    nodes: [
      { nodeId: "plan", nodeType: "subagent", state: "completed", changedAt: 1 },
      { nodeId: "read-facts", nodeType: "subagent", state: "completed", changedAt: 2 },
      { nodeId: "gate-tests", nodeType: "gate", state: "running", changedAt: 3 },
    ],
  });

  // 入列断言：模拟运行中 run → 出现在任务面板「运行中」列表（子代理目录为空
  // 也要入列——工作流在跑时面板不再读「暂无」）。
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const opened: string[] = [];
  await act(async () => {
    root.render(createElement(LocaleProvider, null,
      createElement(SubagentsDockPanel, {
        directory: buildSubagentDirectory([]),
        workflowRuns: [run],
        onOpenWorkflowRun: (runId: string) => { opened.push(runId); },
      })));
  });

  const row = container.querySelector<HTMLButtonElement>("[data-workflow-run='wf-1433']");
  ok(row !== null, "入列：运行中的 run 以任务行出现在运行列表");
  eq(container.querySelector(".subagents-panel__empty") !== null, false, "入列：有运行 run 时不显示空态");

  // 字段显示断言：名称/当前节点/进度/状态徽章（元行经 data-wf-run-meta 定位）。
  const meta = container.querySelector("[data-wf-run-meta='wf-1433']");
  ok(meta !== null, "字段：run 行携带元行");
  const metaText = meta?.textContent ?? "";
  ok(metaText.includes("migration-wf") === false, "字段：元行不重复工作流名（名称在标题位）");
  const rowText = row?.textContent ?? "";
  ok(rowText.includes("migration-wf"), "字段：工作流名显示在行标题位");
  ok(metaText.includes("测试门"), "字段：当前节点显示 DAG 人话名");
  ok(metaText.includes("2/5"), "字段：已完成/总节点数显示（2/5）");
  // 状态徽章：locale 依赖（测试 env 走 en），按既有 harness 惯例做结构性断言
  // ——徽章 span 非空且为三语运行态之一；键接线另有源码钉。
  const statusText = container.querySelector(".subagents-panel__row--workflow .subagents-panel__row-status")?.textContent ?? "";
  ok(["运行中", "執行中", "Running"].includes(statusText), "字段：状态徽章显示（workflowPanel.run.running 三语之一）");
  ok(/\d|\u5206|\u79D2|s/.test(metaText), "字段：运行时长有折算显示（非空占位）");
  ok(row?.getAttribute("aria-label")?.includes("migration-wf"), "字段：行按钮可读名（工作流名）");

  // 分区计数计入 run 行（标题=「运行中 · 1」——纯数字部分跨语言稳定）。
  const runningTitle = container.querySelectorAll(".subagents-panel__section-title")[0]?.textContent ?? "";
  ok(runningTitle.includes("1"), "入列：运行分区计数计入 run 行（·1）");

  // 点击跳转断言：行点击 → 出口回调带 runId（App 据此开面板并锚定选中）。
  await act(async () => { row!.click(); });
  eq(opened.join(","), "wf-1433", "跳转：点击 run 行触发出口回调且带 runId");

  // 备用视图 byte-for-byte：不传 props（WorkspaceDockRegion 降级形态）→ 无
  // run 行、面板与既有形态一致（空目录 → 原空态）。
  await act(async () => { root.unmount(); });
  const fallback = document.createElement("div");
  document.body.appendChild(fallback);
  const fallbackRoot = createRoot(fallback);
  await act(async () => {
    fallbackRoot.render(createElement(LocaleProvider, null,
      createElement(SubagentsDockPanel, { directory: buildSubagentDirectory([]) })));
  });
  eq(fallback.querySelectorAll("[data-workflow-run]").length, 0, "兼容：不传 props 的备用视图无 run 行");
  eq(fallback.querySelector(".subagents-panel__empty") !== null, true, "兼容：不传 props 时空态原样（byte-for-byte）");
  await act(async () => { fallbackRoot.unmount(); });
  fallback.remove();

  // 非运行 run 不入列：completed 即便出现在下发数据（App 已过滤，防御面）
  // 也不渲染成行。root 已 unmount（不可复用），防御段开新 root。
  const guard = document.createElement("div");
  document.body.appendChild(guard);
  const guardRoot = createRoot(guard);
  await act(async () => {
    guardRoot.render(createElement(LocaleProvider, null,
      createElement(SubagentsDockPanel, {
        directory: buildSubagentDirectory([]),
        workflowRuns: [makeRun({ state: "completed", endedAt: Date.now() })],
        onOpenWorkflowRun: (runId: string) => { opened.push(runId); },
      })));
  });
  eq(guard.querySelectorAll("[data-workflow-run]").length, 0, "防御：非运行 run 不渲染成行");
  await act(async () => { guardRoot.unmount(); });
  guard.remove();
  container.remove();

  // ③ 跳转锚定面：WorkflowPanel 收 focusRunId → 选择器选中该 run + 工具条
  // 落点高亮（class + data 标记）。
  const { WorkflowPanel } = await import("../components/WorkflowPanel");
  const panelHost = document.createElement("div");
  document.body.appendChild(panelHost);
  const panelRoot = createRoot(panelHost);
  const runA = makeRun({ runId: "wf-aaa", workflow: "wf-alpha" });
  const runB = makeRun({ runId: "wf-bbb", workflow: "wf-beta" });
  await act(async () => {
    panelRoot.render(createElement(LocaleProvider, null,
      createElement(WorkflowPanel, {
        open: true,
        onClose: () => {},
        runs: [runA, runB],
        focusRunId: "wf-bbb",
        focusNonce: 1,
      })));
  });
  const picker = panelHost.querySelector<HTMLSelectElement>('[data-testid="wf-run-picker"]');
  ok(picker !== null, "跳转：面板渲染运行选择器");
  eq(picker?.value, "wf-bbb", "跳转：选择器选中 focus 指向的 run（非默认最近一条）");
  const toolbar = panelHost.querySelector(".wf-panel__toolbar");
  ok(toolbar?.classList.contains("wf-panel__toolbar--focus"), "跳转：工具条携带落点高亮 class");
  eq(toolbar?.getAttribute("data-wf-focus-run"), "wf-bbb", "跳转：落点高亮带 data 标记（harness/可测）");
  eq(panelHost.querySelector(".wf-panel__name")?.textContent, "wf-beta", "跳转：面板标题切到该 run 的工作流名");
  await act(async () => { panelRoot.unmount(); });
  panelHost.remove();

  (globalThis as { window?: unknown }).window = undefined;
  (globalThis as { document?: unknown }).document = undefined;
  (globalThis as { requestAnimationFrame?: unknown }).requestAnimationFrame = undefined;
  (globalThis as { cancelAnimationFrame?: unknown }).cancelAnimationFrame = undefined;
}

// ── ④ 接线源码钉 ────────────────────────────────────────────────────
{
  const app = read("src/App.tsx");
  ok(app.includes("useSyncExternalStore(workflowRunFeed.subscribe, workflowRunFeed.getRuns"), "接线：App 订阅既有工作流喂单（不造新机制）");
  ok(app.includes("runningWorkflowRuns(workflowFeedRuns)"), "接线：只把 running 子集下发任务面板");
  ok(app.includes("workflowRuns={workflowRunningRows}") && app.includes("onOpenWorkflowRun={openWorkflowRunFromTasks}"), "接线：run 行数据与出口下发给任务面板的 SubagentsDockPanel");
  ok(app.includes("setWorkflowPanelFocus({ runId, nonce"), "接线：跳转先置锚定目标");
  ok(/openWorkflowRunFromTasks[\s\S]{0,400}setWorkflowPanelOpen\(true\)/.test(app), "接线：跳转自动打开工作流面板（未开则开）");
  ok(app.includes("focusRunId={workflowPanelFocus?.runId}") && app.includes("focusNonce={workflowPanelFocus?.nonce}"), "接线：锚定目标透传 WorkflowPanel");

  const panel = read("src/components/SubagentsDockPanel.tsx");
  ok(panel.includes('import { runningWorkflowRuns, workflowRunRowView'), "接线：任务面板行视图走既有喂单的纯投影（workflowRunRows）");
  ok(panel.includes("runningWorkflowRuns(workflowRuns ?? [])"), "接线：面板内再过滤一道 running（防御面，completed 不渲染成说谎的行）");
  ok(panel.includes("data-workflow-run="), "接线：run 行携带可测 data 标记");
  ok((panel.match(/WorkflowRunRow key=/g) ?? []).length === 2, "接线：split 与普通两种形态都渲染 run 行");
  ok(panel.includes("running.length + workflowViews.length"), "接线：运行分区计数计入 run 行");
  ok(panel.includes("running.length === 0 && ended.length === 0 && !hasWorkflowRows"), "接线：空态判定计入 run 行");
  ok(panel.includes('t("workflowPanel.run.running")'), "接线：状态徽章复用既有三语键（不新增文案）");
  ok(panel.includes('t("workflowPanel.progress"') && panel.includes('t("workflowPanel.title")'), "接线：进度与类型文案复用既有三语键");

  const wfPanel = read("src/components/WorkflowPanel.tsx");
  ok(wfPanel.includes("focusRunId?: string") && wfPanel.includes("focusNonce?: number"), "接线：WorkflowPanel 开锚定注入口");
  ok(wfPanel.includes("setSelectedRunId(focusRunId)"), "接线：锚定到达即选中该 run");
  ok(wfPanel.includes('data-wf-focus-run='), "接线：落点高亮 data 标记");

  const dock = read("src/app-shell/WorkspaceDockRegion.tsx");
  ok(dock.includes("<SubagentsDockPanel directory={props.subagents} />"), "兼容：备用视图不传 run 行 props（byte-for-byte）");

  const css = read("src/styles.css");
  ok(css.includes(".subagents-panel__row-main") && css.includes(".subagents-panel__wf-meta"), "样式：run 行元行走主题 token 的既有类族");
  ok(css.includes("@keyframes wf-toolbar-focus"), "样式：落点高亮动画已定义");

  // 三语键：run 行全部文案复用既有键，三种语言各一份。
  for (const locale of ["zh", "zh-TW", "en"]) {
    const dict = read(`src/locales/${locale}.ts`);
    ok(dict.includes('"workflowPanel.run.running"') && dict.includes('"workflowPanel.progress"') && dict.includes('"workflowPanel.title"'),
      `三语：${locale} 复用键齐备（状态/进度/类型）`);
  }
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
