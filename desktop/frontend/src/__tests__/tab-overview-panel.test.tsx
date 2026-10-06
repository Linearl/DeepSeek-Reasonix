// Run: tsx src/__tests__/tab-overview-panel.test.tsx
// 任务 552:标签页概览面板——真实挂载 TabOverviewPanel(jsdom),验收四条行为:
//   ① 打开面板后两组齐备(打开组/最近关闭组),空态有提示;
//   ② 行内 × 关闭不误触切换(close 调用、activate 不调用);
//   ③ 最近关闭条目点击 → onReopenClosedTab(带快照);
//   ④ 搜索 AND 过滤生效;Esc 关面板。
// harness 复刻 ask-card-identity.test.tsx(动态 import + 全局 Object.assign):
// react/react-dom 必须在全局 DOM 就位后再加载,否则 React 事件管线在 JSDOM 下
// 静默失联(onChange/Esc 均不触发);关闭断言要等满 AnchoredPopover 的关闭动画。
import { JSDOM } from "jsdom";
import type { TabMeta } from "../lib/types";
import type { RecentClosedTab } from "../lib/tabOverviewModel";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.SVGElement = dom.window.SVGElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis.window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: false, media: "", addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false, onchange: null }),
});

const { default: React, act } = await import("react");
const reactDomClient = await import("react-dom/client");
const { createRoot } = reactDomClient;
type Root = Awaited<ReturnType<typeof createRoot>>;
const { TabOverviewPanel } = await import("../components/TabOverviewPanel");
const { LocaleProvider } = await import("../lib/i18n");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function section(title: string) {
  process.stdout.write(`\n${title}\n`);
}

const tabMeta = (overrides: Partial<TabMeta> = {}): TabMeta => ({
  id: "tab-1",
  scope: "project",
  workspaceRoot: "/repo",
  workspaceName: "repo",
  topicId: "topic-1",
  topicTitle: "Deploy script",
  label: "model",
  ready: true,
  running: false,
  cancellable: false,
  mode: "normal",
  active: true,
  cwd: "/repo",
  ...overrides,
});

const root = createRoot(document.getElementById("root")!);

async function flush(ms = 10) {
  await new Promise((resolve2) => setTimeout(resolve2, ms));
}

// 关闭面板要走 AnchoredPopover 的关闭动画(matchMedia 关闭减少动效 → 140ms),
// 断言「面板已关」前必须等足时长。
async function flushClose() {
  await flush(260);
}

interface PanelProps {
  tabs?: TabMeta[];
  activeTabId?: string;
  recentClosedTabs?: RecentClosedTab[];
  onActivateTab: (id: string) => void;
  onCloseTab: (id: string) => void;
  onReopenClosedTab: (entry: RecentClosedTab) => void;
}

// 与 ask-card 同款:同一 root 反复 render 替换(不能 unmount 后复用 root,
// React 19 会抛 "Cannot update an unmounted root")。
async function renderPanel(props: PanelProps) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TabOverviewPanel
          tabs={props.tabs ?? []}
          activeTabId={props.activeTabId}
          recentClosedTabs={props.recentClosedTabs ?? []}
          onActivateTab={props.onActivateTab}
          onCloseTab={props.onCloseTab}
          onReopenClosedTab={props.onReopenClosedTab}
        />
      </LocaleProvider>,
    );
    await flush();
  });
}

function triggerButton(): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(".tab-overview__trigger");
  if (!button) throw new Error("missing tab overview trigger");
  return button;
}

async function openPanel() {
  await act(async () => {
    triggerButton().dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
}

function panelElement(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".tab-overview");
}

function items(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".tab-overview__item"));
}

async function typeInto(input: HTMLInputElement | null, text: string) {
  await act(async () => input?.focus());
  await act(async () => {
    Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!.call(input, text);
    input?.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });
}

