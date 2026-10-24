// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1092-collab-card-time-copy.test.tsx
//
// 任务1092（P2）：跨会话消息卡片渲染增强——补时间戳 + 复制按钮，对齐普通消息
// 卡片（Message.tsx msg-meta 的 time + CopyButton）。
//
// 数据层钉（可证伪）：
//  ① collabAsImSource 解析 header 的「，发送于 <RFC3339 周几>」为 sentAt（ms）；
//     无 stamping 的旧投递文本 sentAt=undefined（不渲染错误时间）；
//  ② steer 卡（SteerCollabCard）foot 行渲染 .im-source-card__time + .im-source-card__copy，
//     时间文案与 formatMessageTimestamp 三档同源；
//  ③ user-row 卡（Message.tsx im-source-card）同款位置出现时间+复制；
//  ④ 折叠态（705 autoFold 开）foot 行仍在（时间+复制入口不随正文折叠消失）；
//  ⑤ 非跨会话普通用户消息：msg-meta 时间+复制行为零变化（回归护栏）。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { COLLAB_MSG_PREFIX, collabAsImSource } from "../lib/collabMessage";
import { STEER_NOTICE_PREFIX } from "../lib/useController";

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
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div><div class=\"chat-pane\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
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
  Object.defineProperty(dom.window, "matchMedia", {
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
  return dom;
}

function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ClipboardSetText: async (_text: string) => true,
        ...methods,
      },
      Directory: {
        ListAddressableSessions: async () => [],
        ...((methods as { Directory?: Record<string, unknown> }).Directory ?? {}),
      },
    },
  };
}

async function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

const FROM_ID = "sc_aaaa1111bbbb2222cccc3333";
const TO_ID = "sc_dddd4444eeee5555ffff6666";
const SENT_STAMP = "2026-10-19T22:09:00+08:00 Mon";

// 周三后缀「Mon」是给人读的标注，Date.parse 不认（Windows/部分平台解析结果
// 不稳定）；被测件同样剥掉后缀再解析，比较用剥后主体即可，渲染文案比较不受
// 影响（formatMessageTimestamp 只看 Date 对象）。
const SENT_MS = Date.parse(SENT_STAMP.replace(/\s+[A-Za-z]{3}$/, ""));

// 866/1553 stamping 后的投递文本形态：header（含发送于）+ 正文 + `---` 元数据尾。
function stampedText(body: string): string {
  return [
    `${COLLAB_MSG_PREFIX} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1)，发送于 ${SENT_STAMP}`,
    "",
    body,
    "",
    "---",
    "会话线程：threadId=msg_01",
  ].join("\n");
}

console.log("\ntl1092 collab card time + copy");

// ── ① 数据层解析 ──────────────────────────────────────────────────────────────
{
  const parsed = collabAsImSource(stampedText("帮我看下 758 的两张截图"));
  eq(typeof parsed?.sentAt, "number", "① header stamping 解析出 sentAt（epoch ms）");
  eq(parsed?.sentAt, SENT_MS, "① sentAt 等于 stamping 时间戳");

  const legacy = collabAsImSource([
    `${COLLAB_MSG_PREFIX} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID}`,
    "",
    "旧投递文本没有时间戳",
  ].join("\n"));
  eq(legacy?.sentAt, undefined, "① 无 stamping 的旧投递文本 sentAt=undefined");

  // 正文/元数据尾出现同形片段时不得误解析（只锚 header 行）。
  const spoof = collabAsImSource([
    `${COLLAB_MSG_PREFIX} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID}`,
    "",
    "正文里写了「，发送于 1999-01-01T00:00:00+08:00 Fri」这种话",
  ].join("\n"));
  eq(spoof?.sentAt, undefined, "① 正文里的同形片段不被误解析为发送时刻");
}

