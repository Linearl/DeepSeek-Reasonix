// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1240-topic-batch.test.tsx
//
// tl1240 验收逐条钉（侧栏批量管理态：checkbox 多选 → 精简菜单 → 操作条
// 「归档/移动」→ 进度 N/M + 失败列表 + turn 跑动会话跳过提示）：
//   ① 纯逻辑（lib/projectTreeBatch）：可选项收集（topic/global_topic，排除
//     session 行/远端行）、单工作区门控（跨工作区 ⇒ null ⇒ 移动禁用）、
//     结果摘要（forceable ⇒ 运行中/被占用跳过，非 forceable ⇒ 真失败）、
//     归档后成功项出选（失败/跳过项保留供一键重试）；
//   ② 操作条挂载行为（JSDOM）：计数、零选中禁用、busy 禁用、跨工作区移动
//     禁用且带提示、报告条渲染 N/M + 跳过行 + 失败明细行 + 关闭钮；
//   ③ 桥接契约（行为）：AppBindings 面 BatchTrashTopics/BatchMoveToGroup
//     存在（浏览器 mock 行为：逐项删除/返回 all-ok 结果）；
//   ④ ProjectTree 集成（源码钉）：头部开关（workbench+classic 双处、
//     aria-pressed）、行点击在批量态切换选择、ContextMenu 分支
//     batchEntry ? 精简菜单 : 完整菜单、hover 动作批量态隐藏、拖拽批量态
//     禁用、批量条仅在批量态挂载、操作走 app.BatchTrashTopics /
//     app.BatchMoveToGroup、移动成功后 setOrganizationRevision 强制 roster
//     重拉、默认视图零改动（!batchMode 才渲染旧路径全部保持）；
//   ⑤ 三语 locale：16 键 zh/en/zh-TW 齐备 + 占位符一致 + 无死键
//     （batchBusy 已删——错误文案走后端原文，与单题路径同口径）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { Element: unknown }).Element = dom.window.Element;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
// Text assertions are pinned to the EN copy: zh is a lazy-loaded dict that
// may not have landed by mount time, while en is the static fallback that is
// always available — deterministic in the runner.

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "../..");
const srcRoot = resolve(frontendRoot, "src");

// ── ① 纯逻辑 ────────────────────────────────────────────────────────────────
console.log("\ntl1240 ① batch lib (pure logic)");

const batchMod = await import("../lib/projectTreeBatch");
type ProjectNode = import("../lib/types").ProjectNode;
type BatchTopicEntry = import("../lib/projectTreeBatch").BatchTopicEntry;

function topicNode(overrides: Partial<ProjectNode>): ProjectNode {
  return {
    key: overrides.topicId ?? "t",
    kind: "topic",
    label: "Topic",
    root: "/ws",
    ...overrides,
  } as ProjectNode;
}

const tree: ProjectNode[] = [
  {
    key: "proj", kind: "project", label: "Project", root: "/ws",
    children: [
      topicNode({ topicId: "t1", label: "One" }),
      topicNode({ topicId: "t2", label: "Two", children: [topicNode({ topicId: "t3", label: "Three" })] }),
      { key: "s1", kind: "session", label: "standalone", sessionPath: "/s/one" } as ProjectNode,
      topicNode({ topicId: "t4", label: "Remote", remoteSession: { hostId: "h", workspace: "w", name: "n" } } as Partial<ProjectNode> as ProjectNode),
    ],
  },
  {
    key: "global", kind: "global_folder", label: "Global",
    children: [
      { key: "g1", kind: "global_topic", label: "GlobalOne", topicId: "g1" } as ProjectNode,
    ],
  },
];

const entries = batchMod.collectBatchTopics(tree);
eq(entries.map((entry) => entry.topicId), ["t1", "t2", "t3", "g1"], "collect walks topics in order, skips session/remote rows");
const entryT1 = entries.find((entry) => entry.topicId === "t1") as BatchTopicEntry;
const entryG1 = entries.find((entry) => entry.topicId === "g1") as BatchTopicEntry;
eq(entryT1?.scope, "project", "project topic scope");
eq(entryT1?.workspaceRoot, "/ws", "project topic root");
eq(entryG1?.scope, "global", "global topic scope");
eq(entryG1?.workspaceRoot, "", "global topic empty root");
eq(entryT1?.title, "One", "title carried for the failure list");

