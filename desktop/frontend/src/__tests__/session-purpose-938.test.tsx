// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/session-purpose-938.test.tsx
//
// 任务 938 验收逐条钉：
//   ① hover 展示 purpose 全文——lib/sessionPurpose 缓存语义（未加载=undefined、
//     加载过空值=空串、写缓存通知订阅者）+ hover 卡模型带 purpose + ProjectTree
//     接线源码钉（mouseEnter 拉取 / pre-wrap 换行 CSS / title 拼接）；
//   ② 右键编辑弹窗打开/保存/取消三态——SessionPurposeEditor 挂载断言：
//     打开拉当前值、保存调 SetSessionPurpose、取消零写入、保存失败错误行
//     原样呈现且不调 onSaved；
//   ③ 保存后侧栏 purpose 数据源即时反映——onSaved 回调携带新值（宿主写缓存，
//     订阅者即时重渲染），setCachedSessionPurpose 后 cachedSessionPurpose 即新值；
//   ④ 三语键齐备；
//   ⑤ ProjectTree 右键菜单接线源码钉（编辑职责项 + 锚点 + 弹窗渲染）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { useState, useRef } from "react";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
// jsdom ships no matchMedia; AnchoredPopover consults prefers-reduced-motion.
(dom.window as unknown as { matchMedia: (query: string) => MediaQueryList }).matchMedia = (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
}) as MediaQueryList;
// jsdom lacks ResizeObserver; AnchoredPopover guards but never touches it here.
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
};
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { Element: unknown }).Element = dom.window.Element;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
(globalThis as unknown as { Event: unknown }).Event = dom.window.Event;
(globalThis as unknown as { KeyboardEvent: unknown }).KeyboardEvent = dom.window.KeyboardEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
function ok(value: boolean, label: string) {
  eq(Boolean(value), true, label);
}

const here = dirname(fileURLToPath(import.meta.url));
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { LocaleProvider } = await import("../lib/i18n");
const { SessionPurposeEditor } = await import("../components/SessionPurposeEditor");
const purposeLib = await import("../lib/sessionPurpose");

console.log("\nsession purpose (938) — lib cache semantics");

// ── ① 缓存语义：未加载=undefined（区别于加载过空值）；写缓存通知订阅者 ──
{
  eq(purposeLib.cachedSessionPurpose("/s/fresh"), undefined, "cache: an unseen path answers undefined (not empty)");
  eq(purposeLib.cachedSessionPurpose("  "), undefined, "cache: blank path answers undefined");

  let notified = 0;
  const off = purposeLib.subscribeSessionPurposes(() => { notified += 1; });
  purposeLib.setCachedSessionPurpose("/s/a", "  维护工具引擎  ");
  eq(purposeLib.cachedSessionPurpose("/s/a"), "维护工具引擎", "set: trims and stores the duty");
  eq(notified, 1, "set: notifies subscribers (hover refresh seam)");
  purposeLib.setCachedSessionPurpose("/s/a", "   ");
  eq(purposeLib.cachedSessionPurpose("/s/a"), undefined, "set: blank duty clears the entry");
  eq(notified, 2, "set: clearing also notifies");
  purposeLib.setCachedSessionPurpose("  ", "x");
  eq(notified, 2, "set: a blank path is a no-op (no phantom entries)");
  off();
  purposeLib.setCachedSessionPurpose("/s/b", "y");
  eq(notified, 2, "unsubscribe stops notifications");
}

// ── ②③ 弹窗三态（挂载断言：打开拉值 / 保存写链 / 取消零写 / 失败呈现） ──
console.log("\nsession purpose editor (938) — dialog states");

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];
let purposeOnDisk = "从磁盘拉到的职责";
let saveShouldFail = false;

const bindings = {
  async GetSessionPurpose(sessionPath: string) {
    calls.push({ name: "GetSessionPurpose", args: [sessionPath] });
    return purposeOnDisk;
  },
  async SetSessionPurpose(sessionPath: string, purpose: string) {
    calls.push({ name: "SetSessionPurpose", args: [sessionPath, purpose] });
    if (saveShouldFail) throw new Error("gateway closed");
  },
};

let latestSaved: string | null = null;
let closeCount = 0;

function Host({ initialPurpose }: { initialPurpose?: string }) {
  const [open, setOpen] = useState(true);
  const anchorRef = useRef<HTMLButtonElement>(null);
  return createElement(LocaleProvider, { locale: "en" },
    createElement("button", { id: "anchor", ref: anchorRef }, "row"),
    createElement(SessionPurposeEditor, {
      open,
      anchorRef,
      sessionPath: "/s/dialog",
      initialPurpose,
      onClose: () => { closeCount += 1; setOpen(false); },
      onSaved: (purpose: string) => { latestSaved = purpose; setOpen(false); },
      bindings,
    } as never),
  );
}

const container = dom.window.document.createElement("div");
dom.window.document.body.appendChild(container);
const root = createRoot(container);
const setValue = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;

