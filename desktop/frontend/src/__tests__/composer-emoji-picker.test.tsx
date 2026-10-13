// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-emoji-picker.test.tsx
//
// 任务 869：composer emoji 面板。覆盖面：
// 1. catalog 数据层——8 类非空、全局无重复字符、中英文搜索命中；
// 2. 组合 emoji 数据完好——ZWJ 序列（👨‍👩‍👧‍👦/🏳️‍🌈/🧑‍💻）、肤色修饰符
//    （👍🏽/👍🏿）、变体选择符（❤️）以完整 UTF-16 码元序列存在；
// 3. 面板开合——入口按钮开、Esc 关（AnchoredPopover 统一语义）；
// 4. 分类 tab 与搜索过滤（含空结果态）；
// 5. 插入——光标位置紧凑插入、连续插入、组合 emoji 不截断；
// 6. 发送往返——emoji 随文本原样进入 onSend（无过滤/无变形）；
// 7. 渲染端通道——transcript 气泡文本走 react-markdown 管线，组合 emoji
//    直通输出（与 markdown-pipeline.test 同款渲染路径，webview 原生字形）。

import { JSDOM } from "jsdom";

import { createElement } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import { normalizeMath } from "../components/mathNormalize";
import { createComponents } from "../components/markdownComponents";
import { reasonixRehypePlugins, reasonixRemarkPlugins } from "../components/markdownRemarkPlugins";
import { markdownUrlTransform } from "../lib/markdownPipeline";
import { EMOJI_CATEGORIES, EMOJI_ENTRY_COUNT, FREQUENT_EMOJIS, searchEmojis } from "../lib/emojiCatalog";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// ---------- 1) catalog 数据层（纯函数，无需 DOM） ----------

{
  process.stdout.write("\n任务 869 catalog 数据层\n");

  eq(EMOJI_CATEGORIES.length, 8, "exactly eight categories");
  const seen = new Set<string>();
  let duplicates = 0;
  for (const cat of EMOJI_CATEGORIES) {
    ok(cat.emojis.length >= 24, `category ${cat.id} is populated (${cat.emojis.length})`);
    for (const entry of cat.emojis) {
      if (seen.has(entry.char)) duplicates += 1;
      seen.add(entry.char);
      ok(entry.keywords.length > 0, `${entry.char} carries keywords`);
    }
  }
  eq(duplicates, 0, "no duplicate emoji across categories");
  ok(EMOJI_ENTRY_COUNT >= 240, `catalog is reasonably rich (${EMOJI_ENTRY_COUNT} entries)`);
  eq(FREQUENT_EMOJIS.length, 16, "frequent row ships 16 picks");

  ok(searchEmojis("grin").some((entry) => entry.char === "😀"), "english keyword search hits grinning");
  ok(searchEmojis("笑哭").length > 0, "chinese keyword search hits");
  ok(searchEmojis("  GRIN  ").length > 0, "search trims and case-folds the query");
  eq(searchEmojis("").length, 0, "empty query returns nothing");
  eq(searchEmojis("zzlebrox").length, 0, "unknown query returns nothing");
}

// ---------- 2) 组合 emoji 数据完好性 ----------

{
  process.stdout.write("\n任务 869 组合 emoji 数据完好性\n");
  const allChars = EMOJI_CATEGORIES.flatMap((cat) => cat.emojis.map((entry) => entry.char));
  // ZWJ 序列（U+200D 连接）、肤色修饰符（U+1F3FD/U+1F3FF）、VS16（U+FE0F）。
  for (const combo of ["👨‍👩‍👧‍👦", "🏳️‍🌈", "🧑‍💻", "👍🏽", "👍🏿", "❤️"]) {
    ok(allChars.includes(combo), `catalog carries the combined emoji ${combo}`);
  }
  ok(searchEmojis("family").some((entry) => entry.char === "👨‍👩‍👧‍👦"), "family ZWJ sequence is searchable");
  ok(searchEmojis("点赞").some((entry) => entry.char === "👍"), "skin-tone base thumbs up searchable in chinese");
}

// ---------- 3) 气泡渲染通道（react-markdown 直通，同 transcript 路径） ----------

function renderBubbleText(text: string): string {
  return renderToStaticMarkup(
    createElement(ReactMarkdown, {
      remarkPlugins: reasonixRemarkPlugins,
      rehypePlugins: reasonixRehypePlugins,
      components: createComponents(false),
      urlTransform: markdownUrlTransform,
      children: normalizeMath(text),
    }),
  );
}