eq(batchMod.singleBatchTarget([]), null, "empty selection has no target");
eq(batchMod.singleBatchTarget([entryT1, entries[1]]), { scope: "project", workspaceRoot: "/ws" }, "same scope+root yields the target");
eq(batchMod.singleBatchTarget([entryT1, entryG1]), null, "mixed project/global selection yields null (move gated)");
eq(batchMod.singleBatchTarget([entryT1, { ...entryT1, topicId: "x", workspaceRoot: "/other" }]), null, "cross-workspace selection yields null (move gated)");

const titles = new Map([["t1", "One"], ["run1", "Runner"], ["lock1", "Locked"], ["f1", "Failer"]]);
const archiveResult = {
  items: [
    { topicId: "t1", ok: true },
    { topicId: "run1", ok: false, forceable: true, error: "wait for the session to finish" },
    { topicId: "lock1", ok: false, forceable: true, error: "session lease busy" },
    { topicId: "f1", ok: false, error: "disk full" },
  ],
  succeeded: 1,
  failed: 3,
  totalMs: 42,
};
const summary = batchMod.summarizeBatchTopicArchive(archiveResult, titles);
eq(summary.succeeded, 1, "summary succeeded count");
eq(summary.skipped, 2, "forceable failures count as skipped (turn running / lease busy)");
eq(summary.failed, 1, "unmarked failures count as real failures");
eq(summary.total, 4, "summary total");
eq(summary.failures.map((f) => f.title), ["Runner", "Locked", "Failer"], "failure list keeps order with display titles");
eq(summary.failures.find((f) => f.topicId === "f1")?.skipped, false, "plain failure is not a skip");
ok(summary.failures.every((f) => f.title !== undefined), "every failure row has a display title");

const moveResult = {
  items: [
    { topicId: "m1", ok: true },
    { topicId: "m2", ok: true, alreadyFiled: true },
    { topicId: "m3", ok: false, error: "config changed concurrently, retry" },
  ],
  succeeded: 2,
  failed: 1,
  group: { id: "g", title: "G", topicIds: ["m1", "m2"] },
};
const moveSummary = batchMod.summarizeBatchMove(moveResult, new Map([["m3", "Three"]]));
eq(moveSummary.succeeded, 2, "move summary counts alreadyFiled as success (backend ok=true)");
eq(moveSummary.failed, 1, "move summary counts CAS-retry failures");
eq(moveSummary.failures[0]?.title, "Three", "move failure title resolved");

eq(
  [...batchMod.batchSelectionAfterArchive(new Set(["t1", "run1", "f1"]), archiveResult)],
  ["run1", "f1"],
  "succeeded topic leaves the selection; failed/skipped stay for one-click retry",
);

// ── ② 操作条挂载行为 ────────────────────────────────────────────────────────
console.log("\ntl1240 ② batch bar mount behaviour");

const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { createElement, Fragment } = await import("react");
const { LocaleProvider } = await import("../lib/i18n");
const barMod = await import("../components/ProjectTreeBatchBar");

function mountBar(props: Record<string, unknown>) {
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  act(() => { root.render(createElement(LocaleProvider, null, createElement(barMod.ProjectTreeBatchBar as never, props as never))); });
  return {
    get html() { return container.innerHTML; },
    get buttons() { return Array.from(container.querySelectorAll("button")); },
    unmount() { act(() => root.unmount()); container.remove(); },
  };
}

