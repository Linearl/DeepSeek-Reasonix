// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1421-turn-file-chip.test.tsx
//
// tl1421 验收：turn 结算行「本轮文件」chip 可点击——
//  ① 探针（ResolveOpenablePathForTab，相对路径按会话项目根解析+存在性+门禁
//     一次判定）通过 → chip 可点击态：左键 = OpenLocalPath（默认应用），且
//     传给 bridge 的是解析后的绝对路径（不是工具 args 里的原始相对路径）；
//  ② 探针空串（不存在/已移动/逃出工作区/可执行目标）→ chip 置灰
//     （--missing、无 role=button），title 带说明；点击不发死调用
//     （OpenLocalPath 零调用）；
//  ③ 右键 = 次级入口：ContextMenu 两项（用默认应用打开 / 在文件管理器中
//     显示），文案与 1060 选中路径菜单同源；选中「在文件管理器中显示」→
//     RevealPath 收到绝对路径，菜单关闭。
// 静态钉：三语 turnActions.fileMissing 键 + CSS 可点击/置灰两规则存在。
// 回归：tl1137（chip 渲染面）与 tl1155（tally 行为）各自独立跑全绿。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

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

console.log("\ntl1421 turn file chip click-to-open");

// ── ④ 静态钉：三语文案键 + CSS 规则 ─────────────────────────────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  for (const name of ["zh", "en", "zh-TW"] as const) {
    const source = readFileSync(resolve(here, `../locales/${name}.ts`), "utf8");
    ok(source.includes('"turnActions.fileMissing"'), `④ ${name}.ts 含 turnActions.fileMissing 键（三语齐）`);
  }
  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  ok(/\.turn-actions__file-chip--openable \{[^}]*cursor:\s*pointer;/s.test(css), "④ CSS 可点击态规则存在（pointer）");
  ok(/\.turn-actions__file-chip--missing \{[^}]*opacity:\s*0\.45;/s.test(css), "④ CSS 缺失态规则存在（置灰）");
}

