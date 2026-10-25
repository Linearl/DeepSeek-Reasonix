// 任务 1146 返修2 harness：v3-only 合成的生效语义 + 「当前主线」双标修复。
//
// 背景（用户 20261025 裁决 + 复测现场）：
//  ② 纯 v3 头会话（无副本文件）的合成在返修1只做诚实回执（v3only 中性横幅，
//     列表纹丝不动）；返修2后端升级为生效语义——主线切换到选定 head
//     （switchedTo）+ 落败 heads 走 1064 retire 标记清理（retiredHeads）。
//     前端横幅必须如实报两件事各自发生与否（「已切换至选定版本并清理 N 个
//     落败分支」），不再沿用文件级成功文案（「落败分支还在回收站里」对
//     retire 语义失实：head 清理不走文件回收站）。
//  ③ 「当前主线」标签双标：同一 jsonl 的两条 v3 head 链（同 path 不同
//     headId）旧判定 chain.path === set.mainPath 全部命中 → 都挂「当前主线」。
//     改为只有后端标记 selected 的那条 head 链才标，另一条标「候选链 N」。
//
// 验收（返修2 正文）：
//  D. 后端报 switchedTo + retiredHeads → v3synthesized 成功回执，横幅文案
//     含「已切换/清理」如实读数，不出现文件级的「合成已生效（回收站）」横幅，
//     专属下一步提示（retire 可恢复语义）出现；
//  E. 同文件双 head 的链表：恰好一条「当前主线」，其余为「候选链 N」；
//  F. 单 head（schema-1 整文件行，无 Selected 读数）不回归：仍标「当前主线」。
//
// harness 钉 en-US locale（与兄弟 1065/1117 同口径），断言读 en.ts 实际值。

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

function outcomeText(): string {
  return Array.from(document.querySelectorAll(".rc-outcome--ok, .rc-outcome--failed, .rc-outcome--blocked"))
    .map((el) => el.textContent || "")
    .join("\n");
}

const V3_ONLY_PATH = "/sessions/gc-v3only-1146r2.jsonl";
const MAIN_HEAD_ID = "";
const FORK_HEAD_ID = "deadbeefdeadbeef";

function twoHeadChainSet() {
  return {
    mainPath: V3_ONLY_PATH,
    mainLabel: "GC v3",
    chains: [
      { path: V3_ONLY_PATH, headId: MAIN_HEAD_ID, label: "GC", isMain: true, selected: true, covered: false, messageCount: 12, turns: 6, lastActivity: "2026-10-21T00:33:00Z", bytes: 1024, preview: "hello" },
      { path: V3_ONLY_PATH, headId: FORK_HEAD_ID, label: "GC fork", isMain: true, selected: false, covered: false, messageCount: 4, turns: 2, lastActivity: "2026-10-20T00:00:00Z", bytes: 512, preview: "forked" },
    ],
    longestPath: V3_ONLY_PATH,
    longestHead: MAIN_HEAD_ID,
  };
}

const VERSION_GROUP = {
  mainPath: V3_ONLY_PATH,
  mainLabel: "GC v3",
  directory: "/sessions",
  headCount: 2,
  selected: FORK_HEAD_ID,
  heads: [
    { id: MAIN_HEAD_ID, kind: "main", createdAt: "2026-10-21T00:00:00Z", lastActivity: "2026-10-21T00:33:00Z", retired: false, selected: true, covered: false, messageCount: 12, turns: 6, preview: "hello" },
    { id: FORK_HEAD_ID, kind: "fork", createdAt: "2026-10-20T00:00:00Z", lastActivity: "2026-10-20T00:00:00Z", retired: false, selected: false, covered: false, messageCount: 4, turns: 2, preview: "forked" },
  ],
};

function stubBridge(overrides: Partial<AppBindings>, chainSet = twoHeadChainSet()) {
  const base = {
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [VERSION_GROUP],
    ListRecoveryChains: async () => chainSet,
    ListSessionRepairRecordsFor: async () => [],
    SynthesizeSessionHeads: async (): Promise<HeadSynthesisReport> => ({
      mainPath: V3_ONLY_PATH, promoted: false, prefixed: 0, trashed: [],
      items: [{ path: V3_ONLY_PATH, unique: 0, shared: 4, uniqueToHead: 0, archived: false }],
      afterMessages: 12, afterTurns: 6, skipped: [],
    }),
    PreviewRecoveryChain: async () => ({ path: V3_ONLY_PATH, isMain: true, messageCount: 12, turns: 6, firstUserText: "hello", tailLines: ["hello", "world"], sharedWithMain: 0, uniqueToChain: 0, lastActivity: "2026-10-21T00:33:00Z", degraded: false }),
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

type Root = ReturnType<typeof createRoot>;

async function openAndExpand(): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  const toggle = document.querySelector<HTMLButtonElement>(".rc-modal .rc-toggle");
  await act(async () => { toggle?.click(); });
  await flush();
  return root;
}

async function runSynthesizeToCompletion() {
  const synthBtn = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes("Synthesize longest chain"),
  );
  await act(async () => { synthBtn?.click(); });
  await flush();
  const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"))
    .find((b) => b.textContent?.trim() === "Start synthesis");
  await act(async () => { confirmBtn?.click(); });
  await flush();
}