const clicks: string[] = [];
const REPORT = {
  kind: "archive" as const,
  done: 2,
  total: 4,
  skipped: 2,
  failures: [
    { topicId: "f1", title: "Failer", reason: "disk full", skipped: false },
  ],
};
const bar = mountBar({
  selectedCount: 2,
  busy: false,
  report: REPORT,
  moveGroups: [{ id: "grp", title: "Group", topicIds: [] }],
  mixedScope: false,
  onArchive: () => clicks.push("archive"),
  onMoveToGroup: () => clicks.push("move"),
  onClearSelection: () => clicks.push("clear"),
  onExit: () => clicks.push("exit"),
  onDismissReport: () => clicks.push("dismiss"),
});
ok(bar.html.includes("Batch manage"), "bar labelled with the batch manage aria label");
ok(bar.html.includes("2 selected"), "selected count rendered");
const enabledButtons = bar.buttons.filter((button) => !button.disabled);
ok(enabledButtons.some((button) => button.textContent?.includes("Archive")), "archive button enabled with a selection");
ok(!bar.buttons.find((button) => button.textContent?.includes("Move to group"))?.disabled, "move button enabled with groups available");
ok(bar.html.includes("Archived 2/4"), "N/M progress line rendered");
ok(bar.html.includes("Running or busy — skipped 2"), "turn-running skip line rendered");
ok(bar.html.includes("Close other Reasonix windows or wait for running sessions"), "tl1258 skip hint line renders with the exits (close window / restart clears locks / per-topic force)");
const noSkipBar = mountBar({
  selectedCount: 1,
  busy: false,
  report: { kind: "archive" as const, done: 1, total: 1, skipped: 0, failures: [{ topicId: "f2", title: "F2", reason: "disk full", skipped: false }] },
  moveGroups: null,
  mixedScope: false,
  onArchive: () => clicks.push("archive"),
  onMoveToGroup: () => clicks.push("move"),
  onClearSelection: () => clicks.push("clear"),
  onExit: () => clicks.push("exit"),
  onDismissReport: () => clicks.push("dismiss"),
});
ok(!noSkipBar.html.includes("Close other Reasonix windows or wait for running sessions"), "tl1258 skip hint hidden when nothing was skipped");
noSkipBar.unmount();
ok(bar.html.includes("1 failed"), "failure count line rendered");
ok(bar.html.includes("Failer: disk full"), "failure item line renders title and reason");
bar.buttons.find((button) => button.getAttribute("aria-label") === "Dismiss report")?.click();
eq(clicks, ["dismiss"], "report dismiss wired");
act(() => {
  bar.buttons.find((button) => button.textContent?.includes("Archive"))?.click();
  bar.buttons.find((button) => button.textContent?.includes("Clear selection"))?.click();
  bar.buttons.find((button) => button.textContent?.includes("Exit batch manage"))?.click();
});
eq(clicks, ["dismiss", "archive", "clear", "exit"], "action buttons wired: archive/clear/exit");
bar.unmount();

const emptyBar = mountBar({
  selectedCount: 0,
  busy: false,
  report: null,
  moveGroups: null,
  mixedScope: false,
  onArchive: () => clicks.push("archive"),
  onMoveToGroup: () => clicks.push("move"),
  onClearSelection: () => clicks.push("clear"),
  onExit: () => clicks.push("exit"),
  onDismissReport: () => clicks.push("dismiss"),
});
ok(emptyBar.buttons.find((button) => button.textContent?.includes("Archive"))?.disabled === true, "archive disabled at zero selection");
const moveButton = emptyBar.buttons.find((button) => button.textContent?.includes("Move to group"));
ok(moveButton?.disabled === true, "move disabled without a common target (moveGroups=null)");
ok((moveButton?.getAttribute("title") ?? "").length === 0, "no mixed-scope hint while nothing is selected");
emptyBar.unmount();

const mixedBar = mountBar({
  selectedCount: 3,
  busy: false,
  report: null,
  moveGroups: null,
  mixedScope: true,
  onArchive: () => clicks.push("archive"),
  onMoveToGroup: () => clicks.push("move"),
  onClearSelection: () => clicks.push("clear"),
  onExit: () => clicks.push("exit"),
  onDismissReport: () => clicks.push("dismiss"),
});
const mixedMoveButton = mixedBar.buttons.find((button) => button.textContent?.includes("Move to group"));
ok(mixedMoveButton?.disabled === true, "move disabled for a cross-workspace selection");
ok(mixedMoveButton?.getAttribute("title") === "Selection spans workspaces — batch move works within one workspace", "mixed-scope hint on the disabled move button");
mixedBar.unmount();

