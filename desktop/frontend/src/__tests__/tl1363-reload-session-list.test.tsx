// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1363-reload-session-list.test.tsx
//
// tl1363 回归钉（手动「重载会话列表」前端出口）：
//   ① 调用序（lib 行为）：点击 → ReloadProjectTreeSessions(scope, root)
//     （global 传空 root）→ 本文件夹权威全量重拉（reloadTopicKeys / 全量
//     reloadAllTopics）；后端失败不阻断重拉且错误可见；老 mock（方法缺失）
//     optional chaining 兜底照常全量重拉；
//   ② 回执 toast（lib 行为）：清出残留时 info toast 报数；零清理零 toast；
//   ③ 合并语义锚（lib 行为）：全量重拉的 replace 合并丢弃不在新页里的驻留行
//     ——影子残行清除的机制落点（1093 家族既有钉保行不回归）；
//   ④ 菜单结构（源码钉）：「重载会话列表」一级项在场，且位于「复制路径」
//     正上方（用户 20261102 拍板位置）；调用链收口 lib/projectTreeReload；
//   ⑤ 绑定面（源码钉）：SessionCatalogBindings 可选方法声明 + 浏览器 dev
//     mock 同形回执（浏览器 dev 面不缺方法）+ 后端回执字段/事件面在场；
//   ⑥ 三语 locale：zh/zh-TW/en 菜单键 + 回执键齐备。

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
const desktopRoot = resolve(frontendRoot, "..");

// ── ① 调用序 + ② 回执 toast（lib 行为，最小桩宿主） ──────────────────────────
console.log("\ntl1363 ①② reload call chain and receipt toast (lib behaviour)");

const reloadMod = await import("../lib/projectTreeReload");
const { projectTreeShouldApplyShellSnapshot, mergeProjectTopicPage } = await import("../lib/projectTreeTopic");

type ReloadCall = { scope: string; workspaceRoot: string };
type RefreshOptions = { reloadTopicKeys?: string[]; reloadAllTopics?: boolean };
function makeHarness(backend: (call: ReloadCall) => Promise<unknown>) {
  const reloadCalls: ReloadCall[] = [];
  const refreshes: RefreshOptions[] = [];
  const toasts: Array<{ text: string; level: string }> = [];
  const harness = {
    reloadCalls, refreshes, toasts,
    async run(scope: string, workspaceRoot: string, folderKey: string) {
      await reloadMod.runProjectTreeReload({
        bindings: {
          async ReloadProjectTreeSessions(scope: string, workspaceRoot: string) {
            reloadCalls.push({ scope, workspaceRoot });
            return await backend({ scope, workspaceRoot });
          },
        },
        scope,
        workspaceRoot,
        folderKey,
        refresh: async (options?: RefreshOptions) => { refreshes.push(options ?? {}); },
        showToast: (text: string, level?: string) => { toasts.push({ text, level: level ?? "info" }); },
        t: ((key: string, vars?: Record<string, string | number>) =>
          `${key}:${vars?.n ?? ""}`) as never,
      });
    },
  };
  return harness;
}

// case A：项目行点击 → 后端按 (project, root) 调用，随后只重拉该文件夹
{
  const harness = makeHarness(() => ({ removedTopicIds: ["topic-shadow"], skippedPinned: 0, skippedReferenced: 0, reconciledDirs: 1 }));
  await harness.run("project", "D:/work/proj", "project_D:/work/proj");
  eq(harness.reloadCalls, [{ scope: "project", workspaceRoot: "D:/work/proj" }], "project click calls the backend with (project, root)");
  eq(harness.refreshes, [{ reloadTopicKeys: ["project_D:/work/proj"], reloadAllTopics: false }], "the clicked folder gets the canonical reload (reloadAllTopics stays false, keys win)");
  eq(harness.toasts, [{ text: "projectTree.reloadSessionsDone:1", level: "info" }], "a sweep receipt toasts the removed count");
}

// case B：Global 行点击 → scope=global、root 传空串（后端 normalizeOrganizationTarget 契约）
{
  const harness = makeHarness(() => ({ removedTopicIds: [], skippedPinned: 0, skippedReferenced: 0, reconciledDirs: 2 }));
  await harness.run("global", "", "global_folder");
  eq(harness.reloadCalls, [{ scope: "global", workspaceRoot: "" }], "global click passes an empty workspace root");
  eq(harness.refreshes, [{ reloadTopicKeys: ["global_folder"], reloadAllTopics: false }], "the global folder gets the canonical reload (reloadAllTopics stays false, keys win)");
  eq(harness.toasts, [], "a clean reload toasts nothing (silent success)");
}

// case C：后端失败 → 错误可见 + 重拉照常（重载语义可重试，残留行不丢失入口）
{
  const harness = makeHarness(async () => { throw new Error("reload backend exploded"); });
  await harness.run("project", "D:/work/proj", "project_key");
  eq(harness.refreshes.length, 1, "a backend failure does NOT block the canonical reload");
  eq(harness.toasts, [{ text: "reload backend exploded", level: "error" }], "the backend failure surfaces as an error toast");
}

