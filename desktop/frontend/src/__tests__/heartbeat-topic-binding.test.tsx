// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/heartbeat-topic-binding.test.tsx
//
// tl 1059: the "bind a conversation" picker on the heartbeat task create form
// (hierarchical tree + keyword search + unbound placeholder) and the rewritten
// conversation-mode copy (three states, the reuse-wins precedence, and where
// the binding comes from when nothing is ticked).

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

// react-dom must load AFTER the JSDOM globals exist: canUseDOM and the
// isInputEventSupported flag it drives are computed at module scope, so a
// react-dom that boots without a document falls back to the keyup/keydown
// polyfill and silently swallows the `input` event a controlled input needs.
// Clicks work either way, typing does not — hence the dynamic imports below
// (same contract as heartbeat-maxruns.test.tsx).
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { TaskEditor } = await import("../custom/features/heartbeat/HeartbeatPanel");
const { LocaleProvider } = await import("../lib/i18n");

let nextID = 0;
const backendTasks: HeartbeatTask[] = [];
Object.assign(window, {
  go: {
    main: {
      App: {
        async HeartbeatReloadConfig() { return { revision: 1, etag: "test", tasks: backendTasks }; },
        async HeartbeatSaveConfig(update: { tasks?: HeartbeatTask[] }) {
          backendTasks.length = 0;
          backendTasks.push(...(update.tasks ?? []));
          return { revision: 2, etag: "saved", tasks: backendTasks };
        },
        async HeartbeatTriggerNow() {},
        async HeartbeatGenerateID() { nextID += 1; return `draft-${nextID}`; },
        async ListWorkspaces() { return [{ name: "Project One", path: "/project-one", current: true }]; },
        async Settings() { return { providers: [] }; },
        // The picker's two data sources: the project tree (hierarchical view)
        // and the session directory (search view).
        async ListProjectTree() {
          return [
            {
              key: "proj-a", kind: "project", label: "Proj A", root: "/proj-a",
              children: [
                { key: "g1", kind: "session", label: "Group One", children: [
                  { key: "t1", kind: "topic", label: "Alpha conv", topicId: "topic-alpha" },
                  { key: "t2", kind: "topic", label: "Beta conv", topicId: "topic-beta" },
                ] },
                { key: "t3", kind: "topic", label: "Gamma conv", topicId: "topic-gamma" },
              ],
            },
            {
              key: "global", kind: "global_folder", label: "Global",
              children: [
                { key: "t4", kind: "global_topic", label: "Global conv", topicId: "topic-global" },
              ],
            },
          ];
        },
        async ListAddressableSessions() {
          return [
            { contactId: "c1", title: "Delta working session", purpose: "watch the repo", topicId: "topic-delta", sessionPath: "/s/delta", open: false },
            { contactId: "c2", title: "Alpha conv", topicId: "topic-alpha", sessionPath: "/s/alpha", open: true },
          ];
        },
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

const newTask: HeartbeatTask = {
  id: "draft-1",
  title: "Watch the repo",
  prompt: "Check for updates",
  interval: "30m",
  enabled: true,
};
const editTask: HeartbeatTask = { ...newTask, id: "saved-1", createdAt: 1 };

console.log("\nheartbeat topic binding: create form shows the picker with the unbound placeholder");
let submitted: HeartbeatTask | null = null;
await act(async () => {
  renderEditor(newTask, async (task) => { submitted = task; return true; }, "create");
  await flush();
});
const bindField = query("[data-testid='heartbeat-bind-topic-field']");
ok(bindField !== null, "the bind-conversation field renders on the create form");
ok(query("[data-testid='heartbeat-bind-topic-toggle']")?.textContent?.includes("Not bound") === true,
  "unbound placeholder explains the first run creates the binding");
ok(query("[data-testid='heartbeat-bind-topic-toggle']")?.textContent?.includes("(default)") === true,
  "placeholder marks no-binding as the default behaviour");

console.log("\nheartbeat topic binding: edit form keeps the binding read-only");
await act(async () => {
  renderEditor(editTask, async (task) => { submitted = task; return true; }, "edit");
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-field']") === null, "the picker is create-only: hidden on an existing task");

await act(async () => {
  renderEditor(newTask, async (task) => { submitted = task; return true; }, "create-2");
  await flush();
});

console.log("\nheartbeat topic binding: hierarchical tree browse");
await act(async () => {
  (query<HTMLButtonElement>("[data-testid='heartbeat-bind-topic-toggle']"))?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-menu']") !== null, "opening the picker shows the menu");
const groupTitles = Array.from(document.querySelectorAll<HTMLElement>(".heartbeat-bind-topic__group-title")).map((el) => el.textContent?.trim());
ok(groupTitles?.includes("Proj A") === true && groupTitles?.includes("Global") === true,
  "tree view groups conversations under their project / global folder");
const entries = Array.from(document.querySelectorAll<HTMLElement>("[data-testid='heartbeat-bind-topic-entry']"));
ok(entries.some((el) => el.textContent?.includes("Alpha conv")), "tree lists conversations inside nested groups");
ok(entries.length === 4, `tree lists every conversation across groups (got ${entries.length}, want 4)`);
await act(async () => {
  entries.find((el) => el.textContent?.includes("Alpha conv"))?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-selected']")?.textContent?.includes("Alpha conv") === true,
  "picking a tree entry fills the selection back into the field");

console.log("\nheartbeat topic binding: keyword search by title and purpose");
await act(async () => {
  (query<HTMLButtonElement>("[data-testid='heartbeat-bind-topic-change']"))?.click();
  await flush();
});
const search = query<HTMLInputElement>("[data-testid='heartbeat-bind-topic-search']");
ok(search !== null, "the picker menu offers a search input");
await act(async () => {
  // React owns the input's value: go through the native setter so the value
  // tracker registers the change before the input event fires (same driver
  // the heartbeat-maxruns harness uses for its number input).
  const el = document.querySelector<HTMLInputElement>("[data-testid='heartbeat-bind-topic-search']");
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  setter?.call(el, "watch the repo");
  el?.dispatchEvent(new window.Event("input", { bubbles: true }));
  el?.dispatchEvent(new window.Event("change", { bubbles: true }));
  await flush();
});
const hits = Array.from(document.querySelectorAll<HTMLElement>("[data-testid='heartbeat-bind-topic-hit']"));
ok(hits.length === 1 && hits[0].textContent?.includes("Delta working session"),
  "search matches a conversation by its purpose");
await act(async () => {
  hits[0]?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-selected']")?.textContent?.includes("Delta working session") === true,
  "picking a search hit fills the selection back into the field");

console.log("\nheartbeat topic binding: selected binding persists on save");
await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(submitted?.topicId === "topic-delta", "save carries the bound topic id onto the task");

console.log("\nheartbeat topic binding: clearing returns to the default behaviour");
await act(async () => {
  (query<HTMLButtonElement>("[data-testid='heartbeat-bind-topic-clear']"))?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-toggle']") !== null, "clearing restores the unbound placeholder");
await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(submitted?.topicId === undefined, "save drops the binding after clearing");

console.log("\nheartbeat topic binding: conversation-mode copy distinguishes the three states");
const modeField = query("[data-testid='heartbeat-conversation-mode']");
ok(modeField?.textContent?.includes("One fixed conversation") === true
  && modeField?.textContent?.includes("New conversation each run") === true,
  "the mode segment offers the fixed and per-run-fresh options");
ok(modeField?.textContent?.includes("Every run continues one fixed conversation") === true,
  "fixed-mode hint explains the binding source when nothing is ticked");
await act(async () => {
  button("New conversation each run")?.click();
  await flush();
});
ok(modeField?.textContent?.includes("Every run starts a brand-new conversation") === true,
  "per-run-fresh mode explains the isolation and the binding conflict");
ok(query("[data-testid='heartbeat-bind-topic-field']")?.textContent?.includes("conflicts with a bound conversation") === true,
  "the picker hint names the exclusivity while per-run-fresh mode is on");
ok((query<HTMLButtonElement>("[data-testid='heartbeat-bind-topic-toggle']"))?.disabled === true,
  "the picker is disabled while per-run-fresh mode is on");

console.log("\nheartbeat topic binding: reuse precedence copy and picker re-enable");
await act(async () => {
  button("One fixed conversation")?.click(); // back to fixed mode
  await flush();
});
const reuseField = Array.from(document.querySelectorAll<HTMLElement>(".heartbeat-editor__field"))
  .find((field) => field.textContent?.includes("Resume a conversation")) ?? null;
const reuseToggle = reuseField?.querySelector<HTMLInputElement>("input[type=checkbox]") ?? null;
await act(async () => {
  reuseToggle?.click();
  await flush();
});
ok(modeField?.textContent?.includes("Resume mode wins") === true,
  "the mode hint names the reuse-wins precedence while resume mode is on");

console.log("\nheartbeat topic binding: switching to per-run-fresh clears a binding (mutual exclusion)");
await act(async () => {
  reuseToggle?.click(); // resume mode off again: the plain fixed-mode flow below
  await flush();
});
await act(async () => {
  (query<HTMLButtonElement>("[data-testid='heartbeat-bind-topic-toggle']"))?.click();
  await flush();
});
await act(async () => {
  Array.from(document.querySelectorAll<HTMLElement>("[data-testid='heartbeat-bind-topic-entry']"))
    .find((el) => el.textContent?.includes("Gamma conv"))?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-selected']") !== null, "a binding is set before the exclusion check");
await act(async () => {
  button("New conversation each run")?.click();
  await flush();
});
ok(query("[data-testid='heartbeat-bind-topic-toggle']") !== null, "the binding is cleared when per-run-fresh mode is picked");
await act(async () => {
  button("Save")?.click();
  await flush();
});
ok(submitted?.newConversationEachRun === true && submitted?.topicId === undefined,
  "save carries per-run-fresh mode with no binding (contract-compatible)");

console.log(`\nheartbeat-topic-binding: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
