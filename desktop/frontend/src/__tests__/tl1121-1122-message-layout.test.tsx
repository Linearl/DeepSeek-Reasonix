// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1121-1122-message-layout.test.tsx
//
// 任务1136（P0 重做）+1122（P2）：消息卡片图文排版统一基准 + 时间戳位置统一。
//
// 1136（图文排版回归 17 包前用户消息基准——用户 2026-10-25 加急令）：
//  1121 的 gallery 大图块（.msg-attachment-gallery，图片最大 320px 直排）实测
//  回归（图片过大 + 三类消息不统一），本件整体回退 gallery 形态，三类消息
//  （用户消息 / steer 引导 / 未应用引导 notice）统一到「17 包前用户消息」
//  布局：文字在上、图片在下、多图横排、56px 小缩略图。
//  ① 用户消息带图（UserMessage + MessageAttachments）：文字块与附件容器
//     （.msg-attachments）上下分离，图片渲染为 .msg-attachment--image 缩略卡；
//  ② steer 引导消息带图（SteerCard）：同一缩略卡形态（1091 上下分离不回退）；
//  ③ 助手消息带图（AssistantMessage Markdown img）：`.md > img / p > img` 的
//     `display:block + max-width:100%` 防竖排契约源码级钉住（JSDOM 无布局引擎，
//     无法直接断言盒模型，改为读 CSS 文本断言规则存在——与 1012 ③「源码钉住」
//     同一手法）；
//  ④ 统一基准源码断言：.msg-attachment__icon--image 56×56 小缩略图 + 
//     .msg-attachments flex-wrap 横排（多图一行排开、超出容器宽换行）+
//     gallery 大图块零残留（防回潮）。
//
// 1122（三类消息时间戳统一卡片下方）：
//  ⑤ 普通用户消息：time.msg-meta__time 在 .msg-meta 行（卡片下方）；
//  ⑥ 跨会话用户消息卡（UserMessage im-source）：时间戳移出 foot 行
//     （.im-source-card__time 不再渲染），渲染在 .msg-meta.msg-meta__time 行；
//  ⑦ 跨会话 steer 卡（SteerCollabCard）：时间戳渲染在 .im-source-card__time-row
//     （卡片下方 meta 行，复用 .msg-meta__time 样式）；foot 行保留复制按钮；
//  ⑧ 非跨会话 steer 气泡（SteerCard 非 deferred 非 collab）：时间戳渲染在
//     .steer-meta--time 行（卡片下方），气泡内不再有 .steer-line__time；
//  ⑨ 未应用引导 notice 块（NoticeCard unapplied_steer）：时间戳从
//     .notice-line__text 内末尾移到 .notice-line__text 之后（卡片容器末尾）；
//  ⑩ 助手消息（AssistantMessage createdAt）：时间戳渲染在下方 .msg-meta 行。
//
// 1091 零回归：tl1066-steer-image.test.tsx 独立钉住 steer 图文分离/复制按钮
// 出卡片契约（27/0），本文件不重复断言其内部细节，只验证 1136 回退后 steer
// 面仍满足「文字块在附件容器之前」。
//
// DOM/桥接模式复用 tl1066/tl1092：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh"))。

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

async function waitFor(check: () => boolean, attempts = 20): Promise<void> {
  for (let i = 0; i < attempts; i++) {
    if (check()) return;
    await act(async () => {
      await flushTimers();
    });
  }
}

const IMG_REF = "@[shot.png](.reasonix/attachments/shot-1121.png)";
const FROM_ID = "sc_aaaa1111bbbb2222cccc3333";
const TO_ID = "sc_dddd4444eeee5555ffff6666";
const SENT_STAMP = "2026-10-19T22:09:00+08:00 Mon";

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

console.log("\ntl1136+1122 message layout (17-pack-era thumbnail baseline) + timestamp position");

