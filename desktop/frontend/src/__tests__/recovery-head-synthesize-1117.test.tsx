// 任务 1117 harness：版本管理面板「未选 HEAD 合成」对纯 v3 头会话的三条根因
// ——①后端补扫 v3 heads（见 Go 侧 recovery_head_synthesize.go/Test
// SynthesizeSessionHeadsUnselectedHeadSeesV3Heads，此处只钉前端面）②前端
// error 时不显示成功横幅 ③错误文案走 locale 中文化，三语可验证。
//
// 验收（tl 1117 正文）：
//  1. 未选 HEAD 点合成对纯 v3 头会话不再误报 only one chain（后端已修，前端
//     此处钉住「后端返回 items.length>0」时不再走"无副本"失败分支）；
//  2. (report, error) 互斥渲染：后端返回 (零值 report, error) 时不得同时出现
//     「合成已生效」成功提示；
//  3. 错误文案走 locale：errHeadSynthesisNoBranch 的英文原文必须被翻译成
//     settings.recoveryCopiesSynthesizeNoBranch 的中文文案，不能直出后端英文。
//
// 本 harness 从三个 stub 场景出发：
//  A. 纯 v3 头会话（无副本、2 个 v3 heads）、未选 HEAD 点合成、后端返回
//     items>0 的报告（模拟修复后的正确后端行为）→ 成功横幅照常，不是误报；
//  B. 后端抛出英文错误（含 "only one chain"，即 errHeadSynthesisNoBranch 的
//     英文原文）→ 只显示 locale 化的中文失败文案，不出现成功横幅；
//  C. 后端返回 (零值 report, error) 形态（items 空 + 非 errHeadSynthesisNoBranch
//     的其他错误抛出）→ 只显示失败文案，不出现成功横幅。

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

/** outcomeText collects the synthesis outcome rows only (the rc-outcomes list
 *  inside .rc-result) - the raw error text can appear in unrelated dev-only
 *  console/log surfaces the panel doesn't own, so assertions about "does the
 *  user see this error" must be scoped to the outcome list, not the whole page.
 *  任务 1146 返修：--blocked 一并收进——v3-only 诚实回执复用该中性样式，它也
 *  是用户可见的 outcome 行。 */
function outcomeText(): string {
  return Array.from(document.querySelectorAll(".rc-outcome--ok, .rc-outcome--failed, .rc-outcome--blocked"))
    .map((el) => el.textContent || "")
    .join("\n");
}

const V3_ONLY_PATH = "/sessions/gc-v3only.jsonl";

const VERSION_GROUP = {
  mainPath: V3_ONLY_PATH,
  mainLabel: "GC v3",
  directory: "/sessions",
  headCount: 2,
  selected: "",
  heads: [
    { id: "", kind: "main", createdAt: "2026-10-21T00:00:00Z", lastActivity: "2026-10-21T00:33:00Z", retired: false, selected: true, covered: false, messageCount: 12, turns: 6, preview: "hello" },
    { id: "deadbeefdeadbeef", kind: "fork", createdAt: "2026-10-20T00:00:00Z", lastActivity: "2026-10-20T00:00:00Z", retired: false, selected: false, covered: false, messageCount: 4, turns: 2, preview: "forked" },
  ],
};

const CHAIN_SET = {
  mainPath: V3_ONLY_PATH,
  mainLabel: "GC v3",
  chains: [
    { path: V3_ONLY_PATH, headId: "", label: "GC", isMain: true, selected: true, covered: false, messageCount: 12, turns: 6, lastActivity: "2026-10-21T00:33:00Z", bytes: 1024, preview: "hello" },
    { path: V3_ONLY_PATH, headId: "deadbeefdeadbeef", label: "GC fork", isMain: true, selected: false, covered: false, messageCount: 4, turns: 2, lastActivity: "2026-10-20T00:00:00Z", bytes: 512, preview: "forked" },
  ],
  longestPath: V3_ONLY_PATH,
  longestHead: "",
};

