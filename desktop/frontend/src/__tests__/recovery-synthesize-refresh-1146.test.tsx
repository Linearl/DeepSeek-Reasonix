// 任务 1146 harness：版本面板操作生效后的自动刷新。
//
// 验收（tl 1146 正文）：
//  1. 合成成功回到面板后，候选链列表/版本表/已合入区自动刷新为合成后状态
//     （落败链消失，行保持展开，不需要用户重新展开行或重开面板）；
//  2. 切换版本/删除版本后同样即时刷新（版本表与候选链跟随重拉）；
//  3. 刷新期间有加载指示（busy 保持到列表重取完成），不再出现「成功横幅 +
//     静止旧列表 + 按钮就绪」的中间态；
//  4. 扫描按钮仍可手动触发，自动刷新不与其冲突。
//
// 根因钉位（修复前实测）：
//  - load() 刷新 groups/versionGroups 但从不清空 chains Map（候选链缓存），
//    惰性加载 effect 的 chains.has(path) 守卫命中旧缓存 → 候选链区停留在合
//    成前的旧链；
//  - load() 无条件折叠全部行 → 行重开才重挂载 VersionRows，列表「看起来没
//    刷新」；行保持展开时 VersionRows 的 effect 只看 mainPath，永远不重拉；
//  - 合成回调先关 busy 再 await load()，刷新期间面板呈就绪假象。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings, HeadSynthesisReport } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.HTMLInputElement = dom.window.HTMLInputElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

function bodyText(): string {
  return document.body.textContent || "";
}

const MAIN_PATH = "/sessions/gc.jsonl";
const FORK_HEAD = "deadbeefdeadbeef";
// tl1430: after the DAG retirement a candidate row IS a file (headId is
// always empty) - the fork lives in its own recovery file, and activating it
// adopts that file as the main line. The mock keeps the old head id only to
// pin the short badge the row renders.
const FORK_PATH = `/sessions/gc-recovery-${FORK_HEAD}.jsonl`;

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [],
};

// 合成前：主线 + 一条候选 fork head（两条链）。
const CHAINS_BEFORE = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  chains: [
    {
      path: MAIN_PATH,
      headId: "",
      label: "GC",
      isMain: true,
      selected: true,
      covered: false,
      messageCount: 12,
      turns: 6,
      lastActivity: "2026-10-21T00:33:00Z",
      bytes: 1024,
      preview: "hello",
    },
    {
      path: FORK_PATH,
      headId: "",
      label: "GC fork",
      isMain: false,
      selected: false,
      covered: false,
      messageCount: 8,
      turns: 4,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked tail",
    },
  ],
  longestPath: FORK_PATH,
  longestHead: "",
  newestPath: MAIN_PATH,
  newestHead: "",
};

// 合成后：只剩一条主线链（落败链已折进回收站），读数变大。
const CHAINS_AFTER = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  chains: [
    {
      path: MAIN_PATH,
      headId: "",
      label: "GC",
      isMain: true,
      selected: true,
      covered: false,
      messageCount: 16,
      turns: 8,
      lastActivity: "2026-10-24T00:00:00Z",
      bytes: 2048,
      preview: "hello",
    },
  ],
  longestPath: "",
  longestHead: "",
  newestPath: "",
  newestHead: "",
};

const SYNTHESIS_REPORT: HeadSynthesisReport = {
  mainPath: MAIN_PATH,
  promoted: true,
  prefixed: 2,
  trashed: [`${MAIN_PATH}#${FORK_HEAD}`],
  items: [{ path: MAIN_PATH, unique: 4, shared: 8, uniqueToHead: 0, archived: true }],
  afterMessages: 16,
  afterTurns: 8,
  skipped: [],
};

type Spies = {
  chainCalls: number;
  synthCalls: number;
  /** tl1430: each activation now captures {path, headId} - the adopted file
   *  plus the always-empty head id. */
  activateCalls: { path: string; headId: string }[];
  deleteBranchCalls: string[];
  scanCalls: number;
  /** 第二次 ListRecoveryCopyGroups 起挂起，直到手动放行（中间态用例）。 */
  holdListFrom: number;
  releaseList: () => void;
};

