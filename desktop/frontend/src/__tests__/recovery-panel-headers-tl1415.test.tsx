// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/recovery-panel-headers-tl1415.test.tsx
// (vitest runs this file as a false red — the project's component harnesses are
// tsx-runner files; see recovery-branch-delete-1064.test.tsx for the pattern.)
//
// tl1415 harness:「对话版本管理」面板表格化改造的验收钉。
// tl1415b 一轮（demo v5，docs/drafts/版本面板三表合一-demo-20261106.html）
// 全文件按新形态改写——选择器改表口径（与 1064 同款先例）。覆盖:
//  ① 唯一表头(.rc-maintable__head th)sticky + 不透明背景 + 保持完整(用户
//     截图实证的「表头下滑与内容重叠」修复;tl1415b: 全页一张表,表头随表)。
//  ② 全页唯一一张表且表头列名语义化(「消息数 · 轮次」合并列/类型列/行内
//     操作列——demo v5 列编排)。
//  ③ 全部操作按钮带 hover 功能说明(title 属性)——含「修复并瘦身」的两动作
//     说明(剥离图片 payload + 折叠超窗旧消息)。
//  ④ 「消息数 · 轮次」列实测(会话行 "12 · 6"/副本行 "3 · 2"),未扫描给
//     「unmeasured」状态而非裸「—」。
//  ⑤ 副本行的文件名短标识 recovery-<8hex>。
//  ⑥ 降级/空预览的行内空态与「无预览文本」区分(tl1360 缺陷②)。
//  ⑦ 清空合并历史入口(点9→tl1415b 批量沉底):勾选会话后底部入口可用,
//     确认后调用后端。
//  ⑧ 清理非主线副本(点8→tl1415b 改形):勾选会话后底部入口可用,二次确认
//     后逐份 DeleteRecoveryBranch(回收语义,主线保留)。
//  ⑨ 设置页入口文案(点7):标题「Conversation version management」+描述。
//  ⑩ tl1415b 形态断言:单表/组段折叠(已合入默认折叠+计数徽章)/步骤号按钮
//     (①扫描②切换③合成)+hintline/清理非主线副本按钮/尾部预览(sub2 带
//     「尾部」徽章+弹窗开头摘要行)。
//
// tl1415b-2 二轮返修（20261106 用户装机实测四点打回）新增钉:
//  A(点1) 行内尾部预览 3 行截断(rc-clip3,原 rc-clip2 2 行退役)+预览行 hover
//     title 给行内没有的信息(路径/更新时间/读数/体积——不再复读行内文本)。
//  B(点2) 会话行恢复默认折叠——组段展开≠子行平铺,点会话行展开钮
//     (.rc-session__toggle, aria-expanded)才见主线/副本/版本子行。
//  C(点3) 主线行复选框=选定 HEAD(底部③「以选定 HEAD 合成最长链」由此可用);
//     行内「合成」按钮删除——合成钮唯一、留底部(用户裁决)。
//  D(点4) 版本行(tr.rc-chain)错排根治——旧链行 flex 布局(display:flex/gap/
//     padding)挂在 tr 上拆掉单元格栅格:CSS 静态断言 display 级属性退役、
//     「更长」高亮挂 td、预览行布局归 rc-sub2+rc-clip3;DOM 断言版本行 9 格
//     完整入栅格。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

// ─── 静态样式断言(JSDOM 无布局引擎;command-palette-css.test.ts 模具) ───

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

console.log("\nrecovery panel header/hover contract (tl1415 + tl1415b + tl1415b-2)");

// ① 唯一表头 sticky + 不透明背景(tl1415b: 表头随单表走 .rc-maintable__head th)。
{
  const headBlocks = selectorBlocks(styles, ".rc-maintable__head th").join("\n");
  ok(/position:\s*sticky/.test(headBlocks), "① the single-table header is sticky");
  ok(/z-index:\s*var\(--z-inline-sticky\)/.test(headBlocks), "① the single-table header raises above rows");
  ok(/background:\s*var\(--bg-elev\)/.test(headBlocks), "① the single-table header carries the opaque modal background");
  ok(!/background:\s*inherit/.test(headBlocks), "① the header no longer inherits a transparent background");
  // 整行 opacity 会把背景一起半透明化(透出下方滚动内容)——文字弱化必须走 color。
  ok(!/(^|[;\n])opacity:\s*0?\.72/.test(headBlocks), "① the header row no longer dims via row-level opacity");
}

