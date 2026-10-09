// Run: tsx src/__tests__/task704-trajectory-view.test.ts
// 任务 704: the per-session surface-view store and the view-mount contract.
// The trajectory surface must (a) default to transcript with a per-tab
// persisted selection, (b) mount only through the transcript read projection
// (597 纪律: the view never reads storage), and (c) render the topicbar
// switch only while the lab flag is on and the session is local.
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import {
  getSurfaceView,
  resetSurfaceViewForTests,
  setSurfaceView,
  setSurfaceViewTab,
} from "../lib/trajectoryViewPreference";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");
const chatPane = fs.readFileSync(path.join(frontendRoot, "src/app-shell/ChatPaneRegion.tsx"), "utf8");
const runtimeView = fs.readFileSync(path.join(frontendRoot, "src/app-shell/AppRuntimeView.tsx"), "utf8");
const appRuntime = fs.readFileSync(path.join(frontendRoot, "src/AppRuntime.tsx"), "utf8");
const switchSrc = fs.readFileSync(path.join(frontendRoot, "src/app-shell/TopicbarSurfaceSwitch.tsx"), "utf8");
const trajView = fs.readFileSync(path.join(frontendRoot, "src/components/TrajectoryView.tsx"), "utf8");
const ledger = fs.readFileSync(path.join(frontendRoot, "src/lib/trajectoryLedger.ts"), "utf8");

console.log("\n任务 704 surface-view store + trajectory mount contract");

// Node has no localStorage; the store (like the app runtime) persists through
// the Web Storage API, so the harness stubs it with the same contract.
const storage = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (key: string) => storage.get(key) ?? null,
  setItem: (key: string, value: string) => void storage.set(key, value),
  removeItem: (key: string) => void storage.delete(key),
};


// ① Store: default transcript, per-tab persistence, restore on tab switch.
{
  resetSurfaceViewForTests();
  ok(getSurfaceView() === "transcript", "default view is transcript");
  setSurfaceViewTab("tab-a");
  ok(getSurfaceView() === "transcript", "fresh tab reads transcript");
  setSurfaceView("trajectory");
  ok(getSurfaceView() === "trajectory", "selection applies immediately");
  setSurfaceViewTab("tab-b");
  ok(getSurfaceView() === "transcript", "another tab starts on transcript");
  setSurfaceViewTab("tab-a");
  ok(getSurfaceView() === "trajectory", "switching back restores the persisted view (DSH 同款)");
  setSurfaceView("transcript");
  ok(getSurfaceView() === "transcript", "selection flips back");
  setSurfaceViewTab("");
  ok(getSurfaceView() === "transcript", "empty tab id resets to transcript");
}

// ② Persisted values normalize: unknown stored strings read transcript.
{
  resetSurfaceViewForTests();
  localStorage.setItem("reasonix-surface-view:tab-c", "bogus");
  setSurfaceViewTab("tab-c");
  ok(getSurfaceView() === "transcript", "unknown stored value normalizes to transcript");
  localStorage.removeItem("reasonix-surface-view:tab-c");
  // ① flipped tab-a back to transcript through the store — that write landed.
  ok(storage.get("reasonix-surface-view:tab-a") === "transcript", "store writes land in storage");
}

// ③ Mount contract: the slot lives in ChatPaneRegion inside the navigation
//    wrapper; the lazy import keeps the surface out of the startup bundle.
ok(chatPane.includes("const TrajectoryView = lazy("), "ChatPaneRegion lazy-loads the trajectory surface");
ok(chatPane.includes("transcript.trajectory ? <Suspense fallback={null}><TrajectoryView"), "the slot replaces the transcript inside the navigation wrapper");
ok(chatPane.includes("items={transcript.trajectory.items}") && chatPane.includes("running={transcript.trajectory.running}"),
  "the slot consumes the same transcript read projection");
ok(runtimeView.includes("trajectory.enabled && trajectory.surface === \"trajectory\" && !activeTab?.remote && !sidebarImDetailConnection"),
  "the bag gates on lab switch + view + local session");

// ④ Topicbar switch: gated mount, persisted-selection store, role=tablist.
ok(runtimeView.includes("{trajectory.enabled && Boolean(activeTabId) && !activeTab?.remote && !sidebarImDetailConnection && (") &&
  runtimeView.includes("<TopicbarSurfaceSwitch />"),
  "AppRuntimeView mounts the switch only for enabled local sessions");
