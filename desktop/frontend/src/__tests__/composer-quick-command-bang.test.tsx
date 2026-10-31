// Run: tsx src/__tests__/composer-quick-command-bang.test.tsx
//
// Task 593: "!!" at the head of a line in the composer input opens the
// quick-command picker (same dropdown interaction as "/"), picking a snippet
// consumes the "!!" token and inserts the snippet text. Line-head only — any
// "!!" buried mid-line never triggers — and the "/" / "@" / "#" triggers stay
// untouched.
//
// tl 1273 (A): the full-width "！！" triggers the same way, so Chinese-IME
// users can summon the list without switching layouts.
//
// tl 1273 (B): the trigger extends from "the very head of the input" to the
// head of ANY line, with explicit disambiguation — the popup opens with NO
// item highlighted; Esc or blur dismisses it; only an explicit selection
// consumes the token (Enter with nothing highlighted falls through to the
// normal send path, so an incidental "!!" can never block sending).
//
// jsdom note: these suites install the DOM after react-dom has loaded, so
// React boots with canUseDOM=false and falls back to its keyup-driven
// change-detection polyfill (same contract as composer-ime.test.tsx). The
// polyfill only delivers reliably for the first mount of a document, so the
// scenarios below share one composer instance and run sequentially — each
// scenario restarts from a known text before asserting.

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

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const menuRect = (height: number) => ({
  top: 0, left: 0, bottom: height, right: 360, width: 360, height, x: 0, y: 0, toJSON: () => {},
});

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
globalThis.FocusEvent = dom.window.FocusEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.MutationObserver = dom.window.MutationObserver;
globalThis.File = dom.window.File;
globalThis.FileReader = dom.window.FileReader;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.ResizeObserver = TestResizeObserver;
Object.defineProperty(dom.window.HTMLElement.prototype, "getBoundingClientRect", {
  configurable: true,
  value(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return menuRect(360);
    if (this.classList.contains("slashmenu__row")) return menuRect(34);
    return menuRect(0);
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", {
  configurable: true,
  get: () => 600,
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 360;
    if (this.classList.contains("slashmenu__row")) return 34;
    return 0;
  },
});
Object.defineProperty(window, "matchMedia", {
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

// React's value tracker only reports a change (and fires onChange) when the
// DOM value moved outside its patched setter — same helper as the IME suite.
function setNativeValue(ta: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
  if (!setter) throw new Error("native textarea value setter unavailable");
  setter.call(ta, value);
}

async function typeText(ta: HTMLTextAreaElement, value: string) {
  await act(async () => {
    ta.focus();
    setNativeValue(ta, value);
    ta.setSelectionRange(value.length, value.length);
    ta.dispatchEvent(new window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    ta.dispatchEvent(new window.KeyboardEvent("keyup", { key: "!", bubbles: true }));
    await flushTimers();
  });
}

async function pressKey(ta: HTMLTextAreaElement, key: string) {
  await act(async () => {
    ta.dispatchEvent(new window.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
    await flushTimers();
  });
}

async function blurTextarea(ta: HTMLTextAreaElement) {
  await act(async () => {
    // jsdom's blur() fires the real focusout (bubbling) + blur pair, which is
    // what React's root-delegated onBlur listens for — same pattern as
    // composer-focus-heal.test.tsx. A manually dispatched focusout does not
    // reach React's delegation here.
    ta.blur();
    await flushTimers();
  });
}

function composerTextarea(): HTMLTextAreaElement {
  const ta = document.querySelector<HTMLTextAreaElement>("#composer-input");
  if (!ta) throw new Error("plain composer textarea did not render");
  return ta;
}

function quickCommandRows(): string[] {
  return [...document.querySelectorAll<HTMLElement>(".slashmenu [role=\"option\"]")]
    .map((node) => node.textContent ?? "");
}

function pickerCount(): number {
  return document.querySelectorAll(".slashmenu").length;
}

const snippets = [
  { title: "pre-release关键动作", text: "跑发布检查单" },
  { title: "转线", text: "切换到另一条线" },
  { title: "停用片段", text: "禁用的片段不应出现", enabled: false },
];

async function main() {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const sentTexts: string[] = [];
  const props: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "reasonix",
    onSend: (text: string) => {
      sentTexts.push(text);
    },
    onCancel: () => undefined,
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    quickCommands: snippets,
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const ta = composerTextarea();

  console.log("\n任务 1273 任意行行首 !!/！！ 唤起快捷指令选择列表");

  // 1) 行首 !! 打开列表：禁用片段不出现，hint 在列；默认无高亮（消歧要求一）。
  await typeText(ta, "!");
  eq(pickerCount(), 0, "single ! does not open the picker");
  await typeText(ta, "!!");
  const rows1 = quickCommandRows();
  eq(pickerCount(), 1, "line-head !! opens the picker");
  ok(rows1.some((row) => row.includes("pre-release关键动作")), "enabled snippet 1 listed");
  ok(rows1.some((row) => row.includes("转线")), "enabled snippet 2 listed");
  ok(!rows1.some((row) => row.includes("停用片段")), "disabled snippet never listed");
  eq(rows1.length, 2, "only enabled snippets listed");
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]") ?? null,
    null, "no snippet is highlighted by default");

  // 2) 消歧要求三：选中才消费 token——无高亮时 Enter 不消费，直落发送原文。
  await pressKey(ta, "Enter");
  ok(sentTexts.includes("!!"), "Enter with no selection sends the literal text (!! NOT consumed)");
  await act(async () => {
    await flushTimers();
  });
  eq(pickerCount(), 0, "picker closes after the literal send clears the draft");

  // 3) 键盘选择：ArrowDown 从无高亮到第一项、再到第二项，回车选中替换 token。
  await typeText(ta, "!!");
  eq(pickerCount(), 1, "picker reopens for keyboard selection");
  await pressKey(ta, "ArrowDown");
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]")?.textContent,
    "pre-release关键动作跑发布检查单", "ArrowDown highlights the first snippet");
  await pressKey(ta, "ArrowDown");
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]")?.textContent,
    "转线切换到另一条线", "ArrowDown again moves the highlight to the second snippet");
  await pressKey(ta, "Enter");
  eq(ta.value, "切换到另一条线", "picking consumes the !! token and inserts the snippet text");
  eq(pickerCount(), 0, "picker closes after the pick");

  // 4) ArrowUp 从无高亮直达最后一项，回车同样消费。
  await typeText(ta, "!!");
  await pressKey(ta, "ArrowUp");
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]")?.textContent,
    "转线切换到另一条线", "ArrowUp from no selection highlights the last snippet");
  await pressKey(ta, "Enter");
  eq(ta.value, "切换到另一条线", "picking the ArrowUp highlight consumes the token");

  // 5) 全角！！同样触发（中文输入法免切输入法）：过滤与消费同口径。
  await typeText(ta, "！！");
  eq(pickerCount(), 1, "full-width ！！ at line head opens the picker");
  await typeText(ta, "！！转");
  const rows5 = quickCommandRows();
  ok(rows5.length === 1 && rows5[0].includes("转线"), "typing after full-width ！！ filters the list");
  await pressKey(ta, "ArrowDown");
  await pressKey(ta, "Enter");
  eq(ta.value, "切换到另一条线", "picking consumes the full-width ！！ token");

  // 6) 过滤 + Esc 关闭（消歧要求二的一半）。
  await typeText(ta, "!!转");
  const rows6 = quickCommandRows();
  ok(rows6.length === 1 && rows6[0].includes("转线"), "typing after !! filters the list");
  await pressKey(ta, "Escape");
  eq(pickerCount(), 0, "Escape dismisses the picker");

  // 7) 任意行行首触发：非首行的行首 !! 同样打开；选中只替换该行 token，其余行原样。
  await typeText(ta, "第一行\n!!");
  eq(pickerCount(), 1, "!! at the head of a later line opens the picker");
  await pressKey(ta, "ArrowDown");
  await pressKey(ta, "Enter");
  eq(ta.value, "第一行\n跑发布检查单", "picking replaces only that line's token, other lines intact");

  // 8) 光标在后续行且该行无行首 token：面板不开。
  await typeText(ta, "!!\nsecond line");
  eq(pickerCount(), 0, "caret on a following line without a line-head token keeps the picker closed");

  // 9) 行头硬约束：任何行中位置的 !!/！！ 不触发。
  await typeText(ta, "前面有正文!!");
  eq(pickerCount(), 0, "!! mid-text never triggers");
  await typeText(ta, "前面有正文！！");
  eq(pickerCount(), 0, "full-width ！！ mid-text never triggers");
  await typeText(ta, "ok\nplain!!nothead");
  eq(pickerCount(), 0, "!! mid-line on a later line never triggers");

  // 10) 行首触发恢复：正文之后回到行首 !! 依然打开。
  await typeText(ta, "前面有正文!!");
  eq(pickerCount(), 0, "mid-text !! stays closed");
  await typeText(ta, "!!");
  eq(pickerCount(), 1, "line-head !! triggers again after plain text");

  // 11) 消歧要求二的另一半：失焦即关；继续输入（query 变化）重开。
  await blurTextarea(ta);
  eq(pickerCount(), 0, "blur dismisses the picker");
  await typeText(ta, "!!转");
  eq(pickerCount(), 1, "typing after blur reopens the picker");

  // 12) 既有触发符互斥面：@ 文本接管面板（@ 流自己的文件列表），片段行消失。
  await typeText(ta, "!!");
  eq(pickerCount(), 1, "sanity: !! picker open");
  await typeText(ta, "@");
  ok(!quickCommandRows().some((row) => row.includes("pre-release关键动作")),
    "replacing text with @ hands the panel to the @ flow (no snippet rows)");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
  dom.window.close();

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