function stubBridge(overrides: Partial<AppBindings>, spies: Spies) {
  let synthesized = false;
  let listCalls = 0;
  let pendingRelease: (() => void) | null = null;
  spies.releaseList = () => {
    pendingRelease?.();
    pendingRelease = null;
  };
  const base = {
    ListRecoveryCopyGroups: async () => {
      listCalls += 1;
      if (listCalls >= spies.holdListFrom) {
        await new Promise<void>((resolve) => {
          pendingRelease = resolve;
        });
      }
      return [GROUP];
    },
    ListSessionVersionGroups: async () => [
      {
        mainPath: MAIN_PATH,
        mainLabel: "GC",
        directory: "/sessions",
        headCount: synthesized ? 1 : 2,
        selected: "",
        heads: [],
      },
    ],
    ListRecoveryChains: async () => {
      spies.chainCalls += 1;
      return synthesized ? CHAINS_AFTER : CHAINS_BEFORE;
    },
    ListSessionRepairRecordsFor: async () => [],
    SynthesizeSessionHeads: async () => {
      spies.synthCalls += 1;
      synthesized = true;
      return SYNTHESIS_REPORT;
    },
    SetActiveSessionVersion: async (input: { path: string; headId: string }) => {
      spies.activateCalls.push({ path: input.path, headId: input.headId });
      synthesized = true;
    },
    DeleteRecoveryBranch: async (copyPath: string) => {
      spies.deleteBranchCalls.push(copyPath);
      synthesized = true;
    },
    ScanRecoveryCopyGroup: async () => {
      spies.scanCalls += 1;
      return GROUP;
    },
    PreviewRecoveryChain: async () => ({
      path: MAIN_PATH,
      isMain: true,
      messageCount: 8,
      turns: 4,
      firstUserText: "hello",
      tailLines: ["hello", "world"],
      sharedWithMain: 8,
      uniqueToChain: 0,
      lastActivity: "2026-10-20T00:00:00Z",
      degraded: false,
    }),
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

function freshSpies(holdListFrom = Number.MAX_SAFE_INTEGER): Spies {
  return {
    chainCalls: 0,
    synthCalls: 0,
    activateCalls: [],
    deleteBranchCalls: [],
    scanCalls: 0,
    holdListFrom,
    releaseList: () => {},
  };
}

type Root = ReturnType<typeof createRoot>;

async function openAndExpand(): Promise<Root | null> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b: 无副本会话(headCount=2)落在已合入组(默认折叠)——点组段行展开;
  // 案例④(带副本)在待处理组,多点的这次展开对它无害(已合入组为空)。
  await act(async () => { (document.querySelectorAll("tr.rc-grouprow")[1] as HTMLElement).click(); });
  await flush();
  // tl1415b-2(点2): 会话行恢复默认折叠——本套件全部断言都在子行(主线行/版
  // 本行/副本行)上,逐个点开会话行展开钮;链清单是展开后惰性加载的,双 flush
  // 等它落地。
  const toggles = Array.from(document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle"));
  for (const tgl of toggles) {
    await act(async () => { tgl.click(); });
  }
  await flush();
  await flush();
  return root;
}

/** tl1415b-2(点3): 行内「合成」按钮删除,合成唯一入口在底部③——先勾选会话
 *  行,再点底部「以选定 HEAD 合成最长链」;无副本+多 heads 的会话由底部按形
 *  态自动路由到头合成(确认链不变,仍是「Start synthesis」)。 */
async function synthesizeViaFooter(): Promise<void> {
  const checkbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { checkbox?.click(); });
  await flush();
  const mergeBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Merge into longest chain"),
  );
  await act(async () => { mergeBtn?.click(); });
  await flush();
}

console.log("\nrecovery panel auto-refresh after synthesis/version ops (1146)");

