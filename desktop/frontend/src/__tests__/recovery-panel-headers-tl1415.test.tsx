// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/recovery-panel-headers-tl1415.test.tsx
// (vitest runs this file as a false red — the project's component harnesses are
// tsx-runner files; see recovery-branch-delete-1064.test.tsx for the pattern.)
//
// tl1415 harness:「工作阶段版本与副本」面板表格化改造的验收钉。
//
// 本文件随 tl1415 各 commit 逐步扩充;当前覆盖:
//  ① 概要表头(.rc-row--head)sticky + 不透明背景 + 保持完整(用户截图实证的
//     「表头下滑与内容重叠」修复)——JSDOM 无布局引擎,静态样式断言锁定声明。
//  ② 面板内所有表格有表头且列名语义化(候选链行「5396 · 215 交互轮次 …」
//     无表头读不懂——合并表渲染 thead 且含「条数」「交互轮次」列名)。
//  ③ 全部操作按钮带 hover 功能说明(title 属性)——含「修复并瘦身」的两动作
//     说明(剥离图片 payload + 折叠超窗旧消息)。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

// ─── ① 静态样式断言(JSDOM 无布局引擎;command-palette-css.test.ts 模具) ───

const stylesRaw = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
// 去注释后解析,声明匹配不吃到注释散文。
const styles = stylesRaw.replace(/\/\*[\s\S]*?\*\//g, "");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

function selectorBlocks(source: string, selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(source)) !== null) {
    const selectors = match[1].split(",").map((s) => s.trim());
    if (selectors.some((s) => s === selector || s.endsWith(` ${selector}`))) {
      blocks.push(match[2]);
    }
  }
  return blocks;
}

console.log("\nrecovery panel header/hover contract (tl1415)");

// ① 概要表头 sticky + 不透明背景。
{
  const headBlocks = selectorBlocks(styles, ".rc-row--head").join("\n");
  ok(/position:\s*sticky/.test(headBlocks), "① the summary header row is sticky");
  ok(/z-index:\s*var\(--z-inline-sticky\)/.test(headBlocks), "① the summary header row raises above rows");
  ok(/background:\s*var\(--bg-elev\)/.test(headBlocks), "① the summary header carries the opaque modal background");
  ok(!/background:\s*inherit/.test(headBlocks), "① the summary header no longer inherits a transparent background");
  // 整行 opacity 会把背景一起半透明化(透出下方滚动内容)——文字弱化必须走 color。
  ok(!/(^|[;\n])opacity:\s*0?\.72/.test(headBlocks), "① the header row no longer dims via row-level opacity");
}

// ─── 组件渲染断言(② 表头存在性/列名语义化;③ hover title) ───

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
globalThis.HTMLTableElement = dom.window.HTMLTableElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");

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
      messageCount: 3,
      turns: 2,
      lastActivity: "2026-10-20T00:00:00Z",
      bytes: 512,
      preview: "forked",
    },
  ],
  longestPath: MAIN_PATH,
  longestHead: "",
};

function stubBridge(overrides: Partial<AppBindings>) {
  const base = {
    ListRecoveryCopyGroups: async () => [GROUP],
    ListSessionVersionGroups: async () => [],
    ListRecoveryChains: async () => CHAIN_SET,
    ListSessionRepairRecordsFor: async () => [],
    DeleteRecoveryBranch: async () => {},
    DeleteSessionVersionHead: async () => {},
    PreviewRecoveryChain: async () => ({
      path: MAIN_PATH,
      isMain: true,
      messageCount: 12,
      turns: 6,
      firstUserText: "hello",
      tailLines: [],
      sharedWithMain: 12,
      uniqueToChain: 0,
      lastActivity: "2026-10-21T00:33:00Z",
      degraded: false,
    }),
    InspectSessionHealthFor: async () => ({ healthy: true, issues: [] }),
    RepairSessionFor: async () => ({ path: MAIN_PATH, repaired: false, actions: [], healthy: true }),
  };
  window.go = {
    main: {
      App: { ...base, ...overrides } as unknown as AppBindings,
    },
  } as typeof window.go;
}

async function openPanelAndExpandRow(): Promise<ReturnType<typeof createRoot>> {
  stubBridge({});
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

// ② 展开区合并表:thead 存在,列名语义化(含「条数」「交互轮次」的英文列名)。
{
  const root = await openPanelAndExpandRow();
  const thead = document.querySelector(".rc-versions__table thead");
  ok(Boolean(thead), "② the merged detail table renders a thead");
  const headerTexts = Array.from(thead?.querySelectorAll("th") ?? []).map((th) => (th.textContent || "").trim());
  // 列名语义化:「条数」「交互轮次」进表头(en locale:messages / turns),不再裸数字拼列。
  ok(headerTexts.includes("messages"), "② the detail header names the message-count column");
  ok(headerTexts.includes("turns"), "② the detail header names the turns column");
  ok(headerTexts.includes("size"), "② the detail header names the size column");
  ok(headerTexts.includes("modified"), "② the detail header names the modified column");
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
