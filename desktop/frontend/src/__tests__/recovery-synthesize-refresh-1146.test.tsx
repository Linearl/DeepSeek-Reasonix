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
      path: MAIN_PATH,
      headId: FORK_HEAD,
      label: "GC fork",
      isMain: true,
      selected: false,
      covered: false,
      messageCount: 8,
      turns: 4,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked tail",
    },
  ],
  longestPath: MAIN_PATH,
  longestHead: FORK_HEAD,
  newestPath: MAIN_PATH,
  newestHead: FORK_HEAD,
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
  activateCalls: string[];
  deleteHeadCalls: string[];
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
    SetActiveSessionVersion: async (input: { headId: string }) => {
      spies.activateCalls.push(input.headId);
      synthesized = true;
    },
    DeleteSessionVersionHead: async (_mainPath: string, headId: string) => {
      spies.deleteHeadCalls.push(headId);
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
    DeleteRecoveryBranch: async () => {},
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
    deleteHeadCalls: [],
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
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  return root;
}

console.log("\nrecovery panel auto-refresh after synthesis/version ops (1146)");

// ① 合成成功后候选链区自动刷新：落败链从候选区消失，行保持展开，无需手动重开。
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  const chainsBefore = document.querySelectorAll(".rc-chain").length;
  ok(chainsBefore === 2, "① before synthesis the candidate block shows both chains");
  const chainCallsBefore = spies.chainCalls;

  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.synthCalls === 1, "① the synthesis ran once");
  ok(spies.chainCalls > chainCallsBefore, "① the chain inventory is refetched after synthesis (cache invalidated)");
  // 行保持展开：链区仍在 DOM 里（修复前 load() 会折叠全部行，链区随之卸载）。
  const chainRows = document.querySelectorAll(".rc-chain");
  ok(chainRows.length === 1, "① after synthesis only the winning chain remains in the candidate block");
  ok(!bodyText().includes("forked tail"), "① the losing chain's row is gone without any manual re-expansion");
  ok(bodyText().includes("Synthesized"), "① the synthesis outcome banner is still shown");
  // 版本表同步换代：heads 表只剩一行（合成后的主线），读数是合成后的。
  const versionRows = document.querySelectorAll(".rc-versions__table tbody tr").length;
  ok(versionRows === 1, "① the version table reflects the post-synthesis heads (1 row, was 2)");
  ok(bodyText().includes("16"), "① the version table carries the post-synthesis message count");
  await act(async () => root?.unmount());
}

// ② 刷新期间有加载指示：合成完成但列表重取未返回时，面板不呈「就绪」假象。
{
  const spies = freshSpies(2); // 第二次 ListRecoveryCopyGroups（合成后的重取）挂起
  stubBridge({}, spies);
  const root = await openAndExpand();
  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
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
  ok(document.querySelectorAll(".rc-chain").length === 1, "② the candidate block shows the post-synthesis state after the refetch lands");
  await act(async () => root?.unmount());
}

// ③ 切换版本后即时刷新：版本表与候选链跟随重拉，行保持展开。
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

  ok(spies.activateCalls.length === 1 && spies.activateCalls[0] === FORK_HEAD, "③ the activate call carries the fork head id");
  ok(spies.chainCalls > chainCallsBefore, "③ the version table refetches immediately after switching (行保持展开也重拉)");
  ok(Boolean(document.querySelector(".rc-versions__table")), "③ the version table is still on screen (no manual re-expansion needed)");
  ok(document.querySelectorAll(".rc-versions__table tbody tr").length === 1, "③ the version table shows the post-switch heads");
  await act(async () => root?.unmount());
}

// ④ 删除版本后即时刷新：同口径重拉。
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  const chainCallsBefore = spies.chainCalls;
  const forkRow = Array.from(document.querySelectorAll<HTMLTableRowElement>(".rc-versions__table tbody tr")).find((r) =>
    r.textContent?.includes(FORK_HEAD.slice(0, 8)),
  );
  const deleteBtn = forkRow?.querySelector<HTMLButtonElement>(".rc-delete");
  ok(Boolean(deleteBtn), "④ the fork-head row offers the delete entry");
  await act(async () => { deleteBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Delete");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.deleteHeadCalls.length === 1 && spies.deleteHeadCalls[0] === FORK_HEAD, "④ the delete reaches the backend with the head id");
  ok(spies.chainCalls > chainCallsBefore, "④ the version table refetches immediately after deleting");
  ok(document.querySelectorAll(".rc-versions__table tbody tr").length === 1, "④ the version table drops the deleted head on screen");
  await act(async () => root?.unmount());
}

// ⑤ 扫描按钮仍可手动触发：自动刷新不与其冲突。
{
  const spies = freshSpies();
  stubBridge({}, spies);
  const root = await openAndExpand();
  // 先走一轮合成刷新，刷新落地后再按扫描。
  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
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
  ok(document.querySelectorAll(".rc-chain").length === 2, "⑥ the v3-only conversation shows both head chains before the attempt");
  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.synthCalls === 1, "⑥ the synthesis call reached the backend once");
  ok(!bodyText().includes("Synthesized"), "⑥ the file-level no-op does NOT claim a Synthesized outcome (虚报面)");
  ok(!bodyText().includes("The synthesis has taken effect."), "⑥ the file-level no-op does NOT show the taken-effect banner（虚构成功横幅是用户复测误导源）");
  ok(bodyText().includes("no mergeable copy branches"), "⑥ the honest v3-only receipt is shown instead（含版本表切换出口指引）");
  ok(document.querySelectorAll(".rc-chain").length === 2, "⑥ the list renders the unchanged truth as-is (两链仍如实显示——数据没变)");
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
  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
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
