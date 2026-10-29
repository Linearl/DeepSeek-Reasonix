// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1103-topic-menu-force-archive.test.tsx
//
// tl 1103 验收逐条钉（侧栏会话右键菜单整理 + 强制归档合并进回收站失败二次确认）:
//   ① forceable 错误码（lib 行为）：仅带后端稳定前缀的错误识别为可强制，
//     reason 提取去前缀；其他错误不命中；
//   ② 归档失败分诊（controller 挂载行为）：lease-busy 类失败交 onArchiveFailed
//     处理（确认→force、取消→零动作）则不再弹普通错误 toast；其他错误保留
//     普通 toast；两条路径都触发刷新；
//   ③ 菜单结构（源码钉）：一级项 key 顺序 = 需求规格表；「移动到分组」「移出
//     分组」不在一级（收进「分组 ▸」二级，行为回归由 organization-races 既有
//     测试钉）；「强制归档」一级项删除；复制 ▸ 三项取值 = 会话路径/日志路径/
//     会话 ID；打开方式 ▸ 走 ExternalOpeners 发现 + OpenLocalPathInExternalOpener；
//     trash 项确认后走 trashTopicForceableAny（ConfirmAction 二次确认链）；
//   ④ 心跳不受影响（源码钉）：heartbeat.go 的 TrashTopicForce 自动调用点在场；
//   ⑤ 后端编码面（源码钉）：TrashTopic 标记 forceable 家族、错误链 errors.Is
//     保持（行为由 desktop/topic_archive_forceable_test.go 钉）；
//   ⑥ 三语 locale：新增键 zh/en/zh-TW 齐备（含失败原因 + 不可恢复文案键）。

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

const archiveLib = await import("../lib/projectTreeArchive");

// ── ① forceable 错误码（lib 行为） ────────────────────────────────────────────
console.log("\ntl1103 ① forceable error code (lib behaviour)");

const FORCEABLE_SAMPLE = "reasonix_error:topic_archive_forceable: wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic";
ok(archiveLib.isTopicArchiveForceableError(new Error(FORCEABLE_SAMPLE)), "an Error carrying the backend prefix is forceable");
ok(archiveLib.isTopicArchiveForceableError(FORCEABLE_SAMPLE), "a plain string carrying the prefix is forceable (wails may hand back strings)");
ok(!archiveLib.isTopicArchiveForceableError(new Error("session is in use by another Reasonix window or process")), "an unmarked busy text is NOT forceable (backend decides, not substring matching)");
ok(!archiveLib.isTopicArchiveForceableError(new Error("not enough disk space to finish the operation — free some space and retry")), "a disk-full failure is NOT forceable");
ok(!archiveLib.isTopicArchiveForceableError(new Error("prefix spoof: reasonix_error:topic_archive_forceable: inside")), "the prefix must be at the very start (no mid-string hits)");
eq(archiveLib.topicArchiveForceableReason(new Error(FORCEABLE_SAMPLE)), "wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic", "reason strips the machine prefix, keeps the human cause");
eq(archiveLib.topicArchiveForceableReason(new Error("plain failure")), "plain failure", "reason of an unmarked error stays the whole text");

// ── ② 归档失败分诊（controller 挂载行为） ─────────────────────────────────────
console.log("\ntl1103 ② archive failure triage (controller behaviour)");

type Call = { name: string; args: unknown[] };
function makeGoBridge() {
  const calls: Call[] = [];
  const bridge = {
    async TrashTopic(topicId: string) {
      calls.push({ name: "TrashTopic", args: [topicId] });
      // mirrors the backend markTopicArchiveForceable: stable prefix + human cause
      throw new Error("reasonix_error:topic_archive_forceable: wait for the session to finish, answer pending prompts, and stop background jobs before archiving this topic");
    },
    async TrashTopicForce(topicId: string) {
      calls.push({ name: "TrashTopicForce", args: [topicId] });
    },
  };
  return { calls, bridge };
}

const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { createElement } = await import("react");
const controllerMod = await import("../lib/projectTreeArchive");
const { useState } = await import("react");

