// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task686-workflow-panel.test.tsx
// 任务 686 验收 harness —— 工作流可视化面板（DAG 流程图 + 运行进展动态点亮，
// 实验室开关）：
//  ① 运行前全图预览：fixture DAG 布局纯函数产出全部节点/边坐标；分层正确
//     （源在第 0 层、依赖层递增）；门控循环的反馈边判为回边且不参与分层。
//  ② 运行中节点状态秒级变化（事件驱动非轮询）：mock 事件流逐条 dispatch，
//     store 快照的节点状态/运行态/进度即时变迁；归约为纯函数（同输入同输出）。
//  ③ 节点详情四类下钻：subagent（指令/结果/确认）、gate（命令+exit+stderr）
//     的详情数据在事件后可取；fixture 节点自带指令/命令静态面。
//  ④ 开关关闭态行为等价（铁律 2）：labFlags 默认 false；palette 插条 flag 关
//     返回同一引用；全链路源码断言（config→setter→渲染表→档位→双视图→
//     bridge→labFlags→谓词→布局→yaml→SettingsPanel→App→三语）。
//  ⑤ 与 685 事件契约对齐的挂点：事件类型集合固定（node-state/run-state/
//     node-detail），未知节点事件不炸（前向兼容）。
//  ⑥ 演示面板保留自证（tl933，用户拍板）：playFixtureRun 完整跑通 + 可中止
//     + 面板演示入口源码接线不删。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  WORKFLOW_FIXTURE_DAG,
  layoutWorkflowDag,
  WORKFLOW_NODE_HEIGHT,
  WORKFLOW_NODE_WIDTH,
} from "../lib/workflowDag";
import { insertWorkflowPanelEntry } from "../lib/workflowPanelEntry";
import {
  applyWorkflowEvent,
  createWorkflowRunStore,
  playFixtureRun,
  workflowRunProgress,
  type WorkflowEvent,
} from "../lib/workflowEvents";
import { applyLabFlags, labFlagEnabled } from "../lib/labFlags";

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

console.log("\ntask 686 workflow panel: DAG preview + event-driven lighting + drill-down + 铁律 2");

// ── ① 运行前全图预览（fixture DAG 布局纯函数）────────────────────────
{
  const dag = WORKFLOW_FIXTURE_DAG;
  const layout = layoutWorkflowDag(dag);
  ok(layout.nodes.length === dag.nodes.length, `layout places every node (${layout.nodes.length}/${dag.nodes.length})`);
  ok(layout.edges.length === dag.edges.length, `layout routes every edge (${layout.edges.length}/${dag.edges.length})`);

  // 分层：plan 是唯一源（第 0 层）；下游层号严格大于其非回边前驱。
  const layer = new Map(layout.nodes.map((n) => [n.id, n.layer]));
  ok(layer.get("plan") === 0, "plan (the only source) sits in layer 0");
  const backKeys = new Set(layout.edges.filter((e) => e.back).map((e) => `${e.from}>${e.to}`));
  ok(backKeys.has("fix-failures>gate-tests"), "the gate-loop feedback edge is classified as a back edge");
  for (const edge of layout.edges) {
    if (edge.back) continue;
    const from = layer.get(edge.from);
    const to = layer.get(edge.to);
    assert.ok(from !== undefined && to !== undefined);
    ok(to > from, `forward edge ${edge.from}→${edge.to} advances a layer (${from}→${to})`);
  }
  // 坐标在画布内且节点互不重叠。
  const boxes = layout.nodes.map((n) => ({ x: n.x, y: n.y }));
  ok(
    boxes.every((b) => b.x >= 0 && b.y >= 0 && b.x + WORKFLOW_NODE_WIDTH <= layout.width && b.y + WORKFLOW_NODE_HEIGHT <= layout.height),
    "every node box sits inside the canvas",
  );
  let overlap = false;
  for (let i = 0; i < layout.nodes.length; i += 1) {
    for (let j = i + 1; j < layout.nodes.length; j += 1) {
      const a = layout.nodes[i];
      const b = layout.nodes[j];
      if (a.x < b.x + WORKFLOW_NODE_WIDTH && b.x < a.x + WORKFLOW_NODE_WIDTH && a.y < b.y + WORKFLOW_NODE_HEIGHT && b.y < a.y + WORKFLOW_NODE_HEIGHT) overlap = true;
    }
  }
  ok(!overlap, "no two node boxes overlap");
  // 布局确定性：同一 dag 两次布局逐字节一致（测试可复现）。
  const again = layoutWorkflowDag(dag);
  ok(JSON.stringify(again) === JSON.stringify(layout), "layout is deterministic (same dag → identical output)");
}

