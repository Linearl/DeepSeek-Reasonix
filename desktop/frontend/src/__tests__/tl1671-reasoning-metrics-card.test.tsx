// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1671-reasoning-metrics-card.test.tsx
//
// tl1671（用户 20261117 18:42 截图立项）：转录里的「思考过程」折叠头只写
// 「思考过程」三个字，看不到这段思考花了多久、写了多少字。用户在另一分支
// 看到的是行内的「> 思考 0.4 秒 · 77 字」，拍板要的是悬浮卡片：鼠标停在
// 「思考过程」上就浮出耗时与字数，不点开思考内容也能看到。
//
// 验收矩阵（可证伪）：
//   A  载体复用：卡片落在既有 Tooltip 载体（.tooltip）上，不是新弹层
//      （无 [data-anchored-popover]）——与 tl1582 停止按钮 hover 卡同一条路。
//   B  数值真实：卡片里的秒数 = 该 reasoning 块的耗时字段（毫秒→秒，一位
//      小数），字数 = 该 reasoning 正文的码点数——与转录块本身对账，不是
//      常量、不是估算。
//   C  不点开也能看：hover 出卡片时思考正文仍是折叠的（没有 .reasoning__body）。
//   D  三语：zh / en / zh-TW 三份词典都定义了这几个键，且渲染出的卡片文案
//      随当前语言切换（同一段数据，三种语言各自成立）。
//   E  缺耗时（历史会话未记录）时只显示字数行，并明说「未记录」，不假装
//      一个 0.0 秒。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider, preloadLocale } from "../lib/i18n";
import { InlineAssistantReasoning } from "../components/InlineAssistantReasoning";
import { LiveStreamContext } from "../components/LiveStreamContext";
import type { LiveStream } from "../lib/useController";
import { hydrateSessionExperience } from "../lib/sessionExperience";
import {
  formatThinkingCharCount,
  formatThinkingSeconds,
  hasThinkingDuration,
  reasoningCharCount,
} from "../lib/reasoningMetrics";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}（实际=${String(actual)} 期望=${String(expected)}）`);
}

console.log("\ntl1671 思考指标悬浮卡片");

// ── DOM 全局 ─────────────────────────────────────────────────────────
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
let currentLanguage = "en-US";
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: { get language() { return currentLanguage; } },
});
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

// ── 纯函数：口径 ─────────────────────────────────────────────────────
{
  eq(reasoningCharCount("你好abc"), 5, "口径1 字数 = 码点数（你好abc = 5）");
  eq(reasoningCharCount("👍"), 1, "口径2 代理对算 1 个码点（👍 = 1，不是 2）");
  eq(reasoningCharCount(""), 0, "口径3 空思考 = 0 字");
  eq(reasoningCharCount(undefined), 0, "口径4 缺思考 = 0 字");
  eq(formatThinkingSeconds(0.4, "zh"), "0.4", "口径5 秒保留一位小数（0.4 秒）");
  eq(formatThinkingSeconds(12, "zh"), "12.0", "口径6 整秒也补一位小数（12.0 秒）");
  eq(formatThinkingSeconds(-1, "zh"), "0.0", "口径7 负耗时夹到 0");
  eq(formatThinkingCharCount(1234, "en"), "1,234", "口径8 字数千分位分组");
  eq(hasThinkingDuration(400), true, "口径9 有耗时 = 可展示");
  eq(hasThinkingDuration(undefined), false, "口径10 缺耗时 = 不可展示");
  eq(hasThinkingDuration(0), false, "口径11 0 毫秒不算耗时");
}

// ── 三语词典：键齐、文案齐 ───────────────────────────────────────────
{
  const keys = [
    "msg.thinkingDurationLabel",
    "msg.thinkingDurationValue",
    "msg.thinkingDurationUnknown",
    "msg.thinkingCharsLabel",
    "msg.thinkingCharsValue",
  ] as const;
  for (const key of keys) {
    ok(typeof zh[key] === "string" && zh[key].length > 0, `三语1 zh 定义 ${key}`);
    ok(typeof zhTW[key] === "string" && zhTW[key].length > 0, `三语2 zh-TW 定义 ${key}`);
    ok(typeof en[key] === "string" && en[key].length > 0, `三语3 en 定义 ${key}`);
  }
  eq(zh["msg.thinkingDurationLabel"], "思考耗时", "三语4 zh 文案：思考耗时");
  eq(zhTW["msg.thinkingDurationLabel"], "思考耗時", "三语5 zh-TW 文案：思考耗時");
  eq(en["msg.thinkingDurationLabel"], "Thinking time", "三语6 en 文案：Thinking time");
  eq(zh["msg.thinkingCharsValue"], "{n} 字", "三语7 zh 字数单位：{n} 字");
  eq(zhTW["msg.thinkingCharsValue"], "{n} 字", "三语8 zh-TW 字数单位：{n} 字");
  eq(en["msg.thinkingCharsValue"], "{n} chars", "三语9 en 字数单位：{n} chars");
  // 三份词典键集一致（en 是主键集；zh/zh-TW 的类型标注在编译期兜底，这里再钉一次运行时）
  eq(Object.keys(zh).length, Object.keys(en).length, "三语10 zh 键集与 en 等长");
  eq(Object.keys(zhTW).length, Object.keys(en).length, "三语11 zh-TW 键集与 en 等长");
}

// ── 组件：hover 出卡片 ───────────────────────────────────────────────
type ReasoningItem = React.ComponentProps<typeof InlineAssistantReasoning>["item"];

// 实时流场景的挂载壳：把一条 LiveStream 放进 LiveStreamContext，
// 让 InlineAssistantReasoning 走 live 分支（取当前流式文本而非落定文本）。
function LiveStreamHarness({ item, live }: { item: ReasoningItem; live: LiveStream }) {
  return (
    <LiveStreamContext.Provider value={live}>
      <InlineAssistantReasoning key={item.id} item={item} autoFollowActive={true} />
    </LiveStreamContext.Provider>
  );
}

const REASONING = "先读一遍 reasonix 的会话落库路径，确认思考正文是不是随消息一起写进 jsonl；再看前端的流式 reducer 怎么量思考耗时。";
const EXPECTED_CHARS = reasoningCharCount(REASONING);
const DURATION_MS = 400;

function makeItem(id: string, overrides: Partial<ReasoningItem> = {}): ReasoningItem {
  return {
    kind: "assistant",
    id,
    text: "",
    reasoning: REASONING,
    streaming: false,
    reasoningComplete: true,
    reasoningDurationMs: DURATION_MS,
    ...overrides,
  } as ReasoningItem;
}

async function renderInline(item: ReasoningItem) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <InlineAssistantReasoning key={item.id} item={item} autoFollowActive={false} />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

async function unmountAll() {
  await act(async () => {
    root.render(<LocaleProvider></LocaleProvider>);
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

async function hoverHead() {
  const trigger = document.querySelector(".tooltip-trigger");
  ok(trigger !== null, "载体1 思考头落在既有 tooltip-trigger 上");
  await act(async () => {
    trigger?.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await new Promise((resolve) => setTimeout(resolve, 250)); // tooltip show delay is 180ms
  });
  return document.querySelector<HTMLElement>(".tooltip");
}

hydrateSessionExperience("standard");
await preloadLocale("zh");
await preloadLocale("zh-TW");

// 1. 英文渲染：数值与折叠块本身对账
{
  currentLanguage = "en-US";
  const item = makeItem("tl1671-en");
  await renderInline(item);
  ok(document.querySelector(".turn-collapse__reasoning-head") !== null, "渲染1 转录渲染出思考折叠头");
  ok(document.querySelector(".reasoning__body") === null, "渲染2 思考正文默认折叠");

  const card = await hoverHead();
  ok(card !== null, "载体2 hover 弹出 .tooltip 卡片");
  ok(document.querySelector("[data-anchored-popover]") === null, "载体3 没有另造弹层（无新 popover 体系）");
  const text = card?.textContent ?? "";
  ok(text.includes(en["msg.thinkingDurationLabel"]), `数值1 卡片有耗时标签（${text}）`);
  ok(text.includes("0.4s"), `数值2 卡片耗时 = 400ms → 0.4s（${text}）`);
  ok(text.includes(en["msg.thinkingCharsLabel"]), `数值3 卡片有字数标签（${text}）`);
  ok(text.includes(`${EXPECTED_CHARS} chars`), `数值4 卡片字数 = 思考正文码点数 ${EXPECTED_CHARS}（${text}）`);
  ok(document.querySelector(".reasoning__body") === null, "数值5 不点开思考内容也能看到指标（正文仍折叠）");

  await unmountAll();
}

// 2. 简体中文渲染
{
  currentLanguage = "zh-CN";
  const item = makeItem("tl1671-zh");
  await renderInline(item);
  const card = await hoverHead();
  const text = card?.textContent ?? "";
  ok(text.includes(zh["msg.thinkingDurationLabel"]), `中文1 卡片显示中文耗时标签（${text}）`);
  ok(text.includes("0.4 秒"), `中文2 卡片耗时 = 0.4 秒（${text}）`);
  ok(text.includes(`${EXPECTED_CHARS} 字`), `中文3 卡片字数 = ${EXPECTED_CHARS} 字（${text}）`);
  await unmountAll();
}

// 3. 繁体中文渲染
{
  currentLanguage = "zh-TW";
  const item = makeItem("tl1671-zhtw");
  await renderInline(item);
  const card = await hoverHead();
  const text = card?.textContent ?? "";
  ok(text.includes(zhTW["msg.thinkingDurationLabel"]), `繁中1 卡片显示繁体耗时标签（${text}）`);
  ok(text.includes("0.4 秒"), `繁中2 卡片耗时 = 0.4 秒（${text}）`);
  ok(text.includes(`${EXPECTED_CHARS} 字`), `繁中3 卡片字数 = ${EXPECTED_CHARS} 字（${text}）`);
  await unmountAll();
}

// 4. 缺耗时（历史会话未记录）：只显示字数，并明说未记录
{
  currentLanguage = "en-US";
  const item = makeItem("tl1671-nodur", { reasoningDurationMs: undefined });
  await renderInline(item);
  const card = await hoverHead();
  const text = card?.textContent ?? "";
  ok(text.includes(en["msg.thinkingDurationUnknown"]), `缺值1 耗时缺失时明说未记录（${text}）`);
  ok(!text.includes("0.0s"), `缺值2 不假装一个 0.0 秒（${text}）`);
  ok(text.includes(`${EXPECTED_CHARS} chars`), `缺值3 字数照常可看（${text}）`);
  await unmountAll();
}

// 5. 实时流：思考还在进行时字数跟随当前文本（不是落定后的旧值）
{
  currentLanguage = "en-US";
  const liveText = "正在想";
  const item = makeItem("tl1671-live", { reasoning: "", streaming: true, reasoningComplete: false, reasoningDurationMs: undefined });
  await act(async () => {
    root.render(
      <LocaleProvider>
        <LiveStreamHarness item={item} live={{ id: item.id, text: "", reasoning: liveText, reasoningComplete: false }} />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  const card = await hoverHead();
  const text = card?.textContent ?? "";
  ok(text.includes(`${reasoningCharCount(liveText)} chars`), `实时1 字数取当前流式文本（${text}）`);
  await unmountAll();
}

await act(async () => {
  root.unmount();
});

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
