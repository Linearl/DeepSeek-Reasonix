// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-run-strip-oneline.test.tsx
//
// tl1508 修②：运行状态区一行化——截断版状态行 + 「⋯ 工具」折叠菜单。
//   AC1 主行恒一行：时长/tokens/速率（runMetrics）不再内联在状态词后，收进
//       detail 层（hidden 态在场，展开层可达全部信息）；
//   AC2 「⋯ N」开合：aria-expanded 翻转、detail 显隐随之、resized 定高卡的
//       --composer-run-strip-reserved 30→56px 同步抬升（展开不挤压输入区）；
//   AC3 「⋯ 工具」折叠入口在场（窄态显隐由 CSS 容器断点管，DOM 恒在），
//       菜单开合可用，菜单项与平铺钮同一事件链（未启用的能力不进菜单）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function flushRaf(): Promise<void> {
  return new Promise((resolve) => {
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => resolve()));
  });
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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

console.log("\ncomposer run strip one-line (tl1508 修②)");

{
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer
            running
            collaborationMode={"normal" as CollaborationMode}
            toolApprovalMode={"ask" as ToolApprovalMode}
            goal=""
            cwd="/repo"
            modelLabel="DeepSeek-R1"
            onSend={() => {}}
            onCancel={() => undefined}
            onCycleMode={() => {}}
            onSetMode={() => {}}
            onSetCollaborationMode={() => {}}
            onSetToolApprovalMode={() => {}}
            onToggleYoloApprovalMode={() => {}}
            onClearGoal={() => {}}
            onSwitchModel={() => {}}
            onSetEffort={() => {}}
            currentToolLabel="write_file"
            turnStartAt={Date.now()}
            turnTokens={150}
            ready
          />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const card = document.querySelector(".composer-card") as HTMLElement;
  const strip = document.querySelector(".composer-run-strip") as HTMLElement;
  const text = strip?.querySelector(".composer-run-strip__text") as HTMLElement;
  const more = document.querySelector(".composer-run-strip__more") as HTMLButtonElement;
  const detail = document.querySelector(".composer-run-strip__detail") as HTMLElement;
  ok(strip !== null && text !== null, "running strip renders");
  ok(!text.textContent?.includes("tokens"), "AC1: state word line no longer inlines the readings (constant one line)");
  ok(more !== null, "AC1: detail toggle renders while readings exist");
  eq(more.getAttribute("aria-expanded"), "false", "AC2: detail starts collapsed");
  const metrics = detail.querySelector(".composer-run-strip__metrics");
  ok(metrics !== null, "AC1: readings live in the expandable detail layer (all info reachable)");
  eq(detail.hidden, true, "AC1: detail layer is collapsed in DOM (hidden attribute)");
  eq(card.style.getPropertyValue("--composer-run-strip-reserved"), "30px", "AC2: collapsed strip reserves 30px");

  await act(async () => {
    more.click();
    await flushRaf();
    await flushTimers();
  });
  eq(more.getAttribute("aria-expanded"), "true", "AC2: toggle expands the detail layer");
  eq(detail.hidden, false, "AC2: detail layer becomes visible");
  eq(card.style.getPropertyValue("--composer-run-strip-reserved"), "56px", "AC2: expanded strip reserves 56px (no input squeeze)");

  await act(async () => {
    more.click();
    await flushRaf();
    await flushTimers();
  });
  eq(detail.hidden, true, "AC2: second click collapses again");

  // AC3：工具折叠入口 + 菜单（默认实验态下表情/截图两项在场）。
  const toolsFold = document.querySelector(".composer-tools-fold") as HTMLElement;
  const toolsTrigger = document.querySelector(".composer-tools-fold .composer__btn") as HTMLButtonElement;
  ok(toolsFold !== null && toolsTrigger !== null, "AC3: tools fold trigger is present (visibility is the CSS breakpoint's call)");
  eq(toolsTrigger.getAttribute("aria-expanded"), "false", "AC3: tools menu starts closed");
  await act(async () => {
    toolsTrigger.click();
    await flushRaf();
    await flushTimers();
  });
  eq(toolsTrigger.getAttribute("aria-expanded"), "true", "AC3: tools menu opens");
  const toolsMenu = document.querySelector(".composer-tools-menu__items") as HTMLElement;
  ok(toolsMenu !== null, "AC3: tools menu portal renders");
  eq(toolsMenu.querySelectorAll("[role=\"menuitem\"]").length, 2, "AC3: menu carries exactly the enabled tools (emoji + screenshot)");
  const emojiItem = toolsMenu.querySelectorAll("[role=\"menuitem\"]")[0] as HTMLButtonElement;
  await act(async () => {
    emojiItem.click();
    await flushTimers();
  });
  eq(toolsTrigger.getAttribute("aria-expanded"), "false", "AC3: picking a tool closes the menu (same event chain as the flat buttons)");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
