// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/heartbeat-maxruns.test.tsx
//
// Task 327: run-count budget (maxRuns) on the automation panel.
// Contract under test:
//   - default = Unlimited (every existing task keeps repeating behaviour),
//   - Once / Custom N are selectable and save through the normal dirty path,
//   - the editor reports the spent budget as k/N,
//   - the task list shows k/N and, once the engine has auto-disabled an
//     exhausted task, the "Finished (auto-disabled)" terminal state,
//   - a task with no maxRuns gets no badge at all.

import { JSDOM } from "jsdom";
import type { HeartbeatTask } from "../custom/features/heartbeat/heartbeat.types";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function button(label: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === label);
}

class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.HTMLInputElement = dom.window.HTMLInputElement;
globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.ResizeObserver = NoopResizeObserver as unknown as typeof ResizeObserver;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let nextID = 0;
let backendTasks: HeartbeatTask[] = [];
Object.assign(window, {
  go: {
    main: {
      App: {
        async HeartbeatReloadConfig() { return { revision: 1, etag: "test", tasks: backendTasks }; },
        async HeartbeatSaveConfig(update: { tasks?: HeartbeatTask[] }) {
          backendTasks = update.tasks ?? [];
          return { revision: 2, etag: "saved", tasks: backendTasks };
        },
        async HeartbeatTriggerNow() {},
        async HeartbeatGenerateID() { nextID += 1; return `draft-${nextID}`; },
        async ListWorkspaces() { return []; },
        async Settings() { return { providers: [] }; },
      },
    },
  },
});

// react-dom must load AFTER the JSDOM globals exist: canUseDOM and the
// isInputEventSupported flag it drives are computed at module scope, so a
// react-dom that boots without a document falls back to the keyup/keydown
// polyfill and silently swallows the `input` event a controlled input needs.
// The heartbeat suites use static imports, which is fine for clicks but not
// for typing — hence the dynamic imports below.
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { HeartbeatView, TaskEditor } = await import("../custom/features/heartbeat/HeartbeatPanel");
const { LocaleProvider } = await import("../lib/i18n");

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);
const noopDelete = async () => true;

function renderEditor(task: HeartbeatTask, onSave: (task: HeartbeatTask) => Promise<boolean>, key: string) {
  root.render(
    <LocaleProvider>
      <TaskEditor
        key={key}
        task={task}
        onSave={onSave}
        onDelete={noopDelete}
        onCloseDetail={() => {}}
      />
    </LocaleProvider>,
  );
}

function maxRunsField(): HTMLElement | null {
  return Array.from(document.querySelectorAll<HTMLElement>(".heartbeat-editor__field"))
    .find((field) => field.textContent?.includes("Run limit")) ?? null;
}

function setInputValue(el: HTMLInputElement | null, value: string) {
  if (!el) return;
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  setter?.call(el, value);
  el.dispatchEvent(new window.Event("input", { bubbles: true }));
}

const baseTask: HeartbeatTask = {
  id: "budget",
  title: "Backfill",
  prompt: "Backfill the index",
  interval: "30m",
  enabled: true,
  createdAt: 1,
};

console.log("\nheartbeat maxRuns: default is Unlimited and stays out of the way");
let submitted: HeartbeatTask | null = null;
await act(async () => {
  renderEditor(baseTask, async (task) => { submitted = task; return true; }, "defaults");
  await flush();
});
ok(maxRunsField() !== null, "run-limit field renders in the task editor");
ok(button("Unlimited")?.className.includes("set-seg__btn--on") === true, "no maxRuns renders Unlimited as the selected option");
ok(document.querySelector("[data-testid='heartbeat-max-runs-input']") === null, "no number input while unlimited");
ok(document.querySelector("[data-testid='heartbeat-max-runs-progress']") === null, "no k/N line while unlimited");
ok(button("Save") == null, "default state is not dirty");

console.log("\nheartbeat maxRuns: Once and Custom N save through the dirty path");
await act(async () => {
  button("Once")?.click();
  await flush();
});
ok(button("Save") !== null, "selecting Once makes the editor dirty");
await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(submitted?.maxRuns === 1, "save persists maxRuns=1 (single run is a special case of N)");

let customSubmitted: HeartbeatTask | null = null;
await act(async () => {
  renderEditor({ ...baseTask, id: "custom", maxRuns: 0 }, async (task) => { customSubmitted = task; return true; }, "custom");
  await flush();
});
await act(async () => {
  button("Custom N")?.click();
  await flush();
});
const customInput = document.querySelector<HTMLInputElement>("[data-testid='heartbeat-max-runs-input']");
ok(customInput !== null, "Custom N reveals the run-count input");
ok(Number(customInput?.value) >= 2, "the input starts at a usable N (not 0 or 1)");
await act(async () => {
  setInputValue(document.querySelector<HTMLInputElement>("[data-testid='heartbeat-max-runs-input']"), "5");
  await flush();
});
await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(customSubmitted?.maxRuns === 5, "save persists maxRuns=5");

console.log("\nheartbeat maxRuns: editor reports the spent budget as k/N");
await act(async () => {
  renderEditor({ ...baseTask, id: "progress", maxRuns: 3, runsUsed: 1 }, async () => true, "progress");
  await flush();
});
const progress = document.querySelector("[data-testid='heartbeat-max-runs-progress']");
ok(progress?.textContent?.includes("1/3") === true, "k/N line shows 1/3");

console.log("\nheartbeat maxRuns: task list shows k/N and the terminal state");
backendTasks = [
  { ...baseTask, id: "running", maxRuns: 3, runsUsed: 1 },
  { ...baseTask, id: "done", maxRuns: 3, runsUsed: 3, enabled: false },
  { ...baseTask, id: "plain" },
];
await act(async () => {
  root.render(<LocaleProvider><HeartbeatView /></LocaleProvider>);
  await flush();
  await flush();
});
const badges = Array.from(document.querySelectorAll<HTMLElement>("[data-testid='heartbeat-maxruns-badge']"));
ok(badges.length === 2, "only tasks with a budget carry the badge");
const texts = badges.map((badge) => badge.textContent ?? "");
ok(texts.some((text) => text.includes("1/3")), "in-flight task shows k/N");
const finished = badges.find((badge) => (badge.textContent ?? "").includes("3/3"));
ok(finished !== undefined, "exhausted task shows its k/N");
ok(finished?.textContent?.includes("Finished (auto-disabled)") === true, "exhausted task shows the finished (auto-disabled) terminal state");
ok(finished?.className.includes("heartbeat-maxruns-badge--done") === true, "terminal state uses the done variant");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
