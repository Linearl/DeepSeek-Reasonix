// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1235-steer-icon-inline.test.tsx
//
// 任务 tl1235（P3）：引导消息（steer 卡片）回复图标 ↪ 强制内联——任何时候
// 不得独占一行（用户 20261028 双截图实证：23:41 消息图标独占首行、23:42
// 消息同行内联）。
//
// 根因（机制级）：.steer-line__bubble 是 inline-flex + flex-wrap（任务 1136
// 为附件分层引入），flex 行打包按 item 未收缩的 hypothetical size 判断——
// 正文一长，.steer-line__text 以整段 max-content 宽被打包到第二行，↪ 图标
// 独占首行。短文本则同行内联——「偶发」即文本长度阈值，与渲染时序无关。
//
// 修复：.steer-line__bubble .steer-line__text { flex: 1 1 0 }——text 打包
// 尺寸恒为 0，任何文本长度、任何渲染路径（首次注入/重放/重入副本共用本
// 类）都打包进 icon 所在首行；CSS 层面保证，不依赖渲染分支恰好正确。
//
// 本文件钉两层可证伪契约（JSDOM 无真实布局，视觉结论由 CSS 规范的行打包
// 规则从契约推出：flex-basis 0 ⇒ 打包尺寸 0 ⇒ 恒进首行）：
//  ① DOM 同层：非 deferred / deferred 两分支，icon 均为气泡首个子元素、
//     text 均为其直接兄弟（同 flex 层，长文本下结构不变）；
//  ② CSS 机制：text 的 flex: 1 1 0 在位；bubble 仍 flex-wrap: wrap（附件
//     分层语境）；icon 仍 flex: 0 0 auto（不被压缩）。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

function installBridgeApp() {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: { App: { Commands: async () => [], Models: async () => [], ModelsForTab: async () => [] } },
  };
}

async function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// ── ② CSS 机制契约（读 styles.css，剥注释防散文误配） ──
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

  function matchingBlocks(selector: string): string[] {
    const blocks: string[] = [];
    const rule = /([^{}]+)\{([^{}]*)\}/g;
    let match: RegExpExecArray | null;
    while ((match = rule.exec(styles)) !== null) {
      const selectors = match[1].split(",").map((part) => part.trim());
      if (selectors.includes(selector)) blocks.push(match[2]);
    }
    return blocks;
  }

  function finalDeclaration(selector: string, property: string): string | undefined {
    let value: string | undefined;
    for (const block of matchingBlocks(selector)) {
      const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
      let match: RegExpExecArray | null;
      while ((match = declaration.exec(block)) !== null) {
        value = match[1].trim();
      }
    }
    return value;
  }

  console.log("\ntl1235 steer icon inline css");
  eq(finalDeclaration(".steer-line__bubble .steer-line__text", "flex"), "1 1 0", "text flex: 1 1 0（打包尺寸 0，恒进 icon 所在首行）");
  eq(finalDeclaration(".steer-line__bubble", "flex-wrap"), "wrap", "bubble 仍 flex-wrap: wrap（附件分层语境，1136 语义保持）");
  eq(finalDeclaration(".steer-line__icon", "flex"), "0 0 auto", "icon 仍 flex: 0 0 auto（前缀位不被压缩）");
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

// ── ① DOM 同层契约（非 deferred 长文本 + deferred） ──
console.log("\ntl1235 steer icon inline dom");

const dom = installDom();
installBridgeApp();
const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
const { SteerCard } = await import("../components/TranscriptCards");
const rootEl = document.getElementById("root")!;

const freshHost = () => {
  const host = document.createElement("div");
  rootEl.appendChild(host);
  return { host, root: createRoot(host) };
};

const render = async (host: HTMLElement, root: ReturnType<typeof createRoot>, node: React.ReactElement) => {
  await act(async () => {
    root.render(<LocaleProvider>{node}</LocaleProvider>);
    for (let i = 0; i < 3; i++) await flushTimers();
  });
};

// 断言：气泡内 icon 是首个子元素，text 是 icon 的紧邻后继兄弟（同 flex 层）。
function assertIconInline(bubble: Element | null, label: string) {
  ok(bubble !== null, `${label} 气泡渲染`);
  if (!bubble) return;
  const first = bubble.firstElementChild;
  ok(first !== null && first.classList.contains("steer-line__icon"), `${label} icon 是气泡首个子元素（前缀位）`);
  const icon = bubble.querySelector(".steer-line__icon");
  const text = bubble.querySelector(":scope > .steer-line__text");
  ok(text !== null, `${label} text 是气泡直接子元素（与 icon 同 flex 层）`);
  ok(
    icon !== null && text !== null && Boolean(text.compareDocumentPosition(icon) & Node.DOCUMENT_POSITION_PRECEDING),
    `${label} icon 在 text 之前（内联前缀次序）`,
  );
}

// ①a 非 deferred：长文本（超一行，基线 flex-wrap 行为下 icon 会独占首行的场景）
{
  const { host, root } = freshHost();
  const longBody = `这是一条很长的引导消息用于验证图标内联。`.repeat(12);
  await render(host, root, <SteerCard id="s-1235a" text={`${STEER_NOTICE_PREFIX}${longBody}`} />);
  assertIconInline(host.querySelector(".steer-line__bubble"), "①a 非 deferred 长文本");
  const textEl = host.querySelector(".steer-line__bubble > .steer-line__text");
  ok(textEl?.textContent?.includes("验证图标内联") === true, "①a 正文完整渲染在 text 层");
  await act(async () => { root.unmount(); });
  host.remove();
}

// ①b 非 deferred：短文本（单行场景，结构同样必须同层）
{
  const { host, root } = freshHost();
  await render(host, root, <SteerCard id="s-1235b" text={`${STEER_NOTICE_PREFIX}短引导`} />);
  assertIconInline(host.querySelector(".steer-line__bubble"), "①b 非 deferred 短文本");
  await act(async () => { root.unmount(); });
  host.remove();
}

// ①c deferred 分支（折叠占位按钮内 icon + status + text + chevron）
{
  const { host, root } = freshHost();
  await render(host, root, <SteerCard id="s-1235c" text={`${STEER_NOTICE_PREFIX}推迟引导正文`} deferred />);
  assertIconInline(host.querySelector(".steer-line__bubble--deferred"), "①c deferred 折叠态");
  await act(async () => { root.unmount(); });
  host.remove();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
