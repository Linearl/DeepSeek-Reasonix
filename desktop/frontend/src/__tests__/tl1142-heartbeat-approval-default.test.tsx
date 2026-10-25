// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/tl1142-heartbeat-approval-default.test.tsx
// tl 1142：新建 heartbeat 任务的 approvalMode 跟随设置里的全局默认
// （设置→默认工具审批模式），但 auto 降级为 yolo（无人值守的 auto 计划询问
// 无人在场等于任务卡死），编辑器内给出降级提示；推荐区保留 ask 安全默认。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { HeartbeatView } from "../custom/features/heartbeat/HeartbeatPanel";
import { useAutomationDraftStore } from "../store/automationDrafts";
import { normalizeHeartbeatApproval, demoteAutoForHeartbeat } from "../custom/features/heartbeat/heartbeat.presentation";
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
let savedUpdate: { tasks?: HeartbeatTask[] } | null = null;
let backendTasks: HeartbeatTask[] = [];
let settingsApprovalDefault: unknown = "auto";
let settingsShouldFail = false;
Object.assign(window, {
  go: {
    main: {
      App: {
        async HeartbeatReloadConfig() { return { revision: 1, etag: "test", tasks: backendTasks }; },
        async HeartbeatSaveConfig(update: { tasks?: HeartbeatTask[] }) {
          savedUpdate = update;
          backendTasks = update.tasks ?? [];
          return { revision: 2, etag: "saved", tasks: backendTasks };
        },
        async HeartbeatTriggerNow() {},
        async HeartbeatGenerateID() { nextID += 1; return `draft-${nextID}`; },
        async ListWorkspaces() { return [{ name: "Project One", path: "/project-one", current: true }]; },
        async Settings() {
          if (settingsShouldFail) throw new Error("settings unavailable");
          return { providers: [], defaultToolApprovalMode: settingsApprovalDefault };
        },
      },
    },
  },
});

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);

// HeartbeatView 的全局默认 effect 以 active 为依赖：先渲染 inactive 再激活，
// 强制每个用例按最新 mock 值重新读取 Settings。
async function renderView(active: boolean) {
  await act(async () => {
    root.render(<LocaleProvider><HeartbeatView active={active} /></LocaleProvider>);
    await flush();
  });
}

// 编辑器现状：空 title/prompt 的草稿保存按钮禁用（防呆）。本文件只关心
// approvalMode，保存前直接经草稿 store（编辑器受控状态源）填上必需字段——
// JSDOM 对受控 textarea 的输入事件模拟不可靠，不依赖它。
async function fillRequiredFieldsAndSave() {
  const entries = useAutomationDraftStore.getState().entries;
  const draftId = Object.keys(entries).find((id) => entries[id].baseline === null);
  await act(async () => {
    if (draftId) useAutomationDraftStore.getState().edit(draftId, (task) => ({ ...task, title: "tl1142 probe task", prompt: "probe prompt" }));
    await flush();
  });
  await act(async () => {
    button("Save")?.click();
    await flush();
    await flush();
  });
}

async function openFreshCreateEditor() {
  backendTasks = [];
  savedUpdate = null;
  await renderView(false);
  await renderView(true);
  await flush();
  await act(async () => {
    button("Add task")?.click();
    await flush();
    await flush();
  });
}

console.log("\ntl 1142 approval normalization helpers");

ok(normalizeHeartbeatApproval("auto") === "auto" && normalizeHeartbeatApproval("ask") === "ask" && normalizeHeartbeatApproval("yolo") === "yolo", "normalize keeps the three legal modes");
ok(normalizeHeartbeatApproval(undefined) === "ask" && normalizeHeartbeatApproval("") === "ask" && normalizeHeartbeatApproval("junk") === "ask", "normalize falls back to ask for missing or unknown values");
ok(demoteAutoForHeartbeat("auto") === "yolo", "auto is demoted to yolo for unattended heartbeat runs");
ok(demoteAutoForHeartbeat("ask") === "ask" && demoteAutoForHeartbeat("yolo") === "yolo", "ask and yolo pass through unchanged");

console.log("\ntl 1142 manual create follows the global default (auto → yolo + hint)");

settingsApprovalDefault = "auto";
settingsShouldFail = false;
await openFreshCreateEditor();
ok(button("YOLO")?.classList.contains("set-seg__btn--on") === true, "global auto lands the new draft on yolo");
ok(document.body.textContent?.includes("demoted to YOLO") === true, "editor explains the auto demotion");
await fillRequiredFieldsAndSave();
ok(savedUpdate?.tasks?.[0]?.approvalMode === "yolo", "persisted draft keeps the demoted yolo approval");

console.log("\ntl 1142 manual create keeps ask when the global default is ask");

settingsApprovalDefault = "ask";
await openFreshCreateEditor();
ok(button("Ask")?.classList.contains("set-seg__btn--on") === true, "global ask lands the new draft on ask");
ok(document.body.textContent?.includes("demoted to YOLO") !== true, "no demotion hint when the default is not auto");
await fillRequiredFieldsAndSave();
ok(savedUpdate?.tasks?.[0]?.approvalMode === "ask", "persisted draft keeps ask");

console.log("\ntl 1142 manual create keeps yolo when the global default is yolo");

settingsApprovalDefault = "yolo";
await openFreshCreateEditor();
ok(button("YOLO")?.classList.contains("set-seg__btn--on") === true, "global yolo lands the new draft on yolo");
ok(document.body.textContent?.includes("demoted to YOLO") !== true, "no demotion hint when following yolo directly");
await fillRequiredFieldsAndSave();
ok(savedUpdate?.tasks?.[0]?.approvalMode === "yolo", "persisted draft keeps yolo");

console.log("\ntl 1142 settings read failure falls back to ask");

settingsApprovalDefault = "auto";
settingsShouldFail = true;
await openFreshCreateEditor();
ok(button("Ask")?.classList.contains("set-seg__btn--on") === true, "unreadable global default falls back to ask");
await fillRequiredFieldsAndSave();
ok(savedUpdate?.tasks?.[0]?.approvalMode === "ask", "persisted draft keeps the ask fallback");

console.log("\ntl 1142 recommendation keeps the ask safety default");

settingsApprovalDefault = "auto";
settingsShouldFail = false;
await openFreshCreateEditor();
await act(async () => {
  document.querySelector<HTMLButtonElement>(".heartbeat-suggestion")?.click();
  await flush();
  await flush();
});
ok(button("Ask")?.classList.contains("set-seg__btn--on") === true, "recommendation stays on ask even with a global auto default");
ok(document.body.textContent?.includes("demoted to YOLO") !== true, "recommendation shows no demotion hint");
await fillRequiredFieldsAndSave();
ok(savedUpdate?.tasks?.some((task) => task.approvalMode === "ask" && task.enabled === false) === true, "recommendation persists the safe ask default");

console.log(`\ntl 1142 heartbeat approval default: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
