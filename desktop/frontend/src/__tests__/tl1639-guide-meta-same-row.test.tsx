// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1639-guide-meta-same-row.test.tsx
//
// tl1639（P2，用户截图 clipboard-20261116-133830）：引导消息时间戳与复制按钮
// 未同行——布局收尾钉。1122 把时间戳统一到「卡片下方」时复制按钮留在原地
// （卡内 foot 行 / 独立 meta 行），形成「复制一行、时间戳另一行」的两行分裂
// （user-row 卡还叠加双复制）。本件把四个引导面的复制并入时间戳行：
//  ① user-row 跨会话卡（Message.tsx）：卡内 .im-source-card__meta 不再渲染
//     复制（防回潮），复制=下方 .msg-meta 行的 .msg-meta__copy，与
//     .msg-meta__time 同一父节点（flex 同行；.msg--user 是 align-items:
//     flex-end，行整体右对齐——与普通用户消息同构）；
//  ② 跨会话 steer 卡（SteerCollabCard）：复制（.im-source-card__copy）落
//     .im-source-card__time-row 与 .msg-meta__time 同一父节点，行
//     justify-content:flex-end 右对齐；
//  ③ 非 collab steer 气泡（SteerCard）：独立复制 .steer-meta 行删除，
//     .steer-line__copy 并入 .steer-meta--time 与时间戳同一父节点；
//  ④ 723 deferred 占位（非 collab 展开态）：复制渲染时机不变（展开且非
//     collab），但落 .steer-meta--time 行；折叠态仍无复制按钮（723 语义）。
// 同行钉手法：JSDOM 无布局引擎，用「同一 flex 父节点」断言同行（父是
// display:flex + justify-content:flex-end/gap 源码级钉），与 1012 ③
// 「源码钉住」同一手法。
// 零回退：普通用户消息 msg-meta 行（时间+复制同一父节点）对照断言；三语
// 复制文案走既有 t("msg.copy") 键（A/B 面）零改动；触达 CSS 规则只用 flex/
// gap 机制、无新增颜色字面量（暗色/主题包继承既有 token）。
//
// DOM/桥接模式复用 tl1121-1122/tl1092：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh"))。注意夹具用带闭括号的完整投递头
// （`[跨会话消息]`——tl1566 起 COLLAB_MSG_PREFIX 是不带闭括号的标记族
// 前缀，测试夹具须自带 `]`，否则 collabAsImSource 判非投递形态）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { COLLAB_MSG_PREFIX } from "../lib/collabMessage";
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
      removeEventListener() {},
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
        AttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
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
const SENT_MS = Date.parse("2026-10-19T22:09:00+08:00");

/** 带闭括号的完整投递头（tl1566 标记族前缀 + 测试夹具自带 `]`）。 */
function stampedText(body: string): string {
  return [
    `${COLLAB_MSG_PREFIX}] 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1)，发送于 ${SENT_STAMP}`,
    "",
    body,
  ].join("\n");
}

console.log("\ntl1639 guide card meta rows: timestamp + copy on the same row");

// ── ⑤ CSS 源码钉（同行右对齐机制，先于 DOM 断言独立成立） ──
{
  const here = dirname(fileURLToPath(import.meta.url));
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  ok(/\.im-source-card__time-row \{[^}]*justify-content: flex-end;/s.test(css), "⑤ B 面 time-row 右对齐（justify-content: flex-end）");
  ok(/\.im-source-card__time-row \{[^}]*gap: 8px;/s.test(css) && /\.im-source-card__time-row \{[^}]*align-items: center;/s.test(css), "⑤ B 面 time-row 同行间隙+垂直居中（复制与时间戳并排）");
  const copyRule = /\.im-source-card__copy \{[^}]*\}/.exec(css)?.[0] ?? "";
  ok(copyRule !== "" && !copyRule.includes("margin-left: auto"), "⑤ B 面 .im-source-card__copy 无 margin-left:auto（防时间戳被推到行左端）");
  const steerCopyRule = /\.steer-meta \.steer-line__copy \{[^}]*\}/.exec(css)?.[0] ?? "";
  ok(steerCopyRule.includes("margin-left: 0") && steerCopyRule.includes("align-self: center"), "⑤ C/D 面 .steer-meta .steer-line__copy 压掉 auto margin/贴底（同行贴尾右对齐）");
  ok(/\.steer-meta--time \{[^}]*justify-content: flex-end;/s.test(css), "⑤ C/D 面 .steer-meta--time 右对齐保留");
  ok(!css.includes(".steer-line__expanded .steer-line__copy"), "⑤ 展开块内 copy 专属死规则已删（tl1639 形态收敛）");
  const msgSource = readFileSync(resolve(here, "../components/Message.tsx"), "utf8");
  eq(msgSource.includes("im-source-card__copy"), false, "⑤ A 面 Message.tsx 无 im-source-card__copy 残留（防回潮）");
}

