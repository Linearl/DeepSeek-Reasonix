// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1434-workflow-node-trace.test.tsx
// tl1434 验收 harness —— 工作流节点详情「工作过程」只读回放（三面一体之 B：
// 工作流图内入口；tl1433 任务视图入口 / tl1435 右侧栏归口看同一登记数据）。
//  ① 归约面：wire 节点 subagentRefs → 详情 detail 携带；节点事件就地点亮
//     保留已登记 refs（点亮不得抹掉入口）。
//  ② 面板源码钉：入口只挂 subagent/confirm + 真运行视图（demo 逐字节不变）；
//     逐次执行「第 N 次」打开；只读声明；降级显示（终态无关联）。
//  ③ 桥与降级钉：bridge 接口+mock 诚实空表+wailsjs 手写声明；desktop 桥两道
//     归属证明 + 只读管线（previewSessionMessages）；Go harness 零写入断言
//     存在（TestReadWorkflowNodeSubagentReturnsMessagesAndWritesNothing +
//     assertDirUnchanged）。
//  ④ 文案三语：7 个键 × en/zh/zh-TW。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { liveRunSnapshot, workflowRunFeed, type WireWorkflowRun } from "../lib/workflowRunFeed";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;
const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");
const readRepo = (rel: string) => readFileSync(`${repoRoot}/${rel}`, "utf8");

function makeRun(overrides: Partial<WireWorkflowRun> = {}): WireWorkflowRun {
  return {
    runId: "wf-trace1",
    workflow: "trace-wf",
    state: "running",
    startedAt: 1000,
    nodes: [
      { nodeId: "probe", nodeType: "subagent", state: "completed", changedAt: 2000, nodeStartedAt: 1500, subagentRefs: ["sa_1434_a", "sa_1434_b"] },
      { nodeId: "gate", nodeType: "gate", state: "gate_passed", changedAt: 2100, nodeStartedAt: 2000 },
    ],
    dag: {
      id: "trace-wf", name: "trace-wf",
      nodes: [
        { id: "probe", name: "probe", kind: "subagent" },
        { id: "gate", name: "gate", kind: "gate" },
      ],
      edges: [{ from: "probe", to: "gate", label: "pass" }],
    },
    ...overrides,
  };
}

console.log("\ntl1434 workflow node trace: nodeId→执行实例关联的只读回放入口（工作流图内）");

// ── ① 归约面 ─────────────────────────────────────────────────────────
{
  const snap = liveRunSnapshot(makeRun());
  ok(snap.nodes.probe?.detail?.subagentRefs?.join(",") === "sa_1434_a,sa_1434_b", "liveRunSnapshot carries registered refs into the node detail");
  ok(snap.nodes.probe?.detail?.subagentRefs !== undefined && !(snap.nodes.probe.detail.subagentRefs === undefined), "refs survive the wire→panel projection");
  ok(!("subagentRefs" in (snap.nodes.gate?.detail ?? {})), "trace-less node (gate) carries no refs (degraded, never fabricated)");

  // 节点事件就地点亮保留 refs：先喂快照，再喂 node-state 事件。
  workflowRunFeed.ingest([makeRun()]);
  workflowRunFeed.ingestNodeEvent({ runId: "wf-trace1", nodeId: "probe", state: "running", round: 1 });
  const runs = workflowRunFeed.getRuns();
  const probe = runs.find((r) => r.runId === "wf-trace1")?.nodes.find((n) => n.nodeId === "probe");
  ok(probe?.state === "running", "node event lights the node in place");
  ok(probe?.subagentRefs?.join(",") === "sa_1434_a,sa_1434_b", "node event keeps the snapshot-registered refs (entry not wiped by lighting)");
}

// ── ② 面板源码钉 ─────────────────────────────────────────────────────
{
  const panel = read("src/components/WorkflowPanel.tsx");
  ok(panel.includes("NodeWorkProcessSection"), "detail pane renders the work-process section component");
  ok(panel.includes('kind !== "subagent" && kind !== "confirm"'), "gate/join/phase nodes never render the trace section");
  ok(panel.includes("live && runId") || panel.includes("live={liveView !== null}"), "trace section is gated to the live run view (demo/fixture stays byte-for-byte)");
  ok(panel.includes("ReadWorkflowNodeSubagent(runId, nodeId, occurrence)"), "opening a run calls the read-only bridge with runId+nodeId+occurrence");
  ok(panel.includes('data-testid="wf-node-trace-open"'), "per-occurrence entry buttons exist (index+1 = occurrence)");
  ok(panel.includes('data-testid="wf-node-trace-missing"'), "degraded branch renders for terminal nodes without registered refs");
  ok(panel.includes('t("workflowPanel.detail.processMissing")'), "degraded copy explains no recorded executions");
  ok(panel.includes('t("workflowPanel.detail.processReadOnly")'), "read-only statement is rendered with the trace view");
  ok(panel.includes("historyMessagesToItems(trace.messages"), "transcript reuses the capsule read-only pipeline (historyMessagesToItems + Transcript)");
  ok(panel.includes("<LazyTranscript items={traceItems} onPrompt={() => {}} questionNavigator={false} rewindDisabled />"), "replay renderer is the read-only Transcript form (no input, no rewind)");
}