// tl1524 数值列头/内容严格对齐:表挂双类名(rc-versions__table rc-maintable),
// 旧块 `.rc-versions__table td { text-align: left }`(特异性 0,1,1)按特异性压过
// 裸 `.rc-num { text-align: right }`(0,1,0)——CSS 特异性覆盖坑(同属性两规则
// 胜负看特异性,与书写意图无关),形态=表头右对齐/数值左对齐错位。正文右对齐
// 必须走带前缀的 0,2,1 规则,与表头覆写同形态;两条任一缺失即回潮。
{
  const headNumBlocks = selectorBlocks(styles, ".rc-maintable__head th.rc-num").join("\n");
  ok(/text-align:\s*right/.test(headNumBlocks), "tl1524 the numeric column headers right-align");
  const bodyNumBlocks = selectorBlocks(styles, ".rc-maintable td.rc-num").join("\n");
  ok(/text-align:\s*right/.test(bodyNumBlocks), "tl1524 numeric body cells right-align via the 0,2,1 scoped rule (旧块压裸类判例)");
}

// A(点1) 预览行 3 行截断:rc-clip3 上位,rc-clip2 退役。
{
  const clip3Blocks = selectorBlocks(styles, ".rc-clip3").join("\n");
  ok(/-webkit-line-clamp:\s*3/.test(clip3Blocks), "A① the inline tail preview clamps to three lines (rc-clip3)");
  ok(clip3Blocks.length > 0, "A① the three-line clip class exists");
  ok(selectorBlocks(styles, ".rc-clip2").length === 0, "A① the two-line clip class is retired (用户实测 2 行浪费空间)");
}

// D(点4) 版本行错排根治:tr.rc-chain 不再携带旧链行 flex 布局;高亮挂 td。
{
  const chainBlocks = selectorBlocks(styles, ".rc-chain").join("\n");
  ok(chainBlocks.length > 0, "D④ the version-row class (tr.rc-chain) keeps its identity (回归套件钉)");
  ok(!/display:\s*flex/.test(chainBlocks), "D④ the version row drops the old flex layout (错排根因: flex-on-tr 拆栅格)");
  ok(!/flex-wrap|gap:/.test(chainBlocks), "D④ the version row drops the old flex gap/wrap");
  ok(selectorBlocks(styles, ".rc-chain--recommended > td").length > 0, "D④ the longer-chain highlight hangs on the cells (行级 box-shadow 在表格行不可靠)");
  ok(!/display:\s*flex/.test(selectorBlocks(styles, ".rc-chain__preview").join("")), "D1446b the preview line drops the old flex layout (class name stays as hook, color-only rule)");
  ok(!/white-space:\s*nowrap/.test(selectorBlocks(styles, ".rc-chain__preview").join("")), "D1446b the preview line drops the old nowrap (layout moved to rc-sub2+rc-clip3)");
}

// ─── 组件渲染断言(② 单表+列名;③ hover title;⑩ 形态;A-D 四点) ───

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
  // tl1415d9: 会话行展开态现在跨挂载持久(用户二轮裁决)。每个场景声明
  // 全新档案——上一场景的展开不得流进下一场景的「默认折叠」钉。
  window.localStorage.clear();
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

/** openPanel 打开面板;tl1415b-2 会话行默认折叠——子行(主线/副本/版本)要点
 *  会话行展开钮才出现(点2)。需要子行的场景接着调 expandSession()。 */
async function openPanel(): Promise<ReturnType<typeof createRoot>> {
  stubBridge({});
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  return root;
}

/** expandSession 点开第 index 个会话行(点2 的展开钮);链清单是展开后惰性
 *  加载的,双 flush 等它落地。 */