// ④ 统一基准源码断言（1136 重做）：56×56 小缩略图 + 附件容器 flex-wrap 横排
// + gallery 大图块零残留（JSDOM 无布局引擎，改源码级钉住 CSS 规则存在——
// 与 1012 ③ 手法一致）
{
  const here = dirname(fileURLToPath(import.meta.url));
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  ok(/\.msg-attachment__icon--image \{[^}]*width: 56px;[^}]*height: 56px;/s.test(css), "④ 缩略图 56×56 小图基准（17 包前用户消息形态，非大图直排）");
  ok(/\.msg-attachments \{[^}]*flex-wrap: wrap;/s.test(css), "④ 附件容器 flex-wrap 横排（多图一行排开，超出容器宽换行）");
  ok(!css.includes(".msg-attachment-gallery"), "④ styles.css 无 1121 gallery 大图块残留（回退干净）");
  ok(!readFileSync(resolve(here, "../components/MessageAttachments.tsx"), "utf8").includes("msg-attachment-gallery"), "④ 组件无 MessageImageGallery/gallery 类名残留（回退干净）");
  // Markdown 内联图（助手消息路径）的防竖排契约（MarkdownImage.css 既有规则，
  // 原 1121 验收要求「窄容器下图片不出现在文字流内」覆盖助手消息带图场景）。
  const mdCss = readFileSync(resolve(here, "../components/MarkdownImage.css"), "utf8");
  ok(/\.md p > img \{[^}]*display: block;/s.test(mdCss) && /\.md p > img \{[^}]*max-width: 100%;/s.test(mdCss),
    "③ 助手消息 Markdown 图 display:block + max-width:100%（防竖排契约不回退）");
}

