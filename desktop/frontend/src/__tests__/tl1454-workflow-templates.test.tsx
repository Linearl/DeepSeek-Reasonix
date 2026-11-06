// Run: LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1454-workflow-templates.test.tsx
// tl1454 验收 harness —— 用户自定义工作流模板（工作流面板家族子键）：
//  ① 纯函数：原因码/诊断类别 → 人话键、行列 → 光标偏移、在途节点投影、预算读数；
//  ② 关闭态等价：开关默认关；面板（真渲染）无模板钮/失败横幅/预算读数，
//     已终止运行的 running 节点不被投影（默认视图逐字节旧行为）；
//  ③ 开启态：同一运行出现模板钮、失败横幅（人话+技术细节）、预算读数、在途
//     节点投影为未完成；
//  ④ 模板视图（真渲染 + jsdom 受控输入）：新建→定位诊断→点击跳行→修好→
//     保存出版本→历史→运行前确认窗（deny 标红禁运行 / 放行后回调 runId）；
//  ⑤ 全链路钉：Go 配置/渲染/档位家族/desktop 双视图+setter+桥/前端 bridge+
//     mock/types/灯谓词/yaml+default/实验室附属行/App 映射/懒加载/三语。

import * as React from "react";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;
const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");
const readRepo = (rel: string) => readFileSync(`${repoRoot}/${rel}`, "utf8");

// jsdom 必须先于 react-dom 安装（否则受控输入的 onChange 永不触发）。
const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  ResizeObserver: NoopResizeObserver,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// ── 桥 mock（window.go.main.App；bridge 在调用时现读）────────────────────
const calls: string[] = [];
const VALID_DOC = "version: 1\nname: audit\nnodes:\n  - {id: scan, type: subagent, prompt: p}\n";
let savedVersion = "v·aaaaaaaaaaaa";
let previewBlocked = false;
const mockApp = {
  async WorkflowRunsSnapshot() { return []; },
  async WorkflowTemplates() {
    calls.push("list");
    return {
      templates: [{ name: "audit", version: savedVersion, fingerprint: "a".repeat(64), nodes: 1, edges: 0, gates: 0, subagents: 1, confirms: 0 }],
      invalid: [{ file: "future.json", kind: "version", message: "workflow: version 2 unsupported (want 1)", path: "version", line: 2, column: 3 }],
    };
  },
  async WorkflowTemplateSource(name: string) { calls.push(`source:${name}`); return { document: VALID_DOC, version: savedVersion }; },
  async WorkflowTemplateInvalidSource(file: string) { calls.push(`invalid:${file}`); return "{\n  \"version\": 2\n}"; },
  async WorkflowTemplateValidate(doc: string) {
    calls.push("validate");
    if (!doc.includes("prompt")) {
      return { ok: false, diagnostic: { kind: "field", message: "workflow: subagent node \"scan\": prompt is required", path: "nodes[0].prompt", nodeId: "scan", line: 4, column: 5 } };
    }
    return { ok: true, summary: { name: "audit", version: "v·bbbbbbbbbbbb", fingerprint: "b".repeat(64), nodes: 1, edges: 0, gates: 0, subagents: 1, confirms: 0 },
      contracts: [{ id: "scan", type: "subagent", failure: "abort_run", limits: "timeout 10m" }] };
  },
  async WorkflowTemplateSave(doc: string, expected: string) {
    calls.push(`save:${expected}`);
    savedVersion = "v·bbbbbbbbbbbb";
    return { name: "audit", created: expected === "", version: savedVersion, fingerprint: "b".repeat(64) };
  },
  async WorkflowTemplateHistory(name: string) { calls.push(`history:${name}`); return [{ version: "v·aaaaaaaaaaaa", fingerprint: "aaaaaaaaaaaa", savedAt: "2026-11-06T10:00:00Z" }]; },
  async WorkflowTemplateVersionSource(name: string, version: string) { calls.push(`version:${name}:${version}`); return VALID_DOC.replace("prompt: p", "prompt: old"); },
  async WorkflowTemplateRunPreview(name: string) {
    calls.push(`preview:${name}`);
    return {
      name, version: savedVersion, workspace: "C:/work/repo", blocked: previewBlocked,
      gates: [{ nodeId: "clean", command: "rm -rf build", denied: previewBlocked, reason: previewBlocked ? "denied" : undefined }],
      subagents: [{ nodeId: "scan", type: "subagent", allTools: true, readOnly: false, timeout: "10m" }],
      budget: { tokens: 1000 },
    };
  },
  async WorkflowTemplateRun(name: string) { calls.push(`run:${name}`); return { runId: "wf-started", state: "running" }; },
  async ReadWorkflowNodeSubagent() { return []; },
  async WorkflowCancelRun() { return false; },
  async WorkflowResumeRun() { return false; },
};
(dom.window as unknown as { go: unknown }).go = { main: { App: mockApp } };

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { applyLabFlags, labFlagEnabled } = await import("../lib/labFlags");
const { reasonKey, diagKindKey, lineColumnOffset, settleStaleRunning, budgetReadout, contractFailureKey } = await import("../lib/workflowTemplates");
const { WorkflowPanel } = await import("../components/WorkflowPanel");
const { WorkflowTemplatesView } = await import("../components/WorkflowTemplatesView");
type WireWorkflowRun = import("../lib/workflowTypes").WireWorkflowRun;

