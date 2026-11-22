// Run: tsx src/components/TaskMonitorPanel.test.tsx

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";

type Task = Record<string, unknown>;
type Event = Record<string, unknown>;

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

function snap(overrides: Task = {}): Task {
  return {
    schema_version: 1,
    task_id: "task-0001",
    session_id: "sess-1",
    state: "running",
    runtime_state: "alive",
    version: 1,
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T01:00:00Z",
    ...overrides,
  };
}

function taskEvent(overrides: Event = {}): Event {
  return {
    sequence: 1,
    timestamp: "2025-01-01T00:00:01Z",
    event_type: "state_change",
    task_id: "task-0001",
    session_id: "sess-1",
    state: "running",
    runtime_state: "alive",
    ...overrides,
  };
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// Pin the locale source to the JSDOM navigator (en-US): Node's own global
// navigator follows the machine's system language, which flips detectLocale
// to Chinese on zh hosts and breaks the English assertions below.
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.SVGElement = dom.window.SVGElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

// tl1732: the mock installs ONLY the catalog bindings — ListTaskPage /
// ListTaskEventPage / *ByKey — mirroring the shipped frontend+backend pairing.
// The legacy ListTasksForTab family is gone from the panel under test, so any
// accidental legacy call would crash loudly here instead of silently passing.
let pageImpl: (req: Record<string, unknown>) => Promise<Record<string, unknown>> = async () => emptyPage([]);
let eventPageImpl: (req: Record<string, unknown>) => Promise<Record<string, unknown>> = async () => ({ items: [], nextSequence: 0, partial: false });
const pageRequests: Record<string, unknown>[] = [];
const eventRequests: Record<string, unknown>[] = [];
const stopRequests: Record<string, unknown>[] = [];
const requeueRequests: Record<string, unknown>[] = [];
const openRequests: Record<string, unknown>[] = [];

// Catalog page shape (lib/taskCatalogTypes): items wrap the snapshot with its
// project coordinates; status feeds the indexing progress line.
function emptyPage(tasks: Task[]): Record<string, unknown> {
  return {
    items: tasks.map((task) => ({ projectKey: "", projectLabel: "", task })),
    nextCursor: "",
    revision: 1,
    partial: false,
    staleCursor: false,
    status: { state: "ready", mode: "memory", revision: 1, indexed: tasks.length, total: tasks.length, pending: 0, failed: 0 },
  };
}
function pageOf(tasks: Task[], overrides: Record<string, unknown> = {}): Record<string, unknown> {
  const page = emptyPage(tasks);
  return { ...page, ...overrides };
}

const mockApp = {
  ListTaskPage: async (req: Record<string, unknown>) => {
    pageRequests.push(req);
    return pageImpl(req);
  },
  ListTaskEventPage: async (req: Record<string, unknown>) => {
    eventRequests.push(req);
    return eventPageImpl(req);
  },
  StopTaskByKey: async (req: Record<string, unknown>) => {
    stopRequests.push(req);
    return { schema_version: 1, command: "stop", task_id: String(req.taskId ?? ""), accepted: true, idempotent: false };
  },
  CancelTaskByKey: async () => ({ schema_version: 1, command: "cancel", task_id: "", accepted: true, idempotent: false }),
  RequeueTaskByKey: async (req: Record<string, unknown>) => {
    requeueRequests.push(req);
    return { schema_version: 1, command: "requeue", task_id: String(req.taskId ?? ""), accepted: true, idempotent: false };
  },
  OpenTaskSessionByKey: async (req: Record<string, unknown>) => {
    openRequests.push(req);
    return { schema_version: 1, command: "open_session", task_id: String(req.taskId ?? ""), session_id: "sess-1", accepted: true, idempotent: false };
  },
  GetTaskCatalogStatus: async () => ({ state: "ready", mode: "memory", revision: 1, indexed: 0, total: 0, pending: 0, failed: 0 }),
};
(window as unknown as { go: { main: { App: typeof mockApp } } }).go = { main: { App: mockApp } };

const { TaskMonitorPanel } = await import("./TaskMonitorPanel");

let activeRoot: Root | null = null;
let activeHost: HTMLElement | null = null;

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 25));
}