// ── ①-④ DOM 渲染 ──
{
  installDom();
  installBridgeApp({});
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { setCollabContactDirectory } = await import("../lib/collabContactNames");
  setCollabContactDirectory({ ListAddressableSessions: async () => [] });
  const { UserMessage } = await import("../components/Message");
  const { SteerCard } = await import("../components/TranscriptCards");
  await import("../components/MessageAttachments");
  const rootEl = document.getElementById("root")!;

  const renderIn = async (node: React.ReactNode) => {
    const host = document.createElement("div");
    rootEl.appendChild(host);
    const root = createRoot(host);
    await act(async () => {
      root.render(React.createElement(LocaleProvider, { children: node }));
      for (let i = 0; i < 5; i++) await flushTimers();
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

  // ① user-row 跨会话卡：卡内零复制；时间戳+复制同一 .msg-meta 父节点
  {
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1639", text: stampedText("跨会话引导正文") }),
    );
    ok(host.querySelector(".im-source-card") !== null, "① 投递文本卡片化（夹具自检：collabAsImSource 命中）");
    const cardMeta = host.querySelector(".im-source-card__meta");
    ok(cardMeta !== null, "① 卡内 route chip 行渲染");
    ok(cardMeta?.querySelector(".copybtn, .im-source-card__copy") === null, "① 卡内零复制按钮（防回潮，双复制现场不回潮）");
    const metaRow = host.querySelector(".msg-meta");
    const time = metaRow?.querySelector(".msg-meta__time") ?? null;
    const copy = metaRow?.querySelector(".msg-meta__copy") ?? null;
    ok(metaRow !== null && time !== null && copy !== null, "① 下方 .msg-meta 行带时间戳+复制");
    ok(time !== null && copy !== null && time.parentElement === copy.parentElement, "① 时间戳与复制同一父节点（flex 同行钉）");
    ok(time !== null && Boolean(time.textContent), "① 时间文案渲染（13:32 同款三档）");
    await cleanup();
  }

  // ② 跨会话 steer 卡：复制落 time-row 与时间戳同一父节点
  {
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1639-collab", text: `${STEER_NOTICE_PREFIX}${stampedText("跨会话 steer 正文")}` }),
    );
    ok(host.querySelector(".im-source-card--steer") !== null, "② steer notice 卡片化");
    const cardMeta = host.querySelector(".im-source-card__meta");
    ok(cardMeta?.querySelector(".copybtn, .im-source-card__copy") === null, "② 卡内 meta 行零复制（防回潮）");
    const timeRow = host.querySelector(".im-source-card__time-row");
    const time = timeRow?.querySelector(".msg-meta__time") ?? null;
    const copy = timeRow?.querySelector(".im-source-card__copy") ?? null;
    ok(timeRow !== null && time !== null && copy !== null, "② 卡下 time-row 同时渲染时间戳+复制");
    ok(time !== null && copy !== null && time.parentElement === copy.parentElement, "② 时间戳与复制同一父节点（flex 同行钉）");
    await cleanup();
  }

  // ③ 非 collab steer 气泡：单一 .steer-meta 行承载时间戳+复制
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1639", text: `${STEER_NOTICE_PREFIX}普通引导`, createdAt }),
    );
    const metaRows = Array.from(host.querySelectorAll(".steer-meta"));
    eq(metaRows.length, 1, "③ 只剩一张 .steer-meta 行（独立复制行已删）");
    const time = metaRows[0]?.querySelector(".msg-meta__time") ?? null;
    const copy = metaRows[0]?.querySelector(".steer-line__copy") ?? null;
    ok(time !== null && copy !== null, "③ 该行同时渲染时间戳+复制");
    ok(time !== null && copy !== null && time.parentElement === copy.parentElement, "③ 时间戳与复制同一父节点（flex 同行钉）");
    const bubble = host.querySelector(".steer-line__bubble");
    ok(bubble !== null && copy !== null && !bubble.contains(copy), "③ 复制在气泡之外（1091 契约不回退）");
    await cleanup();
  }

  // ④ 723 deferred：折叠态 time 无复制；展开态 time+copy 同行（时机不变）
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1639-def", text: `${STEER_NOTICE_PREFIX}延迟引导第一行\n第二行展开才可见`, deferred: true, createdAt }),
    );
    const timeRow = host.querySelector(".steer-meta--time");
    ok(timeRow?.querySelector(".msg-meta__time") !== null, "④ 折叠态 time-row 渲染时间戳");
    eq(timeRow?.querySelector(".steer-line__copy") ?? null, null, "④ 折叠态无复制按钮（723 语义不变）");
    const button = host.querySelector("button.steer-line__bubble--deferred") as HTMLButtonElement | null;
    ok(button !== null, "④ 折叠占位按钮渲染");
    if (button) {
      await act(async () => {
        button.click();
        await flushTimers();
      });
      const expandedRow = host.querySelector(".steer-meta--time");
      const time = expandedRow?.querySelector(".msg-meta__time") ?? null;
      const copy = expandedRow?.querySelector(".steer-line__copy") ?? null;
      ok(time !== null && copy !== null, "④ 展开态 time-row 渲染时间戳+复制");
      ok(time !== null && copy !== null && time.parentElement === copy.parentElement, "④ 展开态时间戳与复制同一父节点（flex 同行钉）");
    }
    await cleanup();
  }

  // ⑤ 零回退对照：普通用户消息 msg-meta 行时间+复制同行（既有形态）
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1639-plain", text: "普通消息", createdAt }),
    );
    const metaRow = host.querySelector(".msg-meta");
    const time = metaRow?.querySelector(".msg-meta__time") ?? null;
    const copy = metaRow?.querySelector(".msg-meta__copy") ?? null;
    ok(time !== null && copy !== null && time.parentElement === copy.parentElement, "⑤ 普通用户消息时间戳+复制同一 .msg-meta 行（零回退对照）");
    eq(host.querySelector(".im-source-card"), null, "⑤ 普通消息不卡片化（形态不变）");
    await cleanup();
  }
}

process.stdout.write(`\ntl1639 guide meta same-row: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
