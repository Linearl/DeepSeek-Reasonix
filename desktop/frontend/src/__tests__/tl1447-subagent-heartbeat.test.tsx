// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1447-subagent-heartbeat.test.tsx
// tl1447 验收 harness —— 子代理心跳与续轮：活性检测→异常先发『静默』徽章→
// 「继续」续轮入口（先确认后继续）→ 工作流停止 run 落可续态 + 续跑入口。
//  ① 活跃性判定面（lib/subagentDirectory）：登记行活跃面透传（前台行 stamp /
//     job 行无此面不虚报）；静默时长与阈值边界（<5min false / =5min true /
//     无活跃面=0 永不示警）；merge 时转录卡继承登记孪行活跃面。
//  ② 行渲染面（SubagentsDockPanel）：新鲜运行行零打扰（无徽章无按钮——验收⑤
//     回归钉）；静默越阈运行行出心跳徽章 +「继续」入口；两步确认（先确认后
//     继续，第一步不发桥）；回执 disposition 行；终态行永不示警（边界）。
//  ③ 工作流面（WorkflowPanel）：停止 run（state=interrupted+resumable）出
//     续跑钮；failed（真失败）不出——failed≠interrupted 边界（缺陷③）；
//     运行中节点静默越阈出 data-silent-node 标记（triage 卡死形态）。
//  ④ 接线源码钉：App 绑定 ContinueSubagentForSession；三语键齐备；CSS 主题
//     token（--warn）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  SUBAGENT_SILENCE_ALERT_MS,
  isSubagentEntrySilent,
  mergeRegistryRunningSubagents,
  registryRunningEntries,
  subagentSilenceMs,
  buildSubagentDirectory,
} from "../lib/subagentDirectory";
import type { ForegroundSubagentView, JobView } from "../lib/types";
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

const MIN = 60_000;
const NOW = 1_800_000_000_000;

function fgEntry(overrides: Partial<ForegroundSubagentView> = {}): ForegroundSubagentView {
  return {
    tabId: "tab-1",
    ref: "sa_fg_1",
    name: "调研子代理",
    description: "扫描证据链",
    startedAt: NOW - 30 * MIN,
    lastActivityAt: NOW,
    ...overrides,
  };
}

function job(overrides: Partial<JobView> = {}): JobView {
  return { id: "job-1", label: "后台调研", kind: "task", startedAt: NOW - 10 * MIN, ...overrides } as JobView;
}

function makeRun(overrides: Partial<WireWorkflowRun> = {}): WireWorkflowRun {
  return {
    runId: "wf-1447",
    workflow: "security-run",
    state: "running",
    startedAt: NOW - 40 * MIN,
    nodes: [],
    dag: {
      id: "security-run",
      name: "security-run",
      phases: ["triage"],
      nodes: [
        { id: "triage", name: "分诊", kind: "subagent", phase: "triage" },
        { id: "report", name: "报告", kind: "subagent", phase: "triage" },
      ],
      edges: [{ from: "triage", to: "report" }],
    },
    ...overrides,
  };
}

console.log("\ntl1447 subagent heartbeat: 活跃性判定 → 静默徽章 → 继续续轮 → 停止可续");