function setNativeValue(el: HTMLTextAreaElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;
  setter.call(el, value);
  el.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
async function settle(ms = 0) {
  await act(async () => {
    await flush();
    if (ms) await new Promise((resolve) => setTimeout(resolve, ms));
    await flush();
  });
}
const byTest = (id: string) => document.querySelector(`[data-testid="${id}"]`);

console.log("\ntl1454 workflow templates: 关闭态等价 + 模板视图 + 运行附加面 + 全链路钉");

// ── ① 纯函数 ─────────────────────────────────────────────────────────
{
  ok(reasonKey("timeout") === "workflowPanel.reason.timeout" && reasonKey("bogus") === "workflowPanel.reason.internal" && reasonKey(undefined) === "workflowPanel.reason.internal",
    "reasonKey maps every code and never renders blank for an unknown one");
  ok(diagKindKey("version") === "workflowPanel.templates.diagKind.version" && diagKindKey("nope") === "workflowPanel.templates.diagKind.field",
    "diagKindKey maps diagnostic kinds to plain-language keys");
  const text = "ab\ncdef\ng";
  ok(lineColumnOffset(text, 2, 3) === 5 && lineColumnOffset(text, 1, 1) === 0 && lineColumnOffset(text, 9, 1) === text.length && lineColumnOffset(text, 2, 99) === 7,
    "lineColumnOffset resolves 1-based line/column and clamps out-of-range");
  const snap = { runState: "errored" as const, nodes: { a: { state: "running" as const }, b: { state: "done" as const } } };
  const settledRun = settleStaleRunning(snap, "failed");
  ok(settledRun.snapshot.nodes.a.state === "queued" && settledRun.stale.has("a") && settledRun.snapshot.nodes.b.state === "done",
    "settleStaleRunning projects in-flight nodes of a terminated run as not-finished");
  const live = settleStaleRunning(snap, "running");
  ok(live.snapshot === snap && live.stale.size === 0, "a running run is never projected");
  const parts = budgetReadout({ runId: "r", workflow: "w", state: "failed", nodes: [], budget: { tokens: 1000, rounds: 20, wallMs: 1_800_000 }, usedTokens: 1400, usedRequests: 7 });
  ok(parts.length === 3 && parts[0].vars.used === "1.4k" && parts[0].vars.limit === "1.0k" && parts[1].vars.used === "7" && parts[2].vars.limit === "30m",
    "budgetReadout renders tokens/rounds/wall readouts");
  ok(budgetReadout({ runId: "r", workflow: "w", state: "running", nodes: [] }).length === 0, "no budget declared → no readout");
  ok(contractFailureKey("keep_unconfirmed") === "workflowPanel.templates.contract.failure.keep_unconfirmed" && contractFailureKey("nope") === undefined,
    "contract failure codes map to localized keys (unknown code falls back to the raw code)");
}

// ── ②③ 面板真渲染：关闭态等价 vs 开启态附加面 ─────────────────────────
const failedRun: WireWorkflowRun = {
  runId: "wf-t1", workflow: "spend", state: "failed", startedAt: 1000, endedAt: 5000,
  dag: { id: "spend", name: "spend", nodes: [{ id: "a", name: "a", kind: "subagent" }, { id: "b", name: "b", kind: "subagent" }], edges: [] },
  nodes: [
    { nodeId: "a", nodeType: "subagent", state: "failed", changedAt: 4000, reason: "budget", tokens: 1400, requests: 4 },
    { nodeId: "b", nodeType: "subagent", state: "running", changedAt: 3000 },
  ],
  version: "v·0123456789ab", origin: "panel", budget: { tokens: 1000 }, usedTokens: 1400, usedRequests: 4,
  failure: { nodeId: "a", reason: "budget", detail: "workflow budget exceeded: tokens 1400 >= 1000 budget (crossed after node \"a\")" },
};
{
  applyLabFlags({ workflowPanel: true });
  ok(labFlagEnabled("workflowTemplates") === false, "labFlags default the templates switch to off");
  const host = document.getElementById("root")!;
  const root = createRoot(host);
  await act(async () => {
    root.render(<LocaleProvider><WorkflowPanel open onClose={() => {}} runs={[failedRun]} /></LocaleProvider>);
  });
  await settle(50);
  ok(document.querySelector(".wf-panel") !== null, "the panel renders the live run");
  ok(byTest("wf-templates-toggle") === null, "switch off: no Templates toggle in the toolbar");
  ok(byTest("wf-run-failure") === null, "switch off: no failure banner (default view unchanged)");
  const offTokens = Array.from(document.querySelectorAll('[data-testid="wf-progress"] span')).map((el) => el.textContent ?? "");
  ok(offTokens.some((txt) => txt.startsWith("—")), "switch off: the tokens slot keeps its legacy dash for live runs");
  ok(byTest("wf-budget") === null, "switch off: no budget readout");
  const nodeB = Array.from(document.querySelectorAll(".wf-node")).find((n) => n.textContent?.includes("b"));
  ok(nodeB?.className.includes("wf-node--running") === true, "switch off: a stale running node keeps its legacy rendering (no projection)");
  ok(document.querySelector(".wf-tpl") === null, "switch off: the template view never mounts");

  await act(async () => { applyLabFlags({ workflowPanel: true, workflowTemplates: true }); });
  await settle(20);
  ok(byTest("wf-templates-toggle") !== null, "switch on: the Templates toggle appears (live, no restart)");
  const banner = byTest("wf-run-failure");
  ok(banner?.textContent?.includes("Run stopped after node a") === true && banner.textContent.includes("Over budget") && banner.textContent.includes("workflow budget exceeded: tokens 1400"),
    "switch on: failure banner names the crossing node with the plain reason and the technical detail");
  const tokensSlot = Array.from(document.querySelectorAll('[data-testid="wf-progress"] span')).map((el) => el.textContent ?? "");
  ok(tokensSlot.some((txt) => txt.startsWith("1.4k")), "switch on: the existing tokens slot shows the run's real usage");
  ok(byTest("wf-budget")?.textContent?.includes("tokens 1.4k/1.0k") === true, "switch on: budget readout shows used/limit");
  const nodeB2 = Array.from(document.querySelectorAll(".wf-node")).find((n) => n.textContent?.includes("b"));
  ok(nodeB2?.className.includes("wf-node--queued") === true, "switch on: the in-flight node of the terminated run renders as not finished");
  const nodeA = Array.from(document.querySelectorAll(".wf-node")).find((n) => n.textContent?.trim().startsWith("◈a") || n.querySelector(".wf-node__name")?.textContent === "a");
  await act(async () => { (nodeA as HTMLElement | undefined)?.click(); });
  await settle();
  const detailText = document.querySelector(".wf-panel__detail")?.textContent ?? "";
  ok(detailText.includes("Why it stopped") && detailText.includes("Over budget") && detailText.includes("1.4k tokens · 4 requests"),
    "switch on: node detail shows reason (human) + usage");
  await act(async () => { root.unmount(); });
  applyLabFlags({});
}

// ── ④ 模板视图：新建→定位诊断→跳行→修好→保存→历史→确认窗→运行 ─────────
{
  const host = document.getElementById("root")!;
  const root = createRoot(host);
  let startedRun = "";
  await act(async () => {
    root.render(<LocaleProvider><WorkflowTemplatesView onRunStarted={(id) => { startedRun = id; }} /></LocaleProvider>);
  });
  await settle();
  ok(document.querySelectorAll('[data-testid="wf-tpl-item"]').length === 1, "the list shows the valid template");
  const invalid = byTest("wf-tpl-invalid");
  ok(invalid?.textContent?.includes("future.json") === true && invalid.textContent.includes("Invalid"), "invalid files are listed, not hidden");

  await act(async () => { (byTest("wf-tpl-new") as HTMLButtonElement).click(); });
  await settle();
  const editor = byTest("wf-tpl-editor") as HTMLTextAreaElement;
  ok(editor !== null && editor.value.includes("budget:") && editor.value.includes("timeout:"), "New opens the skeleton with budget/timeout examples");

  const broken = "version: 1\nname: audit\nnodes:\n  - id: scan\n    type: subagent\n";
  await act(async () => { setNativeValue(editor, broken); });
  await settle(450);
  const diag = byTest("wf-tpl-diag");
  ok(diag?.textContent?.includes("A field is missing") === true && diag.textContent.includes("Line 4, column 5") && diag.textContent.includes("Node scan") && diag.textContent.includes("prompt is required"),
    "typing an invalid template shows a located diagnostic (human + line/node + technical)");
  ok((byTest("wf-tpl-save") as HTMLButtonElement).disabled, "save is disabled while invalid");
  await act(async () => { (diag as HTMLButtonElement).click(); });
  ok(editor.selectionStart === lineColumnOffset(broken, 4, 5), "clicking the diagnostic moves the caret to the reported line/column");

  await act(async () => { setNativeValue(editor, VALID_DOC); });
  await settle(450);
  ok(byTest("wf-tpl-valid")?.textContent?.includes("v·bbbbbbbbbbbb") === true, "a fixed template validates and previews its version");
  ok(byTest("wf-tpl-contracts")?.textContent?.includes("scan") === true && byTest("wf-tpl-contracts")?.textContent?.includes("An error stops the run") === true,
    "the node contract summary renders, failure semantics localized from the server code");
  await act(async () => { (byTest("wf-tpl-save") as HTMLButtonElement).click(); });
  await settle();
  ok(calls.includes("save:") && byTest("wf-tpl-notice")?.textContent?.includes("Saved as v·bbbbbbbbbbbb") === true, "save creates the template and reports the new version");
  ok(byTest("wf-tpl-version")?.textContent?.includes("v·bbbbbbbbbbbb") === true, "the editor header shows the saved version");

  await act(async () => { (byTest("wf-tpl-history") as HTMLButtonElement).click(); });
  await settle();
  ok(byTest("wf-tpl-history-list")?.textContent?.includes("v·aaaaaaaaaaaa") === true, "history lists the archived version");

  previewBlocked = true;
  await act(async () => { (byTest("wf-tpl-run") as HTMLButtonElement).click(); });
  await settle();
  const confirm = byTest("wf-tpl-confirm");
  ok(confirm?.textContent?.includes("rm -rf build") === true && confirm.textContent.includes("Blocked by your permission rules") && confirm.textContent.includes("All session tools"),
    "the run confirmation discloses every command, the deny verdict and the tool scope");
  ok((byTest("wf-tpl-confirm-run") as HTMLButtonElement).disabled, "a denied command blocks the run");
  await act(async () => { (byTest("wf-tpl-confirm-cancel") as HTMLButtonElement).click(); });
  previewBlocked = false;
  await act(async () => { (byTest("wf-tpl-run") as HTMLButtonElement).click(); });
  await settle();
  await act(async () => { (byTest("wf-tpl-confirm-run") as HTMLButtonElement).click(); });
  await settle();
  ok(calls.includes("run:audit") && startedRun === "wf-started", "confirming runs the stored template and hands the runId back to the panel");
  await act(async () => { root.unmount(); });
}

// ── ⑤ 全链路钉 ───────────────────────────────────────────────────────
{
  ok(readRepo("internal/config/desktop_preferences.go").includes('toml:"experimental_workflow_templates"'), "Go config carries the key");
  const edit = readRepo("internal/config/edit.go");
  ok(edit.includes("func (c *Config) SetExperimentalWorkflowTemplates(enabled bool) error") && edit.includes("func WorkflowTemplatesLive() bool"), "Go setter + live read");
  const render = readRepo("internal/config/render.go");
  ok(render.includes("experimental_workflow_templates = %v") && render.includes('"experimental_workflow_panel", "experimental_workflow_templates"'),
    "render line (fixed-key-set) + workflowPanel family registration (tier unchanged)");
  const settingsApp = readRepo("desktop/settings_app.go");
  ok(settingsApp.split("\n").filter((l) => l.includes("ExperimentalWorkflowTemplates")).length >= 4, "both desktop settings views + both mappings");
  ok(readRepo("desktop/settings_preferences.go").includes("func (a *App) SetExperimentalWorkflowTemplates(enabled bool) error"), "desktop setter");
  const bridgeGo = readRepo("desktop/workflow_templates_app.go");
  ok(["WorkflowTemplates()", "WorkflowTemplateValidate(", "WorkflowTemplateSave(", "WorkflowTemplateRunPreview(", "WorkflowTemplateRun("].every((m) => bridgeGo.includes(`func (a *App) ${m}`)),
    "desktop template bridges exist");
  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("SetExperimentalWorkflowTemplates(enabled: boolean): Promise<void>;") && bridge.includes("async SetExperimentalWorkflowTemplates() {}"), "bridge setter + mock");
  ok(bridge.includes("WorkflowTemplateRun(name: string): Promise<WorkflowTemplateRunStart>;") && bridge.includes("async WorkflowTemplateRun() {"), "bridge run binding + honest mock");
  ok(read("src/lib/types.ts").includes("experimentalWorkflowTemplates?: boolean;") && read("src/lib/settingsViewTypes.ts").includes("experimentalWorkflowTemplates?: boolean;"), "both TS settings views");
  ok(read("src/lab/labLightKeys.ts").includes("experimentalWorkflowTemplates: (s: SettingsView) => Boolean(s.experimentalWorkflowTemplates)"), "lab light predicate");
  ok(read("src/lab/lab-layout.yaml").includes("experimentalWorkflowPanel, experimentalWorkflowTemplates, experimentalCompletionSummary") &&
    read("src/lab/labLayoutDefault.ts").includes('"experimentalWorkflowPanel", "experimentalWorkflowTemplates", "experimentalCompletionSummary"'),
    "card light keys mirrored in yaml + built-in default (724 同构)");
  const lab = read("src/components/ExperimentalLabSection.tsx");
  ok(lab.includes("await app.SetExperimentalWorkflowTemplates(on);") && lab.includes("settings.workflowTemplatesDepNote") && lab.includes("(on && !s.experimentalWorkflowPanel)"),
    "lab sub-row writes its own setter and is gated on the workflow panel");
  const appTsx = read("src/App.tsx");
  ok(appTsx.includes("workflowTemplates: settings.experimentalWorkflowTemplates ?? false"), "App feeds the lab flag from settings");
  const panel = read("src/components/WorkflowPanel.tsx");
  ok(panel.includes('const LazyTemplatesView = lazy(') && panel.includes("{templatesEnabled && ("), "template view is a lazy chunk behind the switch");
  const en = read("src/locales/en.ts");
  const zh = read("src/locales/zh.ts");
  const zhTw = read("src/locales/zh-TW.ts");
  const keys = ["settings.workflowTemplates", "settings.workflowTemplatesHint", "settings.workflowTemplatesDepNote", "workflowPanel.templates.toggle",
    "workflowPanel.templates.confirm.warning", "workflowPanel.reason.timeout", "workflowPanel.reason.budget", "workflowPanel.templates.diagKind.version"];
  for (const key of keys) {
    ok(en.includes(`"${key}"`) && zh.includes(`"${key}"`) && zhTw.includes(`"${key}"`), `three dialects carry ${key}`);
  }
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
