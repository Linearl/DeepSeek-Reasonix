// Task 199 round 2: user's 1255 repro — pick a NEW provider/model in the
// managed task editor's model-override row and the save footer must flip to
// "unsaved" with an enabled Save button (round 1 fixed only the store layer;
// this pins the editor-level onChange -> dirty -> footer chain end to end).
import assert from "node:assert/strict";
import { managementDom } from "../test-support/managementDom";
import type { HeartbeatTask } from "../custom/features/heartbeat/heartbeat.types";
const dom = managementDom();
const { default: React, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { HeartbeatView } = await import("../custom/features/heartbeat/HeartbeatPanel");
const { useAutomationDraftStore: store } = await import("../store/automationDrafts");

let tasks: HeartbeatTask[] = [
  { id: "a", title: "Iterative inspection", prompt: "Feedback to draft", interval: "1h", enabled: true, createdAt: 1 },
];
Object.assign(window, { go: { main: { App: {
  async HeartbeatReloadConfig() { return { revision: 1, etag: "a", tasks }; },
  async HeartbeatSaveConfig(value: { tasks: HeartbeatTask[] }) {
    tasks = value.tasks; return { revision: 2, etag: "b", tasks };
  },
  async ListWorkspaces() { return []; },
  async HeartbeatGenerateID() { return "draft-new"; },
  async Settings() {
    return { providers: [
      { name: "mimo-api", models: ["mimo-v2.6-flash"] },
      { name: "deepseek-api", models: ["deepseek-chat"] },
    ] };
  },
} } } });

const root = createRoot(document.body.appendChild(document.createElement("div")));
const render = () => <LocaleProvider><HeartbeatView active={true} /></LocaleProvider>;
const button = (text: string) => Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((node) => node.textContent?.trim() === text)!;
const status = () => document.querySelector(".automation-save-status")?.textContent ?? "";
const overrideSelects = () => Array.from(document.querySelectorAll<HTMLSelectElement>(".heartbeat-editor__model-override select"));
try {
  await act(async () => { root.render(render()); });
  await act(async () => { button("Iterative inspection").click(); });
  // Task opened: baseline has no provider/model, footer must start clean.
  assert.equal(button("Save").disabled, true, "opened task starts clean");
  assert.ok(status().includes("已保存") || status().includes("Saved"), `starts as saved (got "${status()}"`);

  const providerSelect = overrideSelects()[0];
  assert.ok(providerSelect, "provider select exists");
  // Pick a NEW provider — the user's exact action.
  await act(async () => {
    providerSelect.value = "deepseek-api";
    providerSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  });
  assert.ok(status().toLowerCase().includes("unsaved") || status().includes("未保存"),
    `picking a provider must flip the footer to unsaved (got "${status()}"`);
  assert.equal(button("Save").disabled, false, "Save must enable after picking a new provider");

  // Pick a model too, then save; value must land in the persisted task.
  const modelSelect = overrideSelects()[1];
  assert.ok(modelSelect, "model select appears once provider chosen");
  await act(async () => {
    modelSelect.value = "deepseek-chat";
    modelSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  });
  assert.equal(store.getState().entries.a.draft.provider, "deepseek-api", "draft carries the picked provider");
  assert.equal(store.getState().entries.a.draft.model, "deepseek-chat", "draft carries the picked model");
  await act(async () => { button("Save").click(); });
  await act(async () => { await Promise.resolve(); });
  assert.equal(tasks[0].provider, "deepseek-api", "provider persisted after save");
  assert.equal(tasks[0].model, "deepseek-chat", "model persisted after save");
  assert.ok(status().includes("已保存") || status().includes("Saved"), `footer returns to saved (got "${status()}"`);
  console.log("PASS task 199 round 2: provider/model pick dirties the footer, Save enables, values persist");
} finally {
  await act(async () => { root.unmount(); });
  dom.window.close();
}