// ── ② 事件驱动的状态变迁（mock 流逐条 dispatch，非轮询）───────────────
{
  const dag = WORKFLOW_FIXTURE_DAG;
  const store = createWorkflowRunStore(dag);
  const initial = store.getSnapshot();
  ok(initial.runState === "planned", "fresh store starts in the planned (plan-view) state");
  ok(Object.values(initial.nodes).every((n) => n.state === "queued"), "every node starts queued (grey)");

  let notified = 0;
  const off = store.subscribe(() => { notified += 1; });

  const stamp = 1_700_000_000_000;
  store.dispatch({ type: "run-state", state: "running", at: stamp });
  store.dispatch({ type: "node-state", nodeId: "plan", state: "running", at: stamp + 1 });
  let snap = store.getSnapshot();
  ok(snap.runState === "running" && snap.startedAt === stamp, "run-state event flips the run to running with startedAt");
  ok(snap.nodes.plan.state === "running", "node-state event lights plan to running immediately");
  ok(notified >= 2, "every applied event notifies subscribers (event-driven, no polling)");

  store.dispatch({ type: "node-detail", nodeId: "plan", at: stamp + 2, detail: { result: "方案全文", tokens: 1840, durationMs: 4200 } });
  store.dispatch({ type: "node-state", nodeId: "plan", state: "done", at: stamp + 3 });
  snap = store.getSnapshot();
  ok(snap.nodes.plan.state === "done", "plan reaches done");
  ok(snap.nodes.plan.detail?.tokens === 1840, "detail event merges into the node run");

  // 进度聚合。
  const progress = workflowRunProgress(snap, stamp + 3);
  ok(progress.total === dag.nodes.length, "progress totals every dag node");
  ok(progress.done === 1 && progress.running === 0, "progress counts plan done");
  ok(progress.tokens === 1840, "progress sums node tokens");
  assert.ok(progress.elapsedMs !== undefined);
  ok(progress.elapsedMs === 3, "elapsed derives from startedAt → event timestamps");

  // unconfirmed 与 failed 两态（685 语义：确认失败保留并标 unconfirmed）。
  store.dispatch({ type: "node-state", nodeId: "confirm", state: "running", at: stamp + 4 });
  store.dispatch({ type: "node-state", nodeId: "confirm", state: "unconfirmed", at: stamp + 5 });
  store.dispatch({ type: "node-state", nodeId: "gate-tests", state: "running", at: stamp + 6 });
  store.dispatch({ type: "node-state", nodeId: "gate-tests", state: "failed", at: stamp + 7 });
  snap = store.getSnapshot();
  const tail = workflowRunProgress(snap, stamp + 7);
  ok(tail.unconfirmed === 1 && tail.failed === 1, "unconfirmed + failed nodes surface in the progress bar");

  // 归约纯函数性：同 (snapshot, event) 输入两次得同一输出。
  const before = store.getSnapshot();
  const ev: WorkflowEvent = { type: "node-state", nodeId: "report", state: "running", at: stamp + 8 };
  const r1 = applyWorkflowEvent(before, ev);
  const r2 = applyWorkflowEvent(before, ev);
  ok(JSON.stringify(r1) === JSON.stringify(r2), "reducer is pure (same input → identical output)");
  ok(before.nodes.report.state === "queued", "reducer does not mutate the previous snapshot");

  // 前向兼容：未知节点事件被忽略而不炸（685 动态节点场景）。
  const untouched = applyWorkflowEvent(before, { type: "node-state", nodeId: "no-such-node", state: "running", at: stamp + 9 });
  ok(untouched === before, "events for unknown nodes are ignored (forward-compatible with 685)");

  // 终态收口。
  store.dispatch({ type: "run-state", state: "completed", at: stamp + 10 });
  snap = store.getSnapshot();
  ok(snap.runState === "completed" && snap.endedAt === stamp + 10, "run-state completed closes the run with endedAt");

  // 重置回到计划视图。
  store.reset(dag);
  ok(store.getSnapshot().runState === "planned", "reset returns the store to the plan view");
  off();
}