// ── ②③④⑤ 组件渲染 ───────────────────────────────────────────────────────────
{
  installDom();
  installBridgeApp({});
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  // 名单解析注入口：测试里不拉真实通讯录（462 的 hook 会在挂载时触发）。
  const { setCollabContactDirectory } = await import("../lib/collabContactNames");
  setCollabContactDirectory({ ListAddressableSessions: async () => [] });
  const { SteerCard } = await import("../components/TranscriptCards");
  const { UserMessage } = await import("../components/Message");
  const { formatMessageTimestamp } = await import("../lib/messageTimestamp");
  const { useCollabDisplayStore } = await import("../store/collabDisplay");
  await import("../components/MessageAttachments");
  const rootEl = document.getElementById("root")!;

  const renderIn = async (node: React.ReactNode) => {
    const host = document.createElement("div");
    rootEl.appendChild(host);
    const root = createRoot(host);
    await act(async () => {
      root.render(React.createElement(LocaleProvider, { children: node }));
      await flushTimers();
    });
    return {
      host,
      cleanup: async () => {
        await act(async () => {
          root.unmount();
        });
        host.remove();
      },
    };
  };

  // ② steer 卡：meta-time 行时间（1122 卡片下方）+ foot 行复制按钮
  {
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1092", text: `${STEER_NOTICE_PREFIX}${stampedText("帮我看下 758 的两张截图")}` }),
    );
    ok(host.querySelector(".im-source-card--steer") !== null, "② 跨会话 steer notice 渲染 im-source 卡");
    // 任务1122: 时间戳统一卡片下方 meta-time 行（.im-source-card__time-row，
    // 复用 .msg-meta__time 样式），不再渲染 foot 行内 .im-source-card__time。
    const timeRow = host.querySelector(".im-source-card__time-row") as HTMLElement | null;
    ok(timeRow !== null, "② 卡片下方渲染 .im-source-card__time-row（1122 统一位置）");
    const time = timeRow?.querySelector(".msg-meta__time") as HTMLTimeElement | null;
    ok(time !== null, "② meta-time 行渲染 .msg-meta__time（与用户消息同款样式）");
    const expected = formatMessageTimestamp(new Date(SENT_MS), new Date(Date.now()), "zh");
    eq(time?.textContent, expected, "② 时间文案与 formatMessageTimestamp 三档同源");
    ok(Boolean(time?.getAttribute("datetime")), "② <time> 带 datetime 属性");
    eq(host.querySelector(".im-source-card__time"), null, "② foot 行内旧 .im-source-card__time 不再渲染（1122 收敛）");
    const copy = host.querySelector(".im-source-card__copy") as HTMLButtonElement | null;
    ok(copy !== null && copy.getAttribute("type") === "button", "② 卡片 foot 行渲染复制按钮（1109/1122 保留）");
    await cleanup();
  }

  // ③ user-row 卡（Message.tsx）：同款 msg-meta 时间 + foot 复制按钮
  {
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1092", text: stampedText("跨会话正文在 user row") }),
    );
    ok(host.querySelector(".im-source-card") !== null && host.querySelector(".msg--im-source") !== null, "③ user row 跨会话消息卡片化");
    // 任务1122: 时间戳统一用户消息 msg-meta 行（不再渲染 foot 行内
    // .im-source-card__time）。
    const metaTime = host.querySelector(".msg-meta .msg-meta__time") as HTMLTimeElement | null;
    ok(metaTime !== null, "③ user-row 卡时间戳渲染在下方 msg-meta 行（1122 统一位置）");
    eq(metaTime?.textContent, formatMessageTimestamp(new Date(SENT_MS), new Date(Date.now()), "zh"), "③ 时间文案与 formatMessageTimestamp 三档同源（1012 契约不变）");
    eq(host.querySelector(".im-source-card__time"), null, "③ foot 行内旧 .im-source-card__time 不再渲染（1122 收敛）");
    ok(host.querySelector(".im-source-card__copy") !== null, "③ user-row 卡 foot 行渲染复制按钮（保留）");
    await cleanup();
  }

  // ④ 折叠态：foot 行时间+复制入口仍在（不随正文折叠消失）
  {
    useCollabDisplayStore.setState({ autoFold: true });
    const longBody = Array.from({ length: 20 }, (_, index) => `第 ${index + 1} 行内容，用于触发 705 autoFold 阈值`).join("\n");
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1092-fold", text: `${STEER_NOTICE_PREFIX}${stampedText(longBody)}` }),
    );
    ok(host.querySelector(".im-source-card__text--folded") !== null, "④ 超长正文折叠为摘要条");
    ok(host.querySelector(".im-source-card__time-row .msg-meta__time") !== null, "④ 折叠态卡片下方 meta-time 行仍有时间戳");
    ok(host.querySelector(".im-source-card__copy") !== null, "④ 折叠态 foot 行仍有复制按钮");
    await cleanup();
    useCollabDisplayStore.setState({ autoFold: false });
  }

  // ⑤ 回归护栏：普通用户消息（非跨会话）msg-meta 时间+复制照常，且无卡片 foot 元素
  {
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-plain", text: "先跑测试再改", createdAt: SENT_MS }),
    );
    eq(host.querySelector(".im-source-card"), null, "⑤ 普通用户消息不渲染跨会话卡片");
    eq(host.querySelector(".msg-meta__time")?.textContent, formatMessageTimestamp(new Date(SENT_MS), new Date(Date.now()), "zh"), "⑤ 普通用户消息 msg-meta 时间零变化");
    ok(host.querySelector(".msg-meta__copy") !== null, "⑤ 普通用户消息 msg-meta 复制按钮零变化");
    await cleanup();
  }
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
