// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/recovery-panel-headers-tl1415.test.tsx
// (vitest runs this file as a false red — the project's component harnesses are
// tsx-runner files; see recovery-branch-delete-1064.test.tsx for the pattern.)
//
// tl1415 harness:「对话版本管理」面板表格化改造的验收钉。
//
// 本文件随 tl1415 各 commit 逐步扩充;当前覆盖:
//  ① 概要表头(.rc-row--head)sticky + 不透明背景 + 保持完整(用户截图实证的
//     「表头下滑与内容重叠」修复)——JSDOM 无布局引擎,静态样式断言锁定声明。
//  ② 面板内所有表格有表头且列名语义化(候选链行「5396 · 215 交互轮次 …」
//     无表头读不懂——合并表渲染 thead 且含「条数」「交互轮次」列名)。
//  ③ 全部操作按钮带 hover 功能说明(title 属性)——含「修复并瘦身」的两动作
//     说明(剥离图片 payload + 折叠超窗旧消息)。
//  ④ 交互轮次列实测(tl1360 缺陷①):概要行轮次列放实测轮次(不再拿消息数
//     冒充)、副本行轮次随扫描实测、未扫描给「unmeasured」状态而非裸「—」。
//  ⑤ 副本/文件链行的文件名短标识 recovery-<8hex>(tl1360 缺陷③)。
//  ⑥ 降级行预览空态与「无预览文本」区分(tl1360 缺陷②)。
//  ⑦ 清空合并历史入口(点9):headCount>1 时展开区出现入口,确认后调用后端。
//  ⑧ 清理多余副本一键(点8):unique=0 副本存在时表格底部出现,确认后逐份
//     DeleteRecoveryBranch。
//  ⑨ 设置页入口文案(点7):标题「Conversation version management」+描述。

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
  mainTurns: 6,
  mainBytes: 1024,
  mainModified: "2026-10-21T00:33:00Z",
  copies: [
    {
      path: COPY_PATH,
      label: "GC copy 1",
      bytes: 2048,
      modified: "2026-10-21T01:00:00Z",
      messages: 3,
      turns: 2,
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

  // ④ 概要行轮次列放实测轮次(6),不再拿消息数(12)冒充列名「交互轮次」。
  const summaryRow = document.querySelector(".rc-row--group");
  const summarySpans = Array.from(summaryRow?.querySelectorAll(":scope > span") ?? []).map((s) => (s.textContent || "").trim());
  // 行内普通 span 顺序: modified, size, turns, ×N, unique, action。
  ok(summarySpans[2] === "6", `④ the summary row's turns cell shows the measured turns (got: ${summarySpans[2]})`);

  // ④ 副本行轮次实测(2)——原恒「—」。
  const copyRow = document.querySelector(".rc-versions__table tr.rc-row--copy");
  const copyCells = Array.from(copyRow?.querySelectorAll("td") ?? []).map((td) => (td.textContent || "").trim());
  ok(copyCells[3] === "2", `④ the copy row's turns cell shows the measured turns (got: ${copyCells[3]})`);

  // ⑤ 副本行带文件名短标识 recovery-<8hex>(用户把面板行对回磁盘文件的锚点)。
  const shortName = copyRow?.querySelector(".rc-copy__shortname");
  ok((shortName?.textContent || "") === "recovery-01234567", `⑤ the copy row carries the short filename signature (got: ${shortName?.textContent})`);
  ok(((shortName as HTMLElement)?.title || "").endsWith("gc-recovery-0123456789abcdef.jsonl"), "⑤ the short signature's hover title carries the full filename");

  // ③+⑥ hover 说明与预览空态:链行操作区(合并表 ops 行)。主线行也带 rc-chain
  //   类,故全局找 ops 行内的预览 span(CHAIN_SET 里候选链的 preview="forked")。
  const previewSpan = Array.from(document.querySelectorAll(".rc-versions__table .rc-chain__preview"))[0];
  // 候选链(preview: "forked")显示内联预览文本,不再是「无预览文本」占位。
  ok((previewSpan?.textContent || "") === "forked", `⑥ the chain row shows the inline preview text (got: ${previewSpan?.textContent})`);

  // ③ hover title:行内扫描按钮+底部修复并瘦身按钮。
  const scanBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-row--group .rc-action button"))[0];
  ok((scanBtn?.title || "").includes("read-only"), "③ the row scan button carries a hover description");
  const dietBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-versions__actions button")).find(
    (b) => (b.title || "").includes("strip image payloads"),
  );
  ok(Boolean(dietBtn), "③ the repair+diet button's hover names both of its actions (strip payloads + fold)");

  // ③ 点5 覆盖收口:概要行的合并勾选框(最后一个无可操作说明的控件)也带
  //   hover 说明——说明勾选语义(参与合并所选,勾选本身不动数据)。
  const mergeCheckbox = document.querySelector<HTMLInputElement>(".rc-row--group input[type=checkbox]");
  ok((mergeCheckbox?.title || "").includes("merge-selected"), "③ the merge checkbox carries a hover description");

  // ⑦ 清空合并历史:ListSessionVersionGroups 报 headCount=3 时,展开区操作行
  // 出现「Clear merge history」入口;点击出确认框,确认后调用后端。
  const clearBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-versions__actions button")).find(
    (b) => b.textContent?.includes("Clear merge history"),
  );
  ok(!clearBtn, "⑦ no clear-merge-history entry when the version list carries no group");

  await act(async () => root.unmount());
}