// ── ① 活跃性判定面 ──────────────────────────────────────────────────
{
  eq(SUBAGENT_SILENCE_ALERT_MS, 5 * MIN, "阈值：默认 5 分钟静默（tl1447 验收口径下限）");

  const rows = registryRunningEntries({ foreground: [fgEntry()] }, "tab-1");
  eq(rows.length, 1, "投影：前台登记行入列");
  eq(rows[0]?.lastActivityAt, NOW, "投影：活跃面随行透传（静默判定输入）");
  eq(rows[0]?.ref, "sa_fg_1", "投影：ref 保留（继续桥的目标身份）");

  const jobRows = registryRunningEntries({ jobs: [job()] }, "tab-1");
  eq(jobRows[0]?.lastActivityAt, undefined, "投影：job 行无活跃面（不虚报静默，自有看门狗族）");

  eq(subagentSilenceMs({}, NOW), 0, "静默：无活跃面 = 0（「不知道」≠「静默」，绝不虚报）");
  eq(subagentSilenceMs({ lastActivityAt: NOW - 3 * MIN }, NOW), 3 * MIN, "静默：时长=now-lastActivity");
  eq(subagentSilenceMs({ lastActivityAt: NOW + 1000 }, NOW), 0, "静默：时钟漂移钳零（负值不外泄）");

  ok(!isSubagentEntrySilent({ lastActivityAt: NOW - 4 * MIN }, NOW), "阈值：静默 4 分钟不示警（心跳不误杀）");
  ok(isSubagentEntrySilent({ lastActivityAt: NOW - 5 * MIN }, NOW), "阈值：静默恰 5 分钟示警（≥边界）");
  ok(isSubagentEntrySilent({ lastActivityAt: NOW - 31 * MIN }, NOW), "阈值：静默 31 分钟示警（triage 30 分钟实测形态）");
  ok(!isSubagentEntrySilent({}, NOW), "阈值：无活跃面永不示警");

  // merge 继承面：转录卡 + 登记孪行 → 卡继承活跃面（有卡子代理也受静默判定覆盖）。
  const directory = buildSubagentDirectory([]);
  const card = { ...rows[0], item: { ...rows[0].item, id: "call-1" } } as typeof rows[0];
  const cardNoStamp = { ...card, lastActivityAt: undefined } as typeof rows[0];
  const merged = mergeRegistryRunningSubagents(
    { running: [cardNoStamp], ended: [] },
    registryRunningEntries({ foreground: [fgEntry()] }, "tab-1"),
  );
  eq(merged.running[0]?.lastActivityAt, NOW, "merge：ref 碰撞时卡继承登记行活跃面（登记表是活跃面唯一权威）");
  eq(merged.running[0]?.item.id, "call-1", "merge：卡本体存活（富态不被登记行顶掉）");

  const mergedNoTwin = mergeRegistryRunningSubagents({ running: [card], ended: [] }, []);
  eq(mergedNoTwin.running[0]?.lastActivityAt, NOW, "merge：空登记表原样返回（纯函数语义）");
  const registryAlone = mergeRegistryRunningSubagents(
    { running: [], ended: [] },
    registryRunningEntries({ foreground: [fgEntry({ ref: "sa_solo", lastActivityAt: NOW - 31 * MIN })] }, "tab-1"),
  );
  eq(registryAlone.running[0]?.lastActivityAt, NOW - 31 * MIN, "merge：无碰撞登记行原样入列（stall 形态自带活跃面）");
}

