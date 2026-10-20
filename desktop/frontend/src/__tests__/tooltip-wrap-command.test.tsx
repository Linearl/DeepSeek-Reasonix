// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tooltip-wrap-command.test.tsx
// 任务1033: wrap-mode Tooltip for the task/sub-agent hover cards — the JS line
// model (72ch greedy wrap), the 30-line cap with the truncation notice and the
// full-text copy button, the body-portal placement (visible even from inside a
// scroll container), aria-describedby wiring, and the plain-tooltip regression
// (non-wrap labels stay byte-for-byte).

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";

import { Tooltip, wrapCommandLines } from "../components/Tooltip";

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
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function section(title: string) {
  process.stdout.write(`\n${title}\n`);
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// Clipboard: no navigator.clipboard / Wails bridge in JSDOM, so the runtime
// bridge stub is the capture point (same shape writeClipboardText probes).
let copiedText = "";
(window as typeof window & { runtime?: unknown }).runtime = {
  ClipboardSetText: async (value: string) => {
    copiedText = value;
    return true;
  },
};

const flush = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });

function hover(el: Element) {
  // React's onMouseEnter is delegated from mouseover (relatedTarget=null = from
  // outside the element).
  el.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
}
function leave(el: Element) {
  el.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
}

async function withRoot(host: HTMLElement, run: (root: Root) => Promise<void>) {
  const root = createRoot(host);
  try {
    await run(root);
  } finally {
    await act(async () => { root.unmount(); });
  }
}

// ── wrapCommandLines: the line model ────────────────────────────────────────
section("wrapCommandLines: 72ch greedy word wrap");
{
  const words = "git -C /tmp/rebase --exec 'apply the patch and run the whole affected test surface before reporting' --keep";
  const long = Array.from({ length: 12 }, () => words).join(" ");
  ok(long.length >= 500, `fixture reaches the 500-char bar (${long.length})`);
  const lines = wrapCommandLines(long, 72);
  ok(lines.length > 1, `a ${long.length}-char command wraps to multiple lines (${lines.length})`);
  ok(lines.every((line) => line.length <= 72), "no wrapped line exceeds 72 chars");
  eq(lines.join(" ").replace(/ +/g, " "), long.replace(/ +/g, " "), "wrap loses no words (whitespace-normalized roundtrip)");
  eq(wrapCommandLines("short command", 72).length, 1, "a short label stays one line");
  eq(wrapCommandLines("a\nb", 72).length, 2, "explicit newlines survive as separate lines");
  const hard = "x".repeat(200);
  ok(wrapCommandLines(hard, 72).every((line) => line.length <= 72), "a token longer than the budget is hard-cut, not overflowed");
}

// ── hover card: full command visible, wrapped, portaled ─────────────────────
section("hover card: 500-char command fully visible in the body portal");
{
  const command = Array.from({ length: 10 }, () => "npm run build --silent && node scripts/check-bundle-budget.mjs").join(" && ");
  const host = document.createElement("div");
  document.body.appendChild(host);
  // A scroll container between body and trigger — the card must NOT live here.
  const scroller = document.createElement("div");
  scroller.style.overflow = "auto";
  host.appendChild(scroller);
  const trigger = document.createElement("button");
  trigger.textContent = "task row";
  scroller.appendChild(trigger);

  await withRoot(host, async (root) => {
    await act(async () => {
      root.render(<Tooltip label={command} wrap><span>task row</span></Tooltip>);
    });
    const span = host.querySelector(".tooltip-trigger")!;
    ok(span.getAttribute("aria-describedby") === null, "no aria-describedby before hover");
    await act(async () => { hover(span); await new Promise((resolve) => setTimeout(resolve, 220)); });
    const card = document.querySelector('[role="tooltip"]');
    ok(card !== null, "card renders on hover");
    ok(card?.parentElement === document.body, "card is portaled to document.body (验收⑦: visible from a low row in a scroll container)");
    ok(!scroller.contains(card), "card is not inside the scroll container");
    eq(card?.className.includes("tooltip--interactive"), true, "wrap mode marks the card interactive");
    ok(card?.className.includes("tooltip--top") === true || card?.className.includes("tooltip--bottom") === true, "card keeps a side modifier");
    ok((card as HTMLElement).style.width.includes("min(72ch, 46vw)"), "card width clamps to min(maxWidthCh ch, 46vw)");
    const text = card?.querySelector(".tooltip__text")?.textContent ?? "";
    eq(text, command, "the full 500-char command is on the card (wrapped, untruncated)");
    ok(card?.querySelector(".tooltip__foot") === null, "no truncation footer under the line cap");
    ok(span.getAttribute("aria-describedby") === card?.id, "aria-describedby points at the card (验收⑧)");
    await act(async () => { leave(span); await flush(); });
    // INTERACTIVE_HIDE_MS grace + flush
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 250)); });
    eq(document.querySelector('[role="tooltip"]'), null, "card hides after the pointer leaves trigger and card");
  });
  host.remove();
}

