// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1249-projecttree-force-confirm-dialog.test.tsx
//
// tl1249 验收：app.ConfirmAction Windows 死按钮同类根治（tl1246 同族）。
// 根因：Windows 端 Wails MessageDialog 忽略 Buttons 数组（destructive 落
// MB_OK 单按钮），返回通用值 "Ok"，后端按「返回值==本地化按钮文案」判定恒
// false——ProjectTree 两处调用点的确认链在 Windows 上不可通行：
//   ① trashTopicForceableAny 的强制归档二次确认（TrashTopic 失败 → force）：
//      死路=lease 卡住的会话无法强制回收（功能缺陷）；
//   ② 无 catalog 老 bindings 的分组删除回退分支（827 契约）。
// 修复：两处统一迁 useConfirmDialog（danger tone，与删除分组/解散分组同载体）。
// 本测试：
//   ① 整树挂载行为面——force 二次确认可达且生效（死路复活）：arm → fire →
//      应用内弹层带失败原因 → 确认 → TrashTopicForce 同 id 调用，零 error toast；
//   ② 取消分支：弹层取消 → 零 TrashTopicForce、零 error toast（会话留原位）；
//   ③ 源码钉：两处新接线 + ProjectTree 域 app.ConfirmAction 调用点全量清零
//      扫描 + 三语键齐备且仍被引用（零死键）。

import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: false, media: "", onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
    dispatchEvent: () => false,
  }),
});

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

// React must load AFTER the jsdom globals (canUseDOM pins at module load) —
// same reason the sibling tree tests use dynamic imports.
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTree } = await import("../components/ProjectTree");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");

const FORCEABLE_PREFIX = "reasonix_error:topic_archive_forceable: ";
const FORCEABLE_CAUSE = "wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic";
const FORCE_TOPIC = "t1";

type Call = { name: string; args: unknown[] };

/** Go bridge: shape-critical overrides + an auto no-op fallback so the full
 * ProjectTree mount walks its real startup fan-out (snapshot, openers, catalog,
 * remote, platform) without enumerating every method. TrashTopic fails with the
 * backend-marked forceable error, mirroring markTopicArchiveForceable. */
function makeGoBridge() {
  const calls: Call[] = [];
  const base = {
    async GetProjectTreeSnapshot() {
      return {
        revision: 1,
        projects: [{
          key: "project-/repo", kind: "project", label: "Repo", root: "/repo",
          children: [{ key: `topic:/repo:${FORCE_TOPIC}`, kind: "topic", label: "Lease stuck", root: "/repo", topicId: FORCE_TOPIC }],
        }],
        catalog: { state: "ready", revision: 0, indexed: 0, total: 0, repairPending: 0, repairActive: 0, repairDeferred: 0, repairBlocked: 0 },
      };
    },
    async ListProjectTopics(req: { workspaceRoot: string }) {
      return {
        items: req.workspaceRoot === "/repo"
          ? [{ key: `topic:/repo:${FORCE_TOPIC}`, kind: "topic", label: "Lease stuck", root: "/repo", topicId: FORCE_TOPIC }]
          : [],
        revision: 1, nextCursor: "", complete: true,
      };
    },
    async ExternalOpeners() { return { openers: [], preferred: null }; },
    async GetProjectGroups() { return { groups: [], revision: 1, applied: true }; },
    async GetProjectTreeRuntimeSnapshot() { return { revision: 0, topics: [] }; },
    async ManageSessionsList() { return []; },
    async RemoteConnectionStatuses() { return {}; },
    async Platform() { return "windows"; },
    async TrashTopic(topicId: string) {
      calls.push({ name: "TrashTopic", args: [topicId] });
      throw new Error(FORCEABLE_PREFIX + FORCEABLE_CAUSE);
    },
    async TrashTopicForce(topicId: string) { calls.push({ name: "TrashTopicForce", args: [topicId] }); },
    async ReportFrontendLog() {},
  };
  return {
    calls,
    bridge: new Proxy(base, {
      get(target, prop: string) {
        if (prop in target) return (target as Record<string, unknown>)[prop];
        return async () => undefined;
      },
    }),
  };
}

async function flush() { await new Promise((resolve) => setTimeout(resolve, 0)); }
async function settle(rounds = 6) { for (let i = 0; i < rounds; i += 1) await act(flush); }

let activeRoot: Awaited<ReturnType<typeof createRoot>> | null = null;
let activeCalls: Call[] = [];

/** Mounts the real ProjectTree (classic variant) against the mock bridge and
 * returns the topic row once the shell paints. */
