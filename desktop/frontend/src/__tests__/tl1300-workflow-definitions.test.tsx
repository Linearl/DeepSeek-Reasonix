// Run: LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1300-workflow-definitions.test.tsx
// tl1300 验收 harness —— 工作流定义目录管理（面板「定义」页签）+ /workflow 斜杠命令：
//  ① 纯函数：slug 折叠（与 Go store 同口径）、改名预检（空/无操作/撞键）、卡片计数行；
//  ② 定义视图（真渲染 + jsdom）：列表=store 一致（钉①）、运行通道（preview 披露
//     确认 → runId 回调，钉③通道）、删除二段确认（武装→再击执行，钉④）、改名
//     （预检拦截撞键 + 提交）、查看（只读 canonical JSON）、开关关=只读降级；
//  ③ 面板集成：wf-defs-toggle 可见（读面无独立开关）→ 切页签 → 运行后面板切回
//     流程视图且 run 选择器选中该 runId（钉②：/workflow 与面板同 tab 进度可见
//     ——同一 live feed 同一选择器）；
//  ④ 全链路钉：Go store/service 桥、desktop 桥+mock、commands 注册、control
//     补全+提交面、slash_args 填充、i18n 三目录、三语 locale、CSS。

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
  HTMLInputElement: dom.window.HTMLInputElement,
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
const DOC_AUDIT = 'version: 1\nname: audit\nnodes:\n  - {id: scan, type: subagent, prompt: p}\n';
interface MockTemplate {
  name: string;
  version: string;
  fingerprint: string;
  description?: string;
  nodes: number;
  edges: number;
  gates: number;
  subagents: number;
  confirms: number;
}
let mockTemplates: MockTemplate[] = [
  { name: "audit", version: "v·aaaaaaaaaaaa", fingerprint: "a".repeat(64), description: "nightly audit", nodes: 3, edges: 2, gates: 2, subagents: 1, confirms: 0 },
  { name: "ship", version: "v·bbbbbbbbbbbb", fingerprint: "b".repeat(64), nodes: 2, edges: 1, gates: 0, subagents: 2, confirms: 1 },
];
let previewBlocked = false;
const mockApp = {
  async WorkflowRunsSnapshot() { return []; },
  async WorkflowTemplates() {
    calls.push("list");
    return { templates: mockTemplates, invalid: [] };
  },
  async WorkflowTemplateSource(name: string) {
    calls.push(`source:${name}`);
    return { document: DOC_AUDIT, version: "v·aaaaaaaaaaaa" };
  },
  async WorkflowTemplateRunPreview(name: string) {
    calls.push(`preview:${name}`);
    return {
      name, version: "v·aaaaaaaaaaaa", workspace: "C:/work/repo", blocked: previewBlocked,
      gates: [{ nodeId: "clean", command: "rm -rf build", denied: previewBlocked, reason: previewBlocked ? "denied" : undefined }],
      subagents: [{ nodeId: "scan", type: "subagent", allTools: true, readOnly: false, timeout: "10m" }],
      budget: { tokens: 1000 },
    };
  },
  async WorkflowTemplateRun(name: string) {
    calls.push(`run:${name}`);
    return { runId: "wf-def-run-1", state: "running" };
  },
  async WorkflowTemplateDelete(name: string) {
    calls.push(`delete:${name}`);
    mockTemplates = mockTemplates.filter((tpl) => tpl.name !== name);
  },
  async WorkflowTemplateRename(name: string, newName: string) {
    calls.push(`rename:${name}:${newName}`);
    mockTemplates = mockTemplates.map((tpl) => (tpl.name === name ? { ...tpl, name: newName } : tpl));
  },
  async WorkflowCancelRun() { return false; },
  async WorkflowResumeRun() { return false; },
};
(dom.window as unknown as { go: unknown }).go = { main: { App: mockApp } };

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { applyLabFlags, labFlagEnabled } = await import("../lib/labFlags");
const { workflowSlug, validateRename, definitionCounters } = await import("../lib/workflowTemplates");
const { WorkflowDefinitionsView } = await import("../components/WorkflowDefinitionsView");
const { WorkflowPanel } = await import("../components/WorkflowPanel");

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
async function settle(ms = 0) {
  await act(async () => {
    await flush();
    if (ms) await new Promise((resolve) => setTimeout(resolve, ms));
    await flush();
  });
}
const byTest = (id: string) => document.querySelector(`[data-testid="${id}"]`);
const byTestAll = (id: string) => Array.from(document.querySelectorAll(`[data-testid="${id}"]`));
function setNativeValue(el: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!;
  setter.call(el, value);
  el.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}

