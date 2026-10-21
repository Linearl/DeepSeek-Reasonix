// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1012-message-timestamp.test.tsx
//
// 任务1012（P3）：消息时间戳补日期——消息气泡时间只有时分（22:09），横跨
// 数天的会话无法区分某条消息属于哪一天（用户 2026-10-19 截图实证）。修法：
// 三档绝对时间（同日 HH:mm / 跨天 MM-DD HH:mm / 跨年 YYYY-MM-DD HH:mm），
// 不用相对时间；窄窗口靠短格式本身（同日档与改动前同宽，跨天档仅加 6 字符，
// 小字号 tabular-nums 已有 nowrap 兜住）。
//
// 本文件钉三层契约：
// ① 分档规则（formatMessageTimestamp 纯函数）：同日 / 跨天 / 跨年三档在
//    三语 locale（en / zh / zh-TW）下输出逐字节一致（locale 中立拼接，不走
//    toLocaleString 的美式逗号歧义）；
// ② 气泡渲染（UserMessage 真渲染）：三条消息（10-15 / 10-16 / 10-19）对同一
//    渲染时刻（固定 Date.now）分别呈现三档文本，title 仍是完整本地化日期兜底；
// ③ 协作聊天面共用同一三档契约（ChannelChatPanel / CollabChatView 的
//    formatTime 走同一函数——源码钉住，避免面间漂移）。
//
// 与 1020 的边界（派单要求核对）：1020 修的是 Composer 心跳报时单位
//（lastTurnEventAt 归一 epoch 毫秒，lib/workStatus.turnHeartbeatSeconds），
// 1012 修的是消息列表日期显示——不同面。本测试复用同一 useTick 渲染时刻
// 源（Date.now()，epoch 毫秒），不触碰 1020 的单位契约；1020 回归由既有
// tl1020-heartbeat-units.test.ts 独立钉住（本批验收同跑）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { UserMessage } from "../components/Message";
import { formatMessageTimestamp } from "../lib/messageTimestamp";
import { LocaleProvider } from "../lib/i18n";

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

// ── ① 分档规则：三语 locale 三档输出一致（locale 中立） ──────────────────────
{
  const now = new Date(2026, 9, 19, 23, 5, 0); // 2026-10-19 23:05 本地时区
  const sameDay = new Date(2026, 9, 19, 22, 9, 0); // 同日 22:09
  const crossDay = new Date(2026, 9, 15, 22, 9, 0); // 跨天 10-15 22:09（同一会话横跨五天的形态）
  const crossYear = new Date(2025, 11, 31, 23, 59, 0); // 跨年 2025-12-31 23:59
  for (const locale of ["en", "zh", "zh-TW"] as const) {
    eq(formatMessageTimestamp(sameDay, now, locale), "22:09", `${locale} 同日档 = HH:mm（零回归）`);
    eq(formatMessageTimestamp(crossDay, now, locale), "10-15 22:09", `${locale} 跨天档 = MM-DD HH:mm`);
    eq(formatMessageTimestamp(crossYear, now, locale), "2025-12-31 23:59", `${locale} 跨年档 = YYYY-MM-DD HH:mm`);
  }
  // 跨年边界：消息 2025-12-31 23:59、渲染时刻 2026-01-01 00:01（跨年翻页瞬间）。
  eq(
    formatMessageTimestamp(new Date(2025, 11, 31, 23, 59), new Date(2026, 0, 1, 0, 1), "zh"),
    "2025-12-31 23:59",
    "跨年翻页瞬间仍判为跨年档（消息年 ≠ 渲染年）",
  );
  // 跨天边界：消息 2026-10-18 23:59、渲染时刻 2026-10-19 00:01。
  eq(
    formatMessageTimestamp(new Date(2026, 9, 18, 23, 59), new Date(2026, 9, 19, 0, 1), "zh"),
    "10-18 23:59",
    "跨天翻页瞬间判为跨天档（消息日 ≠ 渲染日）",
  );
}

// ── ② 气泡渲染：三条消息（10-15 / 10-16 / 10-19）对同一渲染时刻的三档呈现 ──
{
  installDom();
  // 固定渲染时刻：把 useTick 的 Date.now() 钉在 2026-10-19 23:05，让三档可
  // 确定性断言（分档锚点是显式传入的 now；useTick 只负责重绘）。
  const FIXED_NOW = new Date(2026, 9, 19, 23, 5, 0).getTime();
  const realNow = Date.now;
  Date.now = () => FIXED_NOW;
  try {
    const at = (y: number, m: number, d: number, hh: number, mm: number): number => new Date(y, m - 1, d, hh, mm).getTime();
    const cases: { createdAt: number; expected: string; label: string }[] = [
      { createdAt: at(2026, 10, 19, 22, 9), expected: "22:09", label: "10-19（同日）" },
      { createdAt: at(2026, 10, 16, 9, 30), expected: "10-16 09:30", label: "10-16（跨天）" },
      { createdAt: at(2026, 10, 15, 22, 9), expected: "10-15 22:09", label: "10-15（跨天）" },
    ];
    const roots: Root[] = [];
    for (const c of cases) {
      const root = createRoot(document.getElementById("root")!);
      roots.push(root);
      await act(async () => {
        root.render(
          <LocaleProvider>
            <UserMessage text={`msg of ${c.label}`} createdAt={c.createdAt} />
          </LocaleProvider>,
        );
        await new Promise((r) => setTimeout(r, 0));
      });
      const timeEl = document.querySelector<HTMLElement>(`time.msg-meta__time[datetime="${new Date(c.createdAt).toISOString()}"]`);
      ok(Boolean(timeEl), `渲染 ${c.label} 的 <time> 元素存在`);
      eq(timeEl?.textContent?.trim(), c.expected, `${c.label} 气泡时间戳 = ${c.expected}`);
      ok(Boolean(timeEl?.title), `${c.label} title 保留完整本地化日期兜底`);
      await act(async () => root.unmount());
    }
  } finally {
    Date.now = realNow;
  }
}

// ── ③ 协作聊天面共用同一三档契约（源码钉住，避免面间漂移） ─────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const read = (rel: string) => readFileSync(resolve(here, rel), "utf8");
  const channelPanel = read("../components/ChannelChatPanel.tsx");
  const collabChatView = read("../components/CollabChatView.tsx");
  ok(channelPanel.includes("formatMessageTimestamp(date, new Date(), getLocale())"),
    "ChannelChatPanel.formatTime 走共享三档函数（不再裸 toLocaleString）");
  ok(collabChatView.includes("formatMessageTimestamp(new Date(e.at), new Date(), getLocale())"),
    "CollabChatView 回放时间戳走共享三档函数（不再裸 toLocaleString）");
}

process.stdout.write(`\ntl1012 message timestamp: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