// ── DOM 渲染：用户消息 / steer / 协作卡 / notice / 助手消息 ──
{
  installDom();
  installBridgeApp({});
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { setCollabContactDirectory } = await import("../lib/collabContactNames");
  setCollabContactDirectory({ ListAddressableSessions: async () => [] });
  const { UserMessage, AssistantMessage } = await import("../components/Message");
  const { SteerCard, NoticeCard } = await import("../components/TranscriptCards");
  const { formatMessageTimestamp } = await import("../lib/messageTimestamp");
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

  // ① 用户消息带图：文字块在附件容器之前（上下分离）+ 图片走 56px 缩略卡
  {
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1121", text: `看下这张图 ${IMG_REF}` }),
    );
    const textBlock = host.querySelector(".msg--user .msg__text");
    const attachRow = host.querySelector(".msg--user .msg-attachments");
    ok(textBlock !== null && attachRow !== null, "① 用户消息文字块与附件容器均渲染");
    ok(
      textBlock !== null && attachRow !== null
        && Boolean(textBlock.compareDocumentPosition(attachRow) & Node.DOCUMENT_POSITION_FOLLOWING),
      "① 用户消息文字块在附件容器之前（文字上/图下，非嵌进文字流）",
    );
    await waitFor(() => Boolean(host.querySelector(".msg-attachment--image img")));
    ok(host.querySelector(".msg-attachment--image") !== null, "① 用户消息图片渲染为缩略卡（17 包前基准，非大图块）");
    eq(host.querySelector(".msg-attachment-gallery"), null, "① 用户消息无 gallery 大图块（1121 形态已回退）");
    await cleanup();
  }

  // ② steer 引导消息带图：1091 分离不回退 + 同一缩略卡形态（三类统一基准）
  {
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1121", text: `${STEER_NOTICE_PREFIX}看看这张截图 ${IMG_REF}` }),
    );
    // TranscriptCards 侧的 MessageAttachments 是独立 lazy 实例（Message.tsx
    // 同款模式但模块对象不同），首渲染挂 Suspense——先等附件容器提交再断言。
    await waitFor(() => Boolean(host.querySelector(".steer-line__bubble .msg-attachments")));
    const textBlock = host.querySelector(".steer-line__text");
    const attachRow = host.querySelector(".steer-line__bubble .msg-attachments");
    ok(textBlock !== null && attachRow !== null, "② steer 气泡文字块与附件容器均渲染（1091 分离不回退）");
    ok(
      textBlock !== null && attachRow !== null
        && Boolean(textBlock.compareDocumentPosition(attachRow) & Node.DOCUMENT_POSITION_FOLLOWING),
      "② steer 气泡文字块在附件容器之前（文字上/图下）",
    );
    ok(attachRow !== null && attachRow.querySelector(".msg-attachment--image") !== null, "② steer 气泡图片渲染为同一缩略卡（与用户消息同款）");
    eq(host.querySelector(".msg-attachment-gallery"), null, "② steer 气泡无 gallery 大图块（1121 形态已回退）");
    await cleanup();
  }

  // ⑤ 普通用户消息：时间戳渲染在卡片下方 .msg-meta 行（基准样式）
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1122", text: "普通消息", createdAt }),
    );
    const time = host.querySelector(".msg-meta .msg-meta__time") as HTMLTimeElement | null;
    ok(time !== null, "⑤ 普通用户消息时间戳渲染在 .msg-meta 行（卡片下方基准样式）");
    eq(time?.textContent, formatMessageTimestamp(new Date(createdAt), new Date(Date.now()), "zh"), "⑤ 时间文案与 formatMessageTimestamp 同源（1012 契约）");
    await cleanup();
  }

  // ⑥ 跨会话用户消息卡：时间戳移出 foot 行，落 .msg-meta 行
  {
    const { host, cleanup } = await renderIn(
      React.createElement(UserMessage, { id: "u-1122-collab", text: stampedText("跨会话正文") }),
    );
    ok(host.querySelector(".im-source-card") !== null, "⑥ 跨会话用户消息渲染 im-source 卡");
    eq(host.querySelector(".im-source-card__time"), null, "⑥ foot 行内旧 .im-source-card__time 不再渲染（1122 收敛）");
    ok(host.querySelector(".msg-meta .msg-meta__time") !== null, "⑥ 时间戳渲染在卡片下方 .msg-meta 行");
    ok(host.querySelector(".im-source-card__copy") !== null, "⑥ foot 行保留复制按钮（1109/1122）");
    await cleanup();
  }

  // ⑦ 跨会话 steer 卡：时间戳落 .im-source-card__time-row（卡片下方 meta 行）
  {
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1122-collab", text: `${STEER_NOTICE_PREFIX}${stampedText("跨会话 steer 正文")}` }),
    );
    ok(host.querySelector(".im-source-card--steer") !== null, "⑦ 跨会话 steer notice 渲染 im-source 卡");
    const timeRow = host.querySelector(".im-source-card__time-row");
    ok(timeRow !== null, "⑦ 卡片下方渲染 .im-source-card__time-row（1122 统一位置）");
    ok(timeRow?.querySelector(".msg-meta__time") !== null, "⑦ meta-time 行复用 .msg-meta__time 样式（与用户消息同款）");
    eq(host.querySelector(".im-source-card__time"), null, "⑦ foot 行内旧 .im-source-card__time 不再渲染");
    ok(host.querySelector(".im-source-card__copy") !== null, "⑦ foot 行保留复制按钮（1109/1122）");
    await cleanup();
  }

  // ⑧ 非跨会话 steer 气泡：时间戳移出卡片、落 .steer-meta--time 行
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(SteerCard, { id: "s-1122", text: `${STEER_NOTICE_PREFIX}这是一条 steer 引导`, createdAt }),
    );
    const bubble = host.querySelector(".steer-line__bubble");
    const timeRow = host.querySelector(".steer-meta--time");
    ok(bubble !== null && timeRow !== null, "⑧ steer 气泡卡片与 .steer-meta--time 行均渲染");
    ok(
      bubble !== null && timeRow !== null && !bubble.contains(timeRow as Node),
      "⑧ steer 时间戳在卡片之外（.steer-meta--time 行，1122 统一位置）",
    );
    ok(bubble?.querySelector(".steer-line__time") === null, "⑧ 气泡内旧 .steer-line__time 不再渲染（998→1122 收敛）");
    await cleanup();
  }

  // ⑨ 未应用引导 notice 块：时间戳从 .notice-line__text 内末尾移到 .notice-line
  // 卡片容器末尾（actionsRow/detail 之后，卡片最末尾；卡片是 flex column，
  // 时间戳落在正文下方）。不再单独包一层 flex column 外壳（历史尝试过
  // .notice-line-wrap，JSX 里 notice-line 与 time 同级已满足「卡片下方」
  // 视觉口径，不为此多包一层 DOM）。
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(NoticeCard, {
        item: {
          kind: "notice",
          id: "n-1122",
          level: "warn",
          code: "unapplied_steer",
          text: `本条引导未能应用，如仍需要请重新发送：
看看这张截图 ${IMG_REF}`,
          createdAt,
        } as never,
      }),
    );
    const noticeLine = host.querySelector(".notice-line");
    const noticeText = host.querySelector(".notice-line__text");
    const time = host.querySelector(".notice-line__time");
    ok(noticeLine !== null && noticeText !== null && time !== null, "⑨ notice 卡片/正文/时间戳均渲染");
    // 1136: notice（第三类消息）带图同样渲染统一缩略卡（.msg-attachments 容器
    // 内 .msg-attachment--image），与用户消息/steer 气泡同款——三类消息统一基准。
    // （MessageAttachments 为 lazy 组件，先等附件容器提交再断言。）
    await waitFor(() => Boolean(host.querySelector(".notice-line__body .msg-attachments")));
    const noticeAttachRow = host.querySelector(".notice-line__body .msg-attachments");
    ok(noticeAttachRow !== null, "⑨ notice 带图渲染附件容器（三类统一基准）");
    ok(noticeAttachRow !== null && noticeAttachRow.querySelector(".msg-attachment--image") !== null, "⑨ notice 图片渲染为同一缩略卡（与用户消息/steer 同款）");
    eq(host.querySelector(".msg-attachment-gallery"), null, "⑨ notice 无 gallery 大图块（1121 形态已回退）");
    // 1122: .notice-line__time 不再是 .notice-line__text 内部末尾的子元素，
    // 而是 .notice-line 卡片容器的末尾子节点（actionsRow/detail 之后）——
    // 卡片是 flex column，时间戳落在正文下方（「卡片下方」统一位置口径）。
    ok(
      noticeLine !== null && noticeText !== null && time !== null
        && time.parentElement === noticeLine
        && noticeText.parentElement === noticeLine
        && !noticeText.contains(time as Node)
        && Boolean(noticeText.compareDocumentPosition(time) & Node.DOCUMENT_POSITION_FOLLOWING),
      "⑨ notice 时间戳是卡片末尾子节点、在正文之后（1122 移到卡片下方）",
    );
    await cleanup();
  }

  // ⑩ 助手消息：时间戳渲染在下方 .msg-meta 行
  {
    const createdAt = new Date(2026, 9, 19, 22, 9, 0).getTime();
    const { host, cleanup } = await renderIn(
      React.createElement(AssistantMessage, {
        item: {
          kind: "assistant",
          id: "a-1122",
          text: "助手消息正文",
          reasoning: "",
          streaming: false,
          createdAt,
        } as never,
      }),
    );
    const time = host.querySelector(".msg-meta .msg-meta__time") as HTMLTimeElement | null;
    ok(time !== null, "⑩ 助手消息时间戳渲染在 .msg-meta 行（1122 补齐）");
    eq(time?.textContent, formatMessageTimestamp(new Date(createdAt), new Date(Date.now()), "zh"), "⑩ 助手消息时间文案与 formatMessageTimestamp 同源");
    await cleanup();
  }
}

process.stdout.write(`\ntl1136+1122 message layout: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