// ── ③ 节点详情四类下钻的数据面 ──────────────────────────────────────
{
  const dag = WORKFLOW_FIXTURE_DAG;
  const byId = new Map(dag.nodes.map((n) => [n.id, n]));
  const sub = byId.get("plan");
  ok(Boolean(sub?.instruction && sub.instruction.length > 0), "subagent node carries the full instruction text (drill-down ①)");
  ok(Boolean(sub?.resultSpec), "subagent node carries the typed-result spec (drill-down ②)");
  const gate = byId.get("gate-tests");
  ok(gate?.kind === "gate" && Boolean(gate.command), "gate node carries the literal gate command (drill-down ④)");

  // 事件侧：确认记录（③）与门 exit/stderr（④）经 detail 事件落进 store。
  const store = createWorkflowRunStore(dag);
  store.dispatch({ type: "node-detail", nodeId: "confirm", at: 1, detail: { confirmation: { confirmed: true, evidence: "重现门输出 exit=0" } } });
  store.dispatch({
    type: "node-detail",
    nodeId: "gate-tests",
    at: 2,
    detail: { gate: { command: "go test ./internal/config/...", exitCode: 1, stderr: "--- FAIL: TestRenderTOML" } },
  });
  const snap = store.getSnapshot();
  ok(snap.nodes.confirm.detail?.confirmation?.confirmed === true, "confirmation record lands in the node run (drill-down ③)");
  ok(snap.nodes["gate-tests"].detail?.gate?.exitCode === 1, "gate exit code lands in the node run (drill-down ④)");
  ok(snap.nodes["gate-tests"].detail?.gate?.stderr?.includes("FAIL") === true, "gate stderr lands in the node run (drill-down ④)");
}

