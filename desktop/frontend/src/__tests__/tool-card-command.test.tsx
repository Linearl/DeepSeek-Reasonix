import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act, createElement, useLayoutEffect } from "react";
import { createRoot } from "react-dom/client";
import { ToolCard } from "../components/ToolCard";
import { LocaleProvider } from "../lib/i18n";
import type { Item } from "../lib/useController";
import type { AppBindings } from "../lib/bridge";
import "../components/editors/HljsCode";

type Tool = Extract<Item, { kind: "tool" }>;
type Result = Awaited<ReturnType<AppBindings["ToolResultForTab"]>>;
const command = "printf '%s\\n' \"C:\\中文\\file.txt\"\r\n  echo " + "long-command-".repeat(70) + "END_OF_COMMAND";
const card = (patch: Partial<Tool> = {}): Tool => ({
  kind: "tool", id: "command", name: "bash", readOnly: false, status: "done",
  args: JSON.stringify({ command }), output: "OUTPUT_STAYS_VISIBLE", ...patch,
});
function deferred() {
  let resolve!: (value: Result) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<Result>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const settle = () => new Promise<void>(resolve => setImmediate(resolve));

async function mount(item: Tool, load: AppBindings["ToolResultForTab"] = async () => null) {
  const dom = new JSDOM("<!doctype html><html><body><div id='root'></div></body></html>", {
    url: "http://localhost/", pretendToBeVisual: true,
  });
  Object.assign(globalThis, {
    window: dom.window, document: dom.window.document, Node: dom.window.Node,
    Element: dom.window.Element, HTMLElement: dom.window.HTMLElement, Event: dom.window.Event,
    MouseEvent: dom.window.MouseEvent, IS_REACT_ACT_ENVIRONMENT: true,
    requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
    cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  const copied: string[] = [];
  Object.defineProperty(dom.window.navigator, "clipboard", { configurable: true, value: {
    writeText: async (text: string) => { copied.push(text); },
  } });
  // Fork still runs on the Wails seam (upstream retired window.go in favour of
  // window.reasonixDesktop), so stub that shape - bridge.realApp() reads it.
  const goSeam = window as unknown as { go?: unknown };
  const previousGo = goSeam.go;
  goSeam.go = { main: { App: { ToolResultForTab: load } } };
  const root = createRoot(document.getElementById("root")!);
  const paints: string[] = [];
  function PaintProbe({ current, tab }: { current: Tool; tab: string }) {
    useLayoutEffect(() => { paints.push(document.querySelector(".tool__command")?.textContent ?? ""); });
    return createElement(ToolCard, { item: current, tabId: tab });
  }
  async function update(current: Tool, tab = "tab-a") {
    await act(async () => {
      root.render(createElement(LocaleProvider, null, createElement(PaintProbe, { current, tab })));
      await settle();
    });
  }
  await update(item);
  async function click(selector: string) {
    const button = document.querySelector<HTMLButtonElement>(selector);
    assert.ok(button, "missing button " + selector);
    await act(async () => { button.click(); await settle(); });
  }
  return {
    copied, paints, update, click,
    expand: () => click(".tool__head"),
    async cleanup() {
      await act(async () => root.unmount());
      goSeam.go = previousGo;
      dom.window.close();
    },
  };
}

// Full command is a body surface, not the ellipsized summary, and remains
// available independently of whether the process returned any stdout.
for (const output of ["OUTPUT_STAYS_VISIBLE", ""]) {
  const ui = await mount(card({ output }));
  try {
    assert.equal(document.querySelector(".tool__command"), null, "collapsed command is not mounted");
    await ui.expand();
    assert.ok(document.querySelector(".tool__command code")?.textContent?.includes("END_OF_COMMAND"), "expanded command is complete");
    assert.equal(document.querySelectorAll(".tool__command").length, 1);
    if (output) assert.ok(document.querySelector(".tool__body")?.textContent?.includes(output));
    assert.ok(!document.querySelector(".tool__body")?.textContent?.includes('"command":'), "no duplicate args JSON");
    await ui.click(".tool__command .copybtn");
    assert.deepEqual(ui.copied, [command], "copy preserves CRLF, quoting and backslashes");
    await ui.expand();
    assert.equal(document.querySelector(".tool__command"), null);
  } finally { await ui.cleanup(); }
}

for (const [shell, language] of [["bash", "bash"], ["zsh", "bash"], ["powershell", "powershell"], ["pwsh", "powershell"], ["cmd", undefined], ["unknown-shell", undefined]] as const) {
  const ui = await mount(card({ name: "exec_command", execution: { shell }, status: "error", error: "failed" }));
  try {
    await ui.expand();
    assert.ok(document.querySelector(".tool__command code")?.textContent?.includes("END_OF_COMMAND"));
    assert.equal(document.querySelector(".tool__command pre")?.getAttribute("data-lang") ?? undefined, language);
  } finally { await ui.cleanup(); }
}

for (const args of ['{"path":"file.txt"}', "{invalid", '{"command":42}', "null"]) {
  const ui = await mount(card({ args, output: "" }));
  try {
    await ui.expand();
    assert.equal(document.querySelector(".tool__command"), null, "unknown args do not invent a command");
    assert.ok(document.querySelector(".tool__body code"), "raw args remain inspectable");
  } finally { await ui.cleanup(); }
}
{
  const ui = await mount(card({ name: "read_file", args: '{"path":"file.txt"}' }));
  try { await ui.expand(); assert.equal(document.querySelector(".tool__command"), null); }
  finally { await ui.cleanup(); }
}

// Archived titles are summaries, never the source of the complete command.
{
  const request = deferred();
  let calls = 0;
  const ui = await mount(card({ dataArchived: true, args: "", subject: "TRUNCATED_SUMMARY" }), () => { calls++; return request.promise; });
  try {
    assert.equal(calls, 0);
    await ui.expand();
    assert.equal(calls, 1);
    assert.equal(document.querySelector(".tool__command"), null);
    assert.ok(document.querySelector('.tool__data-status[role="status"]'));
    await act(async () => { request.resolve({ args: JSON.stringify({ command }), output: "ARCHIVED_OUTPUT" }); await settle(); });
    assert.ok(document.querySelector(".tool__command code")?.textContent?.includes("END_OF_COMMAND"));
    assert.ok(document.querySelector(".tool__body")?.textContent?.includes("ARCHIVED_OUTPUT"));
  } finally { await ui.cleanup(); }
}
for (const unavailable of [false, true]) {
  let calls = 0;
  const ui = await mount(card({ dataArchived: true, args: "" }), async () => {
    if (++calls === 1) { if (unavailable) return null; throw new Error("fixture read failure"); }
    return { args: JSON.stringify({ command }), output: "" };
  });
  try {
    await ui.expand();
    assert.ok(document.querySelector('.tool__data-status[role="alert"]'), "failed archive load is visible");
    await ui.click(".tool__data-status button");
    assert.equal(calls, 2);
    assert.ok(document.querySelector(".tool__command code")?.textContent?.includes("END_OF_COMMAND"));
  } finally { await ui.cleanup(); }
}

// Reject a late response after tab/item replacement, including the first paint
// before passive effect cleanup gets a chance to clear an old cached result.
{
  const old = deferred(), next = deferred();
  const archived = card({ dataArchived: true, args: "" });
  const ui = await mount(archived, tab => tab === "tab-a" ? old.promise : next.promise);
  try {
    await ui.expand();
    await ui.update(archived, "tab-b");
    await act(async () => { old.resolve({ args: '{"command":"STALE_COMMAND"}', output: "" }); await settle(); });
    assert.ok(!document.querySelector(".tool__command")?.textContent?.includes("STALE_COMMAND"));
    await act(async () => { next.resolve({ args: '{"command":"CURRENT_COMMAND"}', output: "" }); await settle(); });
    assert.ok(document.querySelector(".tool__command")?.textContent?.includes("CURRENT_COMMAND"));
    await ui.update(card({ id: "replacement", args: '{"command":"NEW_COMMAND"}' }), "tab-b");
    assert.ok(!ui.paints.at(-1)?.includes("CURRENT_COMMAND"), "new tool's first paint never uses old full data");
  } finally { await ui.cleanup(); }
}
{
  const pending = deferred();
  const ui = await mount(card({ dataArchived: true, args: "" }), () => pending.promise);
  await ui.expand();
  await ui.cleanup();
  await act(async () => { pending.resolve({ args: '{"command":"UNMOUNTED"}', output: "" }); await settle(); });
}

// tl1365 交互①②③: 点卡只展开结果；命令区默认 3 行截断（--clamped），由
// 「展开命令」二级按钮全量展开、再点收起；结果块渲染在命令块之前。
{
  const ui = await mount(card());
  try {
    assert.equal(document.querySelector(".tool__command"), null, "collapsed state mounts no command");
    await ui.expand();
    const body = document.querySelector(".tool__body")!;
    const command = document.querySelector(".tool__command")!;
    assert.ok(body.textContent?.includes("OUTPUT_STAYS_VISIBLE"), "card click reveals the result");
    const outputBlock = body.querySelector(":scope > .code-block");
    assert.ok(outputBlock, "result block mounted");
    assert.notEqual(outputBlock.compareDocumentPosition(command) & Node.DOCUMENT_POSITION_FOLLOWING, 0, "result renders before the command block");
    assert.ok(command.classList.contains("tool__command--clamped"), "default command is 3-line clamped");
    assert.equal(command.querySelector("pre")?.style.maxHeight, "", "clamped command carries no scroll maxHeight");
    const toggle = command.querySelector<HTMLButtonElement>(".tool__command-toggle");
    assert.ok(toggle, "command toggle present");
    assert.equal(toggle.getAttribute("aria-expanded"), "false", "toggle announces collapsed");
    await ui.click(".tool__command-toggle");
    const expandedCommand = document.querySelector(".tool__command")!;
    assert.ok(!expandedCommand.classList.contains("tool__command--clamped"), "expand command removes the clamp");
    assert.equal(expandedCommand.querySelector("pre")?.style.maxHeight, "240px", "expanded command restores the scroll bound");
    assert.equal(expandedCommand.querySelector(".tool__command-toggle")?.getAttribute("aria-expanded"), "true", "toggle announces expanded");
    await ui.click(".tool__command-toggle");
    assert.ok(document.querySelector(".tool__command")?.classList.contains("tool__command--clamped"), "second click collapses back to 3 lines");
  } finally { await ui.cleanup(); }
}

// tl1365 交互④: 输出「显示全部N行」与命令三态互不影响。
{
  const manyLines = Array.from({ length: 30 }, (_, i) => `line-${i + 1}`).join("\n");
  const ui = await mount(card({ output: manyLines }));
  try {
    await ui.expand();
    assert.ok(document.querySelector(".tool__command")?.classList.contains("tool__command--clamped"), "default output leaves the command clamped");
    await ui.click(".tool__showall");
    assert.ok(document.querySelector(".tool__body")?.textContent?.includes("line-30"), "showAll reveals the full output");
    assert.ok(document.querySelector(".tool__command")?.classList.contains("tool__command--clamped"), "showAll leaves the command state untouched");
    await ui.click(".tool__command-toggle");
    assert.ok(!document.querySelector(".tool__command")?.classList.contains("tool__command--clamped"), "command expand leaves showAll untouched");
    assert.ok(document.querySelector(".tool__body")?.textContent?.includes("line-30"), "output stays fully shown across the command toggle");
  } finally { await ui.cleanup(); }
}
console.log("PASS shell command visibility, exact copy, archived retry and identity fencing");
console.log("PASS tl1365 result-first split, 3-line clamped command toggle, independent showAll");