function stubBridge(overrides: Partial<AppBindings>) {
  const base = {
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [VERSION_GROUP],
    ListRecoveryChains: async () => CHAIN_SET,
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

type Root = ReturnType<typeof createRoot>;

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

console.log("\nrecovery head synthesis v3-only no-misreport + banner mutual exclusion (1117)");

// The harness pins against the en-US locale (the same default the sibling 1065
// and 1085 harnesses use), so assertions read the actual en.ts values rather
// than hardcoded Chinese - the point is "the panel resolves this key through
// i18n", not "the panel is hard-coded to Chinese".
const EN_NO_BRANCH = "This conversation has only one chain; there is nothing to synthesize.";
const EN_SYNTHESIZED = "Synthesized";
const EN_REOPEN_HINT = "The synthesis has taken effect.";
// 任务 1146 返修：v3-only 无文件级效果的诚实回执文案（en.ts）。
const EN_NO_COPY_BRANCH = "This conversation has no mergeable copy branches";

// A. 纯 v3 头会话未选 HEAD 合成、后端返回 items>0：
//    1117 当年修的是「only one chain」误报——这个意图保留（不再出现该误报）。
//    但「已合成」成功回执 + 「合成已生效」横幅是虚报：v3-only 形态文件级无事
//    可做，列表纹丝不动，用户据此预期「合成后只剩一个链」并复测出「列表仍不
//    刷新」（tl 1146 返修工单 20261025）。本场景按 1146 返修改钉：诚实回执 +
//    不显示成功横幅。mock 的 promoted:false + trashed:[] 正是后端 v3-only
//    提前返回的形态（recovery_head_synthesize.go len(copies)==0 分支）。
{
  stubBridge({});
  const root = await openAndExpand();
  await runSynthesizeToCompletion();
  ok(!outcomeText().includes(EN_NO_BRANCH), "A 纯 v3 头会话未选 HEAD 不再出现「只有一条链」误报文案（1117 意图保留）");
  ok(!outcomeText().includes(EN_SYNTHESIZED), "A 无文件级效果不虚报「已合成」读数（1146 返修）");
  ok(!bodyText().includes(EN_REOPEN_HINT), "A 无文件级效果不显示「合成已生效」横幅（1146 返修：落败分支根本不存在）");
  ok(outcomeText().includes(EN_NO_COPY_BRANCH), "A 显示 v3-only 诚实回执（含版本表「切换」出路指引）");
  await act(async () => root?.unmount());
}

// B. 后端抛出 errHeadSynthesisNoBranch 的英文原文：必须被翻译成 locale
//    文案（不是直出后端英文），不出现成功横幅（"合成已生效"提示），只有一条
//    失败记录。
{
  stubBridge({
    SynthesizeSessionHeads: async () => {
      throw new Error("this conversation has only one chain; there is nothing to synthesize");
    },
  });
  const root = await openAndExpand();
  await runSynthesizeToCompletion();
  ok(outcomeText().includes(EN_NO_BRANCH), "B 后端英文错误被翻译成 locale 文案（settings.recoveryCopiesSynthesizeNoBranch）");
  ok(!bodyText().includes(EN_REOPEN_HINT), "B 出错时不显示「合成已生效」成功横幅（互斥）");
  await act(async () => root?.unmount());
}

// C. 后端返回 (零值 report, error) 形态（非 errHeadSynthesisNoBranch 的其他
//    错误）：只显示失败文案，不出现成功横幅（互斥）。
{
  stubBridge({
    SynthesizeSessionHeads: async () => {
      throw new Error("some other backend failure");
    },
  });
  const root = await openAndExpand();
  await runSynthesizeToCompletion();
  ok(outcomeText().includes("some other backend failure"), "C 其他后端错误原样透传（无现成 locale key 的错误不生造文案）");
  ok(!bodyText().includes(EN_REOPEN_HINT), "C 出错时不显示「合成已生效」成功横幅（互斥）");
  await act(async () => root?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