// ④b 未扫描副本的轮次格给「unmeasured」状态而非裸「—」。
{
  stubBridge({
    ListRecoveryCopyGroups: async () => [
      {
        ...GROUP,
        copies: [{ ...GROUP.copies[0], scanned: false, messages: 0, turns: 0, shared: 0, unique: 0 }],
      },
    ],
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  const copyRow = document.querySelector(".rc-versions__table tr.rc-row--copy");
  const copyCells = Array.from(copyRow?.querySelectorAll("td") ?? []).map((td) => (td.textContent || "").trim());
  ok(copyCells[3] === "unmeasured", `④b an unscanned copy's turns cell names the state (got: ${copyCells[3]})`);
  ok((copyRow?.querySelector("td:nth-child(4) span")?.getAttribute("title") || "").includes("Scan"), "④b the unmeasured cell's hover points at Scan");
  await act(async () => root.unmount());
}

// ⑥b 降级行(degraded+无预览)的空态是「读数降级」,不冒充「该分支无预览文本」。
{
  stubBridge({
    ListRecoveryChains: async () => ({
      ...CHAIN_SET,
      chains: [
        ...CHAIN_SET.chains,
        {
          path: COPY_PATH,
          headId: "",
          label: "GC degraded",
          isMain: false,
          selected: false,
          covered: false,
          messageCount: 9,
          turns: 0,
          lastActivity: "2026-10-19T00:00:00Z",
          bytes: 128,
          preview: "",
          degraded: true,
        },
      ],
    }),
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  const degradedRows = Array.from(document.querySelectorAll(".rc-versions__table tr.rc-chain"));
  const degradedOps = degradedRows
    .map((row) => row.nextElementSibling?.textContent || "")
    .find((text) => text.includes("readings degraded"));
  ok(Boolean(degradedOps), "⑥b a degraded chain without preview says the readings are degraded");
  ok(
    !(degradedOps || "").includes("no preview text"),
    "⑥b the degraded empty state does not claim the branch has no preview text",
  );
  await act(async () => root.unmount());
}

// ⑦ 清空合并历史(点9):版本清单有该会话(headCount=3)时入口出现,确认后调后端。
{
  const cleared: string[] = [];
  stubBridge({
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 3, selected: "", heads: [] },
    ],
    ClearSessionMergeHistory: async (path: string) => {
      cleared.push(path);
    },
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await act(async () => { (document.querySelector(".rc-modal .rc-toggle") as HTMLButtonElement).click(); });
  await flush();
  const clearBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-versions__actions button")).find(
    (b) => b.textContent?.includes("Clear merge history"),
  );
  ok(Boolean(clearBtn), "⑦ the clear-merge-history entry appears for a headCount>1 conversation");
  ok((clearBtn?.title || "").includes("bookkeeping"), "⑦ the entry's hover says it is a bookkeeping reset");
  await act(async () => { clearBtn?.click(); });
  await flush();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "⑦ clearing asks for confirmation first");
  ok(cleared.length === 0, "⑦ nothing is cleared before the user confirms");
  await act(async () => {
    (document.querySelector(".reasonix-confirm-dialog .btn--danger") as HTMLButtonElement)?.click();
  });
  await flush();
  ok(cleared.length === 1 && cleared[0] === MAIN_PATH, "⑦ confirming calls the backend with the conversation path");
  await act(async () => root.unmount());
}

// ⑧ 清理多余副本(点8):unique=0 的已测副本存在时,表格底部出现一键入口;
//   确认后逐份 DeleteRecoveryBranch(回收语义)。
{
  const deleted: string[] = [];
  stubBridge({
    ListRecoveryCopyGroups: async () => [
      { ...GROUP, copies: [{ ...GROUP.copies[0], unique: 0, shared: 3 }] },
    ],
    DeleteRecoveryBranch: async (path: string) => {
      deleted.push(path);
    },
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  const cleanupBtn = document.querySelector<HTMLButtonElement>(".rc-cleanup button");
  ok(Boolean(cleanupBtn), "⑧ the one-click cleanup appears at the table bottom for covered copies");
  ok((cleanupBtn?.textContent || "").includes("(1)"), "⑧ the cleanup button names the copy count");
  ok((cleanupBtn?.title || "").includes("recoverable trash"), "⑧ the cleanup hover names the recycle semantics");
  await act(async () => { cleanupBtn?.click(); });
  await flush();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "⑧ cleanup asks for confirmation first");
  ok(deleted.length === 0, "⑧ nothing is deleted before the user confirms");
  await act(async () => {
    (document.querySelector(".reasonix-confirm-dialog .btn--danger") as HTMLButtonElement)?.click();
  });
  await flush();
  ok(deleted.length === 1 && deleted[0] === COPY_PATH, "⑧ confirming archives the covered copy through the branch-delete path");
  await act(async () => root.unmount());
}

// ⑧b 无多余副本(未测量/有独有内容)时一键入口不渲染。
{
  stubBridge({});
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  ok(!document.querySelector(".rc-cleanup"), "⑧b no cleanup entry when no copy is fully covered");
  await act(async () => root.unmount());
}

// ⑨ 设置页入口文案(点7):标题与描述改「对话版本管理」句。
{
  stubBridge({});
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  const title = document.querySelector(".settings-section__title")?.textContent || "";
  const desc = document.querySelector(".settings-section__desc")?.textContent || "";
  ok(title === "Conversation version management", `⑨ the settings entry reads as conversation version management (got: ${title})`);
  ok(desc.includes("versions and copies") && desc.includes("restoring the main line"), `⑨ the entry description states the purpose (got: ${desc})`);
  const modalTitle = (await (async () => {
    await act(async () => { buttonByText("Review and merge")?.click(); });
    await flush();
    return document.querySelector(".rc-modal__title")?.textContent || "";
  })());
  ok(modalTitle === "Conversation version management", `⑨ the panel title carries the same wording (got: ${modalTitle})`);
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