console.log("\ntl1300 workflow definitions: 纯函数 + 定义视图 + 面板页签 + /workflow 全链路钉");

// ── ① 纯函数 ─────────────────────────────────────────────────────────
{
  ok(workflowSlug("A B") === "A-B" && workflowSlug("my_flow-2") === "my_flow-2" && workflowSlug("  ") === "unnamed" && workflowSlug("代理 流程") === "unnamed",
    "workflowSlug folds exactly like the Go store slug (same whitelist + dash collapse + unnamed fallback)");
  ok(validateRename("audit", "", ["audit"]) === "empty", "rename validation refuses empty names");
  ok(validateRename("audit", "audit", ["audit"]) === "unchanged", "rename validation treats same-name as a no-op");
  ok(validateRename("audit", "A B", ["audit", "A-B"]) === "taken", "rename validation refuses a slug collision with another definition");
  ok(validateRename("A B", "A-B", ["A B"]) === null, "a display-name rewrite that keeps the same slug is legal");
  ok(validateRename("audit", "audit2", ["audit", "ship"]) === null, "a fresh name passes");
  const counters = definitionCounters({ name: "x", version: "v", fingerprint: "f", nodes: 3, edges: 1, gates: 2, subagents: 1, confirms: 0 });
  ok(counters.length === 4 && counters[0].kind === "nodes" && counters[0].n === 3 && counters[1].kind === "gates" && counters[1].n === 2,
    "definitionCounters projects the node/gate counts the card line shows");
}

// ── ② 定义视图（真渲染）───────────────────────────────────────────────
{
  applyLabFlags({ workflowPanel: true, workflowTemplates: true });
  const host = document.getElementById("root")!;
  const root = createRoot(host);
  let startedRun = "";
  await act(async () => {
    root.render(<LocaleProvider><WorkflowDefinitionsView onRunStarted={(id) => { startedRun = id; }} /></LocaleProvider>);
  });
  await settle();
  const cards = byTestAll("wf-def-card");
  ok(cards.length === 2, "钉① 定义页签列表=store 目录一致：卡片数与 store 清单一致");
  const cardText = cards.map((c) => c.textContent ?? "").join("|");
  ok(cardText.includes("audit") && cardText.includes("nightly audit") && cardText.includes("v·aaaaaaaaaaaa"),
    "each card shows name, description and version");
  ok(cardText.includes("3 nodes") && cardText.includes("2 gates") && cardText.includes("1 sub-agents"),
    "the counts line shows node/gate/subagent counters");

  // 运行通道（钉③通道）：deny 时披露窗禁运行；放行后回调 runId。
  previewBlocked = true;
  await act(async () => { (byTestAll("wf-def-run")[0] as HTMLButtonElement).click(); });
  await settle();
  const confirm = byTest("wf-defs-confirm");
  ok(confirm?.textContent?.includes("rm -rf build") === true && confirm.textContent.includes("Blocked by your permission rules"),
    "the run disclosure lists every gate command and the deny verdict");
  ok((byTest("wf-defs-confirm-run") as HTMLButtonElement).disabled, "a denied command blocks the run");
  await act(async () => { (byTest("wf-defs-confirm-cancel") as HTMLButtonElement).click(); });
  previewBlocked = false;
  await act(async () => { (byTestAll("wf-def-run")[0] as HTMLButtonElement).click(); });
  await settle();
  await act(async () => { (byTest("wf-defs-confirm-run") as HTMLButtonElement).click(); });
  await settle();
  ok(calls.includes("run:audit") && startedRun === "wf-def-run-1",
    "confirming runs the definition through the same panel run channel and hands the runId back");

  // 改名：撞键被预检拦截（两份定义都在场时撞名），合法名提交。
  const renameBtn = byTestAll("wf-def-rename")[0] as HTMLButtonElement;
  await act(async () => { renameBtn.click(); });
  const renameInput = byTest("wf-def-rename-input") as HTMLInputElement;
  ok(renameInput !== null && renameInput.value === "audit", "rename opens an inline input seeded with the current name");
  await act(async () => { setNativeValue(renameInput, "ship"); });
  await settle();
  ok(renameDraftProblemActive(), "a colliding name is refused in place before submit");
  ok((byTest("wf-def-rename-save") as HTMLButtonElement).disabled, "save stays disabled while the rename is invalid");
  await act(async () => { setNativeValue(renameInput, "audit2"); });
  await settle();
  ok((byTest("wf-def-rename-save") as HTMLButtonElement).disabled === false, "a valid name enables save");
  await act(async () => { (byTest("wf-def-rename-save") as HTMLButtonElement).click(); });
  await settle();
  ok(calls.includes("rename:audit:audit2") && byTest("wf-defs-notice")?.textContent?.includes("Renamed to audit2.") === true,
    "the rename submits through the bridge and reports the new name");
  ok(byTestAll("wf-def-card").some((c) => c.textContent?.includes("audit2") === true), "the list reflects the renamed definition");

  // 删除二段确认（钉④）：第一击武装，再击执行。
  const deleteBtn = byTestAll("wf-def-delete").find((b) => (b as HTMLElement).closest('[data-testid="wf-def-card"]')?.textContent?.includes("audit2")) as HTMLButtonElement;
  await act(async () => { deleteBtn.click(); });
  ok(deleteBtn.getAttribute("data-armed") === "true" && deleteBtn.textContent?.includes("Confirm delete?") === true,
    "the first click only arms the delete (two-step confirm) without touching the store");
  ok(!calls.some((c) => c.startsWith("delete:")), "no delete call before the second click");
  await act(async () => { deleteBtn.click(); });
  await settle();
  ok(calls.includes("delete:audit2"), "the second click executes the delete");
  ok(byTest("wf-defs-notice")?.textContent?.includes("Deleted audit2.") === true, "the delete receipt is honest");
  ok(byTestAll("wf-def-card").length === 1, "the list refreshes after the delete (still the store's truth)");

  // 查看：canonical JSON 只读。
  await act(async () => { (byTestAll("wf-def-view")[0] as HTMLButtonElement).click(); });
  await settle();
  ok(byTest("wf-defs-source")?.textContent?.includes("name: audit") === true,
    "View shows the stored definition's canonical JSON read-only");
  await act(async () => { (byTest("wf-defs-source-close") as HTMLButtonElement).click(); });
  ok(byTest("wf-defs-source") === null, "Hide closes the read-only source");

  await act(async () => { root.unmount(); });
  applyLabFlags({});
}
// rename 撞键断言的读取助手（problem 徽标渲染了文案即视为在位）。
function renameDraftProblemActive(): boolean {
  return (byTest("wf-def-rename-problem")?.textContent ?? "").length > 0;
}