// ── 30-line cap: truncation notice + copy ───────────────────────────────────
section("30-line cap: truncation notice, sr full text, copy button");
{
  const lines = Array.from({ length: 45 }, (_, index) => `echo line-${index + 1} of the very long build command`);
  const command = lines.join("\n");
  const host = document.createElement("div");
  document.body.appendChild(host);
  await withRoot(host, async (root) => {
    await act(async () => {
      root.render(<Tooltip label={command} wrap><span>task row</span></Tooltip>);
    });
    const span = host.querySelector(".tooltip-trigger")!;
    await act(async () => { hover(span); await new Promise((resolve) => setTimeout(resolve, 220)); });
    const card = document.querySelector('[role="tooltip"]') as HTMLElement;
    ok(card !== null, "card renders on hover");
    const text = card.querySelector(".tooltip__text")?.textContent ?? "";
    eq(text.split("\n").length, 30, "visible text is capped at 30 lines");
    ok(!text.includes("line-31"), "line 31 is not shown");
    ok(text.includes("line-1") && text.includes("line-30"), "lines 1..30 are shown");
    const note = card.querySelector(".tooltip__note")?.textContent ?? "";
    ok(note.includes("45"), `truncation notice carries the total line count (${note})`);
    const srFull = card.querySelector(".tooltip__sr")?.textContent ?? "";
    eq(srFull, command, "screen readers still get the full command (验收⑧)");
    const copy = card.querySelector(".tooltip__copy") as HTMLButtonElement;
    ok(copy !== null, "truncated card offers the copy button");
    await act(async () => { copy.click(); });
    await flush();
    eq(copiedText, command, "copy hands over the FULL command, not the visible 30 lines");
    ok((card.querySelector(".tooltip__copy")?.textContent ?? "").length > 0, "copy button has a visible label");
    await act(async () => { root.unmount(); });
    eq(document.querySelector('[role="tooltip"]'), null, "unmount removes the card");
  });
  host.remove();
}

// ── plain tooltip regression ────────────────────────────────────────────────
section("regression: non-wrap tooltips unchanged");
{
  const host = document.createElement("div");
  document.body.appendChild(host);
  await withRoot(host, async (root) => {
    await act(async () => {
      root.render(<Tooltip label="plain label"><span>trigger</span></Tooltip>);
    });
    const span = host.querySelector(".tooltip-trigger")!;
    await act(async () => { hover(span); await new Promise((resolve) => setTimeout(resolve, 220)); });
    const card = document.querySelector('[role="tooltip"]');
    ok(card !== null, "plain card renders on hover");
    eq(card?.className.includes("tooltip--interactive"), false, "no interactive class without wrap");
    eq(card?.querySelector(".tooltip__text"), null, "no wrap text wrapper without wrap");
    eq(card?.querySelector(".tooltip__copy"), null, "no copy button without wrap");
    eq(card?.textContent, "plain label", "plain label renders verbatim");
    await act(async () => { leave(span); await flush(); });
    eq(document.querySelector('[role="tooltip"]'), null, "plain card hides immediately on leave");
  });
  host.remove();
}

process.stdout.write(`\ntooltip-wrap-command: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