// ── ①②③ DOM 级：探针三态 + 打开/置灰/定位入口 ───────────────────────────────
{
  class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
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
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;

  // mock bridge：记录三类调用；探针返回值由用例现场改（probeReply）。
  const calls = {
    probe: [] as Array<[string, string]>,
    open: [] as string[],
    reveal: [] as string[],
  };
  let probeReply: (path: string) => string = (path) => path;
  (window as unknown as { go: unknown }).go = {
    main: {
      App: {
        ClipboardSetText: async () => true,
        ResolveOpenablePathForTab: async (tabID: string, path: string) => {
          calls.probe.push([tabID, path]);
          return probeReply(path);
        },
        OpenLocalPath: async (path: string) => { calls.open.push(path); },
        RevealPath: async (path: string) => { calls.reveal.push(path); },
      },
    },
  };

  const flushTimers = (): Promise<void> => new Promise((r) => setTimeout(r, 0));
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { TurnActions } = await import("../components/Message");
  const { MarkdownImageTabContext } = await import("../components/MarkdownImageContext");
  const rootEl = document.getElementById("root")!;

  const renderIn = async (node: React.ReactNode): Promise<{ host: HTMLDivElement; cleanup: () => Promise<void> }> => {
    const host = document.createElement("div");
    rootEl.appendChild(host);
    const root = createRoot(host);
    await act(async () => {
      root.render(React.createElement(LocaleProvider, null, node));
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

  const chipOf = (host: HTMLDivElement) => host.querySelector<HTMLElement>(".turn-actions__file-chip");
  const menuItemByLabel = (label: string) =>
    Array.from(document.body.querySelectorAll<HTMLButtonElement>("button[role=\"menuitem\"]"))
      .find((button) => button.textContent?.includes(label)) ?? null;

  const TAB = "tab-tl1421";

  // ① 探针通过 → 可点击态；左键 = OpenLocalPath（绝对路径，非原始相对路径）。
  {
    calls.probe.length = 0; calls.open.length = 0; calls.reveal.length = 0;
    probeReply = (path) => `C:\\ws\\${path}`;
    const { host, cleanup } = await renderIn(
      React.createElement(
        MarkdownImageTabContext.Provider,
        { value: TAB },
        React.createElement(TurnActions, { text: "答", turn: 1, tail: { files: ["src/x.ts"], doneAt: 1761360000000 } }),
      ),
    );
    const chip = chipOf(host);
    ok(chip !== null, "① chip 渲染");
    eq(calls.probe.length, 1, "① 探针恰好调用一次");
    eq(calls.probe[0]?.[0], TAB, "① 探针带 tabId（相对路径按会话项目根解析）");
    eq(calls.probe[0]?.[1], "src/x.ts", "① 探针带工具 args 原始相对路径");
    eq(chip?.className.includes("turn-actions__file-chip--openable"), true, "① 探针通过 → chip 可点击态类");
    eq(chip?.getAttribute("role"), "button", "① 可点击态 role=button（键盘可达）");
    eq(chip?.getAttribute("title"), "src/x.ts", "① 可点击态 title=全路径（1137 既有语义不变）");
    await act(async () => {
      chip!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(calls.open.length, 1, "① 点击触发默认应用打开");
    eq(calls.open[0], probeReply("src/x.ts"), "① OpenLocalPath 收到解析后绝对路径（非原始相对路径）");
    eq(calls.reveal.length, 0, "① 左键不触发 RevealPath");
    await cleanup();
  }
  // ② 探针空串 → 置灰 + title 说明；点击不发死调用。
  {
    calls.probe.length = 0; calls.open.length = 0; calls.reveal.length = 0;
    probeReply = () => "";
    const { host, cleanup } = await renderIn(
      React.createElement(
        MarkdownImageTabContext.Provider,
        { value: TAB },
        React.createElement(TurnActions, { text: "答", turn: 1, tail: { files: ["gone.txt"], doneAt: 1761360000000 } }),
      ),
    );
    const chip = chipOf(host);
    ok(chip !== null, "② 置灰 chip 仍渲染（不是消失）");
    eq(calls.probe.length, 1, "② 探针调用一次");
    eq(chip?.className.includes("turn-actions__file-chip--missing"), true, "② 探针空串 → chip 置灰类");
    eq(chip?.getAttribute("role"), null, "② 置灰态无 role=button（不可点）");
    ok(chip?.getAttribute("title")?.includes("不存在或已移动") === true, "② 置灰 title 带缺失说明");
    ok(chip?.getAttribute("title")?.includes("gone.txt") === true, "② 置灰 title 带全路径");
    await act(async () => {
      chip!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(calls.open.length, 0, "② 点击不发死调用（OpenLocalPath 零调用）");
    await cleanup();
  }
  // ③ 右键菜单 = 次级入口：两项；「在文件管理器中显示」→ RevealPath 绝对路径。
  {
    calls.probe.length = 0; calls.open.length = 0; calls.reveal.length = 0;
    probeReply = (path) => `C:\\ws\\${path}`;
    const { host, cleanup } = await renderIn(
      React.createElement(
        MarkdownImageTabContext.Provider,
        { value: TAB },
        React.createElement(TurnActions, { text: "答", turn: 1, tail: { files: ["src/x.ts"], doneAt: 1761360000000 } }),
      ),
    );
    const chip = chipOf(host)!;
    await act(async () => {
      chip.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 40 }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    const revealItem = menuItemByLabel("在文件管理器中显示");
    ok(revealItem !== null, "③ 右键菜单含「在文件管理器中显示」（RevealPath 文案与 1060 同源）");
    const openItem = menuItemByLabel("使用默认应用打开");
    ok(openItem !== null, "③ 右键菜单含「使用默认应用打开」（键盘可达的打开入口）");
    await act(async () => {
      revealItem!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(calls.reveal.length, 1, "③ 菜单项触发定位所在目录");
    eq(calls.reveal[0], probeReply("src/x.ts"), "③ RevealPath 收到解析后绝对路径");
    eq(menuItemByLabel("在文件管理器中显示"), null, "③ 选择后菜单关闭");
    // 菜单里的「使用默认应用打开」= 与左键同链路的显式入口。
    await act(async () => {
      chip.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 40 }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    const openAgain = menuItemByLabel("使用默认应用打开");
    ok(openAgain !== null, "③ 菜单可再次打开");
    await act(async () => {
      openAgain!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(calls.open.length, 1, "③ 菜单「使用默认应用打开」走 OpenLocalPath");
    eq(calls.open[0], probeReply("src/x.ts"), "③ 菜单打开同样收到绝对路径");
    await cleanup();
  }
  // ⑤ 键盘可达：Enter 触发默认应用打开（role=button 语义）。
  {
    calls.probe.length = 0; calls.open.length = 0; calls.reveal.length = 0;
    probeReply = (path) => `C:\\ws\\${path}`;
    const { host, cleanup } = await renderIn(
      React.createElement(
        MarkdownImageTabContext.Provider,
        { value: TAB },
        React.createElement(TurnActions, { text: "答", turn: 1, tail: { files: ["k.ts"], doneAt: 1761360000000 } }),
      ),
    );
    const chip = chipOf(host)!;
    await act(async () => {
      chip.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
      for (let i = 0; i < 5; i++) await flushTimers();
    });
    eq(calls.open.length, 1, "⑤ Enter 触发默认应用打开");
    eq(calls.open[0], probeReply("k.ts"), "⑤ 键盘打开同样走绝对路径");
    await cleanup();
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
