// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1611-steer-render.test.tsx
//
// tl1611: mid-turn steer 渲染两缺口——①无 ↪ 标识 ②无时间戳。
//
// 数据层钉：steer 以 user row 形态落 transcript 时（723：injected-but-unconsumed /
// rejected-to-followup 被派发为下一轮输入），reducer 凭同一 inboxItemId 配对
// （与 723 降级同一判定）给 user item 打 steerProvenance——只有 steer 出身打标，
// 普通 followup（同通道无 ↪ notice）不标；consume-time steer 事件带后端 At
// （tl1152 契约的 steer 半边，配 run_loop.go 241 的发射）时 notice 用领域时间，
// 不再落 Date.now()。
// 渲染层钉：UserMessage 对 steerProvenance 行渲染 .msg__steer-mark（↪，与
// steer-line__icon / im-source-card__steer-mark 同语言）；普通用户行不渲染该
// 元素（验收④ 零回退）；时间戳缺失时整行不渲染（Task 123 语义不变）。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import { initialState, reducer, STEER_NOTICE_PREFIX, isSteerNoticeText } from "../lib/useController";
import type { Item, State } from "../lib/useController";
import type { WireEvent } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function ev(e: Record<string, unknown>): { type: "event"; e: WireEvent } {
  return { type: "event", e: { turnId: "t1611", ...e } as unknown as WireEvent };
}

function steerNotices(s: State): Array<Extract<Item, { kind: "notice" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "notice" }> => it.kind === "notice" && isSteerNoticeText(it.text));
}

function userRows(s: State): Array<Extract<Item, { kind: "user" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "user" }> => it.kind === "user");
}

console.log("\ntl1611 steer render (↪ 标识 + 时间戳)");

// ── ① receipt 气泡 → user_input：实体行打 steerProvenance，气泡降级 ─────────
{
  let s = reducer(initialState, { type: "guidance_bubble", text: "记得写板", inboxItemId: "ib-1611a" });
  s = reducer(s, ev({ kind: "user_input", text: "记得写板", itemId: "ib-1611a", at: 1794751003946 }));
  const rows = userRows(s);
  const notices = steerNotices(s);
  ok(rows.length === 1 && rows[0]?.steerProvenance === true, "user_input 命中 steer notice（同 inboxItemId）→ 实体行打 steerProvenance");
  ok(rows[0]?.createdAt === 1794751003946, "实体行时间戳用事件领域时间（e.at，tl1152）");
  ok(notices.length === 1 && notices[0]?.steerDeferred === true, "723 降级不变：↪ 气泡折叠为 deferred 占位");
}

// ── ② 普通 followup（同通道、无 steer notice）：不打标，普通用户行 ──────────
{
  const s = reducer(initialState, ev({ kind: "user_input", text: "继续", itemId: "ib-follow", at: 1794751003947 }));
  const rows = userRows(s);
  ok(rows.length === 1 && rows[0]?.steerProvenance === undefined, "无 steer notice 配对的 user_input 不打标（验收④：普通消息零回退）");
  ok(steerNotices(s).length === 0, "普通 followup 不产生 ↪ notice");
}

// ── ③ consume-time steer 事件（无 receipt 气泡）：↪ notice 带后端 At；
//    同 itemId 之后 materialize 为 user 行 → 打标 ───────────────────────────
{
  let s = reducer(initialState, ev({ kind: "steer", text: "tl1612 的报告在哪", itemId: "ib-1611c", at: 1794751003948 }));
  const notices = steerNotices(s);
  ok(notices.length === 1, "consume-time steer 事件创建 ↪ notice");
  ok(notices[0]?.createdAt === 1794751003948, "steer 事件带 At 时 notice 用领域时间（不再落 Date.now，tl1152 steer 半边）");
  s = reducer(s, ev({ kind: "user_input", text: "tl1612 的报告在哪", itemId: "ib-1611c" }));
  const rows = userRows(s);
  ok(rows[0]?.steerProvenance === true, "consume-time steer 的同 itemId user_input 同样打标");
  ok(steerNotices(s)[0]?.steerDeferred === true, "723 降级对 consume-time notice 同样生效");
}

// ── ④ 渲染层：UserMessage 的 ↪ 标记与时间戳 ─────────────────────────────────
{
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

  const { UserMessage } = await import("../components/Message");
  const { LocaleProvider } = await import("../lib/i18n");

  let liveRoot: Root | null = null;
  const render = async (node: React.ReactElement) => {
    if (liveRoot) {
      await act(async () => { liveRoot!.unmount(); });
    }
    liveRoot = createRoot(document.getElementById("root")!);
    await act(async () => {
      liveRoot!.render(<LocaleProvider>{node}</LocaleProvider>);
      await new Promise((r) => setTimeout(r, 0));
    });
  };
  try {
    // steer 出身行：↪ 标记在位 + 时间戳在位。
    await render(<UserMessage id="u-a" text="记得写板" steerMark={true} createdAt={new Date(2026, 10, 15, 19, 56).getTime()} onEdit={() => {}} />);
    ok(Boolean(document.querySelector(".msg__steer-mark")), "steerProvenance 行渲染 .msg__steer-mark（↪ 标识）");
    ok(document.querySelector<HTMLElement>(".msg-meta__time")?.textContent?.trim() === "19:56", "steerProvenance 行时间戳显示（同日档 HH:mm）");

    // 普通用户行：无 ↪ 元素、时间戳照旧（零回退）。
    await render(<UserMessage id="u-b" text="普通消息" steerMark={false} createdAt={new Date(2026, 10, 15, 19, 57).getTime()} onEdit={() => {}} />);
    ok(document.querySelector(".msg__steer-mark") === null, "普通用户行不渲染 ↪ 元素（验收④）");
    ok(document.querySelector<HTMLElement>(".msg-meta__time")?.textContent?.trim() === "19:57", "普通用户行时间戳规则不变");

    // createdAt 缺失（历史页读不到的时间，Task 123）：整行时间元素不渲染。
    await render(<UserMessage id="u-c" text="历史行" steerMark={true} />);
    ok(Boolean(document.querySelector(".msg__steer-mark")), "无 createdAt 的 steer 行仍渲染 ↪ 标识");
    ok(document.querySelector(".msg-meta__time") === null, "createdAt 缺失时时间元素不渲染（Task 123 语义不变）");
  } finally {
    if (liveRoot) {
      try {
        await act(async () => { liveRoot!.unmount(); });
      } catch { /* already gone */ }
    }
  }
}

console.log(`\ntl1611 steer render: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