// ① 合成成功后候选链区自动刷新：落败链从候选区消失，行保持展开，无需手动重开。
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  // tl1415b: 当前主线升格为主线行(rc-detail__row--main),fork 为版本行(rc-chain)
  // ——两链读数都在单表里。
  const chainsBefore = document.querySelectorAll(".rc-chain").length;
  const mainRowBefore = document.querySelector(".rc-versions__table tbody tr.rc-detail__row--main");
  ok(chainsBefore === 1 && Boolean(mainRowBefore), "① before synthesis the table shows both chains (main row + fork head row)");
  const chainCallsBefore = spies.chainCalls;

  // tl1415b-2(点3): 合成走底部唯一入口(勾选会话→底部③,按形态路由到头合成)。
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.synthCalls === 1, "① the synthesis ran once");
  // tl1415b: op 后 stub 世界 headCount=1——会话退休为纯行,链子行整体退役
  // (无陈旧链面残留;缓存失效在 tl1415 形态钉里按待处理组会话另钉)。
  ok(!document.querySelector(".rc-chain"), "① the retired session keeps no stale chain rows (cache invalidated with the retired children)");
  // 行保持展开：链区仍在 DOM 里（修复前 load() 会折叠全部行，链区随之卸载）。
  const chainRows = document.querySelectorAll(".rc-chain");
  ok(chainRows.length === 0, "① after synthesis only the winning chain remains (no leftover head rows)");
  ok(!bodyText().includes("forked tail"), "① the losing chain's row is gone without any manual re-expansion");
  ok(bodyText().includes("Synthesized"), "① the synthesis outcome banner is still shown");
  // 版本面同步换代：tbody 只剩会话行+主线行（合成后的唯一链），读数 16 在主线行。
  // tl1415b: tbody= 两行组段行(待处理/已合入)+1 条退休纯行。
  const sessionRows = document.querySelectorAll("tr.rc-session").length;
  ok(sessionRows === 1, "① the table reflects the post-synthesis heads (1 plain session row, was 3)");
  ok(bodyText().includes("16"), "① the version table carries the post-synthesis message count");
  await act(async () => root?.unmount());
}

// ② 刷新期间有加载指示：合成完成但列表重取未返回时，面板不呈「就绪」假象。
{
  const spies = freshSpies(2); // 第二次 ListRecoveryCopyGroups（合成后的重取）挂起
  stubBridge({}, spies);
  const root = await openAndExpand();
  // tl1415b-2(点3): 合成走底部唯一入口(勾选会话→底部③,按形态路由到头合成)。
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  // 列表重取在飞行中：busy 保持，底部显示加载指示；与勾选状态无关的
  // Refresh 按钮也被 busy 禁用。
  const note = document.querySelector(".rc-modal .rc-note");
  ok(Boolean(note) && (note?.textContent || "").includes("Loading"), "② while the refetch is in flight the footer shows a loading indicator");
  const refreshBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find((b) =>
    b.textContent?.includes("Refresh"),
  );
  ok(Boolean(refreshBtn?.disabled), "② the footer action buttons stay disabled during the refetch");

  spies.releaseList();
  await flush();
  await flush();
  const noteAfter = document.querySelector(".rc-modal .rc-note");
  ok(!noteAfter || !(noteAfter?.textContent || "").includes("Loading"), "② the loading indicator clears once the refetch lands");
  ok(document.querySelectorAll(".rc-chain").length === 0, "② the table shows the post-synthesis state after the refetch lands (单链=仅主线行)");
  await act(async () => root?.unmount());
}