type HostHandle = {
  trash: (topicId: string, onArchiveFailed?: (error: unknown) => Promise<boolean> | boolean) => Promise<void>;
};
function mountArchiveHost(goBridge: unknown): { handle: HostHandle; toasts: string[]; reloads: number } {
  (dom.window as unknown as { go: unknown }).go = { main: { App: goBridge } };
  const toasts: string[] = [];
  let reloads = 0;
  let captured: HostHandle | null = null;
  const refreshRef = { current: async () => { reloads += 1; } };
  function Host() {
    const [, force] = useState(0);
    const ctl = controllerMod.useProjectTreeArchiveController({
      treeRef: { current: [] },
      topicLoadSeqRef: { current: {} },
      topicPageStateRef: { current: {} },
      updateTopicPageState: () => {},
      refreshRef,
      optimisticallyRemoveTopic: () => {},
      closeMenu: () => {},
      showToast: (text) => { toasts.push(text); force((n) => n + 1); },
    });
    // 捕获最新 controller；trashTopic 带 onArchiveFailed 透传。
    captured = {
      trash: (topicId, onArchiveFailed) => ctl.trashTopic(topicId, onArchiveFailed),
    };
    return null;
  }
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  act(() => { root.render(createElement(Host)); });
  return {
    get handle() { return captured as HostHandle; },
    toasts,
    get reloads() { return reloads; },
  };
}

const FORCE_TOPIC = "topic-force-confirm";
const CANCEL_TOPIC = "topic-force-cancel";
const PLAIN_TOPIC = "topic-plain-failure";

// case A：lease-busy 失败 + 用户在二次确认点了「强制归档」→ 调 TrashTopicForce，不弹普通错误
{
  const { calls, bridge } = makeGoBridge();
  const host = mountArchiveHost(bridge);
  await act(async () => {
    await host.handle.trash(FORCE_TOPIC, async (error) => {
      if (!archiveLib.isTopicArchiveForceableError(error)) return false;
      // 这里模拟 ConfirmAction 弹窗「确认」分支（ProjectTree 侧的接线由 ③ 源码钉）
      await (bridge as { TrashTopicForce: (id: string) => Promise<void> }).TrashTopicForce(FORCE_TOPIC);
      return true;
    });
  });
  eq(calls.map((call) => call.name), ["TrashTopic", "TrashTopicForce"], "confirmed force path: TrashTopic then TrashTopicForce");
  eq(calls[1]?.args, [FORCE_TOPIC], "TrashTopicForce receives the same topic id");
  eq(host.toasts.length, 0, "handled forceable failure raises NO plain error toast");
  ok(host.reloads > 0, "the failed mutation still triggers the recovery reload");
}

// case B：lease-busy 失败 + 用户点「取消」→ 零动作（会话留在原位），不弹普通错误
{
  const { calls, bridge } = makeGoBridge();
  const host = mountArchiveHost(bridge);
  await act(async () => {
    await host.handle.trash(CANCEL_TOPIC, async (error) => {
      if (!archiveLib.isTopicArchiveForceableError(error)) return false;
      return true; // 取消：吞掉失败、不调 force
    });
  });
  eq(calls.map((call) => call.name), ["TrashTopic"], "cancelled confirm does NOT call TrashTopicForce (session stays)");
  eq(host.toasts.length, 0, "cancelled confirm raises NO plain error toast");
}

// case C：其他失败（未标记）→ onArchiveFailed 返回 false → 保留普通错误 toast
{
  const { calls, bridge } = makeGoBridge();
  (bridge as { TrashTopic: (id: string) => Promise<void> }).TrashTopic = async (topicId: string) => {
    calls.push({ name: "TrashTopic", args: [topicId] });
    throw new Error("not enough disk space to finish the operation — free some space and retry");
  };
  const host = mountArchiveHost(bridge);
  await act(async () => {
    await host.handle.trash(PLAIN_TOPIC, async () => false);
  });
  eq(host.toasts.length, 1, "an unmarked failure keeps the plain error toast");
  ok((host.toasts[0] ?? "").includes("disk space"), "the toast carries the backend cause");
  eq(calls.map((call) => call.name), ["TrashTopic"], "an unmarked failure never reaches TrashTopicForce");
}

// ── ③ 菜单结构（源码钉） ─────────────────────────────────────────────────────
console.log("\ntl1103 ③ menu structure (source pins)");

const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");
const appSource = readFileSync(resolve(frontendRoot, "src/App.tsx"), "utf8");

