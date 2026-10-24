// 任务 1123 harness：版本管理面板候选链推荐拆成「更长」「更新」两条独立标
// 签，各带自己的实际值（条数/轮次、最后活动日期），不再合并成一条可能只对
// 一半成立的「更长且更新」表述（20261024 用户现场：被推荐的长链 6800条/
// 246轮，最后活动 2026-09-28，实际比落选短链 468条/6轮、2026-10-22 折叠
// 更旧——「且更新」这一半是失实的）。
//
// 验收（tl 1123 正文）：
//  1. 推荐标签与实际数据一致（长度/时间各自核对）——长链只标「更长」带真实
//     条数/轮次，新链只标「更新」带真实日期；
//  2. 两链各有理由展示（不是一条链包揽两个标签，另一条链什么都没有）；
//  3. 无失实合并文案——「更长且更新」这一条合并表述不再出现（对应后端
//     RecoveryChainSet.LongestPath/LongestHead 拆出 NewestPath/NewestHead，
//     见 desktop/recovery_chains.go 与 desktop/recovery_chains_test.go）。
//
// 本 harness 从一个「长但旧」的主转录 + 一个「短但新」的副本出发（与后端
// TestListRecoveryChainsSplitsLongerAndNewer 同源场景），钉三点：
//  ① 主转录行只出现「longer」标签、不出现「newer」标签，标签里的条数/轮次
//     与该行的实际列值一致；
//  ② 副本行只出现「newer」标签、不出现「longer」标签，标签里的日期与该行
//     的实际最后活动列一致；
//  ③ 全文不出现旧的合并文案 "longer and newer"（英文 locale 下的旧
//     suggested 键文案，即「更长且更新」的英文对应表述）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

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

const LONG_OLD_PATH = "/sessions/gc-long-old.jsonl";
const SHORT_NEW_PATH = "/sessions/gc-long-old-recovery-0123456789abcdef.jsonl";

const GROUP = {
  mainPath: LONG_OLD_PATH,
  mainLabel: "GC开发专员",
  directory: "/sessions",
  mainExists: true,
  mainMessages: 6800,
  mainBytes: 49_210_175,
  mainModified: "2026-09-28T12:00:39Z",
  copies: [
    {
      path: SHORT_NEW_PATH,
      label: "GC开发专员 copy 1",
      bytes: 2048,
      modified: "2026-10-22T09:00:00Z",
      messages: 468,
      shared: 460,
      unique: 8,
      scanned: true,
    },
  ],
};

// 「长但旧」的主转录（6800条/246轮，最后活动 2026-09-28）+ 「短但新」的副本
// （468条/6轮，最后活动 2026-10-22）——与用户现场一致的形状。后端
// ListRecoveryChains 据此计算 LongestPath=主转录、NewestPath=副本（见
// desktop/recovery_chains_test.go 钉住的数据层）。
const CHAIN_SET = {
  mainPath: LONG_OLD_PATH,
  mainLabel: "GC开发专员",
  chains: [
    // 同一 path+headId 的行会出现两次（主转录本体 + 它的 main head，字段读数
    // 完全相同）——不是本件引入的问题，是 VersionRows 的既有形状；1123 的修
    // 复（两条标签各只打一次）对这个形状也要成立，因此 fixture 里保留两行
    // 重复的主转录行，断言「更长」标签只出现一次。
    {
      path: LONG_OLD_PATH, headId: "", label: "GC开发专员", isMain: true, selected: false,
      covered: false, messageCount: 6800, turns: 246, lastActivity: "2026-09-28T12:00:39Z",
      bytes: 49_210_175, preview: "早前工作",
    },
    {
      path: LONG_OLD_PATH, headId: "", label: "GC开发专员", isMain: true, selected: false,
      covered: false, messageCount: 6800, turns: 246, lastActivity: "2026-09-28T12:00:39Z",
      bytes: 49_210_175, preview: "早前工作",
    },
    {
      path: SHORT_NEW_PATH, headId: "", label: "GC开发专员 copy 1", isMain: false, selected: false,
      covered: false, messageCount: 468, turns: 6, lastActivity: "2026-10-22T09:00:00Z",
      bytes: 2048, preview: "折叠后的短链",
    },
  ],
  longestPath: LONG_OLD_PATH,
  longestHead: "",
  newestPath: SHORT_NEW_PATH,
  newestHead: "",
};

