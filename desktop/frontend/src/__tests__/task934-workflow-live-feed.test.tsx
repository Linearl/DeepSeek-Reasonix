// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task934-workflow-live-feed.test.tsx
// 任务 934 验收 harness —— 686 面板接真数据（WorkflowRunStore 外部源接 685
// 引擎真事件；fixture 降为「演示」保留）：
//  ① 纯归约面：快照合流（runId 整体替换/顺序保持/空推不抖）、最近优先、
//     节点表折叠、六态→五态投影（completed/gate_passed→done）、运行级
//     failed→errored、诊断文本下钻、wire DAG→面板 DAG 同构。
//  ② feed 单例行为：订阅即拉取（mock 环境空表兜底）、ingest 全量快照、
//     ingestNodeEvent 就地点亮（未知运行忽略=前向兼容）、监听通知。
//  ③ 接线源码钉：面板消费 workflowRunFeed + fixture 演示保留（933 拍板
//     playFixtureRun 不删）；bridge 声明并 mock 拉取/中止绑定；前端订阅
//     "workflow:runs" 推送通道与 agent:event 的 workflow_node 过滤；Go 侧
//     hub 推送 + App 绑定 + 引擎事件既走 agent:event 通道（eventwire）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  liveRunSnapshot,
  liveRunStateToPanelState,
  mergeRunsSnapshot,
  runNodeMap,
  runsNewestFirst,
  wireDagToPanelDag,
  workflowRunFeed,
  type WireRunDag,
  type WireWorkflowRun,
} from "../lib/workflowRunFeed";
import type { WorkflowDag } from "../lib/workflowDag";

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

function makeRun(overrides: Partial<WireWorkflowRun> = {}): WireWorkflowRun {
  return {
    runId: "wf-aaa111",
    workflow: "migration-wf",
    state: "running",
    startedAt: 1000,
    nodes: [],
    dag: {
      id: "migration-wf",
      name: "migration-wf",
      phases: ["survey", "deliver"],
      nodes: [
        { id: "plan", name: "plan", kind: "subagent", phase: "survey", instruction: "make a plan" },
        { id: "gate-tests", name: "gate-tests", kind: "gate", phase: "deliver", command: "go test ./..." },
        { id: "confirm", name: "confirm", kind: "confirm", phase: "deliver" },
      ],
      edges: [{ from: "plan", to: "gate-tests" }, { from: "gate-tests", to: "confirm", label: "pass" }],
    },
    ...overrides,
  };
}

console.log("\ntask 934 workflow live feed: 真事件接面板 + fixture 降为演示保留");

// ── ① 纯归约面 ──────────────────────────────────────────────────────
{
  const a = makeRun({ runId: "wf-a" });
  const b = makeRun({ runId: "wf-b", state: "completed", endedAt: 2000 });

  ok(mergeRunsSnapshot([], []) === undefined || mergeRunsSnapshot([], []).length === 0, "empty merge keeps empty");
  const once = mergeRunsSnapshot([], [a, b]);
  ok(once.map((r) => r.runId).join() === "wf-a,wf-b", "incoming order preserved on first merge");

  const updated = makeRun({ runId: "wf-a", state: "completed", endedAt: 3000 });
  const twice = mergeRunsSnapshot(once, [updated]);
  ok(twice.length === 2 && twice[0].runId === "wf-a" && twice[0].state === "completed", "same runId replaces in place");
  ok(twice.map((r) => r.runId).join() === "wf-a,wf-b", "relative order stable across merges");
  ok(mergeRunsSnapshot(once, []) === once, "empty incoming returns the SAME reference (no re-render churn)");

  ok(JSON.stringify(runsNewestFirst([a, b]).map((r) => r.runId)) === '["wf-b","wf-a"]', "newest-first reverses for the picker");
}