// case D：老 mock（方法缺失）→ optional chaining 兜底，重拉照常
{
  const toasts: string[] = [];
  const refreshes: RefreshOptions[] = [];
  await reloadMod.runProjectTreeReload({
    bindings: {},
    scope: "project",
    workspaceRoot: "",
    folderKey: "",
    refresh: async (options?: RefreshOptions) => { refreshes.push(options ?? {}); },
    showToast: (text: string) => { toasts.push(text); },
    t: ((key: string) => key) as never,
  });
  eq(refreshes, [{ reloadAllTopics: true }], "an old mock without the method falls back to a full reload");
  eq(toasts, [], "the fallback path stays silent");
}

// ③ 合并语义锚：全量重拉的 replace 合并（mergeProjectTopicPage 非追加路径）
// 丢弃不在新页里的驻留行——影子残行清除的机制落点（1093 家族既有钉保行不回归）。
const resident = [
  { key: "topic_topic-shadow", kind: "topic", label: "Shadow", topicId: "topic-shadow", children: [] },
  { key: "topic_topic-live", kind: "topic", label: "Live", topicId: "topic-live", children: [] },
];
const merged = mergeProjectTopicPage(resident as never, [{ key: "topic_topic-live", kind: "topic", label: "Live", topicId: "topic-live", children: [] }] as never, false);
eq(merged.map((node: { topicId?: string }) => node.topicId), ["topic-live"], "the replace merge drops resident shadow rows the backend swept");
ok(projectTreeShouldApplyShellSnapshot({ currentRevision: 3, incomingRevision: 3, treeEmpty: false }), "an equal revision still applies (reload re-pull is never revision-blocked)");

// ── ④ 菜单结构（源码钉） ─────────────────────────────────────────────────────
console.log("\ntl1363 ④ menu structure (source pins)");

const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");
ok(treeSource.includes('key: "reload-sessions"'), "the project/global row menu carries the 「重载会话列表」 item");
ok(treeSource.includes('label: t("projectTree.reloadSessions")'), "the menu item reads the localized label");
const reloadItemAt = treeSource.indexOf('key: "reload-sessions"');
const copyItemAt = treeSource.indexOf('key: "copy-path"');
ok(reloadItemAt >= 0 && copyItemAt > reloadItemAt, "「重载会话列表」sits ABOVE 「复制路径」(user-decided position 20261102)");
ok(treeSource.includes("runProjectTreeReload({"), "the menu handler funnels through lib/projectTreeReload (single call chain)");
ok(treeSource.includes('scope === "global" ? "" : projectPath'), "the global row passes an empty workspace root");

// ── ⑤ 绑定面（源码钉） ───────────────────────────────────────────────────────
console.log("\ntl1363 ⑤ binding surface (source pins)");

const bindingsSource = readFileSync(resolve(frontendRoot, "src/lib/sessionCatalogTypes.ts"), "utf8");
ok(bindingsSource.includes("ReloadProjectTreeSessions?(scope: string, workspaceRoot: string): Promise<ProjectTreeReloadResult>;"), "SessionCatalogBindings declares the optional reload method");
ok(bindingsSource.includes("export interface ProjectTreeReloadResult"), "the reload receipt type is declared");
const devMockSource = readFileSync(resolve(frontendRoot, "src/lib/sessionCatalogBridge.ts"), "utf8");
ok(devMockSource.includes("async ReloadProjectTreeSessions"), "the browser-dev mock answers with the same receipt shape");
const backendSource = readFileSync(resolve(desktopRoot, "project_tree_reload.go"), "utf8");
ok(backendSource.includes('json:"removedTopicIds"'), "the Go receipt carries removedTopicIds (the toast count source)");
ok(backendSource.includes('emitProjectTreeChangedV2(revision, nil, "metadata")'), "the reload emits the reason=metadata v2 event the sidebar honors");

// ── ⑥ 三语 locale ────────────────────────────────────────────────────────────
console.log("\ntl1363 ⑥ three locales");

for (const [file, menu, done] of [
  ["zh.ts", "重载会话列表", "已清理 {n} 条残留并重载列表"],
  ["zh-TW.ts", "重載會話列表", "已清理 {n} 條殘留並重載列表"],
  ["en.ts", "Reload session list", "Removed {n} leftover entries and reloaded"],
] as const) {
  const source = readFileSync(resolve(frontendRoot, `src/locales/${file}`), "utf8");
  ok(source.includes(`"projectTree.reloadSessions": "${menu}"`), `${file} carries the menu label`);
  ok(source.includes(`"projectTree.reloadSessionsDone": "${done}"`), `${file} carries the receipt label`);
}

console.log(`\ntl1363: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