function stubBridge(overrides: Partial<AppBindings>) {
  const base = {
    ListRecoveryCopyGroups: async () => [GROUP],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => CHAIN_SET,
    ListSessionRepairRecordsFor: async () => [],
    SynthesizeSessionHeads: async () => ({
      mainPath: LONG_OLD_PATH, promoted: false, prefixed: 0, trashed: [],
      items: [{ path: SHORT_NEW_PATH, unique: 8, shared: 460, uniqueToHead: 0, archived: false }],
      afterMessages: 7268, afterTurns: 252, skipped: [],
    }),
    PreviewRecoveryChain: async () => ({
      path: SHORT_NEW_PATH, isMain: false, messageCount: 468, turns: 6,
      firstUserText: "折叠后的短链", tailLines: ["折叠后的短链"], sharedWithMain: 460,
      uniqueToChain: 8, lastActivity: "2026-10-22T09:00:00Z", degraded: false,
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

/** versionRows returns the rendered version-table rows (in the .rc-versions
 *  block) so a scenario can assert on both candidate chains' own tag content. */
function versionRows(): Element[] {
  return Array.from(document.querySelectorAll(".rc-versions__table tbody tr"));
}

console.log("\ncandidate chain recommendation splits into independent longer/newer tags (1123)");

{
  stubBridge({});
  const root = await openAndExpand();

  // VersionRows 的每一行第一格是 head 身份（main / 副本 headId 前 8 位）——
  // 副本 fixture 里 headId 也是 ""（schema-1 单链），第一格同样显示 "main"，
  // 因此按该行的实际读数（条数/轮次）区分两条链，而不是只看第一格文字。
  // 同一 path+headId 的行会出现两次（主转录本体 + 它的 main head，字段读数
  // 相同）——「更长」标签只在第一次出现时打一次（见
  // RecoveryCopiesSection.tsx 的 seenLongerPaths 去重），因此取第一条命中行。
  const rows = versionRows();
  const mainRows = rows.filter((r) => (r.textContent || "").includes("6800"));
  const mainRow = mainRows[0]?.textContent || "";
  const copyRow = rows.find((r) => (r.textContent || "").includes("468"))?.textContent || "";
  ok(mainRow !== "", "① the long-but-older main chain renders a version row");
  ok(copyRow !== "", "① the short-but-newer copy chain renders a version row");

  // ① 主转录：更长（6800条/246轮），不是"更新"——标签只标其一，且值与该行
  //    实际列值一致。
  ok(mainRow.includes("longer"), "① the longer chain carries the 'longer' tag");
  ok(!mainRow.includes("newer"), "① the older-but-longer chain does NOT carry the 'newer' tag");
  ok(mainRow.includes("6800"), "① the 'longer' tag shows the chain's actual message count (6800)");
  ok(mainRow.includes("246"), "① the 'longer' tag shows the chain's actual turn count (246)");

  // ② 副本：更新（2026-10-22），不是"更长"——标签只标其一，且日期与该行
  //    实际最后活动列一致。
  ok(copyRow.includes("newer"), "② the newer chain carries the 'newer' tag");
  ok(!copyRow.includes("longer"), "② the newer-but-shorter chain does NOT carry the 'longer' tag");
  ok(copyRow.includes("2026-10-22"), "② the 'newer' tag shows the chain's actual last-activity date (2026-10-22)");

  // ③ 不再有合并文案——「更长且更新」的英文对应表述（旧 suggested 键的
  //    "suggested (longer and newer)"）必须已经消失。
  ok(!bodyText().includes("longer and newer"), "③ the misleading merged 'longer and newer' claim is gone");

  // ④ 同一 path+headId 的重复行（主转录本体 + main head）不能把同一条标签
  //    渲染两遍——「更长」「更新」各自在整个版本表里只出现一次。
  const allRowsText = rows.map((r) => r.textContent || "").join("\n");
  const longerTagCount = (allRowsText.match(/longer \(/g) || []).length;
  const newerTagCount = (allRowsText.match(/newer \(/g) || []).length;
  ok(longerTagCount === 1, "④ the 'longer' tag renders exactly once across duplicated same-head rows");
  ok(newerTagCount === 1, "④ the 'newer' tag renders exactly once across duplicated same-head rows");

  await act(async () => root?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