// 打开态：initialPurpose 未注入 → 拉取一次并填入 textarea。
// 注意：AnchoredPopover portal 到 document.body，断言一律查 document。
await act(async () => { root.render(createElement(Host, { key: "open" })); });
{
  const textarea = dom.window.document.querySelector("textarea") as HTMLTextAreaElement | null;
  ok(textarea, "open: the dialog renders a textarea");
  eq(textarea?.value, "从磁盘拉到的职责", "open: an unknown initial value is fetched through GetSessionPurpose");
  eq(calls.filter((c) => c.name === "GetSessionPurpose").length, 1, "open: exactly one fetch");
  eq(calls.filter((c) => c.name === "GetSessionPurpose")[0]?.args[0], "/s/dialog", "open: fetch targets the targeted session path");
  eq(dom.window.document.querySelector('[data-anchored-popover="active"]') !== null, true, "open: the dialog portals through AnchoredPopover (529 anti-clip)");
}

// 保存态：改文本 → 保存 → SetSessionPurpose 落 (path, draft)，onSaved 携新值。
await act(async () => {
  const textarea = dom.window.document.querySelector("textarea") as HTMLTextAreaElement;
  setValue.call(textarea, "新的职责");
  textarea.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
});
await act(async () => {
  const save = Array.from(dom.window.document.querySelectorAll("button")).find((b) => b.textContent?.includes("Save"));
  ok(Boolean(save), "save: the dialog exposes a save button");
  save?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
});
{
  const saveCall = calls.find((c) => c.name === "SetSessionPurpose");
  ok(Boolean(saveCall), "save: saving calls SetSessionPurpose");
  eq(saveCall?.args[0], "/s/dialog", "save: targets the targeted session path");
  eq(saveCall?.args[1], "新的职责", "save: carries the edited draft");
  eq(latestSaved, "新的职责", "save: onSaved delivers the trimmed duty (host writes the hover cache from it)");
}

// 保存失败态：错误行原样呈现，不调 onSaved，弹窗不关。
latestSaved = null;
saveShouldFail = true;
await act(async () => { root.render(createElement(Host, { key: "fail", initialPurpose: "预填职责" })); });
{
  eq((dom.window.document.querySelector("textarea") as HTMLTextAreaElement | null)?.value, "预填职责", "reopen: a cached initial value renders without a fetch");
  eq(calls.filter((c) => c.name === "GetSessionPurpose").length, 1, "reopen: a known initial value skips the fetch");
}
await act(async () => {
  const save = Array.from(dom.window.document.querySelectorAll("button")).find((b) => b.textContent?.includes("Save"));
  save?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
});
{
  const alert = dom.window.document.querySelector('[role="alert"]');
  ok(Boolean(alert), "save failure: an alert row surfaces the error (587: never silent)");
  ok((alert?.textContent ?? "").includes("gateway closed"), "save failure: the raw error text is shown");
  eq(latestSaved, null, "save failure: onSaved never fires");
  eq(dom.window.document.querySelector('[data-anchored-popover="active"]') !== null, true, "save failure: the dialog stays open for a retry");
}
saveShouldFail = false;

// 取消态：点取消 → onClose，零写入。
{
  const before = calls.filter((c) => c.name === "SetSessionPurpose").length;
  const cancel = Array.from(dom.window.document.querySelectorAll("button")).find((b) => b.textContent?.includes("Cancel"));
  ok(Boolean(cancel), "cancel: the dialog exposes a cancel button");
  await act(async () => { cancel?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
  eq(calls.filter((c) => c.name === "SetSessionPurpose").length, before, "cancel: zero writes");
  eq(closeCount >= 1, true, "cancel: onClose fires");
}

await act(async () => { root.unmount(); });

// ── ① hover 接线源码钉 + CSS 换行不裁剪 ──
console.log("\nsession purpose hover wiring (938)");
{
  const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
  ok(treeSource.includes("loadSessionPurpose(node.sessionPath)"), "hover: row mouseEnter fetches the duty (cache makes repeats free)");
  ok(treeSource.includes("titleWithPurpose"), "hover: non-classic rows fold the duty into the native tooltip");
  ok(treeSource.includes("project-tree__hover-card-purpose"), "hover: classic hover card renders a dedicated duty line");
  ok(treeSource.includes("subscribeSessionPurposes"), "hover: a cache subscription re-renders rows when a late fetch lands");
  ok(/key: "editPurpose"/.test(treeSource), "menu: the topic context menu carries the edit-duty item");
  ok(treeSource.includes("setPurposeEditorSessionPath(sessionPath)"), "menu: the item opens the editor anchored on the right-clicked row");
  ok(treeSource.includes("purposeEditorAnchorRef.current = event.currentTarget"), "menu: the right-clicked row element becomes the popover anchor");
  ok(treeSource.includes("setCachedSessionPurpose(purposeEditorSessionPath, purpose)"), "save: onSaved writes the hover cache (immediate sidebar reflection)");

  const css = readFileSync(resolve(here, "../styles.css"), "utf8");
  const purposeLine = css.slice(css.indexOf(".project-tree__hover-card-purpose"));
  ok(purposeLine.includes("white-space: pre-wrap"), "css: the hover-card duty line keeps line breaks");
  ok(purposeLine.includes("overflow-wrap: anywhere"), "css: the hover-card duty line breaks long words (no clipping)");
}

// ── ④ 三语键齐备 ──
console.log("\nsession purpose locales (938)");
for (const locale of ["zh", "zh-TW", "en"] as const) {
  const source = readFileSync(resolve(here, `../locales/${locale}.ts`), "utf8");
  for (const key of ["projectTree.editPurpose", "projectTree.purposeHint", "projectTree.purposePlaceholder", "projectTree.purposeSaveFailed"]) {
    ok(source.includes(`"${key}"`), `${locale}: ${key} exists`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
