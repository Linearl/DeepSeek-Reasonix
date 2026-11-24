// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1797-tab-hover-card.test.tsx
//
// tl1797：对话 tab 悬停卡片（三区块默认）验收钉。
//   ① 纯模型层（lib/tabHoverCard.ts，无 React 挂载）：三区块逐行钉——身份区
//      （完整标题/工作区·分支/最后活跃）、运行区（状态/已耗时/模型·力度/模式）、
//      消耗区（上下文含距压缩阈值/会话成本/总轮次）；缺数据的行不渲染
//      （不硬造）；断开/待确认压过运行态（tl1266/任务730 同序）。
//   ② 渲染层（JSDOM 真挂载 TabBar，locale 钉 zh）：hover 400ms 档前不弹卡、
//      到档后 body portal 卡出现且三区块 data-section 齐全；悬停中该签原生
//      title 置空（防双 tooltip）、移出即收卡且 title 恢复；resolver 抛错时
//      卡片仍渲染直读区块（消耗区缺席，不整卡失败）。
//   ③ CSS 落点钉：anywhere 断词 + pointer-events:none + position:fixed
//      （tl1769 同款弹出层纪律）。
// React onMouseEnter 委托自 mouseover（任务1033 先例：capsule-panel.test.tsx），
// 触发一律 mouseover/mouseout + bubbles:true + relatedTarget:null。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// locale 钉 zh：i18n readPref 恒空、detectLocale 读 navigator.language
// （i18n.tsx:54-59），jsdom 默认 en-US → 覆写 navigator.language。
// react-dom import 期读 navigator.userAgent（Chrome 判定），故带齐常用字段
// 而非裸 {language}——上一轮实测缺 userAgent 直接炸 react-dom 装载。
{
  const nav = dom.window.navigator;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { language: "zh-CN", userAgent: nav.userAgent, platform: nav.platform, vendor: nav.vendor },
  });
}
dom.window.localStorage.setItem("reasonix-lang", "zh");
globalThis.window.requestAnimationFrame = ((cb: FrameRequestCallback) =>
  setTimeout(() => cb(dom.window.performance.now()), 0)) as unknown as typeof window.requestAnimationFrame;
globalThis.window.cancelAnimationFrame = ((id: number) => clearTimeout(id)) as unknown as typeof window.cancelAnimationFrame;
(dom.window.Element.prototype as unknown as { scrollIntoView?: () => void }).scrollIntoView = () => {};

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const hit = actual === expected;
  process.stdout.write(`  ${hit ? "PASS" : "FAIL"}  ${label}${hit ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}\n`);
  if (hit) passed += 1; else { failed += 1; process.exitCode = 1; }
}

const { createElement } = await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { LocaleProvider, useT, preloadLocale } = await import("../lib/i18n");
// 预载 zh 词典：LocaleProvider 的版本 bump 依赖 effect→import 链，探针实测
// 该链在 unmount 场景会被 cancelled 掐断（时序不确定）→ 直接预载模块级
// DICTS，translate 首渲染即命中 zh，测试零时序依赖。
await preloadLocale("zh");
const { tabHoverCardModel, tabFullTitle, findProjectNodeForTab } = await import("../lib/tabHoverCard");
const { TabBar } = await import("../components/TabBar");
import type { ProjectNode, TabMeta } from "../lib/types";

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

let nextIndex = 0;
function makeTab(overrides: Partial<TabMeta> = {}): TabMeta {
  nextIndex += 1;
  return {
    id: `tab-${nextIndex}`,
    scope: "project",
    workspaceRoot: "/ws",
    workspaceName: "全局工作区",
    topicId: `topic-${nextIndex}`,
    topicTitle: `会话标题${nextIndex}`,
    label: `会话标题${nextIndex}`,
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    active: false,
    cwd: "/ws",
    ...overrides,
  } as TabMeta;
}

type Sections = ReturnType<typeof tabHoverCardModel>;
type Extra = Parameters<typeof tabHoverCardModel>[1];

