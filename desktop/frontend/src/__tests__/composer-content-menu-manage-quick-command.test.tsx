// Run: npx --no-install tsx src/__tests__/composer-content-menu-manage-quick-command.test.tsx
//
// 任务 721 验收：输入框「添加内容 → 快捷指令」列表底部「添加快捷指令」入口
// 点击后呼出共享的「管理快捷指令」面板（QuickCommandsManagerDialog，与设置
// 入口同一实现）——656 的 picker 内嵌小表单方案已回退移除——
//  1) 入口固定在列表底部（最后一条 menuitem），无 onManageQuickCommands 通道
//     时不渲染；
//  2) 点击回调 onManageQuickCommands 恰好一次，且 picker 收起（回到一级内容
//     菜单），任何内嵌表单（标题/正文/保存/取消）都不再出现；
//  3) 搜索无匹配时入口仍在（正是「还没存、想加一条」的场景）；
//  4) 选择片段的插入通道不受影响。

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { ComposerContentMenuActions } from "../components/ComposerContentMenuActions";
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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// The zh dictionary loads via a dynamic import inside LocaleProvider's effect,
// so the first paint lands a few macro-tasks after render. Pump act() until the
// predicate holds instead of guessing a fixed delay.
async function settle(predicate: () => boolean, label: string): Promise<void> {
  for (let i = 0; i < 200 && !predicate(); i += 1) {
    await act(async () => { await flushTimers(); });
  }
  if (!predicate()) throw new Error(`settle timeout: ${label}`);
}