async function expandSession(index = 0): Promise<void> {
  const toggle = document.querySelectorAll<HTMLButtonElement>("tr.rc-session .rc-session__toggle")[index];
  await act(async () => { toggle?.click(); });
  await flush();
  await flush();
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

async function checkSessionRow(): Promise<void> {
  const checkbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  await act(async () => { checkbox?.click(); });
  await flush();
}

// ②+⑩+B 单表到底:模态内唯一一张 table;会话行默认折叠(点2),展开钮点开
//   才见子行;thead 列名语义化(demo v5 列编排)。
{
  const root = await openPanel();
  const tables = document.querySelectorAll(".rc-modal table");
  ok(tables.length === 1, "② the whole panel renders exactly ONE table (tl1415b 单表到底)");
  const thead = document.querySelector(".rc-maintable thead");
  ok(Boolean(thead), "② the single table renders a thead");
  const headerTexts = Array.from(thead?.querySelectorAll("th") ?? []).map((th) => (th.textContent || "").trim());
  ok(headerTexts.includes("kind"), "② the header names the kind column (类型列)");
  ok(headerTexts.includes("messages · turns"), "② the header merges message count and turns into one named column");
  ok(headerTexts.includes("size"), "② the header names the size column");
  ok(headerTexts.includes("modified"), "② the header names the modified column");
  ok(headerTexts.includes("row actions"), "② the header names the row-actions column");

  // ⑩ 组段行:两组(待处理默认展开/已合入默认折叠)+计数徽章。
  const groupRows = Array.from(document.querySelectorAll("tr.rc-grouprow"));
  ok(groupRows.length === 2, "⑩ two collapsible group rows render inside the table (待处理/已合入)");
  ok((groupRows[0]?.textContent || "").includes("Pending"), "⑩ the first group row is the Pending group");
  ok((groupRows[1]?.textContent || "").includes("Merged"), "⑩ the second group row is the Merged group");
  ok(Boolean(groupRows[0]?.querySelector(".rc-grouprow__count")), "⑩ the group row carries a count badge");
  const sessionRows = document.querySelectorAll("tr.rc-session");
  ok(sessionRows.length === 1, "⑩ the Pending group is expanded by default (session row visible)");
  ok(Boolean(document.querySelector("tr.rc-session .rc-kindtag--session")), "⑩ the session row carries the session kind tag (类型列)");
  // 已合入组默认折叠:该 fixture 无已合入会话,组段行的 caret 指向折叠态(▸)。
  const mergedCaret = groupRows[1]?.querySelector(".rc-caret")?.textContent || "";
  ok(mergedCaret.trim() === "▸", "⑩ the merged group starts collapsed (caret points to ▸)");

  // B(点2) 会话行默认折叠:子行(主线/副本/版本)不渲染;展开钮带 aria-expanded。
  ok(document.querySelectorAll("tr.rc-row--copy").length === 0, "B② session rows start collapsed (copy rows hidden until expanded)");
  ok(document.querySelectorAll("tr.rc-detail__row--main").length === 0, "B② session rows start collapsed (main row hidden until expanded)");
  ok(document.querySelectorAll("tr.rc-chain").length === 0, "B② session rows start collapsed (version rows hidden until expanded)");
  const sessionToggle = sessionRows[0]?.querySelector<HTMLButtonElement>(".rc-session__toggle");
  ok(Boolean(sessionToggle), "B② the session row carries its expand toggle");
  ok(sessionToggle?.getAttribute("aria-expanded") === "false", "B② the toggle reports aria-expanded=false while collapsed");
  await expandSession();
  ok(document.querySelectorAll("tr.rc-row--copy").length === 1, "B② clicking the session toggle reveals the child rows");
  ok(sessionRows[0]?.querySelector<HTMLButtonElement>(".rc-session__toggle")?.getAttribute("aria-expanded") === "true", "B② the toggle reports aria-expanded=true once expanded");

  // ④ 会话行「消息数 · 轮次」列放实测读数 "12 · 6"(不再拿消息数冒充轮次)。
  const sessionCells = Array.from(sessionRows[0]?.querySelectorAll("td") ?? []).map((td) => (td.textContent || "").trim());
  ok(sessionCells[5] === "12 · 6", `④ the session row's messages·turns cell shows measured readings (got: ${sessionCells[5]})`);
  ok(sessionCells[6] === "×1", `④ the session row's copies cell counts the copies (got: ${sessionCells[6]})`);

  // A(点1) 会话行 hover 真信息:label 之外补路径与更新时间(clip1 未截断时
  //   纯 label 的 hover 与可见文本相同 = 无效 hover)。时间走本机时区渲染,
  //   断言形如 YYYY-MM-DD HH:mm 而非固定 UTC 串。
  const sessionTitle = sessionRows[0]?.querySelector(".rc-col-name")?.getAttribute("title") || "";
  ok(sessionTitle.includes(MAIN_PATH), "A① the session row's hover carries the file path (行内没有的信息)");
  ok(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(sessionTitle), "A① the session row's hover carries the modified time");

  // ③ hover:行内扫描按钮(第①步说明含 read-only)。
  const scanBtn = sessionRows[0]?.querySelector<HTMLButtonElement>(".rc-ops button");
  ok((scanBtn?.title || "").includes("Step ①"), "⑩ the row scan button's hover carries the step-① marker");
  ok((scanBtn?.title || "").includes("read-only"), "③ the row scan button carries a hover description");

  // ③ 点5 覆盖收口:会话行的合并勾选框也带 hover 说明(勾选语义)。
  const mergeCheckbox = document.querySelector<HTMLInputElement>("tr.rc-session input[type=checkbox]");
  ok((mergeCheckbox?.title || "").includes("merge-selected"), "③ the merge checkbox carries a hover description");

  // ③ hover:底部修复并瘦身按钮(两动作说明)。
  const dietBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => (b.title || "").includes("strip image payloads"),
  );
  ok(Boolean(dietBtn), "③ the repair+diet button's hover names both of its actions (strip payloads + fold)");

  // ⑤+⑥ 链行(headId deadbeef,未被副本行并走——fixture 副本无链数据)带短
  //   标识所在副本行;「forked」预览在版本行 sub2。
  const copyRow = document.querySelector("tr.rc-row--copy");
  ok(Boolean(copyRow), "④ the copy row renders inline in the single table");
  const copyCells = Array.from(copyRow?.querySelectorAll("td") ?? []).map((td) => (td.textContent || "").trim());
  ok(copyCells[5] === "3 · 2", `④ the copy row's messages·turns cell shows measured readings (got: ${copyCells[5]})`);

  // ⑤ 副本行带文件名短标识 recovery-<8hex>(用户把面板行对回磁盘文件的锚点)。
  const shortName = copyRow?.querySelector(".rc-copy__shortname");
  ok((shortName?.textContent || "") === "recovery-01234567", `⑤ the copy row carries the short filename signature (got: ${shortName?.textContent})`);
  ok(((shortName as HTMLElement)?.title || "").endsWith("gc-recovery-0123456789abcdef.jsonl"), "⑤ the short signature's hover title carries the full filename");

  // ⑥ 候选链(preview: "forked")行内预览文本在版本行 sub2,不再是「无预览
  //    文本」占位。D(点4):版本行 9 格完整入栅格,类型标签在格内(错排修复
  //    后的结构面)。
  const versionRow = document.querySelector("tr.rc-chain");
  ok(Boolean(versionRow), "D④ the version row renders in the single table");
  ok(versionRow?.querySelectorAll("td").length === 9, "D④ the version row renders exactly 9 cells in the grid (错排修复:每列一格)");
  ok(Boolean(versionRow?.querySelector("td.rc-col-kind .rc-kindtag--version")), "D④ the version kind tag lives inside its cell (不再孤悬)");
  const previewSpan = versionRow?.querySelector(".rc-chain__preview");
  ok((previewSpan?.textContent || "").includes("forked"), `⑥ the head row shows the inline preview text (got: ${previewSpan?.textContent})`);
  ok((previewSpan?.querySelector(".rc-tailtag")?.textContent || "") === "tail", "⑩ the inline preview line carries the tail badge (预览=尾部消息)");

  // A(点1) 预览行 hover 真信息:三行类行(主线/副本/版本)的 sub2 title 都要
  //   比行内文本多给信息(路径/更新时间/读数/体积),不再复读行内文本。
  const mainSub2 = document.querySelector("tr.rc-detail__row--main .rc-sub2");
  const mainSub2Title = mainSub2?.getAttribute("title") || "";
  ok(mainSub2Title.includes(MAIN_PATH), "A① the main row's preview hover carries the file path");
  ok(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(mainSub2Title), "A① the main row's preview hover carries the modified time");
  ok(mainSub2Title.includes("messages · turns 12 · 6"), "A① the main row's preview hover carries the message/turns readings");
  ok(mainSub2Title.includes("1.0 KB"), "A① the main row's preview hover carries the size reading");
  ok(mainSub2Title.length > (mainSub2?.textContent || "").length + 20, "A① the preview hover gives strictly more than the inline text (不复读行内)");
  const versionSub2Title = previewSpan?.getAttribute("title") || "";
  ok(versionSub2Title.includes(MAIN_PATH), "A① the version row's preview hover carries the file path");
  ok(versionSub2Title.length > (previewSpan?.textContent || "").length + 20, "A① the version row's preview hover gives more than the inline text");

  // ⑦ 清空合并历史:未勾选会话时底部入口禁用(fixture 无 versionGroups)。
  const clearBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Clear merge history"),
  );
  ok(Boolean(clearBtn?.disabled), "⑦ the clear-merge-history entry is disabled without an eligible checked conversation");

  // ⑧ 清理非主线副本入口在底部横排(清空合并历史左边,tl1415b 新按钮)。
  const cleanupBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Clean up non-main copies"),
  );
  ok(Boolean(cleanupBtn), "⑧ the clean-up-non-main-copies entry sits in the sunk footer row");
  ok(Boolean(cleanupBtn?.disabled), "⑧ it is disabled while no conversation is checked");
  const cleanupIdx = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).indexOf(cleanupBtn!);
  const clearIdx = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).indexOf(clearBtn!);
  ok(cleanupIdx >= 0 && clearIdx > cleanupIdx, "⑩ the cleanup button sits left of clear-merge-history (demo v5 编排)");

  // ⑩ 三步流程提示线在底部。
  const hintline = document.querySelector(".rc-modal__foot .rc-foot__hintline");
  ok((hintline?.textContent || "").includes("① Scan"), "⑩ the footer carries the three-step flow hintline (① Scan → ② pick → ③ merge)");

  // ⑨ 设置页入口与面板标题(点7)。
  const title = document.querySelector(".settings-section__title")?.textContent || "";
  ok(title === "Conversation version management", `⑨ the settings entry reads as conversation version management (got: ${title})`);

  await act(async () => root.unmount());
}