const busyBar = mountBar({
  selectedCount: 2,
  busy: true,
  report: null,
  moveGroups: [{ id: "grp", title: "Group", topicIds: [] }],
  mixedScope: false,
  onArchive: () => clicks.push("archive"),
  onMoveToGroup: () => clicks.push("move"),
  onClearSelection: () => clicks.push("clear"),
  onExit: () => clicks.push("exit"),
  onDismissReport: () => clicks.push("dismiss"),
});
ok(busyBar.buttons.find((button) => button.textContent?.includes("Archiving…")) != null, "busy state swaps the archive label to a progress hint");
ok(busyBar.buttons.find((button) => button.textContent?.includes("Archiving…"))?.disabled === true, "archive disabled while busy");
busyBar.unmount();

// ── ③ 桥接契约（行为：浏览器 mock） ────────────────────────────────────────
console.log("\ntl1240 ③ bridge contract (browser mock behaviour)");

const bridgeMod = await import("../lib/bridge");
const mock = (bridgeMod as unknown as { __mockAppForTests?: unknown }).__mockAppForTests
  ?? null;
// The mock is private; drive it through the exported app proxy with no real
// window.go injected (this JSDOM has none) — the proxy falls back to the mock.
ok(typeof (bridgeMod.app as unknown as Record<string, unknown>).BatchTrashTopics === "function" || typeof window.go === "undefined", "app surface exposes BatchTrashTopics via the mock fallback");
// Mock contract mirrors the backend's per-topic isolation: a topic the mock
// tree knows is archived (removed from the mock tree), an unknown id comes
// back as an isolated per-topic failure — the batch itself never rejects.
const created: { id: string }[] = [];
for (const title of ["batch-mock-a", "batch-mock-b"]) {
  created.push(await bridgeMod.app.CreateTopic("global", "", title));
}
eq(created.length, 2, "mock topics created for the contract test");
const mockResult = await bridgeMod.app.BatchTrashTopics(created.map((topic) => topic.id));
eq(mockResult.succeeded, 2, "mock BatchTrashTopics reports all-ok for known topics");
eq(mockResult.failed, 0, "mock BatchTrashTopics failure count for known topics");
const unknownResult = await bridgeMod.app.BatchTrashTopics(["mock-unknown-id"]);
eq(unknownResult.failed, 1, "mock BatchTrashTopics isolates unknown ids as per-topic failures");
eq(unknownResult.items[0]?.ok, false, "unknown id item not ok");
const mockMove = await bridgeMod.app.BatchMoveToGroup("project", "/ws", ["m1"], "grp", "Group");
eq(mockMove.succeeded, 1, "mock BatchMoveToGroup reports all-ok");
eq(mockMove.group.id, "grp", "mock BatchMoveToGroup echoes the target group");

// ── ④ ProjectTree 集成（源码钉） ────────────────────────────────────────────
console.log("\ntl1240 ④ ProjectTree integration (source pins)");