{
  process.stdout.write("\n任务 869 气泡渲染通道（markdown 管线直通）\n");
  const plain = renderBubbleText("hi 😀 👍🏽 there");
  ok(plain.includes("😀") && plain.includes("👍🏽"), "plain emoji + skin-tone modifier survive the pipeline");
  const zwj = renderBubbleText("team 👨‍👩‍👧‍👦 pride 🏳️‍🌈 dev 🧑‍💻");
  ok(zwj.includes("👨‍👩‍👧‍👦"), "family ZWJ sequence survives byte-complete");
  ok(zwj.includes("🏳️‍🌈"), "rainbow-flag ZWJ sequence survives byte-complete");
  ok(zwj.includes("🧑‍💻"), "technologist ZWJ sequence survives byte-complete");
  const vs16 = renderBubbleText("love ❤️ wins");
  ok(vs16.includes("❤️"), "VS16 heart survives byte-complete");
}

// ---------- 4) composer 集成（单 document 单 mount，顺序场景） ----------

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.InputEvent = dom.window.InputEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.MutationObserver = dom.window.MutationObserver;
globalThis.File = dom.window.File;
globalThis.FileReader = dom.window.FileReader;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.ResizeObserver = TestResizeObserver;
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }),
});

// React's value tracker only reports a change (and fires onChange) when the
// DOM value moved outside its patched setter — same helper as the IME suite.
function setNativeValue(element: HTMLTextAreaElement | HTMLInputElement, value: string) {
  const proto = element instanceof window.HTMLTextAreaElement
    ? window.HTMLTextAreaElement.prototype
    : window.HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
  if (!setter) throw new Error("native value setter unavailable");
  setter.call(element, value);
}

async function typeText(ta: HTMLTextAreaElement, value: string) {
  await act(async () => {
    ta.focus();
    setNativeValue(ta, value);
    ta.setSelectionRange(value.length, value.length);
    ta.dispatchEvent(new window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    // React 在 canUseDOM=false 下用 keyup 驱动 change 检测（bang 套件同款）。
    ta.dispatchEvent(new window.KeyboardEvent("keyup", { key: "a", bubbles: true }));
    await flushTimers();
  });
}

function composerTextarea(): HTMLTextAreaElement {
  const ta = document.querySelector<HTMLTextAreaElement>("#composer-input");
  if (!ta) throw new Error("plain composer textarea did not render");
  return ta;
}

function emojiTrigger(): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(".composer-emoji-picker button");
  if (!button) throw new Error("emoji picker trigger did not render");
  return button;
}

function emojiMenu(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".composer-emoji-menu");
}

function emojiCells(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>(".composer-emoji-menu__cell")];
}

async function openEmojiPicker() {
  await act(async () => {
    emojiTrigger().click();
    // lazy chunk 的首次动态 import 需要多轮宏/微任务才能落 Suspense；
    // 第二次起走模块缓存，即开即有。
    for (let i = 0; i < 20 && emojiMenu() === null; i += 1) {
      await flushTimers();
    }
  });
}

async function pressEscape() {
  await act(async () => {
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushTimers();
  });
}

async function setSearchQuery(query: string) {
  const input = document.querySelector<HTMLInputElement>(".composer-emoji-menu__search-input");
  if (!input) throw new Error("emoji search input did not render");
  await act(async () => {
    input.focus();
    setNativeValue(input, query);
    input.dispatchEvent(new window.InputEvent("input", { bubbles: true, data: query, inputType: "insertText", isComposing: false }));
    input.dispatchEvent(new window.KeyboardEvent("keyup", { key: "a", bubbles: true }));
    await flushTimers();
  });
}

const sentTexts: string[] = [];

