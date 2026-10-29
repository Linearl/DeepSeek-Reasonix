// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1248-heartbeat-inline-pause.test.tsx
//
// tl 1248: inline pause/resume button in the heartbeat task list row hover
// actions (next to "run now"). Contract under test:
//   - an enabled task's row actions expose a pause button ("Pause");
//   - clicking it persists enabled=false through the normal save path;
//   - the same button then reads "Resume" (icon flips too) without leaving
//     the list — the detail-page entry point stays untouched;
//   - the row class flips to worktree-node--paused so the "Paused" status
//     filter picks it up (and the "Enabled" filter drops it);
//   - clicking "Resume" flips everything back;
//   - both list render sites (grouped + flat) behave the same.

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

function taskRows(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".worktree-node--task"));
}

function inlineToggleButton(): HTMLButtonElement | undefined {
  // The row hover actions carry native titles; the pause/resume button is the
  // one titled "Pause" or "Resume" (distinct from the marker tooltip's
  // "Click to pause"/"Click to start").
  return Array.from(document.querySelectorAll<HTMLButtonElement>(".worktree-node__actions button"))
    .find((item) => item.title === "Pause" || item.title === "Resume");
}

function statusTab(label: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("[role='tab']"))
    .find((item) => item.textContent?.trim() === label);
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

// react-dom must load AFTER the JSDOM globals exist (see heartbeat-maxruns.test.tsx).
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { HeartbeatView } = await import("../custom/features/heartbeat/HeartbeatPanel");
const { LocaleProvider } = await import("../lib/i18n");

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);

async function renderView() {
  await act(async () => {
    root.render(<LocaleProvider><HeartbeatView /></LocaleProvider>);
    await flush();
    await flush();
  });
}

const probeTask: HeartbeatTask = {
  id: "tl1248",
  title: "Inline Pause Probe",
  prompt: "Probe the inline pause button",
  interval: "30m",
  enabled: true,
  createdAt: 1,
};

console.log("\ntl1248 grouped view: enabled task exposes an inline Pause button");
backendTasks = [{ ...probeTask }];
await renderView();
ok(taskRows().length === 1, "task row renders in the grouped list");
const row0 = taskRows()[0];
ok(!row0.className.includes("worktree-node--paused"), "enabled task is not marked paused");
ok(inlineToggleButton()?.title === "Pause", "row actions expose a Pause button");
ok(inlineToggleButton()?.querySelector("svg") !== null, "pause button carries an icon");

console.log("\ntl1248 grouped view: clicking Pause persists and flips the button to Resume");
await act(async () => {
  inlineToggleButton()?.click();
  await flush();
  await flush();
});
ok(backendTasks[0]?.enabled === false, "pause persists enabled=false through the save path");
const row1 = taskRows()[0];
ok(row1.className.includes("worktree-node--paused"), "row switches to the paused variant");
ok(inlineToggleButton()?.title === "Resume", "inline button flips to Resume after pausing");

console.log("\ntl1248 grouped view: paused task follows the Paused/Enabled status filters");
await act(async () => {
  statusTab("Paused")?.click();
  await flush();
});
ok(taskRows().length === 1, "Paused filter shows the paused task");
await act(async () => {
  statusTab("Enabled")?.click();
  await flush();
});
ok(taskRows().length === 0, "Enabled filter drops the paused task");

console.log("\ntl1248 grouped view: clicking Resume flips back and re-enters the Enabled filter");
await act(async () => {
  statusTab("Paused")?.click();
  await flush();
});
ok(inlineToggleButton()?.title === "Resume", "Resume button is reachable under the Paused filter");
await act(async () => {
  inlineToggleButton()?.click();
  await flush();
  await flush();
});
ok(backendTasks[0]?.enabled === true, "resume persists enabled=true through the save path");
await act(async () => {
  statusTab("Enabled")?.click();
  await flush();
});
ok(taskRows().length === 1, "resumed task shows up under the Enabled filter again");
const row2 = taskRows()[0];
ok(!row2.className.includes("worktree-node--paused"), "resumed row drops the paused variant");
ok(inlineToggleButton()?.title === "Pause", "inline button flips back to Pause after resuming");

console.log("\ntl1248 flat view: same inline pause/resume contract");
// Reset the status filter left on "Enabled" by the grouped section, or the
// freshly paused row would be filtered out of the list instead of flipping.
await act(async () => {
  statusTab("All")?.click();
  await flush();
});
await act(async () => {
  const viewToggle = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((item) => item.title === "Flat list");
  viewToggle?.click();
  await flush();
});
ok(taskRows().length === 1, "flat view renders the task row");
ok(inlineToggleButton()?.title === "Pause", "flat view exposes the inline Pause button");
await act(async () => {
  inlineToggleButton()?.click();
  await flush();
  await flush();
});
ok(backendTasks[0]?.enabled === false, "flat view pause persists enabled=false");
ok(taskRows()[0]?.className.includes("worktree-node--paused"), "flat view row switches to the paused variant");
ok(inlineToggleButton()?.title === "Resume", "flat view inline button flips to Resume");

console.log("\ntl1248 flat view: paused task follows the status filters and resumes");
await act(async () => {
  statusTab("Paused")?.click();
  await flush();
});
ok(taskRows().length === 1, "flat view Paused filter shows the paused task");
await act(async () => {
  statusTab("Enabled")?.click();
  await flush();
});
ok(taskRows().length === 0, "flat view Enabled filter drops the paused task");
await act(async () => {
  statusTab("Paused")?.click();
  await flush();
});
await act(async () => {
  inlineToggleButton()?.click();
  await flush();
  await flush();
});
ok(backendTasks[0]?.enabled === true, "flat view resume persists enabled=true");
await act(async () => {
  statusTab("All")?.click();
  await flush();
});
ok(inlineToggleButton()?.title === "Pause", "flat view inline button flips back to Pause after resuming");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
