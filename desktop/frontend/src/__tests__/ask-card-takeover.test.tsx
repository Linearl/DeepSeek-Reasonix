import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { ReactNode } from "react";
import type { WireAsk } from "../lib/types";

// 任务849 组件级测试钉: the ask card shows the local auto-decide countdown and
// the takeover toggle only when the App arms it (countdownMs > 0 + onTakeover
// bridge); the toggle flips its label both ways and rolls back when the
// backend reports the toggle was not consumed. The plain panel (no countdown
// props) stays byte-for-byte the pre-849 card.

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { AskCard } = await import("../components/AskCard");
const { LocaleProvider } = await import("../lib/i18n");

const root = createRoot(document.getElementById("root")!);
const ask: WireAsk = {
  id: "1", runtimeEpoch: "runtime-a", turnId: "turn-a",
  questions: [{ id: "q1", prompt: "Choose", options: [{ label: "Option A" }, { label: "Option B" }] }],
};
let takeoverCalls: boolean[] = [];
async function render(scope: string | null, armed: boolean, applied: boolean) {
  let body: ReactNode = null;
  if (scope !== null) {
    body = (
      <AskCard
        ask={ask}
        draftScope={scope}
        onAnswer={() => {}}
        onStop={() => {}}
        countdownMs={armed ? 15_000 : undefined}
        onTakeover={armed
          ? (_id, taking) => { takeoverCalls.push(taking); return Promise.resolve(applied); }
          : undefined}
      />
    );
  }
  await act(async () => root.render(<LocaleProvider>{body}</LocaleProvider>));
}

const headerText = () => document.querySelector<HTMLElement>(".ask-shelf__header-meta")?.textContent ?? "";
const takeoverButton = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".prompt-shelf__header-button"))
  .find((b) => b.textContent === "Take over" || b.textContent === "Release");
const click = async (element: Element) => {
  await act(async () => { element.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
};

// 钉①: without the countdown props the card renders neither the countdown nor
// the toggle — the attended/remote/app-shell faces stay the pre-849 panel.
await render("plain", false, true);
assert.equal(headerText().includes("auto-answers in"), false, "the plain panel shows no countdown");
assert.equal(takeoverButton(), undefined, "the plain panel shows no takeover toggle");
console.log("  PASS  the plain panel carries no countdown and no takeover toggle");

// 钉②: armed, the card shows the ticking countdown and the toggle; holding
// the takeover swaps the label, shows the held badge, and parks the countdown;
// releasing restores both.
takeoverCalls = [];
await render("armed", true, true);
assert.ok(/auto-answers in \d+s/.test(headerText()), `armed panel shows the countdown, got: ${headerText()}`);
assert.ok(takeoverButton()?.textContent === "Take over", "the toggle starts as Take over");

await click(takeoverButton()!);
assert.deepEqual(takeoverCalls, [true], "holding the takeover hops true to the bridge");
assert.ok(takeoverButton()?.textContent === "Release", "the held toggle reads Release");
assert.ok(headerText().includes("Taken over"), `the held badge shows, got: ${headerText()}`);
assert.ok(!/auto-answers in \d+s/.test(headerText()), "the countdown parks while held");
console.log("  PASS  holding the takeover flips the label, shows the badge, parks the countdown");

await click(takeoverButton()!);
assert.deepEqual(takeoverCalls, [true, false], "releasing the takeover hops false to the bridge");
assert.ok(takeoverButton()?.textContent === "Take over", "the released toggle reads Take over again");
assert.ok(/auto-answers in \d+s/.test(headerText()), "the countdown resumes after the release");
console.log("  PASS  releasing the takeover restores the label and the countdown");

// 钉③: a toggle the backend did not consume rolls the button state back.
takeoverCalls = [];
await render(null, false, false);
await render("rollback", true, false);
await click(takeoverButton()!);
await act(async () => { await Promise.resolve(); });
assert.ok(takeoverButton()?.textContent === "Take over", "a rejected takeover rolls the label back to Take over");
assert.ok(!headerText().includes("Taken over"), "a rejected takeover shows no held badge");
console.log("  PASS  a backend-rejected takeover rolls the button state back");

await render(null, false, false);
await act(async () => root.unmount());
dom.window.close();