// B(点2) 折叠可逆:点展开钮收起子行,aria-expanded 跟随。
{
  const root = await openPanel();
  await expandSession();
  ok(document.querySelectorAll("tr.rc-row--copy").length === 1, "B② expanded once: the child rows render");
  const toggle = document.querySelector<HTMLButtonElement>("tr.rc-session .rc-session__toggle");
  await act(async () => { toggle?.click(); });
  await flush();
  ok(document.querySelectorAll("tr.rc-row--copy").length === 0, "B② clicking the toggle again re-hides the child rows");
  ok(toggle?.getAttribute("aria-expanded") === "false", "B② aria-expanded flips back to false");
  await act(async () => root.unmount());
}

// C(点3) 主线行复选框=选定 HEAD;行内合成钮删除、底部合成钮唯一。
{
  const root = await openPanel();
  await expandSession();
  const mainCheck = document.querySelector<HTMLInputElement>("tr.rc-detail__row--main input[type=checkbox]");
  ok(Boolean(mainCheck), "C③ the main row carries a checkbox (主线行可选中)");
  ok((mainCheck?.title || "").includes("merge HEAD"), "C③ the main-row checkbox hover names the HEAD-pick semantics");
  ok(mainCheck?.checked === false, "C③ the main-row checkbox starts unpicked");
  await act(async () => { mainCheck?.click(); });
  await flush();
  ok(mainCheck?.checked === true, "C③ ticking the main row picks it as the merge HEAD");
  await act(async () => { mainCheck?.click(); });
  await flush();
  ok(mainCheck?.checked === false, "C③ unticking clears the HEAD pick again");

  // C(点3) 合成钮唯一:行内(会话行)合成按钮删除,底部③保留。
  const sessionSynthBtns = Array.from(document.querySelectorAll<HTMLButtonElement>("tr.rc-session button")).filter(
    (b) => b.textContent?.includes("Synthesize"),
  );
  ok(sessionSynthBtns.length === 0, "C③ the session row no longer carries an inline synthesize button (用户裁决:删行内)");
  const footSynthBtns = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).filter(
    (b) => b.textContent?.includes("Merge into longest chain"),
  );
  ok(footSynthBtns.length === 1, "C③ exactly one synthesize entry remains, in the footer (保留底部)");
  ok((footSynthBtns[0]?.title || "").includes("Step ③"), "C③ the kept footer button still carries the step-③ hover");
  // 全局面唯一:匹配任一合成文案(底部按钮读作 "Merge into longest chain",
  // 行内旧钮读作 "Synthesize ..."——两者并计恰为 1 才算重复消除)。
  const allSynthEntries = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal button")).filter(
    (b) => b.textContent?.includes("Synthesize") || b.textContent?.includes("Merge into longest chain"),
  );
  ok(allSynthEntries.length === 1, "C③ the whole panel renders exactly one synthesize entry (行内+底部重复消除)");
  await act(async () => root.unmount());
}

