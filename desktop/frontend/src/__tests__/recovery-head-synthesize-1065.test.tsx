// 任务 1065 harness：版本管理面板「选 HEAD → 反向合成最长链」。
//
// 验收（tl 1065 正文）：
//  1. 面板内选 HEAD + 勾选分支集合 → 一键反向合成最长链；
//  2. 合成后目标链独有计数涵盖各参与分支的独有事件（总条数≈并集），留操
//     作记录+进度展示；
//  3. 重复事件零双份；合成后其余分支可经 1064 删除；旧面板全部规则（tail
//     预览/轮次优先推荐/预览降级）在新面板等价可用；
//  4. Go 包级测试+前端 harness 绿，无预存红。
//
// 本 harness 从合并后的单一分区出发，钉四点：
//  ① 每条候选链自带「以此为 HEAD 合成」入口（一键式主操作在链行内）；
//  ② 合成先弹确认，列出参与分支读数；未确认不得调用 SynthesizeSessionHeads；
//  ③ 确认后调用携带选定的 HEAD，结果区记录参与分支数/独有合计/合成后读数；
//  ④ 合成后 pickedChain 清空（下次运行重新选），列表即时刷新。

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
const COPY_PATH = "/sessions/gc-recovery-0123456789abcdef.jsonl";

const GROUP = {
  mainPath: MAIN_PATH,
  mainLabel: "GC",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 12,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [
    {
      path: COPY_PATH,
      label: "GC copy 1",
      bytes: 2048,
      modified: "2026-10-21T01:00:00Z",
      messages: 3,
      shared: 1,
      unique: 2,
      scanned: true,
    },
  ],
};

// Three chains: the current main (turns=6), the copy (turns=2), and a fork head
// (turns=4). The recommendation (turn-priority) therefore points at the main.
const CHAIN_SET = {
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
      headId: "deadbeefdeadbeef",
      label: "GC fork",
      isMain: true,
      selected: false,
      covered: false,
      messageCount: 4,
      turns: 2,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked",
    },
    {
      path: COPY_PATH,
      headId: "",
      label: "GC copy 1",
      isMain: false,
      selected: false,
      covered: false,
      messageCount: 3,
      turns: 2,
      lastActivity: "2026-10-21T01:00:00Z",
      bytes: 2048,
      preview: "copy tail",
    },
  ],
  longestPath: COPY_PATH,
  longestHead: "", // the copy chain has headId "" in the fixture
};

const SYNTHESIS_REPORT: HeadSynthesisReport = {
  mainPath: MAIN_PATH,
  promoted: true,
  prefixed: 2,
  trashed: [COPY_PATH],
  items: [{ path: COPY_PATH, unique: 2, shared: 1, uniqueToHead: 0, archived: true }],
  afterMessages: 16,
  afterTurns: 8,
  skipped: [],
};