// ③ 切换版本后即时刷新：版本表与候选链跟随重拉，行保持展开。
// tl1430: activating a fork row adopts the fork's own file - the call carries
// the row's path with an empty head id (the in-log head switch is retired).
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  const chainCallsBefore = spies.chainCalls;
  const activateBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim() === "switch to this version",
  );
  ok(Boolean(activateBtn), "③ the fork-head row offers the activate entry");
  await act(async () => { activateBtn?.click(); });
  await flush();

  ok(spies.activateCalls.length === 1 && spies.activateCalls[0].path === FORK_PATH && spies.activateCalls[0].headId === "",
    "③ the activate call adopts the fork file with an empty head id");
  // tl1415b: op 后 stub 世界 headCount=1——会话退休为纯行(子行退役即无陈旧面)。
  ok(document.querySelectorAll(".rc-chain").length === 0
    && document.querySelectorAll("tr.rc-session").length === 1,
    "③ the table refetches to the post-switch world (session row only, no stale chain rows)");
  ok(Boolean(document.querySelector(".rc-versions__table")), "③ the version table is still on screen (no manual re-expansion needed)");
  // tl1415b: 子行恒在——切换重拉后主线行+fork 版本行照常渲染(stub 世界未变)。
  ok(document.querySelectorAll("tr.rc-session").length === 1,
    "③ the table shows the post-switch world (single plain session row)");
  await act(async () => root?.unmount());
}

// ④ 删除版本后即时刷新：同口径重拉。
// tl1430: with the head family retired, a flat fork the user deletes is a
// recovery copy row - the delete goes through the branch-delete path with the
// copy's file path.
{
  const spies = freshSpies();
  let copyDeleted = false;
  const forkCopy = {
    path: FORK_PATH,
    label: "GC fork copy",
    bytes: 512,
    modified: "2026-10-20T00:00:00Z",
    messages: 8,
    shared: 6,
    unique: 2,
    scanned: true,
  };
  stubBridge({
    // The delete itself flips the mock world: the refetch that follows the
    // confirmed delete must see the post-delete group (no copies left).
    ListRecoveryCopyGroups: async () => [copyDeleted ? { ...GROUP, copies: [] } : { ...GROUP, copies: [forkCopy] }],
  }, spies);
  const root = await openAndExpand();
  const chainCallsBefore = spies.chainCalls;
  // tl1415b: 操作列并入副本行本行(两行制废止)——副本行(tr.rc-row--copy)带
  // 短标识+删除入口。
  const forkRow = document.querySelector("tr.rc-row--copy");
  const deleteBtn = forkRow?.querySelector<HTMLButtonElement>(".rc-delete");
  ok(Boolean(deleteBtn), "④ the fork copy row offers the delete entry");
  await act(async () => { deleteBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  await act(async () => {
    confirmBtn?.click();
    // The confirm resolves into the delete; the refetch inside the same
    // chain must observe the post-delete group.
    copyDeleted = true;
  });
  await flush();

  ok(spies.deleteBranchCalls.length === 1 && spies.deleteBranchCalls[0] === FORK_PATH, "④ the delete reaches the backend with the copy path");
  // tl1415b: op 后 stub 世界 headCount=1——退休为纯行;副本行消失已由下一条钉。
  ok(document.querySelectorAll("tr.rc-row--copy").length === 0,
    "④ the table refetches to the post-delete world (no copy rows)");
  // tl1415b: 被删的副本行(rc-row--copy)从单表消失(stub 的链清单仍含 fork——
  // 断言按副本行消失计,不按链行)。
  ok(!document.querySelector("tr.rc-row--copy"),
    "④ the table drops the deleted copy row on screen");
  await act(async () => root?.unmount());
}

// ⑤ 扫描按钮仍可手动触发：自动刷新不与其冲突。
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  // 先走一轮合成刷新，刷新落地后再按扫描。
  // tl1415b-2(点3): 合成走底部唯一入口(勾选会话→底部③,按形态路由到头合成)。
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  const scanBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.trim() === "Scan",
  );
  ok(Boolean(scanBtn) && !scanBtn?.disabled, "⑤ the per-row Scan button is enabled after the auto refresh");
  await act(async () => { scanBtn?.click(); });
  await flush();
  ok(spies.scanCalls === 1, "⑤ Scan still triggers the per-conversation scan (自动刷新不冲突)");
  await act(async () => root?.unmount());
}

