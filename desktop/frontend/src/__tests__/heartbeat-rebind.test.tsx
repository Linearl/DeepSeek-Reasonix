// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/heartbeat-rebind.test.tsx
//
// tl1851: bound-conversation display + re-bind on the automation page.
//   ① the bare topicId becomes「项目名-会话名」on the list badge, the editor's
//      bound block and the picker (id demoted to tooltip / small text), and the
//      name follows session renames via the shared session directory;
//   ② the existing-task editor offers「更换绑定会话」— keyword search reusing the
//      tl1059 picker interaction — and saving lands the new topicId on disk;
//   ③ unbound / per-run-fresh tasks render exactly as before.

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

function query<T extends Element = HTMLElement>(selector: string): T | null {
  return document.querySelector<T>(selector);
}

function queryAll<T extends Element = HTMLElement>(selector: string): T[] {
  return Array.from(document.querySelectorAll<T>(selector));
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

// react-dom must load AFTER the JSDOM globals exist (same contract as
// heartbeat-topic-binding.test.tsx).
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { HeartbeatView } = await import("../custom/features/heartbeat/HeartbeatPanel");
const { LocaleProvider } = await import("../lib/i18n");

let nextID = 0;
const backendTasks: HeartbeatTask[] = [
  { id: "task-resume", title: "Nightly digest", prompt: "p", interval: "1h", enabled: true, createdAt: 1,
    reuseSession: true, topicId: "topic-alpha", scope: "project", workspaceRoot: "/project-one" },
  { id: "task-fresh", title: "Fresh each run", prompt: "p", interval: "2h", enabled: true, createdAt: 2,
    newConversationEachRun: true },
  { id: "task-plain", title: "Plain fixed task", prompt: "p", interval: "3h", enabled: true, createdAt: 3 },
];
let savedUpdate: { tasks?: HeartbeatTask[] } | null = null;
// The session directory is mutable so the rename-following pin can change a
// title between mounts (the panel refetches on activation / task reload).
const directorySessions: { topicId: string; title: string; purpose?: string; scope?: string; workspaceRoot?: string; sessionPath: string }[] = [
  { topicId: "topic-alpha", title: "Alpha conv", scope: "project", workspaceRoot: "/project-one", sessionPath: "/s/alpha" },
  { topicId: "topic-delta", title: "Delta working session", purpose: "watch the repo", scope: "global", sessionPath: "/s/delta" },
];

Object.assign(window, {
  go: {
    main: {
      App: {
        async HeartbeatReloadConfig() { return { revision: 1, etag: "test", tasks: backendTasks }; },
        async HeartbeatSaveConfig(update: { tasks?: HeartbeatTask[] }) {
          savedUpdate = update;
          backendTasks.length = 0;
          backendTasks.push(...(update.tasks ?? []));
          return { revision: 2, etag: "saved", tasks: backendTasks };
        },
        async HeartbeatTriggerNow() {},
        async HeartbeatGenerateID() { nextID += 1; return `draft-${nextID}`; },
        async Settings() { return { providers: [] }; },
        async ListWorkspaces() { return [{ name: "Proj One", path: "/project-one", current: true }]; },
        async ListAddressableSessions() { return directorySessions.map((item) => ({ ...item, contactId: item.topicId, open: false })); },
        async ListProjectTree() {
          return [
            {
              key: "proj", kind: "project", label: "Proj One", root: "/project-one",
              children: [{ key: "t1", kind: "topic", label: "Alpha conv", topicId: "topic-alpha" }],
            },
            {
              key: "global", kind: "global_folder", label: "Global",
              children: [{ key: "t2", kind: "global_topic", label: "Delta working session", topicId: "topic-delta" }],
            },
          ];
        },
      },
    },
  },
});

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
// A React root cannot render again after unmount — the rename-following pin
// remounts by creating a fresh root for the second activation.
let root = createRoot(rootElement);

function renderView(key: string) {
  root.render(
    <LocaleProvider key={key}>
      <HeartbeatView active={true} />
    </LocaleProvider>,
  );
}

function remount(key: string) {
  root.unmount();
  root = createRoot(rootElement);
  return renderView(key);
}

const taskRow = (title: string) =>
  Array.from(document.querySelectorAll<HTMLElement>(".worktree-node--task")).find((node) => node.textContent?.includes(title));

console.log("\ntl1851 ①: list badge shows 项目名-会话名 instead of the bare id");
await act(async () => {
  renderView("mount");
  await flush();
  await flush();
});
const badge = query(".heartbeat-reuse-badge");
ok(badge?.textContent?.includes("Proj One-Alpha conv") === true,
  `resume badge resolves to the project-session label (got: ${badge?.textContent})`);
ok(badge?.textContent?.includes("topic-alpha") !== true, "the raw topic id is gone from the badge text");
ok(badge?.getAttribute("title")?.includes("topic-alpha") === true, "the id survives as the badge tooltip");
ok(queryAll(".heartbeat-reuse-badge").length === 1, "tasks without resume mode render no badge (unchanged)");

console.log("\ntl1851 ①: editor bound block shows the readable name with the id as small text");
await act(async () => {
  taskRow("Nightly digest")?.querySelector<HTMLButtonElement>(".automation-task-select")?.click();
  await flush();
  await flush();
});
const selected = query("[data-testid='heartbeat-bind-topic-selected']");
ok(selected?.textContent?.includes("Proj One-Alpha conv") === true,
  "the closed picker already shows the resolved name (directory-resolved, no menu opened)");
ok(selected?.textContent?.includes("topic-alpha") === true, "the id stays as secondary text next to the name");
const boundBlock = query("[data-testid='heartbeat-bound-topic']");
ok(boundBlock?.textContent?.includes("Proj One-Alpha conv") === true, "the resume block shows the readable name");
ok(query("[data-testid='heartbeat-bound-topic-id']")?.textContent === "topic-alpha",
  "the resume block keeps the raw id as small text");
ok(button("Change binding") != null, "the 更换绑定会话 button is offered on an existing task");

console.log("\ntl1851 ②: keyword search re-binds and the save lands the new topicId");
await act(async () => {
  button("Change binding")?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-menu']") !== null, "the re-bind menu opens");
const search = query<HTMLInputElement>("[data-testid='heartbeat-bind-topic-search']");
ok(search !== null, "the menu offers the keyword search input");
await act(async () => {
  const el = document.querySelector<HTMLInputElement>("[data-testid='heartbeat-bind-topic-search']");
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  setter?.call(el, "delta");
  el?.dispatchEvent(new window.Event("input", { bubbles: true }));
  el?.dispatchEvent(new window.Event("change", { bubbles: true }));
  await flush();
});
const hits = queryAll("[data-testid='heartbeat-bind-topic-hit']");
ok(hits.length === 1 && hits[0].textContent?.includes("Delta working session") === true,
  "keyword search matches the target conversation");
await act(async () => {
  hits[0]?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-selected']")?.textContent?.includes("Global-Delta working session") === true,
  "the global target resolves with the Global- prefix");
await act(async () => {
  button("Save")?.click();
  await flush();
  await flush();
});
const savedTask = savedUpdate?.tasks?.find((task) => task.id === "task-resume");
ok(savedTask?.topicId === "topic-delta", `the save carries the re-bound topicId (got: ${savedTask?.topicId})`);
const badgeAfter = query(".heartbeat-reuse-badge");
ok(badgeAfter?.textContent?.includes("Global-Delta working session") === true,
  "the list badge follows the re-bind immediately after the save");

console.log("\ntl1851 ①: session rename follows the display on the next panel activation");
directorySessions[1].title = "Delta renamed";
await act(async () => {
  remount("remount");
  await flush();
  await flush();
  await flush();
});
ok(query(".heartbeat-reuse-badge")?.textContent?.includes("Global-Delta renamed") === true,
  "the badge shows the renamed session after re-activation (rename follows)");

console.log("\ntl1851 ③: unbound / per-run-fresh tasks are unchanged");
const freshRow = taskRow("Fresh each run");
ok(freshRow?.querySelector(".heartbeat-reuse-badge") === null, "per-run-fresh rows still show no badge");
await act(async () => {
  freshRow?.querySelector<HTMLButtonElement>(".automation-task-select")?.click();
  await flush();
  await flush();
});
ok((query("[data-testid='heartbeat-bind-topic-toggle']") as HTMLButtonElement | null)?.disabled === true,
  "the picker stays disabled while per-run-fresh mode is on");
ok(query("[data-testid='heartbeat-bound-topic']") === null, "no bound block without resume mode");

console.log(`\nheartbeat-rebind: ${passed} passed, ${failed} failed`);
await act(async () => root.unmount());
if (failed > 0) process.exit(1);