// 纯模型探针：LocaleProvider 下调 useT 取真实译者喂 tabHoverCardModel。
async function modelOf(tab: TabMeta, extra: Extra): Promise<Sections> {
  let sections: Sections | null = null;
  const holder = document.createElement("div");
  document.body.appendChild(holder);
  const root = createRoot(holder);
  function Probe() {
    const t = useT();
    sections = tabHoverCardModel(tab, extra, t, NOW);
    return null;
  }
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(Probe)));
  });
  await act(async () => { root.unmount(); });
  holder.remove();
  return sections as Sections;
}

const NOW = 1_700_000_000_000;

function rowOf(sections: Sections, section: "identityRows" | "runtimeRows" | "consumptionRows", key: string) {
  return (sections[section] ?? []).find((row) => row.key === key);
}

{
  process.stdout.write("① 纯模型层：三区块逐行钉\n");
  const tab = makeTab({
    gitBranch: "feat-x",
    running: true,
    turnStartedAt: NOW - 90_000,
    collaborationMode: "autopilot",
    autopilot: true,
    toolApprovalMode: "auto",
  });
  const sections = await modelOf(tab, {
    lastActivityAt: NOW - 3 * 60 * 1000,
    turns: 12,
    modelRef: "deepseek/deepseek-v4-flash",
    effortLabel: "high",
    context: { used: 64_000, window: 128_000, compactRatio: 0.8 },
    sessionCost: 1.25,
    sessionCurrency: "CNY",
  });

  ok(sections.identityTitle.startsWith("全局工作区 / 会话标题"), "身份区标题=完整 工作区 / 标题");
  eq(rowOf(sections, "identityRows", "workspace")?.value, "全局工作区 · feat-x", "身份区行：工作区+分支");
  const activity = rowOf(sections, "identityRows", "activity")?.value ?? "";
  ok(activity.length > 0 && (activity.includes("分钟") || activity.includes("分钟前")), `身份区行：最后活跃相对时间档（got: ${activity}）`);

  eq(rowOf(sections, "runtimeRows", "state")?.value, "运行中", "运行区状态=运行中");
  eq(rowOf(sections, "runtimeRows", "state")?.tone, "running", "运行区状态 tone=running");
  const elapsed = rowOf(sections, "runtimeRows", "elapsed")?.value ?? "";
  ok(elapsed.length > 0 && elapsed.includes("分"), `运行区行：已耗时 90s 落分钟档（got: ${elapsed}）`);
  ok(rowOf(sections, "runtimeRows", "model")!.value.includes("deepseek-v4-flash"), "运行区行：模型 ref/display");
  ok(rowOf(sections, "runtimeRows", "model")!.value.includes("high"), "运行区行：力度档并入模型行（仅活跃会话可得）");
  ok((rowOf(sections, "runtimeRows", "modes")?.value ?? "").length > 0, "运行区行：模式态（autopilot+自动审批）");

  const context = rowOf(sections, "consumptionRows", "context")!.value;
  ok(context.includes("64K"), `消耗区行：已用 tokens 紧凑格式（got: ${context}）`);
  ok(context.includes("50%"), "消耗区行：占用百分比");
  ok(context.includes("38.4K"), "消耗区行：距压缩阈值余量（阈值 128K×0.8=102.4K − 已用 64K = 38.4K）");
  ok((rowOf(sections, "consumptionRows", "cost")?.value ?? "").length > 0, "消耗区行：会话成本（本轮成本桥上不可得，如实命名会话成本）");
  ok((rowOf(sections, "consumptionRows", "turns")?.value ?? "").includes("12"), "消耗区行：总轮次 12");

  // 缺数据不硬造：无 extra 时消耗区空、模型/最后活跃行缺席；直读字段照常。
  const bare = await modelOf(tab, undefined);
  eq(bare.consumptionRows.length, 0, "无补充数据：消耗区不渲染（不硬造读数）");
  eq(rowOf(bare, "runtimeRows", "model"), undefined, "无补充数据：模型行缺席");
  eq(rowOf(bare, "identityRows", "activity"), undefined, "无补充数据：最后活跃行缺席");
  eq(rowOf(bare, "runtimeRows", "elapsed")?.key, "elapsed", "直读字段照常：已耗时来自 TabMeta.turnStartedAt");

  // 状态互斥序：断开 > 待确认 > 运行（tl1266/任务730 同序）。
  const missing = await modelOf(makeTab({ running: true, runtime: { phase: "ready", epoch: "e", issue: { code: "session_missing", detail: "" } } }), undefined);
  eq(rowOf(missing, "runtimeRows", "state")?.value, "已断开", "断开态压过运行态");
  eq(rowOf(missing, "runtimeRows", "state")?.tone, "attention", "断开态 tone=attention");
  const waiting = await modelOf(makeTab({ running: true, pendingPrompt: true }), undefined);
  eq(rowOf(waiting, "runtimeRows", "state")?.value, "待确认", "待确认压过运行态");

  // 创建时模型标注（tl1387 链）：无 live modelRef → metaModel 兜底行渲染。
  const createdAt = await modelOf(makeTab(), { metaModel: "glm/glm-5", turns: 3 });
  ok((rowOf(createdAt, "runtimeRows", "model")?.value ?? "").length > 0, "创建时模型行渲染（metaModel 兜底）");
  ok((rowOf(createdAt, "runtimeRows", "model")?.value ?? "").includes("创建时"), "创建时模型走 tl1387 既有标注文案");

  // 树查找：topicId 命中嵌套节点 + file 型直拒。
  const tree: ProjectNode[] = [{ key: "p", kind: "project", label: "P", root: "/ws", children: [
    { key: "t", kind: "topic", label: "T", topicId: "topic-999", lastActivityAt: 123, turns: 7, modelRef: "m/r" },
  ] }];
  eq(findProjectNodeForTab(tree, makeTab({ topicId: "topic-999" }))?.turns, 7, "树查找按 topicId 命中嵌套节点");
  eq(findProjectNodeForTab(tree, makeTab({ tabType: "file", scope: "file", filePath: "/ws/a.go" })), undefined, "file 型 tab 无会话节点");
  eq(findProjectNodeForTab(tree, makeTab({ topicId: "topic-nope", sessionPath: "/ws/s.jsonl" })), undefined, "无命中返回 undefined（不猜测）");
  eq(tabFullTitle(makeTab({ scope: "global", workspaceName: "Global", topicTitle: "G1" })), "Global / G1", "global 作用域完整标题");
}