console.log("\nv3-only synthesis effective semantics + current-main single label (1146 rework 2)");

// en.ts 实际值（防 key 失配；与 1117 harness 同口径——钉「i18n 解析正确」而非
// 具体中文）。
const EN_V3_BOTH = "Switched to the selected version and cleaned up 1 losing branch(es)";
const EN_V3_HINT = "The main line was switched, or losing versions were cleaned up";
const EN_TRASH_HINT = "The synthesis has taken effect.";
const EN_CURRENT_MAIN = "current main";
const EN_CHAIN_CANDIDATE = "chain";

// D. 后端报 switchedTo + retiredHeads：v3synthesized 成功回执 + 如实横幅 +
//    专属提示；不出现文件级的「合成已生效（回收站）」横幅。
{
  stubBridge({
    SynthesizeSessionHeads: async (): Promise<HeadSynthesisReport> => ({
      mainPath: V3_ONLY_PATH, promoted: false, prefixed: 0, trashed: [],
      items: [{ path: V3_ONLY_PATH, unique: 0, shared: 4, uniqueToHead: 0, archived: false }],
      afterMessages: 4, afterTurns: 2, skipped: [],
      switchedTo: FORK_HEAD_ID, retiredHeads: ["retiredhead01"],
    }),
  });
  const root = await openAndExpand();
  await runSynthesizeToCompletion();
  ok(outcomeText().includes(EN_V3_BOTH), "D 横幅如实报「切换 + 清理 1 个落败分支」（返修2 生效语义）");
  ok(bodyText().includes(EN_V3_HINT), "D 出现 v3 专属下一步提示（retire 可恢复语义，非回收站表述）");
  ok(!bodyText().includes(EN_TRASH_HINT), "D 不出现文件级「合成已生效（落败分支还在回收站）」横幅（对 retire 语义失实）");
  await act(async () => root?.unmount());
}

// E. 同文件双 head 的链表：恰好一条「当前主线」，另一条是「候选链 N」。
//    计数只数链名元素（.rc-chain__name）——「current main」也出现在 pick 提示
//    文案里，整页计数会误报。
{
  stubBridge({});
  const root = await openAndExpand();
  const chainNames = Array.from(document.querySelectorAll(".rc-chain__name")).map((el) => el.textContent || "");
  const currentMainCount = chainNames.filter((t) => t === EN_CURRENT_MAIN).length;
  ok(currentMainCount === 1, `E 双 head 场景链名「当前主线」恰好 1 条（实际 ${currentMainCount}，链名=${JSON.stringify(chainNames)}）`);
  ok(bodyText().includes(`${EN_CHAIN_CANDIDATE} 1`), "E 落败 head 链标「候选链 1」");
  await act(async () => root?.unmount());
}

// F. 单 head（无 Selected 读数的整文件行）不回归：仍标「当前主线」。
{
  const single = {
    mainPath: V3_ONLY_PATH,
    mainLabel: "GC v3",
    chains: [
      { path: V3_ONLY_PATH, headId: "", label: "GC", isMain: true, selected: false, covered: false, messageCount: 12, turns: 6, lastActivity: "2026-10-21T00:33:00Z", bytes: 1024, preview: "hello" },
    ],
    longestPath: V3_ONLY_PATH,
    longestHead: "",
  };
  stubBridge({}, single);
  const root = await openAndExpand();
  const chainNames = Array.from(document.querySelectorAll(".rc-chain__name")).map((el) => el.textContent || "");
  const currentMainCount = chainNames.filter((t) => t === EN_CURRENT_MAIN).length;
  ok(currentMainCount === 1, `F 单 head 场景链名「当前主线」仍 1 条（实际 ${currentMainCount}，不回归）`);
  await act(async () => root?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