// 一级 key 顺序 = 3.1 规格表：置顶 / 重命名 / AI 重命名 / 标为已读 ── 分组 ▸ ── 复制 ▸ /
// 打开方式 ▸ / 编辑职责 ── 移到回收站。pin 是 classic 变体条件项。
function assertOrdered(haystack: string, needles: Array<[string, string]>, label: string) {
  let cursor = 0;
  for (const [needle, name] of needles) {
    const index = haystack.indexOf(needle, cursor);
    ok(index >= 0, `${label}: ${name} present`);
    if (index < 0) continue;
    ok(index >= cursor, `${label}: ${name} in order`);
    cursor = index;
  }
}
const menuSlice = treeSource.slice(treeSource.indexOf("const topicMenuItems: ContextMenuItem[]"), treeSource.indexOf("if (!isSessionNode && editingTopic === topicId)"));
assertOrdered(menuSlice, [
  ['key: pinned ? "unpin" : "pin"', "pin"],
  ['key: "rename"', "rename"],
  ['key: "aiRename"', "aiRename"],
  ['key: "markAllRead"', "markAllRead"],
  ['key: "ctx-sep-group"', "separator before grouping"],
  ['key: "grouping"', "grouping submenu"],
  ['key: "ctx-sep-copy"', "separator before copy"],
  ['key: "copy-info"', "copy submenu"],
  ['key: "open-with"', "open-with submenu"],
  ['key: "editPurpose"', "editPurpose"],
  ['key: "ctx-sep-trash"', "separator before trash"],
  ['key: "trash"', "trash"],
], "topic menu order");

ok(!menuSlice.includes('key: "forceArchive"'), "acceptance: the force-archive item is gone from the top level");
ok(!treeSource.includes("confirmForceArchiveTarget"), "the two-step force-arm state is gone (confirm dialog replaced it)");
ok(menuSlice.includes('key: "move-to-group"') === false, "「移动到分组」is no longer a TOP-LEVEL row key in ProjectTree (it lives inside the grouping submenu items array)");
// tl 1168: the entry label moved to the action wording 「移动到分组」 (shared projectGroup.moveInto key); wrapping unchanged.
ok(treeSource.includes('label: t("projectGroup.moveInto")') && treeSource.includes("items: organizationItems"), "organization move/remove rows are wrapped into the 「移动到分组 ▸」 submenu (tl 1168 action wording), behaviour unchanged");
ok(treeSource.includes("organizationItems.length > 0"), "the grouping submenu renders only when the workspace has groups");

// 复制 ▸：取值口径 = 会话路径 / 日志路径 / 会话 ID（与 topicbar 复制菜单同键）
ok(treeSource.includes("key: \"copy-session-path\"") && treeSource.includes("copyToClipboard(sessionPath)"), "copy submenu: session path copies node.sessionPath");
ok(treeSource.includes("key: \"copy-log-path\"") && treeSource.includes("copyToClipboard(logDirectory)"), "copy submenu: log path copies the装配层 logDirectory");
ok(treeSource.includes("key: \"copy-session-id\"") && treeSource.includes("copyToClipboard(topicId)"), "copy submenu: session id copies topicId");
ok(treeSource.includes('label: t("topicBar.copySessionPath")') && treeSource.includes('label: t("topicBar.copyLogPath")') && treeSource.includes('label: t("topicBar.copySessionId")'), "copy submenu reuses the topicbar copy-menu labels (zero new keys)");