// ── ④ 铁律 2 全链路（开关关闭态行为等价）─────────────────────────────
{
  // labFlags 默认 false（新装/未喂值时面板从不挂载、chunk 不加载）。
  applyLabFlags({});
  ok(labFlagEnabled("workflowPanel") === false, "labFlags defaults workflowPanel to off");
  applyLabFlags({ workflowPanel: true });
  ok(labFlagEnabled("workflowPanel") === true, "applyLabFlags feeds the switch from the boot snapshot");
  applyLabFlags({});

  // palette 插条：flag 关返回同一引用（逐字节不变）。
  const cmds = [{ id: "cmd-reload-runtime" }, { id: "cmd-other" }];
  const entry = { id: "cmd-workflow-panel" };
  ok(insertWorkflowPanelEntry(cmds, false, entry) === cmds, "palette entry helper returns the SAME array while off");
  const on = insertWorkflowPanelEntry(cmds, true, entry);
  ok(on.length === 3 && on[1].id === "cmd-workflow-panel", "flag on inserts the entry after the session-wall anchor");

  // Go 链：config 字段 + setter + 渲染表（fixed-key-set）+ 档位（未稳定）。
  const goConfig = readFileSync(`${repoRoot}/internal/config/desktop_preferences.go`, "utf8");
  ok(goConfig.includes('toml:"experimental_workflow_panel"'), "Go config carries the experimental_workflow_panel field");
  const goEdit = readFileSync(`${repoRoot}/internal/config/edit.go`, "utf8");
  ok(goEdit.includes("func (c *Config) SetExperimentalWorkflowPanel(enabled bool) error"), "Go config carries the setter");
  const goRender = readFileSync(`${repoRoot}/internal/config/render.go`, "utf8");
  ok(goRender.includes('experimental_workflow_panel = %v'), "render.go writes the key unconditionally (fixed-key-set rule)");
  ok(goRender.includes('{"workflowPanel", LabTierUnstable, []string{"experimental_workflow_panel"}}'), "labFeatureTiers registers workflowPanel at the unstable tier");

  // desktop 快照层：双视图 + 双映射（81/123 两视图课）+ App setter。
  const settingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
  const settingsAppLines = settingsApp.split("\n").filter((l) => l.includes("ExperimentalWorkflowPanel"));
  ok(settingsAppLines.length >= 4, `both settings views carry the field with both mappings (${settingsAppLines.length} sites)`);
  const settingsPrefs = readFileSync(`${repoRoot}/desktop/settings_preferences.go`, "utf8");
  ok(settingsPrefs.includes("func (a *App) SetExperimentalWorkflowPanel(enabled bool) error"), "App exposes the setter");

  // 前端链：bridge 契约 + mock、types 双视图、谓词、布局（default+yaml 同构）、
  // SettingsPanel pane、App 门控挂载、三语。
  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("SetExperimentalWorkflowPanel(enabled: boolean): Promise<void>;"), "bridge AppBindings declares the setter");
  ok(bridge.includes("async SetExperimentalWorkflowPanel() {}"), "bridge mock implements the setter");
  const startupTypes = read("src/lib/types.ts");
  ok(startupTypes.includes("experimentalWorkflowPanel?: boolean;"), "DesktopStartupSettingsView declares the switch");
  const viewTypes = read("src/lib/settingsViewTypes.ts");
  ok(viewTypes.includes("experimentalWorkflowPanel?: boolean;"), "SettingsView declares the switch");
  const lightKeys = read("src/lab/labLightKeys.ts");
  ok(lightKeys.includes("experimentalWorkflowPanel: (s: SettingsView) => Boolean(s.experimentalWorkflowPanel)"), "lab light predicate reads the switch");
  const layoutDefault = read("src/lab/labLayoutDefault.ts");
  ok(layoutDefault.includes('id: "workflowPanel"') && layoutDefault.includes('onKeys: ["experimentalWorkflowPanel"]'), "built-in lab layout carries the card");
  const layoutYaml = read("src/lab/lab-layout.yaml");
  ok(layoutYaml.includes("- id: workflowPanel") && layoutYaml.includes("onKeys: [experimentalWorkflowPanel]"), "lab-layout.yaml mirrors the card (724 同构)");
  // 预存红修复（tl933）：pane 分支已从 SettingsPanel.tsx 迁至实验室卡片面
  // ExperimentalLabSection.tsx（tl1193b 重构欠账），断言随迁。
  const labSection = read("src/components/ExperimentalLabSection.tsx");
  ok(labSection.includes('selected === "workflowPanel"'), "lab section carries the pane branch");
  ok(labSection.includes('labLabel("workflowPanel"'), "pane row carries exactly one tier badge (621)");
  ok(labSection.includes("await app.SetExperimentalWorkflowPanel(on);"), "pane row writes through the bridge setter");
  const appTsx = read("src/App.tsx");
  ok(appTsx.includes('labFlagEnabled("workflowPanel")'), "App reads the lab flag");
  ok(appTsx.includes("insertWorkflowPanelEntry("), "App inserts the palette entry through the gated helper");
  ok(appTsx.includes("{workflowPanelOpen && ("), "the panel mounts only while open (flag off → never)");
  ok(appTsx.includes("const WorkflowPanel = lazy("), "the panel chunk is lazy (switch off costs the startup bundle nothing)");
  ok(appTsx.includes("<WorkflowPanelBoundary>"), "the panel mounts behind a local error boundary (627 先例)");
  const en = read("src/locales/en.ts");
  const zh = read("src/locales/zh.ts");
  const zhTw = read("src/locales/zh-TW.ts");
  const keys = [
    "settings.workflowPanel", "settings.workflowPanelHint", "settings.workflowPanel.on", "settings.workflowPanel.off",
    "palette.cmd.workflowPanel", "workflowPanel.title", "workflowPanel.run.running", "workflowPanel.progress",
    "workflowPanel.action.demo", "workflowPanel.state.queued", "workflowPanel.kind.gate", "workflowPanel.detail.instruction",
    "workflowPanel.detail.command", "workflowPanel.detail.exitCode", "workflowPanel.detail.confirmation", "workflowPanel.detail.empty",
  ];
  for (const key of keys) {
    ok(en.includes(`"${key}"`) && zh.includes(`"${key}"`) && zhTw.includes(`"${key}"`), `three dialects carry ${key}`);
  }
  // 面板 CSS 与 reduced-motion：脉冲动画有全局归零兜底（31395 通用重置）。
  const styles = read("src/styles.css");
  ok(styles.includes(".wf-panel {") && styles.includes(".wf-node {") && styles.includes(".wf-edge__path {"), "panel CSS defines the workflow classes (class contract)");
  ok(styles.includes("wf-node-pulse"), "running pulse animation exists (collapsed by the global reduced-motion reset)");
}

