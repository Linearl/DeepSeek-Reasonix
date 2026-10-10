import assert from "node:assert/strict";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { SessionExperienceSettings } from "../components/SessionExperienceSettings";
import { LocaleProvider } from "../lib/i18n";
import { getSessionExperience } from "../lib/sessionExperience";
import { getToolGroupingEnabled } from "../lib/toolGroupingPreference";
import type { SettingsView } from "../lib/types";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent, IS_REACT_ACT_ENVIRONMENT: true });
let backend: SettingsView = { sessionExperience: "standard" } as SettingsView;
let release!: () => void;
let failed = false;
const writes: string[] = [];
Object.assign(window, { go: { main: { App: { SetSessionExperience: async (mode: string) => {
  writes.push(mode);
  await new Promise<void>(resolve => { release = resolve; });
  if (failed) throw new Error("write failed");
  backend = { ...backend, sessionExperience: mode as "deep" | "standard" };
} } } } });
let completion: Promise<boolean>;
let reload!: () => void;
function SettingsHost() {
  const [snapshot, setSnapshot] = useState(backend);
  const [busy, setBusy] = useState(false);
  reload = () => setSnapshot({ ...backend });
  // Exercise the component's shared apply/reload boundary, not a guessed rollback.
  const apply = (write: () => Promise<unknown>) => {
    setBusy(true);
    completion = (async () => {
      try { await write(); return true; } catch { return false; }
      finally { reload(); setBusy(false); }
    })();
    return completion;
  };
  return <SessionExperienceSettings snapshot={snapshot} busy={busy} apply={apply} />;
}
const root = createRoot(document.getElementById("root")!);
// The section hosts the three-mode session experience group plus the task-668
// tool-grouping toggle, so queries are scoped to each radiogroup's aria-label
// instead of every radio on the page. (The old page-wide `[role=radio]` query
// was written for a two-mode era and has been red since 简洁 landed.)
const radiosIn = (label: string) => [...document.querySelectorAll<HTMLButtonElement>(
  `[role=radiogroup][aria-label="${label}"] > [role=radio]`)];
const buttons = () => radiosIn("Session experience");
const groupingButtons = () => radiosIn("Message stream tool grouping");
try {
  await act(async () => root.render(<LocaleProvider><SettingsHost /></LocaleProvider>));
  assert.equal(buttons().length, 3, "concise/standard/deep all render as mode options");
  assert.equal(buttons()[1].getAttribute("aria-checked"), "true");
  await act(async () => buttons()[2].click());
  assert.equal(getSessionExperience(), "deep");
  assert.ok(buttons().every(button => button.disabled));
  await act(async () => { release(); await completion; });
  assert.equal(buttons()[2].getAttribute("aria-checked"), "true");

  // 任务 668：工具分组开关——纯前端偏好，无后端写入、无 busy 门控，即点即生效。
  assert.equal(groupingButtons().length, 2);
  assert.equal(getToolGroupingEnabled(), true, "empty storage defaults to grouping on (status quo)");
  assert.equal(groupingButtons()[1].getAttribute("aria-checked"), "true");
  await act(async () => groupingButtons()[0].click());
  assert.equal(getToolGroupingEnabled(), false, "clicking Off flips the store synchronously");
  assert.equal(localStorage.getItem("reasonix-tool-grouping"), "off");
  assert.equal(groupingButtons()[0].getAttribute("aria-checked"), "true");
  assert.ok(buttons().every(button => !button.disabled), "grouping toggle never gates the backend-backed mode buttons");
  await act(async () => groupingButtons()[1].click());
  assert.equal(getToolGroupingEnabled(), true);
  assert.equal(localStorage.getItem("reasonix-tool-grouping"), "on");

  failed = true;
  await act(async () => buttons()[0].click());
  assert.equal(getSessionExperience(), "concise");
  await act(async () => { release(); await completion; });
  assert.equal(getSessionExperience(), "deep", "failed write reloads even when backend returns the same previous value");
  assert.equal(buttons()[2].getAttribute("aria-checked"), "true");
  assert.deepEqual(writes, ["deep", "concise"]);

  backend = { ...backend, sessionExperience: undefined };
  await act(async () => reload());
  assert.equal(getSessionExperience(), "standard");
  assert.equal(buttons()[1].getAttribute("aria-checked"), "true");
  assert.equal(buttons()[0].tabIndex, 0, "all segment buttons remain keyboard reachable");
  assert.equal(buttons()[2].tabIndex, 0);
  console.log("session experience controls: success, failure snapshot, busy state, legacy backend, tool grouping toggle and keyboard reachability passed");
} finally { await act(async () => root.unmount()); dom.window.close(); }