// ── ③ 面板集成：定义页签 + 运行后同 tab 进度（钉②）─────────────────────
{
  applyLabFlags({ workflowPanel: true });
  const liveRun = {
    runId: "wf-def-run-1", workflow: "audit", state: "running" as const, startedAt: 1000,
    dag: { id: "audit", name: "audit", nodes: [{ id: "scan", name: "scan", kind: "subagent" as const }], edges: [] },
    nodes: [{ nodeId: "scan", nodeType: "subagent", state: "running" as const, changedAt: 1500 }],
  };
  const host = document.getElementById("root")!;
  const root = createRoot(host);
  await act(async () => {
    root.render(<LocaleProvider><WorkflowPanel open onClose={() => {}} runs={[liveRun]} /></LocaleProvider>);
  });
  await settle(50);
  ok(byTest("wf-defs-toggle") !== null, "the Definitions toggle renders whenever the panel is visible (read face has no extra switch)");
  ok(byTest("wf-defs") === null, "the definitions chunk stays unloaded until the tab is opened");
  await act(async () => { (byTest("wf-defs-toggle") as HTMLButtonElement).click(); });
  await settle(50);
  ok(byTest("wf-defs") !== null, "opening the tab mounts the definitions view");
  // 运行一条定义（写面默认关 → 先开模板开关再点运行）。
  applyLabFlags({ workflowPanel: true, workflowTemplates: true });
  await settle(20);
  await act(async () => { (byTestAll("wf-def-run")[0] as HTMLButtonElement).click(); });
  await settle();
  await act(async () => { (byTest("wf-defs-confirm-run") as HTMLButtonElement).click(); });
  await settle(30);
  ok(byTest("wf-defs") === null, "after a run the panel switches back to the flow view (same tab)");
  const picker = byTest("wf-run-picker") as HTMLSelectElement;
  ok(picker !== null && picker.value === "wf-def-run-1",
    "钉② /workflow 与面板同源：运行后 run 选择器已选中该 run（进度同 tab 可见）");
  ok(Array.from(document.querySelectorAll(".wf-node")).length === 1, "the flow canvas renders the selected run's DAG");
  await act(async () => { root.unmount(); });
  applyLabFlags({});
}