// React controlled inputs ignore direct value writes; follow the repo's typing
// helper (capabilities-test-helpers): prototype setter + reset the value
// tracker to the previous value, then dispatch input/change.
function typeInto(node: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const win = node.ownerDocument.defaultView;
  const previous = node.value;
  const proto = node.tagName === "TEXTAREA"
    ? (win?.HTMLTextAreaElement ?? HTMLTextAreaElement).prototype
    : (win?.HTMLInputElement ?? HTMLInputElement).prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
  if (!setter) throw new Error("missing value setter");
  setter.call(node, value);
  (node as HTMLInputElement & { _valueTracker?: { setValue: (next: string) => void } })._valueTracker?.setValue(previous);
  const eventCtor = win?.Event ?? Event;
  node.dispatchEvent(new eventCtor("input", { bubbles: true }));
  node.dispatchEvent(new eventCtor("change", { bubbles: true }));
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
// Pin the zh dictionary so labels are assertable (keys are compile-checked
// across zh/en/zh-TW by tsc).
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
Object.defineProperty(dom.window.navigator, "languages", { configurable: true, value: ["zh-CN"] });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLInputElement = dom.window.HTMLInputElement;
globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
// React DOM's legacy input-event polyfill probes attachEvent/detachEvent on the
// active element; without the stubs every controlled-input keystroke throws
// (same harness trick as composer-content-menu-running.test.tsx).
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
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

// react-dom must be evaluated AFTER the DOM globals above are in place —
// imported earlier, its event system binds to the pre-JSDOM globals and
// controlled-input change events never fire (same pattern as
// blank-project-dialog.test.tsx).
const { createRoot } = await import("react-dom/client");

const snippets = [
  { title: "片段A", text: "片段A正文" },
  { title: "pre-release 检查", text: "准备转线：1.写…" },
];

type Props = Parameters<typeof ComposerContentMenuActions>[0];

function renderActions(root: ReturnType<typeof createRoot>, overrides: Partial<Props>) {
  const base: Props = {
    attachmentInputEnabled: true,
    textPresent: false,
    onChooseAttachment: () => {},
    onInsertTrigger: () => {},
    quickCommands: snippets,
  };
  return act(async () => {
    root.render(
      <LocaleProvider>
        <ComposerContentMenuActions {...base} {...overrides} />
      </LocaleProvider>,
    );
    await flushTimers();
  });
}

async function main() {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  console.log("\n任务 721 picker「添加快捷指令」入口呼出管理面板（内嵌表单已回退）");

  const menuItems = () => [...document.querySelectorAll<HTMLButtonElement>("[role=\"menuitem\"]")];
  const itemWithTitle = (label: string) => menuItems().find((node) => (node.textContent ?? "").includes(label));
  const draftFormGone = () =>
    !document.querySelector(".composer-content-menu__draft") &&
    ![...document.querySelectorAll("input.mem-input, textarea.mem-input")].some((node) =>
      (node as HTMLElement).closest(".composer-content-menu__draft") !== null);

  {
    const picked: string[] = [];
    let managed = 0;
    await renderActions(root, {
      onChooseQuickCommand: (text) => picked.push(text),
      onManageQuickCommands: () => { managed += 1; },
    });
    await settle(() => (document.getElementById("root")?.textContent ?? "").includes("快捷指令"), "content menu rendered");

    // 1) 一级菜单 → 快捷指令二级页。
    const pickerEntry = itemWithTitle("快捷指令");
    ok(Boolean(pickerEntry), "content menu lists the quick-commands picker entry");
    await act(async () => { pickerEntry!.click(); await flushTimers(); });
    ok(Boolean(document.querySelector(".composer-content-menu__search")), "picker page with search is open");

    // 2) 列表底部入口：最后一条 menuitem 即「添加快捷指令」。
    const addEntry = itemWithTitle("添加快捷指令");
    ok(Boolean(addEntry), "add entry renders at the picker bottom");
    eq(menuItems()[menuItems().length - 1]?.textContent, "添加快捷指令", "add entry is the LAST menuitem in the list");
    ok((menuItems()[menuItems().length - 1]?.classList.contains("composer-content-menu__add")) === true,
      "add entry carries the composer-content-menu__add class");

    // 3) 点击呼出管理面板通道：回调一次、picker 收起、内嵌表单不存在。
    await act(async () => { addEntry!.click(); await flushTimers(); });
    eq(managed, 1, "click fires onManageQuickCommands exactly once");
    eq(picked.length, 0, "opening the manager does NOT trigger the insert channel");
    ok(!document.querySelector(".composer-content-menu__search"), "picker closes after handing over to the manager");
    ok((document.getElementById("root")?.textContent ?? "").includes("添加内容"),
      "popover is back on the top-level content menu");
    ok(draftFormGone(), "no inline create form remains anywhere (656 form removed)");

    // 4) 选择片段的插入通道不受影响。
    await act(async () => { itemWithTitle("快捷指令")!.click(); await flushTimers(); });
    const snippet = itemWithTitle("片段A");
    ok(Boolean(snippet), "snippet list still reachable");
    await act(async () => { snippet!.click(); await flushTimers(); });
    eq(picked.length, 1, "choosing a snippet fires the insert channel once");
    eq(picked[0], "片段A正文", "insert channel receives the snippet text");
    eq(managed, 1, "choosing a snippet does not open the manager");

    // 5) 搜索无匹配时入口仍在，呼出行为一致。
    await renderActions(root, {
      onChooseQuickCommand: (text) => picked.push(text),
      onManageQuickCommands: () => { managed += 1; },
    });
    await settle(() => Boolean(itemWithTitle("快捷指令")), "content menu re-rendered");
    await act(async () => { itemWithTitle("快捷指令")!.click(); await flushTimers(); });
    const search = document.querySelector<HTMLInputElement>(".composer-content-menu__search");
    await act(async () => { typeInto(search!, "zzz-无匹配"); await flushTimers(); });
    const addAfterSearch = itemWithTitle("添加快捷指令");
    ok(Boolean(addAfterSearch), "add entry survives a no-match search");
    await act(async () => { addAfterSearch!.click(); await flushTimers(); });
    eq(managed, 2, "manager channel fires from the no-match state too");
    ok(draftFormGone(), "no inline form from the no-match state either");

    await act(async () => {
      root.unmount();
      await flushTimers();
    });
    dom.window.close();
  }

  // 6) 无 onManageQuickCommands 通道（上层未接线）时入口不渲染，列表行为不变。
  {
    const dom2 = new JSDOM("<!doctype html><html><body><div id=\"root2\"></div></body></html>", {
      pretendToBeVisual: true,
      url: "http://localhost/",
    });
    globalThis.window = dom2.window as unknown as Window & typeof globalThis;
    globalThis.document = dom2.window.document;
    globalThis.localStorage = dom2.window.localStorage;
    const root2 = createRoot(document.getElementById("root2")!);
    await act(async () => {
      root2.render(
        <LocaleProvider>
          <ComposerContentMenuActions
            attachmentInputEnabled={true}
            textPresent={false}
            onChooseAttachment={() => {}}
            onInsertTrigger={() => {}}
            quickCommands={snippets}
          />
        </LocaleProvider>,
      );
      await flushTimers();
    });
    await settle(() => (document.getElementById("root2")?.textContent ?? "").includes("快捷指令"), "content menu (no-channel) rendered");
    const pickerEntry = [...document.querySelectorAll<HTMLButtonElement>("[role=\"menuitem\"]")]
      .find((node) => (node.textContent ?? "").includes("快捷指令"));
    await act(async () => { pickerEntry!.click(); await flushTimers(); });
    eq(itemWithTitle("添加快捷指令"), undefined, "no add entry without the onManageQuickCommands channel");
    ok(itemWithTitle("片段A") !== undefined, "picker list unaffected without the channel");
    await act(async () => {
      root2.unmount();
      await flushTimers();
    });
    dom2.window.close();
  }

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