{
  process.stdout.write("\n任务 869 composer 集成（开合/搜索/插入/往返）\n");
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const props: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "reasonix",
    onSend: async (displayText: string) => { sentTexts.push(displayText); },
    onCancel: async () => ({ discardedItemIds: [] }),
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onPauseGoal: () => {},
    onResumeGoal: () => {},
    onSwitchModel: () => true,
    onSetEffort: () => {},
    ready: true,
  };

  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });

  // 开合：入口按钮打开面板（lazy chunk 解析后），Esc 关闭。
  eq(emojiMenu(), null, "panel is closed initially");
  await openEmojiPicker();
  const menu = emojiMenu();
  ok(menu !== null, "trigger opens the panel (lazy chunk resolves)");
  if (menu) {
    eq(menu.getAttribute("aria-label"), "Emoji", "panel carries the localized title");
    const search = menu.querySelector<HTMLInputElement>(".composer-emoji-menu__search-input");
    eq(search?.getAttribute("placeholder"), "Search emoji", "search placeholder is localized");
  }
  ok(emojiCells().length >= 24, "first category grid is rendered");
  ok(document.querySelector(".composer-emoji-menu__frequent") !== null, "frequent row is rendered");
  await pressEscape();
  await act(async () => { await flushTimers(); });
  eq(emojiMenu(), null, "Escape closes the panel");

  // 分类 tab：切到 Animals & nature，网格随之切换。
  await openEmojiPicker();
  const tabs = [...document.querySelectorAll<HTMLButtonElement>(".composer-emoji-menu__tab")];
  eq(tabs.length, 8, "eight category tabs render");
  const animalsTab = tabs.find((tab) => tab.textContent === "Animals & nature");
  ok(animalsTab !== undefined, "animals tab carries the localized label");
  await act(async () => {
    animalsTab?.click();
    await flushTimers();
  });
  const animalCells = emojiCells().map((cell) => cell.textContent ?? "");
  ok(animalCells.includes("🐶"), "animals grid shows the dog");
  ok(document.querySelector(".composer-emoji-menu__tab--active") === animalsTab, "active tab is highlighted");

  // 搜索：中英命中 + 空结果态。
  await setSearchQuery("family");
  const familyCells = emojiCells().map((cell) => cell.textContent ?? "");
  ok(familyCells.includes("👨‍👩‍👧‍👦"), "searching family surfaces the ZWJ sequence");
  ok(document.querySelector(".composer-emoji-menu__frequent") === null, "search mode hides the frequent row");
  await setSearchQuery("zzlebrox");
  ok(document.querySelector(".composer-emoji-menu__empty") !== null, "unknown query shows the empty state");
  await setSearchQuery("");

  // 插入：光标位置紧凑插入（无片段式空行分隔），光标跟随。
  const ta = composerTextarea();
  await typeText(ta, "hello world");
  await act(async () => {
    ta.setSelectionRange(5, 5);
    await flushTimers();
  });
  const thumbsUp = emojiCells().find((cell) => cell.textContent === "👍");
  ok(thumbsUp !== undefined, "frequent row carries the thumbs up");
  await act(async () => {
    thumbsUp?.click();
    await flushTimers();
  });
  eq(ta.value, "hello👍 world", "emoji inserts at the caret without separators");
  eq(ta.selectionStart, "hello👍".length, "caret follows the inserted emoji");

  // 连续插入：面板保持打开，第二次插入紧跟第一次。
  const joy = emojiCells().find((cell) => cell.textContent === "😂");
  ok(joy !== undefined, "frequent row carries joy");
  await act(async () => {
    joy?.click();
    await flushTimers();
  });
  eq(ta.value, "hello👍😂 world", "consecutive picks append at the trailing caret");
  ok(emojiMenu() !== null, "panel stays open for consecutive picks");

  // 组合 emoji 插入不截断。
  await setSearchQuery("family");
  const familyCell = emojiCells().find((cell) => cell.textContent === "👨‍👩‍👧‍👦");
  ok(familyCell !== undefined, "family cell is reachable via search");
  await act(async () => {
    familyCell?.click();
    await flushTimers();
  });
  eq(ta.value, "hello👍😂👨‍👩‍👧‍👦 world", "ZWJ family sequence inserts byte-complete");

  // 发送往返：emoji 随文本原样进入 onSend，无过滤/无变形。
  await typeText(ta, "hello👍😂👨‍👩‍👧‍👦 world ❤️");
  const send = document.querySelector<HTMLButtonElement>(".composer__btn--send");
  ok(send !== null, "send button renders");
  await act(async () => {
    send?.click();
    await flushTimers();
  });
  eq(sentTexts.length, 1, "send delivers exactly one message");
  eq(sentTexts[0], "hello👍😂👨‍👩‍👧‍👦 world ❤️", "onSend receives the emoji text byte-complete");

  await pressEscape();
  await act(async () => { await flushTimers(); });
  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