// ── ④ 全链路钉 ───────────────────────────────────────────────────────
{
  const storeGo = readRepo("internal/workflow/templates.go");
  ok(storeGo.includes("func (s *store) deleteFile(name string) error") && storeGo.includes("func (s *store) renameFile(oldName, newName string)"),
    "Go store carries delete/rename (head file + versions dir)");
  const serviceGo = readRepo("internal/workflow/template_service.go");
  ok(serviceGo.includes("func (s *Service) DeleteTemplate(name string) error") && serviceGo.includes("func (s *Service) RenameTemplate(oldName, newName string) error"),
    "Go service carries the gated write faces");
  const bridgeGo = readRepo("desktop/workflow_templates_app.go");
  ok(bridgeGo.includes("func (a *App) WorkflowTemplateDelete(name string) error") && bridgeGo.includes("func (a *App) WorkflowTemplateRename(name, newName string) error"),
    "desktop template bridges exist for delete/rename");
  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("WorkflowTemplateDelete(name: string): Promise<void>;") && bridge.includes("async WorkflowTemplateDelete() {") &&
    bridge.includes("WorkflowTemplateRename(name: string, newName: string): Promise<void>;") && bridge.includes("async WorkflowTemplateRename() {"),
    "bridge declarations + honest preview-build mocks");
  const commands = readRepo("desktop/commands_app.go");
  ok(commands.includes('{Name: "workflow", Description: i18n.M.CmdWorkflow, Hint: "<name>", Kind: "builtin", Group: "actions"}'),
    "/workflow is registered in the composer's slash menu");
  const slash = readRepo("internal/control/slash.go");
  ok(slash.split('case "/workflow":').length >= 3 && slash.includes("func workflowArgItems(prior []string, d ArgData) []SlashItem {") && slash.includes("WorkflowNames []string"),
    "control completes /workflow args and handles the submission");
  const slashArgs = readRepo("desktop/slash_args.go");
  ok(slashArgs.includes('fields[0] == "/workflow"') && slashArgs.includes("data.WorkflowNames"),
    "desktop fills the definition names for /workflow completion (lazy per command)");
  const workflowSlash = readRepo("internal/control/workflow_slash.go");
  ok(workflowSlash.includes("workflow.Global().RunTemplate(") && workflowSlash.includes("func (c *Controller) workflowSlashText("),
    "/workflow runs through the same RunTemplate channel as the panel (SSOT)");
  const i18nGo = readRepo("internal/i18n/messages_en.go");
  const i18nZh = readRepo("internal/i18n/messages_zh.go");
  const i18nTw = readRepo("internal/i18n/messages_zh_tw.go");
  ok(["CmdWorkflow", "SlashWorkflowStartedFmt"].every((k) => i18nGo.includes(k) && i18nZh.includes(k) && i18nTw.includes(k)),
    "all three Go catalogues carry the /workflow strings");
  const panel = read("src/components/WorkflowPanel.tsx");
  ok(panel.includes("LazyDefinitionsView = lazy(") && panel.includes('data-testid="wf-defs-toggle"'),
    "the definitions tab is a lazy chunk toggled inside the panel");
  const en = read("src/locales/en.ts");
  const zh = read("src/locales/zh.ts");
  const zhTw = read("src/locales/zh-TW.ts");
  const keys = ["workflowPanel.definitions.toggle", "workflowPanel.definitions.deleteConfirm", "workflowPanel.definitions.disabledHint",
    "workflowPanel.definitions.count.gates", "workflowPanel.definitions.sourceReadOnly"];
  for (const key of keys) {
    ok(en.includes(`"${key}"`) && zh.includes(`"${key}"`) && zhTw.includes(`"${key}"`), `three dialects carry ${key}`);
  }
  ok(read("src/styles.css").includes(".wf-def-card {"), "the definitions card styles are appended to the stylesheet");
  ok(labFlagEnabled("workflowPanel") === false && labFlagEnabled("workflowTemplates") === false, "no new lab switch was introduced (defaults restored)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
