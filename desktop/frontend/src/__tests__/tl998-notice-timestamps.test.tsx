// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl998-notice-timestamps.test.tsx
//
// 任务998（P3）：引导消息与未应用 steer 块缺时间戳——transcript 的 steer 气泡
// （含未应用降级占位）、未应用引导 notice 块、composer 引导队列行都没有时间
// 显示，而跨会话消息卡（user row）有。修法：**复用 1012 的 messageTimestamp
// 三档契约**（同日 HH:mm / 跨天 MM-DD HH:mm / 跨年 YYYY-MM-DD HH:mm），不
// 另起格式化逻辑。
//
// 本文件钉三层契约：
// ① 数据层：guidance_bubble / steer 事件落 notice item 时携带 createdAt
//    （receipt 时刻 / emittedAt / inbox CreatedAt），notice item 类型新增
//    createdAt 字段；
// ② 渲染层：SteerCard（含 deferred 占位）与 NoticeCard 对同一渲染时刻呈现
//    三档文本，title 保留完整本地化日期兜底；队列行（ComposerGuidanceShelf）
//    同档；
// ③ 格式层：三个卡片面调用共享的 formatMessageTimestamp（源码钉住，避免面
//    间漂移——与 1012 的 ③ 同款钉法）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { SteerCard, NoticeCard } from "../components/TranscriptCards";
import { ComposerGuidanceShelf, type PendingGuidance } from "../components/ComposerGuidanceShelf";
import { formatMessageTimestamp } from "../lib/messageTimestamp";
import { LocaleProvider } from "../lib/i18n";
import { STEER_NOTICE_PREFIX, initialState, reducer } from "../lib/useController";
import type { State } from "../lib/useController";
import type { NoticeItem } from "../lib/transcriptRows";

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
function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
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
  globalThis.ResizeObserver = TestResizeObserver;
  return dom;
}

// 固定渲染时刻：2026-10-19 23:05（本地时区）。三档锚点是显式注入的 now；
// useTick 只负责重绘，Date.now 钉死后文本可确定性断言。
const FIXED_NOW = new Date(2026, 9, 19, 23, 5, 0).getTime();
const at = (y: number, m: number, d: number, hh: number, mm: number): number => new Date(y, m - 1, d, hh, mm).getTime();

// ── ① 数据层：事件 → notice item 的 createdAt 落位 ─────────────────────────
{
  const realNow = Date.now;
  Date.now = () => FIXED_NOW;
  try {
    // guidance_bubble 显式带 createdAt（receipt 时刻/inbox CreatedAt）。
    const explicit = reducer(initialState, { type: "guidance_bubble", text: "改用 Pillow 10 验证", inboxItemId: "ib-998", createdAt: at(2026, 10, 16, 9, 30) } as never) as State;
    const explicitItem = explicit.items.find((item) => item.kind === "notice" && item.inboxItemId === "ib-998");
    eq(explicitItem?.kind === "notice" && explicitItem.createdAt, at(2026, 10, 16, 9, 30), "guidance_bubble 保留显式 createdAt（inbox 入队时刻）");

    // 无 createdAt 的旧调用退化为渲染时刻（Date.now 已钉），不是 undefined。
    const fallback = reducer(initialState, { type: "guidance_bubble", text: "降级提示", inboxItemId: "ib-legacy" } as never) as State;
    const fallbackItem = fallback.items.find((item) => item.kind === "notice" && item.inboxItemId === "ib-legacy");
    eq(fallbackItem?.kind === "notice" && fallbackItem.createdAt, FIXED_NOW, "guidance_bubble 缺 createdAt 时退化为渲染时刻");

    // 未应用 steer notice（notice 事件）带 emittedAt → createdAt。
    const unapplied = reducer(initialState, {
      type: "event",
      e: { turnId: "t998", kind: "notice", level: "warn", code: "unapplied_steer", text: "reworded unapplied copy\nuse plan B", emittedAt: at(2026, 10, 15, 22, 9) },
    } as never) as State;
    const unappliedItem = unapplied.items.find((item) => item.kind === "notice" && item.code === "unapplied_steer");
    eq(unappliedItem?.kind === "notice" && unappliedItem.createdAt, at(2026, 10, 15, 22, 9), "未应用 steer notice 保留 emittedAt 作为 createdAt");
  } finally {
    Date.now = realNow;
  }
}