section("面板打开:两组齐备 + 当前 tab 高亮");
{
  const activated: string[] = [];
  await renderPanel({
    tabs: [
      tabMeta({ id: "tab-1", topicTitle: "Deploy script" }),
      tabMeta({ id: "tab-2", topicTitle: "Research notes", active: false }),
    ],
    activeTabId: "tab-1",
    recentClosedTabs: [{ tab: tabMeta({ id: "old", topicTitle: "Old session" }), closedAt: Date.now() - 5_000 }],
    onActivateTab: (id) => activated.push(id),
    onCloseTab: () => {},
    onReopenClosedTab: () => {},
  });
  await openPanel();
  ok(panelElement() !== null, "点击触发按钮后面板打开");
  const titles = document.querySelectorAll<HTMLElement>(".tab-overview__group-title");
  ok(Array.from(titles).some((el) => el.textContent === "Open tabs"), "「打开的标签页」组头");
  ok(Array.from(titles).some((el) => el.textContent === "Recently closed"), "「最近关闭的标签页」组头");
  ok(items().length === 3, "两组共 3 行(2 打开 + 1 最近关闭)");
  ok(document.querySelector(".tab-overview__item--active") !== null, "当前 tab 有高亮行");
  ok(document.querySelector(".tab-overview__item-time")?.textContent === "just now", "最近关闭条目显示相对时间(just now)");

  // 点击打开组第 2 行 → 切换,不触发关闭
  activated.length = 0;
  await act(async () => {
    items()[1].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
  ok(activated.join(",") === "tab-2", "点击行 → 切换到该 tab");
  await act(async () => { await flushClose(); });
  ok(panelElement() === null, "切换后面板关闭");


}

section("行内 × 关闭:防误触(不触发切换)");
{
  let activated = 0;
  let closedId = "";
  await renderPanel({
    tabs: [tabMeta({ id: "tab-1", topicTitle: "Deploy script" })],
    activeTabId: "tab-1",
    onActivateTab: () => { activated += 1; },
    onCloseTab: (id) => { closedId = id; },
    onReopenClosedTab: () => {},
  });
  await openPanel();
  const closeBtn = document.querySelector<HTMLButtonElement>(".tab-overview__item-close");
  ok(closeBtn !== null, "打开组行内 × 按钮存在");
  await act(async () => {
    closeBtn?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
  ok(closedId === "tab-1", "× 点击调用 onCloseTab");
  ok(activated === 0, "× 点击不触发切换(防冒泡生效)");

}

section("最近关闭:点击重开(带快照)");
{
  let reopened: RecentClosedTab | null = null;
  const snapshot: RecentClosedTab = {
    tab: tabMeta({ id: "old", topicId: "topic-old", sessionPath: "/s/old.json" }),
    closedAt: Date.now() - 120_000,
  };
  await renderPanel({
    tabs: [tabMeta()],
    recentClosedTabs: [snapshot],
    onActivateTab: () => {},
    onCloseTab: () => {},
    onReopenClosedTab: (entry) => { reopened = entry; },
  });
  await openPanel();
  // 最近关闭组 = 第 2 行(打开组 1 行之后)
  const closedRow = items()[1];
  ok(closedRow.querySelector(".tab-overview__item-close") === null, "最近关闭行没有行内 ×");
  await act(async () => {
    closedRow.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flush(30);
  });
  ok(reopened !== null && reopened.tab.sessionPath === "/s/old.json", "点击最近关闭 → 重开回调携带关闭前快照");
  await act(async () => { await flushClose(); });
  ok(panelElement() === null, "重开后面板关闭");

}

section("搜索:AND 过滤 + 空结果提示 + Esc 关闭");
{
  await renderPanel({
    tabs: [
      tabMeta({ id: "tab-1", topicId: "topic-alpha", topicTitle: "Deploy script", workspaceName: "repo", workspaceRoot: "/work/repo" }),
      tabMeta({ id: "tab-2", topicId: "topic-beta", topicTitle: "Research notes", workspaceName: "lab", workspaceRoot: "/work/lab", active: false }),
    ],
    activeTabId: "tab-1",
    onActivateTab: () => {},
    onCloseTab: () => {},
    onReopenClosedTab: () => {},
  });
  await openPanel();
  const input = document.querySelector<HTMLInputElement>(".tab-overview__input");
  ok(input !== null && input.placeholder === "Search tabs…", "搜索框存在且占位符正确");
  // 「research」只命中第 2 个 tab
  await typeInto(input, "research");
  ok(items().length === 1, "关键词过滤后只剩命中行");
  // 两词 AND:「research」命中 tab-2、「zzz」无处命中 → 零结果 → 空态
  await typeInto(input, "research zzz");
  ok(document.querySelector(".tab-overview__empty") !== null, "AND 不满足 → 显示空结果提示");
  ok(panelElement() !== null, "搜索中面板保持打开");

  // 会话标识可命中(hint 面):topicId 只在 tab-1
  await typeInto(input, "topic-alpha");
  ok(items().length === 1, "会话标识(topicId)可命中——「手里只有 id」也能找到");

  // Esc 关面板(dispatch 与动画等待分两段 act,与行点击关闭同款时序)
  await act(async () => {
    window.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flush(30);
  });
  await act(async () => { await flushClose(); });
  if (panelElement() !== null) {
    const panels = document.querySelectorAll(".tab-overview");
    const states = Array.from(panels).map((p) => p.closest("[data-anchored-popover]")?.getAttribute("data-state")).join(",");
    process.stdout.write(`  DEBUG panels after Esc: ${panels.length}, states: ${states}\n`);
  }
  ok(panelElement() === null, "Esc 关闭面板");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