// ⑩+A 步骤号按钮:②切换到此版本(副本行,hover 带第②步)——子行需先展开。
{
  const root = await openPanel();
  await expandSession();
  const copyRow = document.querySelector("tr.rc-row--copy");
  const switchBtn = Array.from(copyRow?.querySelectorAll<HTMLButtonElement>("button") ?? []).find(
    (b) => b.textContent?.includes("switch to this version"),
  );
  ok(Boolean(switchBtn), "⑩ the copy row carries the switch-to-this-version button (第②步)");
  ok((switchBtn?.title || "").includes("Step ②"), "⑩ the switch button's hover carries the step-② marker");
  const footMerge = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Merge into longest chain"),
  );
  ok(Boolean(footMerge), "⑩ the footer's step-③ merge button reads as the demo label");
  ok((footMerge?.title || "").includes("Step ③"), "⑩ the merge button's hover carries the step-③ marker");
  await act(async () => root.unmount());
}

// ④b 未扫描副本的「消息数 · 轮次」格给「unmeasured」状态而非裸「—」。
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
  await expandSession();
  const copyRow = document.querySelector("tr.rc-row--copy");
  const copyCells = Array.from(copyRow?.querySelectorAll("td") ?? []).map((td) => (td.textContent || "").trim());
  ok(copyCells[5] === "unmeasured", `④b an unscanned copy's messages·turns cell names the state (got: ${copyCells[5]})`);
  ok((copyRow?.querySelector("td:nth-child(6) span")?.getAttribute("title") || "").includes("Scan"), "④b the unmeasured cell's hover points at Scan");
  await act(async () => root.unmount());
}