// ── ② 行渲染面（React）──────────────────────────────────────────────
{
  const dom = new (await import("jsdom")).JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

  const { createElement } = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = await import("react");
  const { LocaleProvider } = await import("../lib/i18n");
  const { SubagentsDockPanel } = await import("../components/SubagentsDockPanel");

  const mount = async (props: Record<string, unknown>) => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    await act(async () => {
      root.render(createElement(LocaleProvider, null, createElement(SubagentsDockPanel, props as never)));
    });
    return { container, unmount: async () => { await act(async () => { root.unmount(); }); container.remove(); } };
  };

  // 行渲染段的活跃面用真实时钟相对值（面板墙钟取 Date.now()，固定时间戳
  // 会被钳零——纯投影段才用固定 NOW）。
  const realNow = Date.now();
  const freshRows = registryRunningEntries({ foreground: [fgEntry({ lastActivityAt: realNow })] }, "tab-1");
  const silentRows = registryRunningEntries({ foreground: [fgEntry({ ref: "sa_fg_stuck", description: "卡死的子代理", lastActivityAt: realNow - 31 * MIN })] }, "tab-1");

  // 验收⑤回归钉：新鲜运行行零打扰——无徽章、无继续入口。
  const quiet = await mount({ directory: buildSubagentDirectory([]), registryRunning: freshRows, now: undefined });
  eq(quiet.container.querySelectorAll("[data-subagent-silent]").length, 0, "零打扰：新鲜运行行无心跳徽章");
  eq(quiet.container.querySelectorAll("[data-subagent-continue]").length, 0, "零打扰：新鲜运行行无继续入口");
  await quiet.unmount();

  // 卡死形态：静默越阈 → 心跳徽章 +「继续」入口（需桥在手）。
  const bridgeCalls: string[] = [];
  const stuck = await mount({
    directory: buildSubagentDirectory([]),
    registryRunning: silentRows,
    sessionPath: "/mock/sessions/s1",
    onContinueSubagent: async (_sessionPath: string, ref: string) => { bridgeCalls.push(ref); return { messageId: "m1", disposition: "steered" }; },
  });
  ok(stuck.container.querySelector("[data-subagent-silent]") !== null, "缺陷①：静默越阈运行行出心跳警示徽章（异常先发声）");
  const continueBtn = stuck.container.querySelector<HTMLButtonElement>("[data-subagent-continue='sa_fg_stuck']");
  ok(continueBtn !== null, "缺陷②：静默越阈运行行出「继续」入口");

  // 两步确认：第一步不发桥，第二步（确认）才发。
  await act(async () => { continueBtn!.click(); });
  const confirmBtn = stuck.container.querySelector<HTMLButtonElement>("[data-subagent-confirm-continue='sa_fg_stuck']");
  ok(confirmBtn !== null, "缺陷②：第一步进确认态（先确认）");
  eq(bridgeCalls.length, 0, "缺陷②：确认态前桥未触发（不误发）");
  await act(async () => { confirmBtn!.click(); });
  eq(bridgeCalls.join(","), "sa_fg_stuck", "缺陷②：确认后桥带 ref 触发（后继续）");
  const receipt = stuck.container.querySelector("[data-subagent-continue-disposition='steered']");
  ok(receipt !== null, "缺陷②：回执 disposition 落行内（steered=注入运行轮）");
  await stuck.unmount();

  // 无桥（备用视图形态）：静默行只出徽章不出死按钮（数据与出口同源注入）。
  const noBridge = await mount({ directory: buildSubagentDirectory([]), registryRunning: silentRows });
  ok(noBridge.container.querySelector("[data-subagent-silent]") !== null, "兼容：无桥时徽章仍在（提示面独立于处置面）");
  eq(noBridge.container.querySelectorAll("[data-subagent-continue]").length, 0, "兼容：无桥时无继续入口（不渲染死按钮）");
  await noBridge.unmount();

  // job 行（无 ref 无活跃面）：永不示警、永无入口。
  const jobOnly = await mount({ directory: buildSubagentDirectory([]), registryRunning: registryRunningEntries({ jobs: [job({ id: "job-silent" })] }, "tab-1") });
  eq(jobOnly.container.querySelectorAll("[data-subagent-silent]").length, 0, "边界：job 行不示警（活跃面缺省=0）");
  eq(jobOnly.container.querySelectorAll("[data-subagent-continue]").length, 0, "边界：job 行无继续入口");
  await jobOnly.unmount();

  await act(async () => {});
  (globalThis as { window?: unknown }).window = undefined;
  (globalThis as { document?: unknown }).document = undefined;
}

// ── ③ 工作流面：停止可续 + failed 边界 + 节点静默 ────────────────────
{
  const dom = new (await import("jsdom")).JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(globalThis, "requestAnimationFrame", { configurable: true, writable: true, value: dom.window.requestAnimationFrame.bind(dom.window) });
  Object.defineProperty(globalThis, "cancelAnimationFrame", { configurable: true, writable: true, value: dom.window.cancelAnimationFrame.bind(dom.window) });

  const { createElement } = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = await import("react");
  const { LocaleProvider } = await import("../lib/i18n");
  const { WorkflowPanel } = await import("../components/WorkflowPanel");

  const mountPanel = async (run: WireWorkflowRun) => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    await act(async () => {
      root.render(createElement(LocaleProvider, null, createElement(WorkflowPanel, { open: true, onClose: () => {}, runs: [run] })));
    });
    return { container, unmount: async () => { await act(async () => { root.unmount(); }); container.remove(); } };
  };

  // 缺陷③ 正面：停止 run（tl1447 后落 interrupted+resumable）→ 续跑钮点亮
  // （1316 既有入口，面板续跑 = 「继续」续轮的工作流形态）。
  const stopped = await mountPanel(makeRun({ state: "interrupted", resumable: true, endedAt: NOW }));
  ok(stopped.container.querySelector('[data-testid="wf-resume-live"]') !== null, "缺陷③：停止 run（interrupted+resumable）出续跑入口");
  await stopped.unmount();

  // 缺陷③ 边界：真失败（failed）不出续跑钮——failed≠interrupted 语义边界
  // 在 UI 面的投影（failed 的下一步是重跑，不是续跑）。
  const failed = await mountPanel(makeRun({ state: "failed", endedAt: NOW, error: "node exploded" }));
  eq(failed.container.querySelectorAll('[data-testid="wf-resume-live"]').length, 0, "边界：failed run 无续跑入口（failed≠interrupted）");
  await failed.unmount();

  // 缺陷③ 边界：interrupted 但服务侧判不可续（无断点/定义漂移）→ 无入口。
  const notResumable = await mountPanel(makeRun({ state: "interrupted", resumable: false, endedAt: NOW }));
  eq(notResumable.container.querySelectorAll('[data-testid="wf-resume-live"]').length, 0, "边界：interrupted 但无断点不虚标续跑入口");
  await notResumable.unmount();

  // 缺陷① 工作流形态：运行中节点静默越阈（triage 卡死 30 分钟）→
  // data-silent-node 标记；新鲜运行节点不标（心跳不误杀）。
  const staleChangedAt = Date.now() - 31 * MIN;
  const stuckNode = await mountPanel(makeRun({
    state: "running",
    nodes: [
      { nodeId: "triage", nodeType: "subagent", state: "running", changedAt: staleChangedAt },
      { nodeId: "report", nodeType: "subagent", state: "queued" },
    ],
  }));
  ok(stuckNode.container.querySelector("[data-silent-node='triage']") !== null, "缺陷①：运行中节点静默越阈出警示标记（triage 卡死形态）");
  ok(stuckNode.container.querySelector(".wf-node--silent") !== null, "缺陷①：静默节点携带警示类（主题 token）");
  await stuckNode.unmount();

  const freshNode = await mountPanel(makeRun({
    state: "running",
    nodes: [{ nodeId: "triage", nodeType: "subagent", state: "running", changedAt: Date.now() - 1 * MIN }],
  }));
  eq(freshNode.container.querySelectorAll("[data-silent-node]").length, 0, "零打扰：新鲜运行节点不示警（心跳不误杀）");
  await freshNode.unmount();

  (globalThis as { window?: unknown }).window = undefined;
  (globalThis as { document?: unknown }).document = undefined;
  (globalThis as { requestAnimationFrame?: unknown }).requestAnimationFrame = undefined;
  (globalThis as { cancelAnimationFrame?: unknown }).cancelAnimationFrame = undefined;
}