// ── ② 渲染层：SteerCard / NoticeCard / 队列行对同一 now 的三档呈现 ─────────
{
  installDom();
  const realNow = Date.now;
  Date.now = () => FIXED_NOW;
  const roots: Root[] = [];
  try {
    const render = async (node: React.ReactElement) => {
      const root = createRoot(document.getElementById("root")!);
      roots.push(root);
      await act(async () => {
        root.render(<LocaleProvider>{node}</LocaleProvider>);
        await new Promise((r) => setTimeout(r, 0));
      });
    };
    const timeText = (cls: string): string | undefined =>
      document.querySelector<HTMLElement>(cls)?.textContent?.trim();

    // steer 气泡：同日 / 跨天 / 跨年三档。
    const steerCases: { createdAt: number; expected: string; label: string }[] = [
      { createdAt: at(2026, 10, 19, 22, 9), expected: "22:09", label: "steer 同日档" },
      { createdAt: at(2026, 10, 16, 9, 30), expected: "10-16 09:30", label: "steer 跨天档" },
      { createdAt: at(2025, 12, 31, 23, 59), expected: "2025-12-31 23:59", label: "steer 跨年档" },
    ];
    for (const c of steerCases) {
      await render(<SteerCard id={`s-${c.label}`} text={`${STEER_NOTICE_PREFIX}改用 Pillow 10 验证`} createdAt={c.createdAt} />);
      eq(timeText(".steer-line__time"), c.expected, `${c.label} = ${c.expected}`);
    }

    // deferred 占位（723 折叠态）也有时间戳。
    await render(<SteerCard id="s-def" text={`${STEER_NOTICE_PREFIX}看看 723 的截图`} deferred={true} createdAt={at(2026, 10, 19, 22, 9)} />);
    eq(timeText(".steer-line__time"), "22:09", "deferred steer 占位显示时间戳");

    // 未应用 steer notice 块（NoticeCard）。
    const warnItem: NoticeItem = {
      kind: "notice", id: "n-warn", level: "warn", code: "unapplied_steer",
      text: "本条引导未能应用，因为当前回合在处理它之前已结束。如仍需要，请重新发送：\nuse plan B",
      createdAt: at(2026, 10, 15, 22, 9),
    };
    await render(<NoticeCard item={warnItem} />);
    eq(timeText(".notice-line__time"), "10-15 22:09", "未应用 steer notice 块显示跨天档时间戳");

    // 无 createdAt 的 notice 不渲染时间元素（不占位、不显示 NaN）。
    const bareItem: NoticeItem = { kind: "notice", id: "n-bare", level: "info", text: "deployment completed" };
    await render(<NoticeCard item={bareItem} />);
    eq(document.querySelector(".notice-line__time"), null, "无 createdAt 的 notice 不渲染时间元素");

    // 引导队列行（ComposerGuidanceShelf）：有 createdAt 渲染、无则不渲染。
    const shelfItem = (id: string, createdAt?: number): PendingGuidance => ({
      id, text: `guidance ${id}`, submitText: "", state: "queued", createdAt,
    });
    await render(
      <ComposerGuidanceShelf
        recovery={null} recoveryDisabled={false}
        items={[shelfItem("a", at(2026, 10, 19, 22, 9)), shelfItem("b", at(2026, 10, 16, 9, 30)), shelfItem("c")]}
        expanded={true} running={false} disabled={false} readOnly={false} sendingId={null}
        onReview={() => {}} onRecoveryResumed={() => {}} onRecoveryError={() => {}}
        onToggleExpanded={() => {}} onSend={() => {}} onDismiss={() => {}}
      />,
    );
    const shelfTimes = [...document.querySelectorAll<HTMLElement>(".composer-guidance-item__time")].map((el) => el.textContent?.trim());
    eq(shelfTimes.length, 2, "队列行只给带 createdAt 的行渲染时间");
    eq(shelfTimes[0], "22:09", "队列行 a = 同日档");
    eq(shelfTimes[1], "10-16 09:30", "队列行 b = 跨天档");
  } finally {
    Date.now = realNow;
    for (const root of roots) {
      try { root.unmount(); } catch { /* already gone */ }
    }
  }
}

// ── ③ 格式层：三个卡片面共用同一三档函数（源码钉住，避免面间漂移） ────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const read = (rel: string) => readFileSync(resolve(here, rel), "utf8");
  ok(read("../components/TranscriptCards.tsx").includes("formatMessageTimestamp(sentAt, new Date(now), getLocale())"),
    "TranscriptCards（steer/notice 卡）走共享三档函数");
  ok(read("../components/ComposerGuidanceShelf.tsx").includes("formatMessageTimestamp(sentAt, new Date(now), getLocale())"),
    "ComposerGuidanceShelf（队列行）走共享三档函数");
}

console.log(`\ntl998 notice timestamps: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