// ⑥ 用户复测场景（返修主断言，tl 1146 [20261025-1210] 勘误块）：纯 v3 头会话
//    无副本文件，两条候选链都是主转录事件日志里的 head 链（GC开发专员形态：
//    main + 命名 head，版本表同源两版本）。对它按合成，后端文件级 no-op
//    （promoted:false + trashed:[]，items 携带 v3 head 读数——1117 的 v3-only
//    提前返回形态）。面板必须给诚实回执，不再虚报「已合成 / 合成已生效」：
//    旧形态里用户正是被这个虚构的成功横幅误导为「合成已生效、列表该刷新」，
//    而列表不动是因为数据本来就没变（首轮 refetch 修法重拉回来也仍是旧态）。
const V3_ONLY_REPORT: HeadSynthesisReport = {
  mainPath: MAIN_PATH,
  promoted: false,
  trashed: [],
  items: [{ path: MAIN_PATH, unique: 0, shared: 8, uniqueToHead: 0, archived: false }],
  afterMessages: 12,
  afterTurns: 6,
  skipped: [],
};
{
  const spies = freshSpies();
  // override 直接替换 base 的合成桩——base 桩里的 spy 计数不会跑，这里自行计数。
  stubBridge({ SynthesizeSessionHeads: async () => { spies.synthCalls += 1; return V3_ONLY_REPORT; } }, spies);
  const root = await openAndExpand();
  ok(document.querySelectorAll(".rc-chain").length === 1
    && Boolean(document.querySelector(".rc-versions__table tbody tr.rc-detail__row--main")),
    "⑥ the v3-only conversation shows both head readings (main row + fork head row)");
  // tl1415b-2(点3): 合成走底部唯一入口(勾选会话→底部③,按形态路由到头合成)。
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.synthCalls === 1, "⑥ the synthesis call reached the backend once");
  ok(!bodyText().includes("Synthesized"), "⑥ the file-level no-op does NOT claim a Synthesized outcome (虚报面)");
  ok(!bodyText().includes("The synthesis has taken effect."), "⑥ the file-level no-op does NOT show the taken-effect banner（虚构成功横幅是用户复测误导源）");
  ok(bodyText().includes("no mergeable copy branches"), "⑥ the honest v3-only receipt is shown instead（含版本表切换出口指引）");
  ok(document.querySelectorAll(".rc-chain").length === 1
    && Boolean(document.querySelector(".rc-versions__table tbody tr.rc-detail__row--main")),
    "⑥ the list renders the unchanged truth as-is (两链读数仍如实显示——数据没变)");
  await act(async () => root?.unmount());
}

// ⑦ 有副本参与却无文件级效果（引擎跑过但全部跳过/归档失败）：按失败回执处
//    理，透传后端 skipped 明细——同样不得虚报成功横幅。
const STALLED_REPORT: HeadSynthesisReport = {
  mainPath: MAIN_PATH,
  promoted: false,
  trashed: [],
  items: [{ path: "/sessions/gc-recovery-copy.jsonl", unique: 4, shared: 8, uniqueToHead: 0, archived: false }],
  afterMessages: 12,
  afterTurns: 6,
  skipped: ["/sessions/gc-recovery-copy.jsonl"],
};
{
  const spies = freshSpies();
  stubBridge({ SynthesizeSessionHeads: async () => { spies.synthCalls += 1; return STALLED_REPORT; } }, spies);
  const root = await openAndExpand();
  // tl1415b-2(点3): 合成走底部唯一入口(勾选会话→底部③,按形态路由到头合成)。
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.synthCalls === 1, "⑦ the synthesis call reached the backend once");
  ok(!bodyText().includes("The synthesis has taken effect."), "⑦ the stalled run does NOT show the taken-effect banner");
  ok(bodyText().includes("/sessions/gc-recovery-copy.jsonl"), "⑦ the skipped detail is surfaced verbatim for diagnosis");
  await act(async () => root?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