// ── ② 渲染层 ─────────────────────────────────────────────────────────────
interface RenderResult {
  tabs: () => HTMLElement[];
  card: () => HTMLElement | null;
  titleOf: (tab: HTMLElement) => string | null;
  hover: (tab: HTMLElement) => void;
  leave: (tab: HTMLElement) => void;
  unmount: () => Promise<void>;
}

async function renderTabBar(opts: { resolver?: (tab: TabMeta) => Promise<unknown> | unknown } = {}): Promise<RenderResult> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const tabs = [
    makeTab({ topicTitle: "一个特别特别长的会话标题其实还没有结束".repeat(2) }),
    makeTab(),
  ];
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(TabBar, {
      tabs,
      activeTabId: undefined,
      onTabChange: () => {},
      onTabClose: () => {},
      onTabsClose: () => {},
      onTabsReorder: () => {},
      onNewTab: () => {},
      ...(opts.resolver ? { onFetchTabHoverInfo: opts.resolver } : {}),
    } as never)));
  });
  return {
    tabs: () => Array.from(container.querySelectorAll<HTMLElement>(".tabbar__tab")),
    card: () => document.querySelector<HTMLElement>("[data-tab-hover-card]"),
    titleOf: (tab) => tab.getAttribute("title"),
    hover(tab) { tab.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true, relatedTarget: null })); },
    // 移出走仓内既有约定：mouseout + relatedTarget:document.body（session-wall/
    // guidance-hover-preview 同款；null 相关目标不构成「移到别处」语义）。
    leave(tab) { tab.dispatchEvent(new dom.window.MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })); },
    async unmount() { await act(async () => { root.unmount(); }); container.remove(); },
  };
}

