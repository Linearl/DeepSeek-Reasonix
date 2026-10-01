// Run: npx --no-install tsx src/__tests__/quick-commands-dialog-width.test.tsx
//
// 任务 278 验收：「管理快捷指令」弹窗宽度链——
//  1) 管理弹窗走 ProviderDialog 的 wide 变体（900px shell，调用点传 wide）；
//  2) wide shell 宽度 = min(900px, 100vw - 32px)：常见条目（开关钮+标题输入+
//     内容列）一行放下，窄屏（<1280px）被视口宽兜住不溢出；
//  3) 行内 flex 子项可收缩（textarea min-width:0），超长无空格内容不会把行
//     撑出横向滚动条（防反弹：3ccd3d2b9 之后唯一残余溢出路径）；
//  4) 新增/编辑表单与管理列表同弹窗（同一 wide shell 覆盖）。
// 注：核心加宽修复为 3ccd3d2b9（2026-09-24 已在树），本测试钉住宽度链防
// 上游 merge 顶掉。

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { ProviderDialog } from "../components/ProviderDialog";
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
  }
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "",
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

async function renderDialog(wide: boolean) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ProviderDialog title="管理快捷指令" onClose={() => {}} wide={wide}>
          <div className="settings-quick-commands settings-quick-commands--panel settings-quick-commands--wide" />
        </ProviderDialog>
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return root;
}

console.log("\n任务 278 快捷指令弹窗宽度链");

// 1) wide 变体类名真实渲染（默认调用方不受影响）。
{
  const dom = installDom();
  const root = await renderDialog(true);
  const wideDialog = document.querySelector(".modal.provider-dialog.provider-dialog--wide");
  ok(Boolean(wideDialog), "wide dialog renders .provider-dialog--wide");
  ok(Boolean(document.querySelector(".provider-dialog--wide header h3")), "wide dialog renders its title");
  await act(async () => {
    root.unmount();
    await flushTimers();
  });
  const plainRoot = await renderDialog(false);
  ok(!document.querySelector(".provider-dialog--wide") && Boolean(document.querySelector(".provider-dialog")),
    "default callers stay on the 620px shell (no --wide)");
  await act(async () => {
    plainRoot.unmount();
    await flushTimers();
  });
  dom.window.close();
}

// 2/3) 宽度链与防反弹的源码锚点（上游 merge 顶掉即红）。
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const read = (p: string) => readFileSync(here + "../" + p, "utf8").replace(/\r\n/g, "\n");
  const shellCss = read("components/ProviderAccessSettings.css");
  const stylesCss = read("styles.css");
  const panelSrc = read("components/SettingsPanel.tsx");
  const dialogSrc = read("components/ProviderDialog.tsx");

  ok(/\{[^{}]*width:\s*min\(900px,\s*calc\(100vw - 32px\)\)/.test(shellCss.split(".provider-dialog--wide")[1] ?? ""),
    "wide shell = min(900px, 100vw-32px): rows fit on one line, narrow viewports capped");
  ok(/settings-quick-commands--wide\s*\{[^{}]*width:\s*100%/.test(stylesCss),
    "quick-commands content fills the wide shell (no inner width floor to fight it)");
  ok(/settings-quick-commands__row > textarea\.mem-input\s*\{[^{}]*min-width:\s*0/.test(stylesCss),
    "row content column is shrinkable (min-width:0) so long tokens cannot reintroduce h-scroll");
  ok(/ProviderDialog title=\{t\("settings\.quickCommandsManage"\)\}[\s\S]{0,80}?wide>/.test(panelSrc.replace(/\n\s*/g, " ")),
    "manage dialog passes wide (add/edit form lives in the same dialog)");
  ok(dialogSrc.includes("wide?: boolean"),
    "ProviderDialog keeps the opt-in wide prop (default callers untouched)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