// ⑥b 降级副本行(链枚举返回 degraded 行)的空态是「读数降级」,不冒充
//     「该分支无预览文本」——tl1415b: 降级链数据并入副本行的 sub2。
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
  await expandSession();
  const copyRow = document.querySelector("tr.rc-row--copy");
  const sub2 = copyRow?.querySelector(".rc-sub2")?.textContent || "";
  ok(sub2.includes("readings degraded"), `⑥b a degraded copy's inline preview says the readings are degraded (got: ${sub2})`);
  ok(!sub2.includes("no preview text"), "⑥b the degraded empty state does not claim the branch has no preview text");
  await act(async () => root.unmount());
}

// ⑩ 组迁移语义:已合入组默认折叠——点组段行展开,再点收起(计数徽章随组)。
//   fixture: 副本 unique=0(被主线覆盖)+ headCount=3(有合并历史)→ 已合入组。
{
  stubBridge({
    ListRecoveryCopyGroups: async () => [
      { ...GROUP, copies: [{ ...GROUP.copies[0], unique: 0, shared: 3 }] },
    ],
    ListSessionVersionGroups: async () => [
      { mainPath: MAIN_PATH, mainLabel: "GC", directory: "/sessions", headCount: 3, selected: "", heads: [] },
    ],
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  const groupRows = Array.from(document.querySelectorAll("tr.rc-grouprow"));
  const mergedRow = groupRows[1];
  ok((mergedRow?.querySelector(".rc-grouprow__count")?.textContent || "") === "1", "⑩ the merged group's badge counts its conversations");
  ok(document.querySelectorAll("tr.rc-session").length === 0, "⑩ the merged group starts collapsed (no session rows visible)");
  await act(async () => { (mergedRow as HTMLElement)?.click(); });
  await flush();
  ok(document.querySelectorAll("tr.rc-session").length === 1, "⑩ clicking the group row expands the merged group");
  await act(async () => { (mergedRow as HTMLElement)?.click(); });
  await flush();
  ok(document.querySelectorAll("tr.rc-session").length === 0, "⑩ clicking the group row again collapses it");
  await act(async () => root.unmount());
}

// ⑦ 清空合并历史(点9→tl1415b 批量):勾选 headCount>1 的会话后入口可用,
//   确认后按会话路径调用后端。
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
  const clearBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Clear merge history"),
  );
  // fixture: unique=2 副本 → 待处理组;勾选后批量入口按 headCount>1 启用。
  await checkSessionRow();
  ok(!clearBtn?.disabled, "⑦ the clear-merge-history entry enables for a checked headCount>1 conversation");
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

// ⑧ 清理非主线副本(点8→tl1415b 改形):勾选会话后入口可用;二次确认后
//   逐份 DeleteRecoveryBranch(回收语义;孤立副本不在目标里)。
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
  await checkSessionRow();
  const cleanupBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".rc-modal__foot button")).find(
    (b) => b.textContent?.includes("Clean up non-main copies"),
  );
  ok(Boolean(cleanupBtn && !cleanupBtn.disabled), "⑧ the cleanup enables once a conversation is checked");
  ok((cleanupBtn?.title || "").includes("recoverable trash"), "⑧ the cleanup hover names the recycle semantics");
  await act(async () => { cleanupBtn?.click(); });
  await flush();
  ok(Boolean(document.querySelector(".reasonix-confirm-dialog")), "⑧ cleanup asks for confirmation first (二次确认)");
  ok(deleted.length === 0, "⑧ nothing is deleted before the user confirms");
  await act(async () => {
    (document.querySelector(".reasonix-confirm-dialog .btn--danger") as HTMLButtonElement)?.click();
  });
  await flush();
  ok(deleted.length === 1 && deleted[0] === COPY_PATH, "⑧ confirming archives the non-main copy through the branch-delete path (主线保留)");
  await act(async () => root.unmount());
}