// ── ④ 接线源码钉 + 三语键 + CSS token ────────────────────────────────
{
  const app = read("src/App.tsx");
  ok(app.includes("ContinueSubagentForSession") && app.includes("onContinueSubagent={continueSubagentForSession}"), "接线：App 绑定「继续」桥并下发给子代理面板");

  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("ContinueSubagentForSession(sessionPath: string, ref: string): Promise<SubagentSendReceiptView>"), "接线：桥面声明（Wails 绑定 + dev mock 双侧）");

  const dock = read("src/components/SubagentsDockPanel.tsx");
  ok(dock.includes("isSubagentEntrySilent") && dock.includes("data-subagent-silent"), "接线：行静默判定走单一阈值常量并带可测标记");
  ok(dock.includes("data-subagent-confirm-continue"), "接线：两步确认有独立可测标记（先确认后继续）");
  ok(dock.includes("useWorkflowWallClock(workflowViews.length > 0 || running.length > 0)"), "接线：运行行墙钟随运行行在列武装（静默时长随时钟增长；静止面板零定时器）");
  ok(dock.includes("onContinueSubagent && sessionPath ? continueSubagent : undefined"), "接线：桥缺席时行拿 undefined——不出继续入口（byte-for-byte）");

  const wfPanel = read("src/components/WorkflowPanel.tsx");
  ok(wfPanel.includes("SUBAGENT_SILENCE_ALERT_MS") && wfPanel.includes("data-silent-node"), "接线：工作流节点静默判定与子代理行同源阈值");

  for (const locale of ["zh", "zh-TW", "en"]) {
    const dict = read(`src/locales/${locale}.ts`);
    ok(dict.includes('"subagentPanel.silentBadge"') && dict.includes('"subagentPanel.continueSent"') && dict.includes('"subagentPanel.continueConfirm"') && dict.includes('"subagentPanel.continueSending"') && dict.includes('"workflowPanel.nodeSilent"'),
      `三语：${locale} 心跳/继续键齐备`);
  }

  const css = read("src/styles.css");
  ok(css.includes(".subagents-panel__silent") && css.includes(".subagents-panel__continue"), "样式：静默徽章与继续按钮走主题 token 的类族");
  ok(css.includes("var(--warn"), "样式：警示色用既有 --warn token（不新造颜色）");
  ok(css.includes(".wf-node--silent"), "样式：工作流节点静默态已定义");
}

let code = 0;
if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  code = 1;
} else {
  process.stdout.write(`\n${passed} passed, 0 failed\n`);
}
process.exit(code);