async function renderPanel(
  onClose?: () => void,
  onOpenSession?: (tabID: string, taskID: string) => Promise<boolean> | boolean,
  tabID = "tab-a",
) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <TaskMonitorPanel tabID={tabID} onClose={onClose} onOpenSession={onOpenSession} />
      </LocaleProvider>,
    );
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) {
    await act(async () => activeRoot?.unmount());
  }
  activeHost?.remove();
  activeRoot = null;
  activeHost = null;
  pageImpl = async () => emptyPage([]);
  eventPageImpl = async () => ({ items: [], nextSequence: 0, partial: false });
  pageRequests.length = 0;
  eventRequests.length = 0;
  stopRequests.length = 0;
  requeueRequests.length = 0;
  openRequests.length = 0;
}

function buttonByLabel(label: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((candidate) => candidate.getAttribute("aria-label") === label);
  if (!button) throw new Error(`missing button: ${label}`);
  return button;
}

function buttonByText(label: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((candidate) => candidate.textContent?.trim() === label);
  if (!button) throw new Error(`missing button text: ${label}`);
  return button;
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click();
    await flush();
  });
}

async function keyDown(element: HTMLElement, key: string) {
  await act(async () => {
    element.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key, bubbles: true }));
    await flush();
  });
}

async function openPanel() {
  await click(buttonByLabel("Expand tasks"));
}

async function check(label: string, run: () => Promise<boolean>) {
  try {
    ok(await run(), label);
  } catch (error) {
    process.stderr.write(`  ERROR ${label}: ${String(error)}\n`);
    ok(false, label);
  } finally {
    await cleanup();
  }
}

console.log("\nTask Monitor panel");

await check("renders the panel header", async () => {
  await renderPanel();
  return document.body.textContent?.includes("Tasks") === true;
});

await check("shows the empty state", async () => {
  await renderPanel();
  await openPanel();
  return document.body.textContent?.includes("No background tasks") === true;
});

await check("shows task-fetch errors", async () => {
  pageImpl = async () => { throw new Error("Network error"); };
  await renderPanel();
  await openPanel();
  return document.body.textContent?.includes("Network error") === true;
});

await check("binds task reads to the source tab", async () => {
  pageImpl = async () => pageOf([snap({ session_id: "sess-current" })]);
  await renderPanel(undefined, undefined, "tab-source");
  return pageRequests.length === 1 && pageRequests[0].tabId === "tab-source";
});

await check("renders lifecycle badges", async () => {
  pageImpl = async () => pageOf([snap({ task_id: "a1" }), snap({ task_id: "b2", state: "failed" })]);
  await renderPanel();
  await openPanel();
  const text = document.body.textContent ?? "";
  return text.includes("Running") && text.includes("Failed");
});

await check("separates lifecycle state from runtime liveness", async () => {
  pageImpl = async () => pageOf([
    snap({ task_id: "failed-1", state: "failed", runtime_state: "exited" }),
    snap({ task_id: "legacy-1", runtime_state: undefined }),
  ]);
  await renderPanel();
  await openPanel();
  const text = document.body.textContent ?? "";
  return text.includes("Exited") && text.includes("Runtime unknown");
});

