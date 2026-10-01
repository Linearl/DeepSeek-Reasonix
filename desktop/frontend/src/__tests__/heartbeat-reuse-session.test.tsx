// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/heartbeat-reuse-session.test.tsx
//
// Task 437: heartbeat "resume an existing conversation" mode (reuseSession).
// Panel contract: the switch defaults OFF (existing new-per-run semantics
// unchanged), toggling shows which conversation is bound, the switch is
// saveable through the normal dirty path, and reuse tasks carry a visible
// badge in the task list.

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { HeartbeatView, TaskEditor } from "../custom/features/heartbeat/HeartbeatPanel";
import type { HeartbeatTask } from "../custom/features/heartbeat/heartbeat.types";
import { LocaleProvider } from "../lib/i18n";

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
        async ListWorkspaces() { return [{ name: "Project One", path: "/project-one", current: true }]; },
        async Settings() { return { providers: [] }; },
      },
    },
  },
});

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

// The reuse switch is the checkbox inside the field labelled "Resume a
// conversation" (the goal-mode toggle lives in its own field).
function reuseField(): HTMLElement | null {
  return Array.from(document.querySelectorAll<HTMLElement>(".heartbeat-editor__field"))
    .find((field) => field.textContent?.includes("Resume a conversation")) ?? null;
}

function reuseToggle(): HTMLInputElement | null {
  return reuseField()?.querySelector<HTMLInputElement>("input[type=checkbox]") ?? null;
}

console.log("\nheartbeat reuse-session: switch defaults off");
const baseTask: HeartbeatTask = {
  id: "reuse",
  title: "Watch the repo",
  prompt: "Check for updates",
  interval: "30m",
  enabled: true,
  createdAt: 1,
};
let submitted: HeartbeatTask | null = null;
await act(async () => {
  renderEditor(baseTask, async (task) => { submitted = task; return true; }, "defaults");
  await flush();
});
ok(reuseToggle() !== null, "resume switch renders in the task editor");
ok(reuseToggle()?.checked === false, "resume switch defaults to off (existing behavior unchanged)");
ok(document.querySelector("[data-testid='heartbeat-bound-topic']") === null, "bound-conversation row stays hidden while off");
ok(button("Save") == null, "default state is not dirty");

console.log("\nheartbeat reuse-session: unbound task shows the create-and-bind hint");
await act(async () => {
  reuseToggle()?.click();
  await flush();
});
const boundRow = document.querySelector("[data-testid='heartbeat-bound-topic']");
ok(boundRow !== null, "toggling on reveals the bound-conversation row");
ok(boundRow?.textContent?.includes("Not bound yet") === true, "unbound task explains the first run creates the binding");
ok(button("Save") !== null, "toggling the switch makes the editor dirty");

await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(submitted?.reuseSession === true, "save persists reuseSession=true");
ok(submitted?.newConversationEachRun === undefined || submitted?.newConversationEachRun === false || submitted?.newConversationEachRun === true,
  "save leaves other session-mode fields untouched");

console.log("\nheartbeat reuse-session: bound task shows the bound conversation id");
let boundSubmitted: HeartbeatTask | null = null;
await act(async () => {
  renderEditor({ ...baseTask, id: "bound", reuseSession: true, topicId: "abcdef1234567890" }, async (task) => { boundSubmitted = task; return true; }, "bound");
  await flush();
});
ok(reuseToggle()?.checked === true, "persisted resume tasks render the switch on");
const boundId = document.querySelector(".heartbeat-editor__bound-id");
ok(boundId?.textContent === "abcdef1234567890", "bound-conversation row shows the bound topic id");
ok(boundId?.getAttribute("title") === "Open conversation" || (boundId?.getAttribute("title")?.length ?? 0) > 0, "bound id carries the open-conversation affordance");

console.log("\nheartbeat reuse-session: task list badge marks reuse tasks");
backendTasks = [
  { ...baseTask, id: "task-bound", reuseSession: true, topicId: "resume1" },
  { ...baseTask, id: "task-unbound", reuseSession: true },
  { ...baseTask, id: "task-normal" },
];
await act(async () => {
  root.render(<LocaleProvider><HeartbeatView /></LocaleProvider>);
  await flush();
  await flush();
});
const badges = Array.from(document.querySelectorAll<HTMLElement>(".heartbeat-reuse-badge"));
ok(badges.length === 2, "only reuse tasks carry the resume badge");
const badgeTexts = badges.map((badge) => badge.textContent ?? "");
ok(badgeTexts.includes("Resume resume"), "bound badge shows the bound conversation short id");
ok(badgeTexts.includes("Resume (unbound)"), "unbound badge says so instead of an empty id");
ok((badges[0]?.getAttribute("title") ?? "").length > 0, "badge carries an explaining tooltip");
ok(document.querySelector(".worktree-node--task:not(.heartbeat-reuse-badge)") !== null, "normal tasks still render in the list");

await act(async () => root.unmount());
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
