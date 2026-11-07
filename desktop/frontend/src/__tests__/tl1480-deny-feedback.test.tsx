// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1480-deny-feedback.test.tsx
//
// tl1480: deny-with-feedback on the approval card — Enter submits (empty text
// = pure deny), Esc cancels, input clamps at 1000 chars, and the pure-deny
// wire shape stays byte-identical (no feedback key without user text).

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ApprovalModal } from "../components/ApprovalModal";
import { LocaleProvider } from "../lib/i18n";
import { resolvePromptForTab } from "../lib/exactPromptSubmit";
import type { AppBindings } from "../lib/bridge";
import type { WireApproval } from "../lib/types";

const styles = readFileSync(new URL("../styles.css", import.meta.url), "utf8");

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

function flushTimers(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function installDom(language = "en-US") {
  const dom = new JSDOM("<!doctype html><html><head></head><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: language });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  const style = document.createElement("style");
  style.textContent = styles;
  document.head.appendChild(style);
  return dom;
}

function findButton(label: string): HTMLButtonElement {
  const button = [...document.querySelectorAll("button")].find((candidate) =>
    candidate.textContent?.includes(label),
  ) as HTMLButtonElement | undefined;
  if (!button) throw new Error(`button not found: ${label}`);
  return button;
}

// Tool approvals are select-then-confirm: clicking an action only selects it;
// the confirm bar submits.
async function confirmSelected(): Promise<void> {
  const confirm = document.querySelector<HTMLButtonElement>(".decision-confirm-bar__confirm");
  if (!confirm) throw new Error("decision confirm bar did not render");
  confirm.click();
  await flushTimers(220);
}

// React's value tracker swallows direct .value writes, and a bare "input"
// Event does not drive React 19's change extraction — mirror the proven
// decision-surface pattern: prototype setter + InputEvent + change + keyup.
function typeIntoInput(input: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
  setter.call(input, value);
  input.dispatchEvent(new window.InputEvent("input", { bubbles: true, inputType: "insertText", data: value }));
  input.dispatchEvent(new window.Event("change", { bubbles: true }));
  input.dispatchEvent(new window.KeyboardEvent("keyup", { key: ".", bubbles: true }));
}

type Answer = [boolean, boolean, boolean, string | undefined];

async function renderApproval(dom: ReturnType<typeof installDom>, answers: Answer[]): Promise<void> {
  const root = createRoot(document.getElementById("root")!);
  const approval: WireApproval = { id: "bash-1480", tool: "bash", subject: "ls -la" };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          onAnswer={(allow, session, persist, feedback) => answers.push([allow, session, persist, feedback])}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  (dom as unknown as { __root?: unknown }).__root = root;
}

async function unmount(dom: ReturnType<typeof installDom>): Promise<void> {
  const root = (dom as unknown as { __root?: { unmount(): Promise<void> } }).__root;
  if (root) await act(async () => { await root.unmount(); });
  dom.window.close();
}

console.log("\ntl1480 deny with feedback");

// Reverse pin: the plain Deny action still fires the legacy 3-arg call — the
// empty-feedback wire shape is untouched.
{
  const dom = installDom();
  const answers: Answer[] = [];
  await renderApproval(dom, answers);
  const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
  eq(actions.length, 4, "ordinary bash approval keeps its four actions");
  const deny = actions[3];
  ok(deny.textContent?.includes("Deny"), "deny action stays the last option");
  await act(async () => {
    deny.click();
    await flushTimers();
  });
  await confirmSelected();
  eq(answers.length, 1, "plain deny answers once");
  eq(JSON.stringify(answers[0]), JSON.stringify([false, false, false, undefined]), "plain deny carries no feedback (zero wire change)");
  await unmount(dom);
}

// Deny with feedback: trigger opens the inline line, Enter submits the deny
// with the typed reason.
{
  const dom = installDom();
  const answers: Answer[] = [];
  await renderApproval(dom, answers);
  eq(answers.length, 0, "opening the feedback line decides nothing");
  await act(async () => {
    findButton("Deny with feedback").click();
    await flushTimers();
  });
  const input = document.querySelector<HTMLInputElement>(".recovery-guidance input");
  ok(input != null, "feedback line renders in one click");
  ok(input?.getAttribute("maxlength") === "1000", "feedback input clamps at 1000 chars");
  await act(async () => {
    input!.focus();
    typeIntoInput(input!, "do not touch the main branch");
    await flushTimers();
  });
  await act(async () => {
    input!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    await flushTimers(220);
  });
  eq(answers.length, 1, "Enter submits the deny with feedback");
  eq(JSON.stringify(answers[0]), JSON.stringify([false, false, false, "do not touch the main branch"]), "Enter denies with the typed feedback");
  await unmount(dom);
}

// Empty feedback + Enter is the pure deny (现行为零变化).
{
  const dom = installDom();
  const answers: Answer[] = [];
  await renderApproval(dom, answers);
  await act(async () => {
    findButton("Deny with feedback").click();
    await flushTimers();
  });
  const input = document.querySelector<HTMLInputElement>(".recovery-guidance input");
  await act(async () => {
    input!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    await flushTimers(220);
  });
  eq(answers.length, 1, "Enter on an empty feedback line still denies");
  eq(JSON.stringify(answers[0]), JSON.stringify([false, false, false, undefined]), "empty feedback deny carries no feedback");
  await unmount(dom);
}

// Esc cancels: the box closes without answering, and the card keeps working.
{
  const dom = installDom();
  const answers: Answer[] = [];
  await renderApproval(dom, answers);
  await act(async () => {
    findButton("Deny with feedback").click();
    await flushTimers();
  });
  const input = document.querySelector<HTMLInputElement>(".recovery-guidance input");
  await act(async () => {
    input!.value = "cancelled reason";
    input!.dispatchEvent(new window.Event("input", { bubbles: true }));
    await flushTimers();
  });
  await act(async () => {
    input!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await flushTimers();
  });
  eq(document.querySelector(".recovery-guidance input"), null, "Esc closes the feedback line");
  eq(answers.length, 0, "Esc never answers the approval");
  await act(async () => {
    const actions = [...document.querySelectorAll(".prompt-shelf__actions .prompt-action")] as HTMLButtonElement[];
    actions[3].click();
    await flushTimers();
  });
  await confirmSelected();
  eq(answers.length, 1, "the card still denies normally after cancel");
  eq(JSON.stringify(answers[0]), JSON.stringify([false, false, false, undefined]), "post-cancel deny stays a pure deny");
  await unmount(dom);
}

// Length cap: typing beyond 1000 chars is sliced by onChange.
{
  const dom = installDom();
  const answers: Answer[] = [];
  await renderApproval(dom, answers);
  await act(async () => {
    findButton("Deny with feedback").click();
    await flushTimers();
  });
  const input = document.querySelector<HTMLInputElement>(".recovery-guidance input");
  const long = "x".repeat(1200);
  await act(async () => {
    typeIntoInput(input!, long);
    await flushTimers();
  });
  eq(input!.value.length, 1000, "input slices pasted text to 1000 chars");
  await act(async () => {
    input!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    await flushTimers(220);
  });
  ok((answers[0]?.[3] ?? "").length === 1000, "submitted feedback is the clamped text");
  await unmount(dom);
}

// Transport shape: the exact-identity resolve carries feedback only when the
// user typed one; the pure-deny answer object keeps its legacy keys only.
{
  const calls: Array<Record<string, unknown>> = [];
  const binding = {
    ListTabs: async () => [],
    ResolvePromptForTab: (
      _tabID: string,
      _promptID: string,
      _turnID: string,
      _runtimeEpoch: string,
      _kind: string,
      answer: Record<string, unknown>,
    ) => {
      calls.push(answer);
      return Promise.resolve();
    },
  } as unknown as AppBindings;
  await resolvePromptForTab(binding, "t1", "12", "approval", { allow: false, session: false, persist: false, feedback: "why" }, "turn-1", "ep-1");
  await resolvePromptForTab(binding, "t1", "12", "approval", { allow: false, session: false, persist: false }, "turn-1", "ep-1");
  eq(calls.length, 2, "both answers reached the exact-identity binding");
  eq(calls[0].feedback, "why", "feedback rides the resolve answer");
  eq("feedback" in calls[1], false, "pure deny answer carries no feedback key");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
