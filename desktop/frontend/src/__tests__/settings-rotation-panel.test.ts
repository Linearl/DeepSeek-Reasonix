// Run: tsx src/__tests__/settings-rotation-panel.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/SessionEventsPanel.tsx"), "utf8");
const settings = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const viewTypes = readFileSync(resolve(testDir, "../lib/settingsViewTypes.ts"), "utf8");
const backend = readFileSync(resolve(testDir, "../../../session_events_app.go"), "utf8");
const prefs = readFileSync(resolve(testDir, "../../../settings_preferences.go"), "utf8");
const appGo = readFileSync(resolve(testDir, "../../../app.go"), "utf8");
const gateEvents = readFileSync(resolve(testDir, "../../../../internal/agent/session_events.go"), "utf8");
const rotationConfig = readFileSync(resolve(testDir, "../../../../internal/config/events_rotation.go"), "utf8");
const locales = ["zh.ts", "zh-TW.ts", "en.ts"].map((name) => readFileSync(resolve(testDir, `../locales/${name}`), "utf8"));

let passed = 0;
let failed = 0;

function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntask 333 rotation gate contract");

// Render table (81/123 lesson: a missing entry silently drops the save).
// Task 561 M7: the entry folds into the sessionStore family card (storage).
ok(
  /\{ id: "sessionStore", group: "storage", label: t\("settings\.sessionStore"\)/.test(settings),
  "Render table carries the sessionStore family entry in the storage group",
);
ok(settings.includes('| "sessionStore"'), "ExperimentFeatureId union exposes sessionStore");
ok(!settings.includes('| "eventsRotation"'), "union drops the standalone eventsRotation id (folded into sessionStore)");
// Task 345: the repair list must answer "which session, is it safe" before the
// click — display name with the path demoted to the hover title, a status
// badge, and a disabled repair on busy rows.
ok(panel.includes("title={entry.path}"), "Row demotes the raw path to the hover title");
ok(panel.includes("events-rotation-panel__status"), "Row renders a status badge");
ok(panel.includes("entry.busy ? \"settings.eventsRotation.card.statusBusy\" : \"settings.eventsRotation.card.statusIdle\""), "Status badge picks idle/busy copy by entry.busy");
// Task 356: manual mode is a deliberate action — busy rows stay clickable
// behind a confirm, while auto/off keep the pre-disable from task 345.
ok(
  panel.includes('mode === "manual" ? busy : busy || entry.busy'),
  "repair disabled splits by mode: manual drops the entry-busy gate, auto/off keep it",
);
ok(panel.includes("settings.eventsRotation.card.busyHint"), "auto/off busy rows keep the actionable hint");
ok(
  panel.includes("settings.eventsRotation.card.busyHintManual"),
  "manual busy rows carry their own risk hint",
);
ok(
  panel.includes('window.confirm(t("settings.eventsRotation.card.busyConfirm"))'),
  "manual clicks on an in-use session confirm the collision risk first",
);
ok(settings.includes('selected === "sessionStore"') && settings.includes("<SessionEventsPanel"), "sessionStore detail card mounts the rotation panel");
ok(settings.includes('import { SessionEventsPanel, type RotationMode }'), "SettingsPanel imports the panel with its literal mode type");

// Bridge contract: three views + two setters, declared and mocked.
const bridgeMethods = [
  "SetEventsAutoRotation(mode: string): Promise<void>;",
  "SetEventsRotation(factor: number, capMB: number): Promise<void>;",
  "SessionEventsInventory(): Promise<SessionEventsInventoryView>;",
  "CompactSessionEvents(path: string): Promise<SessionEventsCompactResult>;",
  "CompactAllSessionEvents(maxRounds: number): Promise<SessionEventsCompactResult[]>;",
];
ok(bridgeMethods.every((m) => bridge.includes(m)), "AppBindings declares all five task-333 methods");
ok(bridge.includes("export interface SessionEventsInventoryView") && bridge.includes("export interface SessionEventsCompactResult"), "Inventory/result views are exported for the panel");
ok(
  bridge.includes("async SetEventsAutoRotation(mode: string) { settings.eventsAutoRotation = mode; }"),
  "Mock writes the mode back to the shared settings view",
);

// Settings view plumbing (startup + main view + default view).
ok(viewTypes.includes("eventsAutoRotation?: string;") && viewTypes.includes("eventsRotationFactor?: number;") && viewTypes.includes("eventsRotationCapMB?: number;"), "SettingsView exposes all three fields");
ok(prefs.includes("func (a *App) SetEventsAutoRotation(mode string) error") && prefs.includes("func (a *App) SetEventsRotation(factor float64, capMB int64) error"), "Desktop setters exist for mode and thresholds");
ok(prefs.includes("a.pushEventsRotationSettings()"), "Both setters push into the agent save path immediately");
ok(appGo.includes("agent.SetEventsAutoRotation(") && appGo.includes("Task 333: forward the event-log rotation gate"), "Boot forwards the gate settings once");

// Backend actions.
ok(backend.includes("agent.CompactSessionFile("), "Compact API wraps the production engine entry");
ok(backend.includes("func (a *App) CompactAllSessionEvents(maxRounds int)") && backend.includes("maxRounds > 3"), "Batch repair runs a bounded multi-round loop (max 3)");
ok(backend.includes("agent.EventsLogAboveThreshold("), "Inventory over-limit marks share the gate's judgment source");
// Review round (2026-09-28) contracts:
ok(backend.includes("isSessionNotIdle(err)") && backend.includes("agent.SessionLeaseError"), "Busy skip uses the typed lease error, not a string match");
ok(backend.includes("return res, nil") && !backend.includes("return res, err"), "Per-row compact resolves with the result so Wails cannot discard skipped/error state");
ok(gateEvents.includes(`"path", path`) && gateEvents.includes("sessionEventLogOversized(path string"), "Off-mode WARN attributes the session path");
ok(rotationConfig.includes("EventsRotationCapMBMax") && rotationConfig.includes("capMB > EventsRotationCapMBMax"), "Cap is upper-bounded in config (capMB<<20 must not overflow)");
ok(panel.includes("max={1073741824}"), "Cap input mirrors the backend bound");
ok(prefs.includes("events rotation settings push skipped") && appGo.includes("events rotation gate not push"), "Failed gate pushes are logged, not silent");

// Panel behavior markers.
ok(panel.includes('const ROTATION_MODES = ["off", "manual", "auto"] as const'), "Panel offers the three-way gate");
ok(panel.includes("await app.SetEventsAutoRotation(candidate)"), "Seg buttons save the selected mode");
ok(panel.includes("await app.SetEventsRotation(Number(factor), Number(capMB))"), "Auto mode saves both thresholds");
ok(panel.includes("app.CompactAllSessionEvents(3)"), "Repair-all runs the bounded multi-round batch");
ok(panel.includes("overCount") && panel.includes("overLimit") && panel.includes("topThree"), "Statistic card reads over-count + top offenders");
ok(panel.includes("settings.eventsRotation.desc.${mode}") && panel.includes("settings.eventsRotation.mode.${candidate}"), "Mode copy resolves through the typed locale keys");

// Task 362: the LEFT-MENU entry mirrors the switch state itself (the 356
// card-button split is about busy, the menu must not gray out on
// manual/auto). The old gate treated "manual" as off AND let "off" light up —
// both wrong against the three-state mode.
ok(
  settings.includes('on: (s.sessionStorage ?? "v3_only") !== "v3_only" || (s.eventsAutoRotation ?? "manual") !== "off"'),
  "sessionStore entry light reads the rotation mode (manual/auto lit, off gray, task 362 shape)",
);
ok(
  !settings.includes('on: (s.eventsAutoRotation ?? "manual") !== "manual"'),
  "the old manual-as-off gate is gone",
);

// Locales: every key present in all three languages.
const requiredKeys = [
  "settings.eventsRotation",
  "settings.eventsRotationHint",
  "settings.eventsRotation.mode.off",
  "settings.eventsRotation.mode.manual",
  "settings.eventsRotation.mode.auto",
  "settings.eventsRotation.desc.off",
  "settings.eventsRotation.desc.manual",
  "settings.eventsRotation.desc.auto",
  "settings.eventsRotation.factor",
  "settings.eventsRotation.cap",
  "settings.eventsRotation.apply",
  "settings.eventsRotation.card.overCount",
  "settings.eventsRotation.card.refresh",
  "settings.eventsRotation.card.repairAll",
  "settings.eventsRotation.card.repair",
  "settings.eventsRotation.card.empty",
  "settings.eventsRotation.card.failed",
  "settings.eventsRotation.card.skipped",
  "settings.eventsRotation.card.failedRow",
  "settings.eventsRotation.card.doneRow",
  // Task 345: idle/busy badge + hint (repair-list state).
  "settings.eventsRotation.card.statusIdle",
  "settings.eventsRotation.card.statusBusy",
  "settings.eventsRotation.card.busyHint",
  // Task 356: manual-mode risk copy split off the shared hint.
  "settings.eventsRotation.card.busyHintManual",
  "settings.eventsRotation.card.busyConfirm",
];
ok(
  locales.every((locale) => requiredKeys.every((key) => locale.includes(`"${key}"`))),
  "zh, zh-TW and en each define all 25 task-333/345/356 keys",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