{
  const run = makeRun({
    state: "failed",
    error: "gate world failure",
    endedAt: 5000,
    nodes: [
      { nodeId: "plan", nodeType: "subagent", state: "completed", changedAt: 1100 },
      { nodeId: "gate-tests", nodeType: "gate", state: "gate_passed", round: 2, detail: "exit 0", changedAt: 1200 },
      { nodeId: "confirm", nodeType: "confirm", state: "unconfirmed", detail: "not reproduced", changedAt: 1300 },
      { nodeId: "phantom", nodeType: "phase", state: "failed", changedAt: 1400 },
    ],
  });

  const snap = liveRunSnapshot(run);
  ok(snap.runState === "errored" && snap.runError === "gate world failure", "failed run maps to errored badge with the error");
  ok(snap.startedAt === 1000 && snap.endedAt === 5000, "wall-clock stamps carried for the elapsed meter");
  ok(snap.nodes.plan?.state === "done", "completed node maps to done (green)");
  ok(snap.nodes["gate-tests"]?.state === "done", "gate_passed maps to done (green terminal)");
  ok(snap.nodes["gate-tests"]?.detail?.diagnostic === "exit 0", "engine detail lands as the diagnostic row");
  ok(snap.nodes.confirm?.state === "unconfirmed", "unconfirmed survives the projection (yellow)");
  ok(snap.nodes.phantom?.state === "failed", "failed maps to failed (red)");
  ok(snap.nodes["gate-tests"]?.changedAt === 1200, "changedAt carried for per-node timing");

  ok(liveRunStateToPanelState("running") === "running", "running stays running");
  ok(liveRunStateToPanelState("completed") === "completed", "completed stays completed");
  ok(liveRunStateToPanelState("failed") === "errored", "failed renders as errored");
}

{
  const run = makeRun();
  const map = runNodeMap(run);
  ok(map.size === 0, "node map of a stateless run starts empty");
  run.nodes.push({ nodeId: "plan", nodeType: "subagent", state: "running", changedAt: 900 });
  run.nodes.push({ nodeId: "plan", nodeType: "subagent", state: "completed", changedAt: 950 });
  const merged = runNodeMap(run);
  ok(merged.get("plan")?.state === "completed" && merged.get("plan")?.changedAt === 950, "later same-id entries override in place");
}

{
  const wireDag: WireRunDag = makeRun().dag!;
  const panel: WorkflowDag = wireDagToPanelDag(wireDag);
  ok(panel.id === wireDag.id && panel.name === wireDag.name && panel.phases?.join() === "survey,deliver", "dag identity and phases survive the mapping");
  ok(panel.nodes.length === 3 && panel.nodes[1].kind === "gate" && panel.nodes[1].command === "go test ./...", "gate node carries the literal command (drill-down)");
  ok(panel.nodes[0].instruction === "make a plan", "subagent node carries the instruction (drill-down)");
  ok(panel.edges[1].label === "pass", "gate branch label survives (pass/fail edge)");
}

// ── ② feed 单例行为 ─────────────────────────────────────────────────
{
  let notifications = 0;
  const unsubscribe = workflowRunFeed.subscribe(() => { notifications += 1; });

  workflowRunFeed.ingest([makeRun({ runId: "wf-live-1" })]);
  ok(workflowRunFeed.getRuns().length === 1 && notifications >= 1, "ingest notifies subscribers");

  workflowRunFeed.ingestNodeEvent({ runId: "wf-live-1", nodeId: "plan", state: "running", at: 1234 });
  const run = workflowRunFeed.getRuns().find((r) => r.runId === "wf-live-1");
  ok(run?.nodes[0]?.nodeId === "plan" && run.nodes[0]?.state === "running" && run.nodes[0]?.changedAt === 1234, "node event lights the run in place");

  workflowRunFeed.ingestNodeEvent({ runId: "wf-unknown", nodeId: "x", state: "running" });
  ok(workflowRunFeed.getRuns().length === 1, "unknown run's node event is ignored (forward compatible)");

  workflowRunFeed.ingestNodeEvent({ runId: "wf-live-1", nodeId: "plan", state: "done", round: 1, detail: "ok", at: 1300 });
  const after = workflowRunFeed.getRuns().find((r) => r.runId === "wf-live-1")!;
  const plan = after.nodes.find((n) => n.nodeId === "plan")!;
  ok(plan.state === "done" && plan.round === 1 && plan.detail === "ok", "later events override the node state machine");

  unsubscribe();
  workflowRunFeed.ingest([makeRun({ runId: "wf-live-2" })]);
  ok(notifications >= 1 && workflowRunFeed.getRuns().some((r) => r.runId === "wf-live-2"), "ingest after unsubscribe still lands (no leak into other listeners)");
}