// ── ③ 桥与降级钉 ─────────────────────────────────────────────────────
{
  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("ReadWorkflowNodeSubagent(runId: string, nodeId: string, occurrence: number): Promise<HistoryMessage[]>"), "bridge interface declares the read-only replay binding");
  ok(bridge.includes("async ReadWorkflowNodeSubagent()"), "browser mock answers honestly (no fabricated replay)");
  const wailsDts = read("wailsjs/go/main/App.d.ts");
  ok(wailsDts.includes("ReadWorkflowNodeSubagent(arg1:string,arg2:string,arg3:number)"), "hand-written wailsjs declaration exists (gitignored generated file)");

  const goBridge = readRepo("desktop/workflow_runs_app.go");
  ok(goBridge.includes("func (a *App) ReadWorkflowNodeSubagent("), "desktop binding exists for the panel entry");
  ok(goBridge.includes("svc.SubagentRefForNode(runID, nodeID, occurrence)"), "membership proof 1: ref must come from the run's registry (no arbitrary ref reads)");
  ok(goBridge.includes("agent.ListSubagentsByParent(dir, parentSession)"), "membership proof 2: ref must be owned by the parent session artifact list");
  ok(goBridge.includes("previewSessionMessages(dir, artifact.SessionPath)"), "replay reads through the read-only preview pipeline (same as ReadSubagentSession)");
  ok(goBridge.includes("subagentRefs,omitempty"), "wire node carries optional subagentRefs (append-only field, legacy snapshots unaffected)");

  // 只读铁律的 Go harness 存在且断言真实（内容+mtime+文件集+meta.Status）。
  const goTest = readRepo("desktop/workflow_node_subagent_test.go");
  ok(goTest.includes("func TestReadWorkflowNodeSubagentReturnsMessagesAndWritesNothing"), "Go harness: zero-write acceptance test exists");
  ok(goTest.includes("func assertDirUnchanged"), "Go harness: content+mtime+file-set assertion exists");
  ok(goTest.includes("meta.Status != agent.SubagentCompleted"), "Go harness: meta status must stay untouched after viewing");

  // 登记面 Go harness（关联缺失/降级矩阵）。
  const wfTest = readRepo("internal/workflow/tl1434_subagent_trace_test.go");
  ok(wfTest.includes("func TestLegacySnapshotWithoutTraceDegrades"), "Go harness: legacy snapshot (no fields) degrades explicitly");
  ok(wfTest.includes("func TestSubagentRefForNodeMembershipAndBounds"), "Go harness: query membership + bounds degrade");
  const agentTest = readRepo("internal/agent/subagent_ref_test.go");
  ok(agentTest.includes("func TestRunProfileSpecEphemeralRunLeavesSinkEmpty"), "Go harness: ephemeral dispatch never fabricates an identity");
}

// ── ④ 文案三语 ───────────────────────────────────────────────────────
{
  const keys = [
    "workflowPanel.detail.process",
    "workflowPanel.detail.processOpen",
    "workflowPanel.detail.processLoading",
    "workflowPanel.detail.processFailed",
    "workflowPanel.detail.processEmpty",
    "workflowPanel.detail.processMissing",
    "workflowPanel.detail.processReadOnly",
  ];
  for (const key of keys) {
    ok(key in zh, `zh has ${key}`);
    ok(key in zhTW, `zh-TW has ${key}`);
  }
  const en = read("src/locales/en.ts");
  ok(en.includes('"workflowPanel.detail.processReadOnly"'), "en has the read-only statement key");
  ok(zh["workflowPanel.detail.processReadOnly"].includes("只读") && zh["workflowPanel.detail.processReadOnly"].includes("不会唤醒或写入"), "zh read-only copy states the no-wake/no-write promise");
  ok(zhTW["workflowPanel.detail.processReadOnly"].includes("唯讀"), "zh-TW read-only copy states the same promise");
}

process.stdout.write(`\ntl1434 workflow node trace: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