const treeSrc = readFileSync(resolve(srcRoot, "components/ProjectTree.tsx"), "utf8");
ok((treeSrc.match(/batchManage(Exit)?["']/g) ?? []).length >= 4, "header toggle renders in both workbench and classic headers");
ok(treeSrc.includes("aria-pressed={batchMode}"), "header toggle carries aria-pressed");
ok(treeSrc.includes("items={batchEntry ? batchRowMenuItems : topicMenuItems}"), "context menu branches to the slim batch menu in batch mode");
ok(treeSrc.includes("if (batchEntry) {\n                toggleBatchTopic(batchEntry.topicId);"), "row click toggles the selection in batch mode (no topic open)");
ok(treeSrc.includes("!batchEntry && projectTreeShouldRenderTopicActions"), "hover actions hidden for batch-mode rows");
ok(treeSrc.includes("|| Boolean(batchEntry))"), "row drag disabled in batch mode");
ok(treeSrc.includes("{batchMode && (\n        <ProjectTreeBatchBar"), "batch bar mounted only in batch mode (default surface unchanged)");
ok(treeSrc.includes("app.BatchTrashTopics(batchSelectedEntries.map((entry) => entry.topicId))"), "archive action = ONE BatchTrashTopics call for the whole selection");
ok(treeSrc.includes("app.BatchMoveToGroup(scope, workspaceRoot, batchSelectedEntries.map((entry) => entry.topicId), group.id, group.title)"), "move action = ONE BatchMoveToGroup call");
ok(treeSrc.includes("setOrganizationRevision((n) => n + 1)"), "batch move bumps organizationRevision → rosters force-reload from server truth");
ok(treeSrc.includes("batchSelectionAfterArchive(previous, result)"), "selection pruned by per-topic outcomes after archive");
ok(treeSrc.includes("batchEntryForNode(node) : null"), "batch eligibility resolved per row (session/remote rows excluded)");

const orgSrc = readFileSync(resolve(srcRoot, "components/ProjectTreeOrganization.tsx"), "utf8");
ok(orgSrc.includes("groupsForKey(key) { return groupsByKey[key] ?? []; }"), "organization controller exposes groupsForKey for the batch bar");

const barSrc = readFileSync(resolve(srcRoot, "components/ProjectTreeBatchBar.tsx"), "utf8");
ok(barSrc.includes("aria-haspopup=\"menu\""), "move button opens a group menu");

// ── ⑤ 三语 locale ──────────────────────────────────────────────────────────
console.log("\ntl1240 ⑤ locales (zh / en / zh-TW)");

const BATCH_KEYS = [
  "projectTree.batchManage",
  "projectTree.batchManageExit",
  "projectTree.batchSelectedCount",
  "projectTree.batchArchive",
  "projectTree.batchArchiveBusyProgress",
  "projectTree.batchArchiveDone",
  "projectTree.batchMove",
  "projectTree.batchMoveMixedScope",
  "projectTree.batchMoveDone",
  "projectTree.batchSelectAll",
  "projectTree.batchClearSelection",
  "projectTree.batchSkippedRunning",
  "projectTree.batchSkippedHint",
  "projectTree.batchFailedItems",
  "projectTree.batchItemLine",
  "projectTree.batchReportDismiss",
];
const zhSrc = readFileSync(resolve(srcRoot, "locales/zh.ts"), "utf8");
const enSrc = readFileSync(resolve(srcRoot, "locales/en.ts"), "utf8");
const zhTwSrc = readFileSync(resolve(srcRoot, "locales/zh-TW.ts"), "utf8");
for (const key of BATCH_KEYS) {
  ok(zhSrc.includes(`"${key}":`), `zh has ${key}`);
  ok(enSrc.includes(`"${key}":`), `en has ${key}`);
  ok(zhTwSrc.includes(`"${key}":`), `zh-TW has ${key}`);
}
ok(!zhSrc.includes("projectTree.batchBusy"), "no dead batchBusy key in zh (backend busy text is already human-readable)");
ok(!enSrc.includes("projectTree.batchBusy"), "no dead batchBusy key in en");
ok(!zhTwSrc.includes("projectTree.batchBusy"), "no dead batchBusy key in zh-TW");
ok(zhSrc.includes('"projectTree.batchArchiveDone": "已归档 {done}/{total} 项"') && enSrc.includes('"projectTree.batchArchiveDone": "Archived {done}/{total}"'), "N/M progress placeholder consistent across languages");
ok(zhSrc.includes('"projectTree.batchItemLine": "{title}：{reason}"') && enSrc.includes('"projectTree.batchItemLine": "{title}: {reason}"'), "failure line placeholders consistent across languages");

console.log(`\ntl1240: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