{
  process.stdout.write("② 渲染层：延迟弹出/三区块/双 tooltip 防护/移出收卡\n");
  let resolverCalls = 0;
  const view = await renderTabBar({
    resolver: () => {
      resolverCalls += 1;
      return {
        lastActivityAt: NOW - 60_000,
        turns: 5,
        modelRef: "deepseek/deepseek-v4-flash",
        effortLabel: "mid",
        context: { used: 10_000, window: 100_000, compactRatio: 0.8 },
        sessionCost: 0.5,
        sessionCurrency: "USD",
      };
    },
  });
  const first = view.tabs()[0]!;
  eq(view.card(), null, "初始无卡片");

  view.hover(first);
  await act(async () => { await sleep(200); });
  eq(view.card(), null, "200ms 未到 400ms 档：卡片不弹（划过不弹卡）");

  await act(async () => { await sleep(400); });
  const card = view.card();
  ok(card !== null, "400ms 档：卡片弹出（body portal）");
  if (card) {
    eq(card.querySelectorAll('[data-section="identity"]').length, 1, "身份区块渲染");
    eq(card.querySelectorAll('[data-section="runtime"]').length, 1, "运行区块渲染");
    eq(card.querySelectorAll('[data-section="consumption"]').length, 1, "消耗区块渲染");
    const titleText = card.querySelector(".tabbar__hover-card-title")?.textContent ?? "";
    ok(titleText.startsWith("全局工作区 / 一个特别特别长") && titleText.includes("还没有结束"), "卡片首行=完整标题（工作区前缀+全文，不截断）");
    const cardText = card.textContent ?? "";
    ok(cardText.includes("10K") && cardText.includes("10%"), "消耗区上下文行落地（占用+百分比）");
    ok(cardText.includes("70K"), "消耗区距压缩阈值余量落地（阈值 100K×0.8=80K − 已用 10K = 70K）");
    ok(cardText.includes("5"), "消耗区轮次行落地");
  }

  eq(view.titleOf(first), null, "悬停中该签原生 title 置空（防双 tooltip）");
  const second = view.tabs()[1]!;
  ok(view.titleOf(second) !== null, "非悬停签原生 title 保留（压缩档 hover title 语义不变）");
  ok(resolverCalls >= 1, "resolver 被调用（补充数据拉取）");

  view.leave(first);
  await act(async () => { await sleep(30); });
  eq(view.card(), null, "移出即收卡");
  ok(view.titleOf(first) !== null, "移出后原生 title 恢复");
  await view.unmount();
}

{
  process.stdout.write("③ resolver 失败降级：卡片仍渲染直读区块\n");
  const view = await renderTabBar({ resolver: () => { throw new Error("boom"); } });
  const first = view.tabs()[0]!;
  view.hover(first);
  await act(async () => { await sleep(500); });
  const card = view.card();
  ok(card !== null, "resolver 抛错：卡片不消失");
  if (card) {
    eq(card.querySelectorAll('[data-section="identity"]').length, 1, "身份区块仍渲染（TabMeta 直读）");
    eq(card.querySelectorAll('[data-section="runtime"]').length, 1, "运行区块仍渲染（TabMeta 直读）");
    eq(card.querySelectorAll('[data-section="consumption"]').length, 0, "消耗区块缺席（不硬造读数）");
  }
  view.leave(first);
  await act(async () => { await sleep(20); });
  await view.unmount();
}

// ── ④ CSS 落点钉：anywhere 断词 + pointer-events:none（tl1769 同款） ───────
{
  process.stdout.write("④ CSS 落点钉\n");
  const cssPath = join(dirname(fileURLToPath(import.meta.url)), "..", "styles.css");
  const css = readFileSync(cssPath, "utf8");
  const start = css.indexOf(".tabbar__hover-card {");
  const titleStart = css.indexOf(".tabbar__hover-card-title");
  const sectionStart = css.indexOf(".tabbar__hover-card-section");
  const valueStart = css.indexOf(".tabbar__hover-card-row-value");
  ok(start > 0 && titleStart > start, "styles.css 存在 tl1797 卡片段");
  const cardBlock = css.slice(start, titleStart);
  ok(cardBlock.includes("pointer-events: none"), "卡片 pointer-events:none（不抢悬停）");
  ok(cardBlock.includes("position: fixed"), "卡片 position:fixed（body portal 定位层）");
  ok(css.slice(titleStart, sectionStart).includes("overflow-wrap: anywhere"), "标题 anywhere 断词（长标题换行不溢出）");
  ok(css.slice(valueStart).includes("overflow-wrap: anywhere"), "行值 anywhere 断词");
}

process.stdout.write(`\ntl1797-tab-hover-card: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