// 打开方式 ▸：ExternalOpeners 发现 + OpenLocalPathInExternalOpener 调起
ok(treeSource.includes("void app.ExternalOpeners()"), "open-with: discovers via the existing ExternalOpeners bridge (mount-time, cached 15s)");
ok(treeSource.includes("app.OpenLocalPathInExternalOpener(workspaceForOpen, opener.id)"), "open-with: launches via the existing OpenLocalPathInExternalOpener bridge");
ok(treeSource.includes("externalOpener.failed"), "open-with: launch failure shows the existing opener error toast");
ok(treeSource.includes("const workspaceForOpen = openRequest?.workspaceRoot ?? node.root ?? \"\""), "open-with target = the session's own workspace root");
// 打开方式「当前项 ✓」（验收：当前项 ✓）：checked 行 = ExternalOpener 的 selected
// 口径（偏好项，无偏好落第一项），只展示不落盘；ContextMenu 侧以 menuitemradio
// + Check 图标渲染（同 topicbar more-menu 的 check 视觉口径）。
ok(treeSource.includes("checked: opener.id === (externalOpeners?.preferred || externalOpeners?.openers[0]?.id)"), "open-with marks the current opener (preferred, falling back to the first — ExternalOpener's selected semantics)");
const contextMenuSource = readFileSync(resolve(frontendRoot, "src/components/ContextMenu.tsx"), "utf8");
ok(contextMenuSource.includes("checked?: boolean"), "ContextMenu items accept a checked flag");
ok(contextMenuSource.includes('role={item.checked === undefined ? "menuitem" : "menuitemradio"}'), "checked rows opt into menuitemradio + aria-checked");
ok(contextMenuSource.includes("context-menu__check"), "checked rows render the Check glyph");
ok(readFileSync(resolve(frontendRoot, "src/styles.css"), "utf8").includes(".context-menu__check"), "the check glyph carries its own style");

// trash 项确认链：ConfirmAction 二次确认 → trashTopicForceableAny → TrashTopicForce
ok(treeSource.includes("const trashTopicForceableAny"), "trash menu wires the forceable entry");
ok(treeSource.includes('message: t("projectTree.forceArchiveConfirm", { reason: topicArchiveForceableReason(error) })'), "confirm message carries the failure reason ({reason})");
ok(treeSource.includes("if (!isTopicArchiveForceableError(error)) return false;"), "only backend-marked failures may raise the force confirm");
ok(treeSource.includes("if (!confirmed) return true;"), "cancel: handled with zero further action");
ok(treeSource.includes("await trashTopicForceAny(topicId);"), "confirm: routes into TrashTopicForce");
ok(treeSource.includes("void trashTopicForceableAny(topicId)"), "the trash item's second click runs the forceable entry");
ok(/key: "trash",[\s\S]*?danger: true/.test(menuSlice), "trash keeps the danger styling (the only danger row left in the topic menu)");
ok(!/key: "forceArchive"/.test(treeSource), "no second danger row (force) remains");