// ⑩ 尾部预览弹窗:结尾优先(尾部区在开头摘要行之前)。
{
  stubBridge({
    ListRecoveryChains: async () => ({
      ...CHAIN_SET,
      chains: [
        ...CHAIN_SET.chains,
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
    }),
    PreviewRecoveryChain: async () => ({
      path: COPY_PATH,
      isMain: false,
      messageCount: 12,
      turns: 6,
      firstUserText: "hello from the start",
      tailLines: ["tail line 1", "tail line 2"],
      sharedWithMain: 10,
      uniqueToChain: 2,
      lastActivity: "2026-10-21T00:33:00Z",
      degraded: false,
    }),
  });
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  await act(async () => { buttonByText("Review and merge")?.click(); });
  await flush();
  await expandSession();
  const copyRow = document.querySelector("tr.rc-row--copy");
  const previewBtn = Array.from(copyRow?.querySelectorAll<HTMLButtonElement>("button") ?? []).find(
    (b) => b.textContent?.includes("Preview"),
  );
  await act(async () => { previewBtn?.click(); });
  await flush();
  const dialog = document.querySelector(".rc-preview");
  ok(Boolean(dialog), "⑩ the preview dialog opens from the copy row");
  const bodyText = document.body.textContent || "";
  ok(bodyText.includes("tail line 1"), "⑩ the dialog shows the tail messages");
  ok(bodyText.includes("hello from the start"), "⑩ the dialog demotes the opening to a summary line (开头降为摘要)");
  const startNode = dialog?.querySelector(".rc-preview__start");
  const tailNode = dialog?.querySelector(".rc-preview__section--tail");
  ok(Boolean(startNode && tailNode), "⑩ both the tail section and the start summary render");
  // 结尾优先:尾部区在 DOM 序上先于开头摘要行。
  ok(!!(startNode && tailNode && (tailNode.compareDocumentPosition(startNode) & Node.DOCUMENT_POSITION_FOLLOWING)), "⑩ the tail section precedes the start summary (结尾置顶)");
  // A(点1):该 fixture 的副本行有链数据(sub2 渲染)——hover title 补行内没有
  //   的路径/时间,不复读行内文本。
  const copySub2 = copyRow?.querySelector(".rc-sub2");
  const copySub2Title = copySub2?.getAttribute("title") || "";
  ok(copySub2Title.includes(COPY_PATH), "A① the copy row's preview hover carries the file path");
  ok(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/.test(copySub2Title), "A① the copy row's preview hover carries the modified time");
  ok(copySub2Title.includes("2.0 KB"), "A① the copy row's preview hover carries the size reading");
  ok(copySub2Title.length > (copySub2?.textContent || "").length + 20, "A① the copy row's preview hover gives more than the inline text");
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
