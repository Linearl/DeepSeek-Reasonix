// 任务 1085 harness：v3-only 会话整行缺席——RecoveryCopiesSection 的 groups
// 来源修正。
//
// 根因：groups 仅来自 ListRecoveryCopyGroups（有副本文件的会话），versionGroups
// 只做徽标叠加。v3 多 heads 会话若没有副本文件，就不在 groups 里，整行连同
// 1064 删除/1065 合成入口一起消失。
//
// 验收（tl 1085 正文）：
//  1. v3 多 heads 无 recovery 副本会话：整行渲染（含 1064 删除/1065 合成入口）；
//  2. 空态文案区分「无副本」与「有副本但无 heads」；
//  3. 前端 tsc clean + pnpm check:class-contract 全绿，无预存红。
//
// 本 harness 从「ListRecoveryCopyGroups 返回空数组 + ListSessionVersionGroups
// 返回一个多 heads 会话」出发，钉三点：
//  ① 该会话照常渲染成一行（不是整行消失）；
//  ② 展开行后 1064/1065 入口（链表块的合成按钮 + VersionRows 的删除按钮）可见；
//  ③ 空态文案区分——纯空（both 为空）与「有副本但无 heads」各说各话。

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

const V3_ONLY_PATH = "/sessions/gc.jsonl";

// One multi-head v3 session, no recovery copies on disk.
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

// The chain list (lazy-loaded on expand) reports the two v3 heads as candidates.
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
    SynthesizeSessionHeads: async () => ({ mainPath: V3_ONLY_PATH, promoted: true, prefixed: 0, trashed: [], items: [], afterMessages: 12, afterTurns: 6, skipped: [] }),
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

async function openPanel(): Promise<Root> {
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  return root;
}

type Root = ReturnType<typeof createRoot>;

console.log("\nv3-only session gets a full row (1085)");

// ① v3 多 heads 无副本会话：整行渲染（不是整行消失）。
{
  stubBridge({});
  const root = await openPanel();
  const rows = document.querySelectorAll(".rc-modal .rc-group");
  ok(rows.length === 1, "① the v3-only conversation renders as a row (不是整行缺席)");
  const toggle = document.querySelector<HTMLButtonElement>(".rc-modal .rc-toggle");
  ok(Boolean(toggle && toggle.textContent?.includes("GC v3")), "① the row carries the conversation's label");
  const badge = document.querySelector(".rc-versions__badge");
  ok(Boolean(badge), "① the v3 version badge is present on the row");
  // 展开行，检查 1064/1065 入口是否可见。
  await act(async () => { toggle?.click(); });
  await flush();
  const synthBtn = buttonByText("Synthesize longest chain");
  ok(Boolean(synthBtn), "② the 1065 synthesis entry (one-key) is visible in the chain block");
  // VersionRows 的删除入口（1064 单版本删除）——当前选中行不可删，降级行也不可删，
  // 但非当前、非降级、有 headId 的行必须有删除按钮。
  const versionDeleteBtns = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-versions__table button")).filter((b) =>
    b.textContent?.includes("Delete"),
  );
  ok(versionDeleteBtns.length >= 1, "② the 1064 delete entry is visible on a non-current head row");
  await act(async () => root?.unmount());
}

// ② 空态文案区分——「有副本但无 heads」与「纯无副本」。
{
  // Case A: 有副本行，但 v3 无 heads（versionGroups 为空）——「没有发现 recovery 副本」不应出现。
  stubBridge({
    ListRecoveryCopyGroups: async () => [
      {
        mainPath: V3_ONLY_PATH,
        mainLabel: "GC",
        directory: "/sessions",
        mainExists: true,
        mainMessages: 12,
        mainBytes: 1024,
        mainModified: "2026-10-21T00:33:00Z",
        copies: [{ path: V3_ONLY_PATH.replace(".jsonl", "-recovery-0123456789abcdef.jsonl"), label: "GC copy", bytes: 2048, modified: "2026-10-21T01:00:00Z", messages: 3, shared: 1, unique: 2, scanned: true }],
      },
    ],
    ListSessionVersionGroups: async () => [],
  });
  const rootA = await openPanel();
  const textA = bodyText();
  ok(!textA.includes("No recovery copies found."), "③ 「有副本但无 heads」空态不出「No recovery copies found.」——两态要分清");
  await act(async () => rootA?.unmount());

  // Case B: 两侧都空——两个空态文案都不出现（不是空态，而是有 v3-only 行）。
  // 重设为 v3-only 场景，确认其渲染成一行而不是空态。
  stubBridge({});
  const rootB = await openPanel();
  ok(!bodyText().includes("No recovery copies found."), "③ v3-only 有行时不出「No recovery copies found.」");
  await act(async () => rootB?.unmount());

  // Case C: 两侧都空，且没有 v3-only 行——纯空态。
  stubBridge({
    ListRecoveryCopyGroups: async () => [],
    ListSessionVersionGroups: async () => [],
  });
  const rootC = await openPanel();
  ok(bodyText().includes("No recovery copies found."), "③ 真正的空态照常显示「No recovery copies found.」");
  await act(async () => rootC?.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