// 一级项数与全量键账（验收：一级 ≤10 + 17 功能每项可触达）。菜单源码里所有
// `key:` 值构成一个封闭账本：任何新增一级行、或把二级叶误提到一级，都会撞红。
// 账本口径（顶层功能行 9 = 置顶（条件项，key 由三元表达式给出，不入正则捕获）/
// 重命名 / AI 重命名 / 标为已读 / 分组 ▸ / 复制 ▸ / 打开方式 ▸ / 编辑职责 / 移到回收站，
// 3 条分隔线不计行数；捕获集另含复制 ▸ 的 3 个二级叶与打开方式 ▸ 的模板键）。
// 17 功能账：置顶① 重命名② AI重命名③ 标为已读④ 移动到分组⑤ 移出分组⑥（⑤⑥由
// organizationItems 承载、行为回归由 organization-races 钉）复制路径⑦ 日志⑧
// 会话ID⑨ 打开方式×5⑩-⑪⑫⑬⑭（同一发现列表派生）编辑职责⑮ 回收站⑯
// 强制归档（二次确认可触达）⑰——上方断言逐项覆盖。
const menuKeys = [...menuSlice.matchAll(/key: ("[^"]+"|`[^`]+`)/g)].map((match) => match[1]);
eq(new Set(menuKeys).size, menuKeys.length, "no duplicate keys in the topic menu ledger");
// default sort keeps double-quoted keys ahead of the backtick template key.
eq(menuKeys.slice().sort(), [
  '"aiRename"',
  '"copy-info"',
  '"copy-log-path"',
  '"copy-session-id"',
  '"copy-session-path"',
  '"ctx-sep-copy"',
  '"ctx-sep-group"',
  '"ctx-sep-trash"',
  '"editPurpose"',
  '"grouping"',
  '"markAllRead"',
  '"open-with"',
  '"rename"',
  '"trash"',
  "`open:${opener.id}`",
], "the topic menu's closed key ledger (a new level-1 row or a wrongly promoted leaf breaks this)");
// 顶层功能行 = 双引号键 − 3 条分隔线 − 复制 ▸ 的 3 个二级叶，再加条件项置顶行。
const nestedLeafKeys = new Set(['"copy-session-path"', '"copy-log-path"', '"copy-session-id"']);
const topLevelRows = menuKeys.filter((key) => key.startsWith('"') && !key.includes("sep-") && !nestedLeafKeys.has(key)).length + 1;
ok(topLevelRows <= 10, `acceptance: level-1 stays within 10 rows (got ${topLevelRows})`);
eq(topLevelRows, 9, "acceptance: level-1 functional rows = 9 exactly (the spec table)");

// App 装配：logDirectory prop 注入
ok(appSource.includes("logDirectory={topicbarLogDirectory}"), "App wires the assembly-layer log directory into ProjectTree");

// ── ④ 心跳不受影响（源码钉） ─────────────────────────────────────────────────
console.log("\ntl1103 ④ heartbeat force call untouched (source pin)");

const heartbeatSource = readFileSync(resolve(desktopRoot, "heartbeat.go"), "utf8");
ok(/TrashTopicForce\(/.test(heartbeatSource), "heartbeat.go still calls TrashTopicForce (the automatic empty-shell cleanup must survive)");

// ── ⑤ 后端编码面（源码钉；行为在 Go 测试） ────────────────────────────────────
console.log("\ntl1103 ⑤ backend marking surface (source pin)");

const goArchiveSource = readFileSync(resolve(desktopRoot, "topic_archive.go"), "utf8");
ok(goArchiveSource.includes('topicArchiveForceablePrefix = "reasonix_error:topic_archive_forceable: "'), "TrashTopic marks forceable failures with the stable machine prefix");
// tl 1201 extended the family with the zombie classification (dead holder pid):
// a crashed Reasonix process cannot be closed by hand, so force is the repair.
// Pin stays verbatim so any future family change forces a conscious update.
ok(goArchiveSource.includes("errors.Is(err, errTopicHasActiveWork) || errors.Is(err, errSessionBusyElsewhere) || errors.Is(err, errSessionBusyZombie) || errors.Is(err, agent.ErrSessionLeaseHeld)"), "exactly the force-clearable family is marked (active-work gate + lease-busy + zombie + lease-held)");
ok(goArchiveSource.includes("func (e *topicArchiveForceableError) Unwrap() error { return e.cause }"), "the marked error keeps its chain so Go tests' errors.Is keep passing");
ok(/func \(a \*App\) TrashTopic\(topicID string\) error \{\s*\n\treturn markTopicArchiveForceable\(friendlySessionFileError/.test(goArchiveSource), "TrashTopic wraps through the marker");
ok(/func \(a \*App\) TrashTopicForce\(topicID string\) error \{\s*\n\treturn friendlySessionFileError/.test(goArchiveSource), "TrashTopicForce itself stays unmarked (it is already the fallback)");

// ── ⑥ 三语 locale ────────────────────────────────────────────────────────────
console.log("\ntl1103 ⑥ three-locale keys");

const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
const en = (await import("../locales/en")).en as unknown as Record<string, string>;
const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
const newKeys = [
  "projectTree.forceArchiveConfirm",
  "projectGroup.moveInto",
  "projectTree.copyInfo",
  "projectTree.openWith",
  "projectTree.forceArchiveTopic",
  "topicBar.copySessionPath",
  "topicBar.copyLogPath",
  "topicBar.copySessionId",
  "common.cancel",
  "msg.copied",
  "history.moveToTrash",
] as const;
for (const key of newKeys) {
  ok(typeof zh[key] === "string" && zh[key].length > 0, `zh has ${key}`);
  ok(typeof en[key] === "string" && en[key].length > 0, `en has ${key}`);
  ok(typeof zhTw[key] === "string" && zhTw[key].length > 0, `zh-TW has ${key}`);
}
const confirmZh = zh["projectTree.forceArchiveConfirm"] ?? "";
ok(confirmZh.includes("{reason}"), "zh confirm message carries the {reason} placeholder (失败原因)");
ok(confirmZh.includes("不可恢复"), "zh confirm message says 不可恢复");
const confirmEn = en["projectTree.forceArchiveConfirm"] ?? "";
ok(confirmEn.includes("cannot be undone"), "en confirm message says it cannot be undone");
ok(!zh["history.confirmForceArchive"], "the dead two-step label key is removed from zh");

console.log(`\ntl1103: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
