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
ok(
  /\{ id: "eventsRotation", group: "storage", label: t\("settings\.eventsRotation"\)/.test(settings),
  "Render table carries the eventsRotation entry in the storage group",
);
ok(settings.includes('| "eventsRotation"'), "ExperimentFeatureId union exposes eventsRotation");
// Task 345: the repair list must answer "which session, is it safe" before the
// click — display name with the path demoted to the hover title, a status
// badge, and a disabled repair on busy rows.
ok(panel.includes("title={entry.path}"), "Row demotes the raw path to the hover title");
ok(panel.includes("events-rotation-panel__status"), "Row renders a status badge");
ok(panel.includes("entry.busy ? \"settings.eventsRotation.card.statusBusy\" : \"settings.eventsRotation.card.statusIdle\""), "Status badge picks idle/busy copy by entry.busy");
ok(panel.includes("disabled={busy || entry.busy}"), "Repair button is disabled for busy sessions");
ok(panel.includes("settings.eventsRotation.card.busyHint"), "Busy rows carry an actionable hint");
ok(settings.includes('selected === "eventsRotation"') && settings.includes("<SessionEventsPanel"), "Storage detail card mounts the panel");
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
ok(backend.includes(`strings.Contains(err.Error(), "not idle")`), "Busy sessions are reported as skipped, not silent");

// Panel behavior markers.
ok(panel.includes('const ROTATION_MODES = ["off", "manual", "auto"] as const'), "Panel offers the three-way gate");
ok(panel.includes("await app.SetEventsAutoRotation(candidate)"), "Seg buttons save the selected mode");
ok(panel.includes("await app.SetEventsRotation(Number(factor), Number(capMB))"), "Auto mode saves both thresholds");
ok(panel.includes("app.CompactAllSessionEvents(3)"), "Repair-all runs the bounded multi-round batch");
ok(panel.includes("overCount") && panel.includes("overLimit") && panel.includes("topThree"), "Statistic card reads over-count + top offenders");
ok(panel.includes("settings.eventsRotation.desc.${mode}") && panel.includes("settings.eventsRotation.mode.${candidate}"), "Mode copy resolves through the typed locale keys");

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
];
ok(
  locales.every((locale) => requiredKeys.every((key) => locale.includes(`"${key}"`))),
  "zh, zh-TW and en each define all 20 task-333 keys",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