function stubBridge(overrides: Partial<AppBindings>, spies: { synthCalls: { mainPath: string; headId: string }[]; listCalls: number; consolidateCalls?: { mainPath: string; winnerPath: string }[] }) {
  const base = {
    ListRecoveryCopyGroups: async () => {
      spies.listCalls += 1;
      return [GROUP];
    },
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => CHAIN_SET,
    ListSessionRepairRecordsFor: async () => [],
    SynthesizeSessionHeads: async (mainPath: string, headId: string) => {
      spies.synthCalls.push({ mainPath, headId });
      return SYNTHESIS_REPORT;
    },
    // tl1415b-2(点3): 有副本的会话在底部③按形态路由到文件级合并引擎——
    // 选定 HEAD 以 winnerPath 落地(Go 端 SynthesizeSessionHeads 的数据层前身
    // 就是同一个 Consolidate 引擎,选定 HEAD 的采纳语义不变)。
    // IsConversationDualOpen: 合并前的 203 双开检查,合并路由的必经门。
    IsConversationDualOpen: async () => false,
    ConsolidateSessionRecoveryCopies: async (mainPath: string, winnerPath: string) => {
      spies.consolidateCalls?.push({ mainPath, winnerPath });
      return {
        mainPath: MAIN_PATH,
        winnerPath: COPY_PATH,
        promoted: true,
        blockedByDivergence: false,
        normalizedMain: false,
        mainMessageCount: 12,
        winnerMessageCount: 16,
        trashed: [COPY_PATH],
        skippedNotCovered: [],
        skippedUnloadable: [],
        prefixed: 2,
      };
    },
    PreviewRecoveryChain: async () => ({
      path: COPY_PATH,
      isMain: false,
      messageCount: 12,
      turns: 6,
      firstUserText: "hello",
      tailLines: ["hello", "world"],
      sharedWithMain: 12,
      uniqueToChain: 0,
      lastActivity: "2026-10-21T00:33:00Z",
      degraded: false,
    }),
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openAndExpand(): Promise<Root | null> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  // tl1415b-2(点2): 会话行恢复默认折叠——副本行的链清单由会话行展开驱动,
  // 点开展开钮,双 flush 等链清单落地。
  const toggles = Array.from(document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle"));
  for (const tgl of toggles) {
    await act(async () => { tgl.click(); });
  }
  await flush();
  await flush();
  return root;
}

/** tl1415b-2(点3): 合成唯一入口在底部③——勾选会话行,点底部「以选定 HEAD
 *  合成最长链」;本套件的会话带副本,底部按形态路由到文件级合并引擎(选定
 *  HEAD 以 winnerPath 采纳),确认链走合并预览确认。 */
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

type Root = ReturnType<typeof createRoot>;

console.log("\nrecovery head synthesis pick-HEAD + one-key merge (1065)");

// ① 底部③是唯一合成入口;推荐标记与链预览保留。
{
  const spies = { synthCalls: [] as { mainPath: string; headId: string }[], listCalls: 0, consolidateCalls: [] as { mainPath: string; winnerPath: string }[] };
  stubBridge({}, spies);
  const root = await openAndExpand();
  const synthEntries = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal button")).filter((b) =>
    b.textContent?.includes("Merge into longest chain"),
  );
  ok(synthEntries.length === 1, "① the footer offers exactly one synthesis entry (整表级一键操作,tl1415b-2 行内钮删除)");
  const recommendedRows = document.querySelectorAll(".rc-chain--recommended");
  ok(recommendedRows.length === 1, "① exactly one chain carries the recommendation (1.34 turn-priority rule kept)");
  ok(Boolean(document.querySelector(".rc-chain__preview")), "① the chain preview column is still there (tail preview rule kept)");
  await act(async () => root?.unmount());
}

// ② 合成先弹确认；未确认不得调用后端。
{
  const spies = { synthCalls: [] as { mainPath: string; headId: string }[], listCalls: 0, consolidateCalls: [] as { mainPath: string; winnerPath: string }[] };
  stubBridge({}, spies);
  const root = await openAndExpand();
  await synthesizeViaFooter();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "② synthesis asks for confirmation first");
  ok(spies.consolidateCalls.length === 0 && spies.synthCalls.length === 0, "② no synthesis happens before the user confirms");
  await act(async () => root?.unmount());
}

// ③ 确认后调用携带选定 HEAD(winnerPath)；结果区记录合并读数。
{
  const spies = { synthCalls: [] as { mainPath: string; headId: string }[], listCalls: 0, consolidateCalls: [] as { mainPath: string; winnerPath: string }[] };
  stubBridge({}, spies);
  const root = await openAndExpand();

  // Pick the copy chain as the HEAD first (the preview dialog's confirm),
  // then run the footer synthesis entry.
  // tl1415b: 候选链数据并入副本行——副本行(tr.rc-row--copy)的 sub2 带尾部
  // 预览文本,预览/切换/删除都在同一行的操作列;picked 徽标也落在该行。
  const copyRow = Array.from(document.querySelectorAll("tr.rc-row--copy")).find((el) =>
    el.textContent?.includes("copy tail"),
  );
  const previewBtn = copyRow?.querySelector<HTMLButtonElement>("button");
  await act(async () => { previewBtn?.click(); });
  await flush();
  const previewConfirm = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Make this the merged main") ??
    Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
      .find((b) => b.textContent?.trim() === "Keep the current main line");
  ok(Boolean(previewConfirm), "③ the tail preview dialog offers the pick-HEAD confirm");
  await act(async () => { previewConfirm?.click(); });
  await flush();
  ok(Boolean(copyRow?.querySelector(".rc-chain__picked-badge")), "③ the picked HEAD is marked on its chain row");

  const listCallsBefore = spies.listCalls;
  await synthesizeViaFooter();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button")).find(
    (b) => b.classList.contains("btn--danger"),
  );
  ok(Boolean(confirmBtn), "③ the synthesis confirmation offers a start action");
  await act(async () => { confirmBtn?.click(); });
  await flush();

  ok(spies.consolidateCalls.length === 1 && spies.consolidateCalls[0].mainPath === MAIN_PATH && spies.consolidateCalls[0].winnerPath === COPY_PATH,
    "③ the synthesis call carries the picked HEAD (winnerPath)");
  ok(spies.listCalls > listCallsBefore, "③ the list reloads immediately after synthesis");
  ok(bodyText().includes("Merged"), "③ the outcome record reports the merge");
  ok(bodyText().includes("16"), "③ the outcome shows the post-synthesis message reading (并集可核对)");
  ok(!bodyText().includes("Selected as main line"), "③ the picked HEAD is reset after synthesis (next run re-picks)");
  await act(async () => root?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