// ── ③ 接线源码钉 ────────────────────────────────────────────────────
{
  const panel = read("src/components/WorkflowPanel.tsx");
  ok(panel.includes("workflowRunFeed.subscribe"), "panel subscribes the live feed (WorkflowRunStore 外部源接真数据)");
  ok(panel.includes("playFixtureRun("), "fixture demo path retained (933 三重钉：playFixtureRun 不删)");
  ok(panel.includes("workflowPanel.action.demo"), "demo button stays in the toolbar");
  ok(panel.includes("runs?: WireWorkflowRun[]"), "panel keeps the external runs injection point (test seam)");
  ok(panel.includes("detail?.diagnostic"), "drill-down renders the engine diagnostic row");
  ok(panel.includes("WorkflowCancelRun"), "panel carries the stop-live-run control");

  const feed = read("src/lib/workflowRunFeed.ts");
  ok(feed.includes('"workflow:runs"'), "feed subscribes the runs push channel");
  ok(feed.includes('payload.kind !== "workflow_node"') && feed.includes('"agent:event"'), "feed filters workflow_node off the agent:event bus");
  ok(feed.includes("app.WorkflowRunsSnapshot()"), "feed pulls the full snapshot on first subscribe (拉推合流)");
  ok(feed.includes("mergeRunsSnapshot"), "feed reduces through the pure merge (可测可回放)");

  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("WorkflowRunsSnapshot(): Promise<WireWorkflowRun[]>;"), "bridge declares the snapshot pull binding");
  ok(bridge.includes("WorkflowCancelRun(runId: string): Promise<boolean>;"), "bridge declares the cancel binding");
  ok(bridge.includes("async WorkflowRunsSnapshot()"), "bridge mock implements the pull (browser dev honest empty)");
  ok(bridge.includes("async WorkflowCancelRun()"), "bridge mock implements cancel");

  const dagTypes = read("src/lib/workflowDag.ts");
  ok(dagTypes.includes('"phase" | "confirm"'), "panel DAG kind union opens all five 685 vertex types");

  // Go 链：hub 推送 + App 绑定 + 事件通路既有（eventwire 的 workflow_node）。
  const goHub = readFileSync(`${repoRoot}/desktop/workflow_runs_app.go`, "utf8");
  ok(goHub.includes('"workflow:runs"'), "desktop hub pushes the runs snapshot channel");
  ok(goHub.includes("func (a *App) WorkflowRunsSnapshot()"), "desktop App exposes the snapshot pull binding");
  ok(goHub.includes("func (a *App) WorkflowCancelRun("), "desktop App exposes cancel");
  ok(goHub.includes("workflowRunsCoalesce"), "push coalesces (250ms) instead of per-event spam");
  const lifecycle = readFileSync(`${repoRoot}/desktop/app_lifecycle.go`, "utf8");
  ok(lifecycle.includes("startWorkflowRunsPump"), "hub pump starts in the app lifecycle");
  const wireGo = readFileSync(`${repoRoot}/internal/eventwire/wire.go`, "utf8");
  ok(wireGo.includes('event.WorkflowNode:            "workflow_node"'), "node events ride the existing agent:event wire kind (685 已预拟面，不双投)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