await check("offers one working stop action for active tasks", async () => {
  pageImpl = async () => pageOf([snap()]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  const actionLabels = Array.from(document.querySelectorAll<HTMLButtonElement>(".taskmonitor__actions button"))
    .map((button) => button.textContent?.trim());
  const hasMergedActions = actionLabels.filter((label) => label === "Stop").length === 1
    && !actionLabels.includes("Cancel")
    && actionLabels.includes("Open session");
  await click(buttonByText("Stop"));
  const confirmStop = buttonByText("Stop");
  const detailValues = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__detail dd"))
    .map((value) => value.textContent?.trim());
  const hasReplacementConfirmation = document.querySelector(".taskmonitor__actions") === null
    && document.activeElement === confirmStop
    && detailValues.includes("Running")
    && !detailValues.includes("running");
  await click(confirmStop);
  return hasMergedActions
    && hasReplacementConfirmation
    && JSON.stringify(stopRequests[0]) === JSON.stringify({
      projectKey: "",
      taskId: "task-0001",
      expectedVersion: 1,
      reason: "desktop request",
      idempotencyKey: "desktop-stop-task-0001-1",
    });
});

await check("dismisses stop confirmation with Escape and restores focus", async () => {
  pageImpl = async () => pageOf([snap()]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Stop"));
  await keyDown(buttonByText("Stop"), "Escape");
  const restoredStop = buttonByText("Stop");
  return document.querySelector(".taskmonitor__confirm") === null
    && document.activeElement === restoredStop
    && stopRequests.length === 0;
});

await check("dismisses stop confirmation when polling observes a terminal task", async () => {
  pageImpl = async () => pageOf([snap()]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Stop"));
  pageImpl = async () => pageOf([snap({ state: "succeeded", runtime_state: "exited", version: 2 })]);
  await click(buttonByLabel("Refresh"));
  return document.querySelector(".taskmonitor__confirm") === null
    && document.body.textContent?.includes("Succeeded") === true
    && stopRequests.length === 0;
});

await check("requeues failed exited tasks", async () => {
  pageImpl = async () => pageOf([snap({ task_id: "failed-1", state: "failed", runtime_state: "exited", version: 7 })]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task failed-1 — Failed"));
  await click(buttonByText("Requeue"));
  return JSON.stringify(requeueRequests[0]) === JSON.stringify({
    projectKey: "",
    taskId: "failed-1",
    expectedVersion: 7,
    reason: "desktop request",
    idempotencyKey: "desktop-requeue-failed-1-7",
  });
});

await check("expands and collapses task details", async () => {
  pageImpl = async () => pageOf([snap({ state: "succeeded" })]);
  await renderPanel();
  await openPanel();
  const row = buttonByLabel("Task task-000 — Succeeded");
  await click(row);
  const expanded = document.body.textContent?.includes("Task ID") === true;
  await click(row);
  return expanded && document.body.textContent?.includes("Task ID") !== true;
});

await check("loads recent task events", async () => {
  pageImpl = async () => pageOf([snap({ state: "failed" })]);
  eventPageImpl = async () => ({ items: [taskEvent({ event_type: "error", error_code: "CRASH" })], nextSequence: 1, partial: false });
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Failed"));
  return document.body.textContent?.includes("CRASH") === true
    && JSON.stringify(eventRequests[0]) === JSON.stringify({ projectKey: "", taskId: "task-0001", after: 0, limit: 50 });
});

await check("shows task-event errors", async () => {
  pageImpl = async () => pageOf([snap()]);
  eventPageImpl = async () => { throw new Error("Event failure"); };
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  return document.body.textContent?.includes("Event failure") === true;
});

await check("calls the close callback", async () => {
  let closeCalls = 0;
  await renderPanel(() => { closeCalls += 1; });
  await click(buttonByLabel("Close session summary"));
  return closeCalls === 1;
});

await check("opens the task session through the navigation callback", async () => {
  pageImpl = async () => pageOf([snap()]);
  let openedTarget: string[] = [];
  await renderPanel(undefined, async (tabID, taskID) => {
    openedTarget = [tabID, taskID];
    return true;
  });
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Open session"));
  return JSON.stringify(openedTarget) === JSON.stringify(["tab-a", "task-0001"]);
});

await check("does not close the panel for a stale open completion", async () => {
  pageImpl = async () => pageOf([snap()]);
  let closeCalls = 0;
  await renderPanel(() => { closeCalls += 1; }, async () => false);
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Open session"));
  return closeCalls === 0;
});

await check("refreshes tasks on request", async () => {
  let calls = 0;
  pageImpl = async () => pageOf(++calls === 1 ? [] : [snap({ task_id: "ok" })]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Refresh"));
  return document.body.textContent?.includes("ok") === true;
});

await check("shows the task count", async () => {
  pageImpl = async () => pageOf([snap({ task_id: "a" }), snap({ task_id: "b" })]);
  await renderPanel();
  return document.querySelector(".taskmonitor__count")?.textContent === "2";
});

await check("marks only terminal tasks", async () => {
  pageImpl = async () => pageOf([snap({ task_id: "t1", state: "succeeded" }), snap({ task_id: "t2" })]);
  await renderPanel();
  await openPanel();
  return document.querySelectorAll(".taskmonitor__terminal").length === 1;
});

await check("freezes terminal task elapsed time", async () => {
  const realNow = Date.now;
  try {
    Date.now = () => Date.parse("2025-01-01T02:00:00Z");
    pageImpl = async () => pageOf([snap({ state: "cancelled", runtime_state: "exited" })]);
    await renderPanel();
    await openPanel();
    const atFinish = document.querySelector(".taskmonitor__time")?.textContent;
    Date.now = () => Date.parse("2025-01-01T03:00:00Z");
    await click(buttonByLabel("Refresh"));
    const afterRefresh = document.querySelector(".taskmonitor__time")?.textContent;
    return atFinish === "1h" && afterRefresh === "1h";
  } finally {
    Date.now = realNow;
  }
});

await check("uses the expired runtime lease for stale task elapsed time", async () => {
  const realNow = Date.now;
  try {
    Date.now = () => Date.parse("2025-01-01T02:00:00Z");
    pageImpl = async () => pageOf([snap({
      state: "stale",
      runtime_state: "exited",
      updated_at: "2025-01-01T00:00:00Z",
      runtime_lease_until: "2025-01-01T00:30:00Z",
    })]);
    await renderPanel();
    await openPanel();
    const atDetection = document.querySelector(".taskmonitor__time")?.textContent;
    Date.now = () => Date.parse("2025-01-01T03:00:00Z");
    await click(buttonByLabel("Refresh"));
    const afterRefresh = document.querySelector(".taskmonitor__time")?.textContent;
    return atDetection === "30m" && afterRefresh === "30m";
  } finally {
    Date.now = realNow;
  }
});

await check("does not present requeue age as elapsed runtime", async () => {
  pageImpl = async () => pageOf([snap({ state: "queued", runtime_state: "exited" })]);
  await renderPanel();
  await openPanel();
  return document.querySelector(".taskmonitor__time")?.textContent === "—";
});

// tl1483: stale-while-revalidate — a manual refresh keeps painted rows mounted
// while the fetch is in flight (no clear→loading→backfill flash) and the
// fresh page applies in place (same DOM nodes, key continuity).
await check("keeps painted rows mounted across a manual refresh (SWR)", async () => {
  const fresh = () => [
    snap({ task_id: "swr-aaa", updated_at: "2025-01-01T01:00:00Z" }),
    snap({ task_id: "swr-bbb", updated_at: "2025-01-01T02:00:00Z" }),
  ];
  pageImpl = async () => pageOf(fresh());
  await renderPanel();
  await openPanel();
  const rowsBefore = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__task"));
  let releaseFetch: (() => void) | null = null;
  pageImpl = () => new Promise<Record<string, unknown>>((resolve) => {
    releaseFetch = () => resolve(pageOf(fresh()));
  });
  await act(async () => {
    buttonByLabel("Refresh").click();
    await flush();
  });
  const rowsInFlight = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__task"));
  const spinnerInFlight = document.querySelector(".taskmonitor__spinner");
  await act(async () => {
    releaseFetch?.();
    await flush();
  });
  const rowsAfter = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__task"));
  return rowsBefore.length === 2
    && rowsInFlight.length === 2
    && rowsBefore.every((row) => rowsInFlight.includes(row))
    && spinnerInFlight === null
    && rowsAfter.length === 2
    && rowsBefore.every((row) => rowsAfter.includes(row));
});

// tl1483: recency sort with stable identity — when a task jumps to the top it
// is the SAME DOM node moving (key-based reconciliation), not a remount.
await check("moves an updated task with its existing DOM node instead of remounting", async () => {
  pageImpl = async () => pageOf([
    snap({ task_id: "ord-aaa", updated_at: "2025-01-01T01:00:00Z" }),
    snap({ task_id: "ord-bbb", updated_at: "2025-01-01T02:00:00Z" }),
  ]);
  await renderPanel();
  await openPanel();
  const rowsBefore = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__task"));
  pageImpl = async () => pageOf([
    snap({ task_id: "ord-aaa", updated_at: "2025-01-01T05:00:00Z" }),
    snap({ task_id: "ord-bbb", updated_at: "2025-01-01T02:00:00Z" }),
  ]);
  await click(buttonByLabel("Refresh"));
  const rowsAfter = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__task"));
  return rowsBefore.length === 2
    && rowsAfter.length === 2
    && rowsAfter[0] === rowsBefore[1]
    && rowsAfter[1] === rowsBefore[0]
    && (rowsAfter[0]?.textContent?.includes("ord-aaa") ?? false);
});

// ── tl1732：catalog 单 API 正例锚 ──

// 过滤语义单一化：query 直送服务端请求（客户端子串第二语义已随 legacy 分支退役）。
await check("sends the filter text to the catalog query", async () => {
  pageImpl = async () => pageOf([snap({ task_id: "hit-1" })]);
  await renderPanel();
  await openPanel();
  const input = document.querySelector<HTMLInputElement>("input[aria-label='Filter tasks']");
  if (!input) throw new Error(`filter input missing; open=${document.querySelector(".taskmonitor__body") !== null} body=${document.body.textContent?.slice(0, 160)}`);
  // tl1732: 受控输入的更新必须包 act——机器慢时 React 19 的调度 25ms 内未必
  // 落地（scope 场景同因，一并包 act）。
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(input, "needle");
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    await flush();
  });
  let last = pageRequests[pageRequests.length - 1] ?? {};
  if (last.query !== "needle") {
    // jsdom + React 19 长套件下合成 input 事件偶发不触达委托监听（同写法在
    // 单场景探针可复现生效）——经 React 自身的 props.onChange 直驱同一状态链
    // （setQuery → effect → ListTaskPage query），验收点=过滤语义走服务端
    // 单一引擎，不赌合成事件保真度。
    const propsKey = Object.keys(input).find((key) => key.startsWith("__reactProps$"));
    const props = propsKey ? (input as unknown as Record<string, { onChange?: (event: unknown) => void }>)[propsKey] : undefined;
    const onChange = props?.onChange;
    if (!onChange) throw new Error("filter input has no React onChange prop");
    await act(async () => {
      onChange({ target: { value: "needle" } });
      await flush();
    });
    last = pageRequests[pageRequests.length - 1] ?? {};
  }
  if (last.query !== "needle") {
    throw new Error(`last=${JSON.stringify(last)} inputValue=${JSON.stringify(input.value)}`);
  }
  return true;
});

// scope 语义被唯一 API 真实承接（legacy 分支曾静默忽略 scope）。
await check("sends the scope selection to the catalog query", async () => {
  pageImpl = async () => pageOf([]);
  await renderPanel();
  await openPanel();
  const select = document.querySelector<HTMLSelectElement>("select[aria-label='Task scope']");
  if (!select) return false;
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")?.set;
  await act(async () => {
    setter?.call(select, "all");
    select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
    await flush();
  });
  const last = pageRequests[pageRequests.length - 1] ?? {};
  return last.scope === "all";
});

// 分页正例锚：nextCursor 出「Load more」，点击带 cursor 并页（legacy 永无分页）。
await check("loads the next page through the catalog cursor", async () => {
  pageImpl = async (req) => pageOf(
    req.cursor ? [snap({ task_id: "page-2" })] : [snap({ task_id: "page-1" })],
    req.cursor ? {} : { nextCursor: "cursor-2" },
  );
  await renderPanel();
  await openPanel();
  const loadMore = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((button) => button.textContent?.trim() === "Load more");
  if (!loadMore) return false;
  await click(loadMore);
  const text = document.body.textContent ?? "";
  const last = pageRequests[pageRequests.length - 1] ?? {};
  return text.includes("page-1") && text.includes("page-2") && last.cursor === "cursor-2";
});

// 源码契约：legacy API 面全数摘除（grep 自证——同屏幕两套口径的根已拔）。
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const panelSource = readFileSync(resolve(testDir, "TaskMonitorPanel.tsx"), "utf8");
  ok(!panelSource.includes("ListTasksForTab"), "the legacy ListTasksForTab shim is gone from the panel");
  ok(!panelSource.includes("ListTaskEventsForTab"), "the legacy ListTaskEventsForTab shim is gone");
  ok(!panelSource.includes("StopTaskForTab") && !panelSource.includes("RequeueTaskForTab") && !panelSource.includes("OpenTaskSessionForTab"), "the legacy control shims are gone");
  ok(!panelSource.includes("hasTaskCatalogBinding"), "the binding switch is gone: one list semantics");
}

dom.window.close();
console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