ok(runtimeView.includes("import { TopicbarSurfaceSwitch } from \"./TopicbarSurfaceSwitch\""), "switch imported in the shell");
ok(switchSrc.includes("setSurfaceView(") && switchSrc.includes("useSyncExternalStore"), "switch writes the per-session store");
ok(appRuntime.includes("setSurfaceViewTab(activeTabId ?? \"\")") && appRuntime.includes("useSurfaceView()"),
  "AppRuntime owns the tab re-point + view subscription");
ok(appRuntime.includes("useState(labFlagEnabled(\"trajectoryView\"))") && appRuntime.includes("onLabFlagsChange"),
  "AppRuntime syncs the lab flag from the snapshot");
ok(appRuntime.includes("trajectory={{ enabled: trajectoryViewEnabled, surface: surfaceView }}"),
  "AppRuntime passes the gate + selection down");

// ⑤ 597 纪律: the view consumes the ledger projection and never reaches for
//    storage or the bridge directly.
ok(trajView.includes("buildTrajectoryLedger(items)"), "the view projects through trajectoryLedger");
ok(!trajView.includes("from \"../lib/bridge\""), "the view does not import the bridge");
ok(!trajView.includes("localStorage"), "the view does not read storage");
ok(!trajView.includes("readFileSync") && !trajView.includes("wailsjs"), "the view does not read session files");
ok(ledger.includes("import type { Item } from \"./useController\""), "the ledger consumes the Item read projection");

// ⑥ Default-off regression: with the flag off nothing mounts — the switch is
//    behind trajectory.enabled and the bag behind surface === trajectory.
ok(runtimeView.includes("trajectory: {") && runtimeView.includes("enabled: boolean"), "the gate travels as explicit props");
ok(chatPane.includes("trajectory?:"), "the slot is optional — transcript renders unchanged when absent");

// ⑦ Inspector (③): click-to-open local panel with honest degrade — timing
//    rows only when measured, usage note for assistant/tool records.
ok(trajView.includes("function TrajectoryInspector("), "the inspector lives in the lazy trajectory chunk");
ok(trajView.includes("role=\"dialog\"") && trajView.includes("trajectory.inspector.close"), "the inspector is a dismissible dialog");
ok(trajView.includes("trajectory.inspector.usageNA") && /record\.kind === "assistant" \|\| record\.kind === "tool"/.test(trajView),
  "the token-usage degrade note shows on assistant/tool records only");
ok(trajView.includes("record.ttftMs != null") && trajView.includes("record.durationMs != null"),
  "timing rows render only from measured fields (no invented numbers)");
ok(trajView.includes("inspectorSections(record, item, t)") || trajView.includes("inspectorSections("),
  "inspector sections derive from the record + its source item");
ok(trajView.includes("traj-row--selected") && trajView.includes("onSelect(record.id)"),
  "ledger rows select on click/Enter with a selected state");
ok(trajView.includes("item?.kind === \"tool\" ? item.args : undefined") || trajView.includes("item.args"),
  "tool input surfaces args in the inspector");

// ⑧ Timeline overview (②): fixed bar above the ledger, drag-select focuses
//    (out-of-range rows dim), click/right-click clears, running records draw
//    start markers only.
const timelineLib = fs.readFileSync(path.join(frontendRoot, "src/lib/trajectoryTimeline.ts"), "utf8");
ok(trajView.includes("<TrajectoryTimelineBar") && trajView.includes("buildTrajectoryTimeline(ledger.records)"),
  "the view mounts the timeline bar over the ledger");
ok(timelineLib.includes("running: boolean"),
  "timeline module exposes running on spans (marker rendering keys off it)");
ok(trajView.includes("pointerRangeToDomain") && trajView.includes("recordIdsInFocus"),
  "drag selection maps fractions onto the domain and focuses ledger records");
ok(trajView.includes("onFocus(null)"), "plain click / right-click clears the focus range (DSH 同款)");
ok(trajView.includes("traj-row--dimmed") && trajView.includes("dimmed={focusIds != null && !focusIds.has(record.id)}"),
  "out-of-focus ledger rows dim (anchor-less records never focus)");
ok(trajView.includes("traj-timeline__ttft") && trajView.includes("span.ttftFraction"),
  "assistant spans split TTFT/decode when live-only TTFT data exists");
ok(timelineLib.includes("PADDING_FRACTION") && timelineLib.includes("if (t1 <= t0) t1 = t0 + 1;"), "domain stays padded and non-degenerate");


console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("task 704 trajectory view mount contract: OK\n");