// ── ⑤ 与 685 事件契约对齐的挂点 ─────────────────────────────────────
{
  // 事件类型集合固定：node-state / run-state / node-detail（685 的
  // 「排队/运行中/完成/unconfirmed/失败/门通过」映射面）；新增类型必须同时
  // 扩本断言——这是 685 schema 落地后做真对齐断言的锚点。
  const store = createWorkflowRunStore(WORKFLOW_FIXTURE_DAG);
  const types = new Set<string>();
  for (const ev of [
    { type: "run-state", state: "running", at: 1 },
    { type: "node-state", nodeId: "plan", state: "running", at: 2 },
    { type: "node-detail", nodeId: "plan", at: 3, detail: { result: "x" } },
  ] as WorkflowEvent[]) {
    types.add(ev.type);
    store.dispatch(ev);
  }
  ok(JSON.stringify([...types].sort()) === JSON.stringify(["node-detail", "node-state", "run-state"]), "event contract surface is exactly {node-state, run-state, node-detail}");
  // 五节点态齐备（685 任务书语义）。
  const states = ["queued", "running", "done", "unconfirmed", "failed"] as const;
  store.dispatch({ type: "node-state", nodeId: "plan", state: "done", at: 4 });
  store.dispatch({ type: "node-state", nodeId: "confirm", state: "unconfirmed", at: 5 });
  store.dispatch({ type: "node-state", nodeId: "gate-tests", state: "failed", at: 6 });
  // scan-deps 保持 running（第 3 条只把 plan 置了 running 后又 done）。
  store.dispatch({ type: "node-state", nodeId: "scan-deps", state: "running", at: 7 });
  const snap = store.getSnapshot();
  ok(states.every((s) => Object.values(snap.nodes).some((n) => n.state === s)), "all five node states are representable in one snapshot");
}

// ── ⑥ 演示面板保留自证（tl933，用户拍板：fixture 演示脚本不删）────────
{
  // 行为面：playFixtureRun 驱动 store 走完整脚本直到 completed，节点逐个点亮。
  // （playFixtureRun 用 window.setTimeout 计时；tsx-runner 无 DOM，测试自备
  // 最小 window 垫片，不改产品代码。）
  const windowShim = globalThis as { window?: { setTimeout: typeof setTimeout; clearTimeout: typeof clearTimeout } };
  windowShim.window = { setTimeout, clearTimeout };
  const dag = WORKFLOW_FIXTURE_DAG;
  const store = createWorkflowRunStore(dag);
  const cancel = playFixtureRun(store, { stepMs: 8 });
  const deadline = Date.now() + 5_000;
  while (store.getSnapshot().runState !== "completed" && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  const finalSnap = store.getSnapshot();
  ok(finalSnap.runState === "completed", "playFixtureRun drives the fixture run to completion (demo preserved)");
  ok(
    dag.nodes.every((n) => finalSnap.nodes[n.id]?.state === "done"),
    "the demo script lights every node to done",
  );
  // 中止面：cancel 后脚本停摆（快照冻结，不再推进）。
  const dag2 = WORKFLOW_FIXTURE_DAG;
  const store2 = createWorkflowRunStore(dag2);
  const cancel2 = playFixtureRun(store2, { stepMs: 8 });
  await new Promise((resolve) => setTimeout(resolve, 60));
  cancel2();
  const frozen = JSON.stringify(store2.getSnapshot());
  await new Promise((resolve) => setTimeout(resolve, 80));
  ok(JSON.stringify(store2.getSnapshot()) === frozen, "cancelling the demo freezes the run (no ghost ticks)");

  // 源码接线面：面板的演示入口仍在（按钮 + 脚本调用），删演示 = 红灯。
  const panelSource = read("src/components/WorkflowPanel.tsx");
  ok(panelSource.includes("playFixtureRun(store)"), "WorkflowPanel keeps wiring playFixtureRun (demo entry retained)");
  ok(panelSource.includes("workflowPanel.action.demo"), "the demo action button stays in the toolbar");
  windowShim.window = undefined;
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