async function mountTree() {
  if (activeRoot) await act(async () => { activeRoot!.unmount(); await flush(); });
  window.localStorage.clear();
  const { calls, bridge } = makeGoBridge();
  activeCalls = calls;
  (dom.window as unknown as { go: unknown }).go = { main: { App: bridge } };
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(
      <StrictMode>
        <LocaleProvider>
          <ToastProvider>
            {/* timeFilter 是显示过滤的必传口径：缺省会把无活动时间的 shell 行全滤掉 */}
            <ProjectTree timeFilter="all" onTimeFilterChange={() => {}} />
          </ToastProvider>
        </LocaleProvider>
      </StrictMode>,
    );
    await flush();
  });
  await settle(4);
  // 快照行是壳：topic 行在项目文件夹展开后才渲染（折叠态只画 pinned shell）。
  const folder = document.querySelector(".project-tree__folder-main");
  if (folder instanceof HTMLElement) {
    await act(async () => { folder.click(); await flush(); });
    await settle(4);
  }
  return [...document.querySelectorAll(".project-tree__topic")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

async function cleanup() {
  if (activeRoot) await act(async () => { activeRoot!.unmount(); await flush(); });
  activeRoot = null;
}

const menuItems = () => [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
  (el): el is HTMLElement => el instanceof HTMLElement,
);
function findByText(items: HTMLElement[], needles: string[]): HTMLElement | undefined {
  return items.find((item) => {
    const text = item.textContent ?? "";
    return needles.some((needle) => text.includes(needle));
  });
}
const dialog = () => document.querySelector(".reasonix-confirm-dialog") as HTMLElement | null;
const dialogMessage = () => document.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "";
const dialogButtons = () => [...document.querySelectorAll(".reasonix-confirm-dialog .modal__actions button")] as HTMLElement[];
const confirmButton = () => dialogButtons().find((b) => b.classList.contains("btn--danger")) ?? null;
const cancelButton = () => dialogButtons().find((b) => !b.classList.contains("btn--danger")) ?? null;
const errorToasts = () => [...document.querySelectorAll(".toast--error")] as HTMLElement[];

/** Opens the topic context menu and walks the two-step trash arm (first click
 * arms — the menu stays open and the label flips; the second click fires the
 * forceable chain). */
async function armAndFireTrash(row: HTMLElement) {
  await act(async () => {
    row.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const trash = findByText(menuItems(), ["Move to trash", "移到回收站"]);
  ok(Boolean(trash), "菜单含「移到回收站」项");
  if (!trash) return false;
  await act(async () => { trash.click(); await flush(); });
  // 重开菜单会重置 arm（openTopicMenu null 掉 confirmArchiveTarget），所以
  // 第二击发生在同一张仍打开的菜单里——label 已翻成确认态。
  const confirmTrash = findByText(menuItems(), ["Confirm move to trash", "确认移入回收站", "移到回收站"]);
  ok(Boolean(confirmTrash), "arm 后菜单项翻为确认态（两段式门还在）");
  if (!confirmTrash) return false;
  await act(async () => { confirmTrash.click(); await flush(); });
  await settle(4);
  return true;
}

// ── ① 死路复活：force 二次确认可达且生效（确认分支） ─────────────────────────
console.log("\ntl1249① force confirm reachable and effective (the Windows dead path revives)");
{
  const rows = await mountTree();
  ok(rows.length > 0, "整树挂载：topic 行渲染");
  const walked = rows.length > 0 && await armAndFireTrash(rows[0]!);
  if (walked) {
    ok(dialog() !== null, "应用内确认对话框弹出（不再依赖原生 ConfirmAction）");
    ok(dialogMessage().includes("wait for the session to finish"), "弹层消息携带后端失败原因（{reason} 注入）");
    ok(dialogMessage().includes("cannot be undone") || dialogMessage().includes("不可恢复"), "弹层消息声明不可恢复");
    const confirm = confirmButton();
    ok(confirm !== null, "确认按钮带 danger tone（与删除分组同视觉口径）");
    ok(errorToasts().length === 0, "弹层阶段零 error toast（forceable 失败由确认链接管）");
    eq(activeCalls.map((c) => c.name), ["TrashTopic"], "确认前只有 TrashTopic（force 待命中）");
    if (confirm) {
      await act(async () => { confirm.click(); await flush(); });
      await settle(6);
    }
    eq(activeCalls.map((c) => c.name), ["TrashTopic", "TrashTopicForce"], "确认后路由进 TrashTopicForce（死路复活）");
    eq(activeCalls[1]?.args, [FORCE_TOPIC], "TrashTopicForce 收到同一 topic id");
    eq(dialog(), null, "确认后对话框关闭");
    ok(errorToasts().length === 0, "整链零 error toast（handled 语义保持）");
  }
  await cleanup();
}

// ── ② 取消分支：零 force、零 error toast（会话留原位） ────────────────────────
console.log("\ntl1249② cancel branch: zero force, zero toast, session stays");
{
  const rows = await mountTree();
  const walked = rows.length > 0 && await armAndFireTrash(rows[0]!);
  if (walked) {
    ok(dialog() !== null, "应用内确认对话框弹出");
    const cancel = cancelButton();
    ok(cancel !== null, "取消按钮在场");
    if (cancel) {
      await act(async () => { cancel.click(); await flush(); });
      await settle(4);
    }
    eq(activeCalls.map((c) => c.name), ["TrashTopic"], "取消后零 TrashTopicForce（会话留在原位）");
    eq(dialog(), null, "取消后对话框关闭");
    ok(errorToasts().length === 0, "取消=已处理语义：零 error toast");
  }
  await cleanup();
}

// ── ③ 源码钉：两处新接线 + app.ConfirmAction 调用点清零扫描 + 三语零死键 ──────
console.log("\ntl1249③ source pins: wiring, call-site zero scan, three-locale live keys");

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = resolve(here, "..");
const treeSource = readFileSync(join(srcRoot, "components/ProjectTree.tsx"), "utf8");

// ① force 归档二次确认的接线
ok(treeSource.includes("const forceArchiveDialog = useConfirmDialog();"), "force 归档确认持有独立 useConfirmDialog 实例");
ok(treeSource.includes("await forceArchiveDialog.confirm({"), "force 确认走 forceArchiveDialog.confirm（应用内弹层）");
ok(treeSource.includes("{forceArchiveDialog.dialog}"), "forceArchiveDialog 的 dialog 元素挂在组件树内（portal 载体）");
ok(treeSource.includes('message: t("projectTree.forceArchiveConfirm", { reason: topicArchiveForceableReason(error) })'), "弹层消息仍携带失败原因（{reason}）");
ok(treeSource.includes("if (!isTopicArchiveForceableError(error)) return false;"), "仅后端标记的失败允许升级 force（分诊门保持）");
ok(treeSource.includes("if (!confirmed) return true;"), "取消=已处理语义保持（零进一步动作、无普通 toast）");
ok(treeSource.includes("await trashTopicForceAny(topicId);"), "确认后仍路由进 TrashTopicForce");
// ② 老 bindings 回退分支的接线
ok(treeSource.includes("await projectGroupDeleteDialog.confirm({"), "分组删除回退分支共用应用内确认对话框实例");
ok(treeSource.includes("<p>{t(\"projectGroup.deleteConfirm\", { title: group.title })}</p>") && treeSource.includes("<p>{t(\"projectGroup.deleteDetail\")}</p>"), "回退分支消息保留 deleteConfirm + deleteDetail（detail 并入 ReactNode）");

// app.ConfirmAction 调用点全量清零扫描（app 源码 = frontend src 去掉
// __tests__——测试钉子/本文件注释会合法地提及该字面量；桥接层声明/mock
// 不用 `app.ConfirmAction(` 形态，天然不在扫描面）。
function collectSourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__") continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...collectSourceFiles(full));
    else if (/\.(tsx?|mts?)$/.test(entry.name)) out.push(full);
  }
  return out;
}
const offenders = collectSourceFiles(srcRoot)
  .map((file) => ({ file, text: readFileSync(file, "utf8") }))
  .filter(({ text }) => text.includes("app.ConfirmAction("));
eq(offenders.map(({ file }) => file), [], "全量扫描：frontend src 零 app.ConfirmAction( 调用点（Windows 死按钮根治清零）");

// 三语键齐备且仍被源码引用（零死键：迁移不产生孤儿键）。
const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
const en = (await import("../locales/en")).en as unknown as Record<string, string>;
const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
const liveKeys = [
  "projectGroup.deleteConfirm",
  "projectGroup.deleteDetail",
  "projectTree.forceArchiveConfirm",
  "projectTree.forceArchiveTopic",
  "history.moveToTrash",
] as const;
for (const key of liveKeys) {
  ok(typeof zh[key] === "string" && zh[key].length > 0, `zh has ${key}`);
  ok(typeof en[key] === "string" && en[key].length > 0, `en has ${key}`);
  ok(typeof zhTw[key] === "string" && zhTw[key].length > 0, `zh-TW has ${key}`);
  ok(treeSource.includes(`"${key}"`), `迁移后 ${key} 仍被 ProjectTree 引用（未成死键）`);
}

process.stdout.write(`\ntl1249-projecttree-force-confirm-dialog: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
