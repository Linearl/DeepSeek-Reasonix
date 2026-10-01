// Run: tsx src/__tests__/task326-autopilot-guard-dials.test.ts
//
// Task 326 — the two autopilot guard dials have to travel the whole chain:
// config key → setter → render table → SettingsView → bridge → panel control →
// three locales. The renderer and the view are the two places a preference has
// historically vanished (an unlisted key is dropped on save and the control
// flips itself back), so each hop is asserted from the source rather than
// assumed.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(testDir, "../..");
const repoRoot = resolve(frontendRoot, "../..");

const read = (path: string) => readFileSync(path, "utf8");

const viewTypes = read(resolve(frontendRoot, "src/lib/settingsViewTypes.ts"));
const bridge = read(resolve(frontendRoot, "src/lib/bridge.ts"));
const panel = read(resolve(frontendRoot, "src/components/SettingsPanel.tsx"));
const configFields = read(resolve(repoRoot, "internal/config/desktop_preferences.go"));
const configAccessors = read(resolve(repoRoot, "internal/config/edit.go"));
const renderTable = read(resolve(repoRoot, "internal/config/render.go"));
const settingsApp = read(resolve(repoRoot, "desktop/settings_app.go"));

const dials = ["autopilotGuardInterval", "autopilotGuardQuiescent"] as const;
const setters = ["SetDesktopAutopilotGuardInterval", "SetDesktopAutopilotGuardQuiescent"] as const;

// 1. Config layer: the field exists, an accessor normalizes it, and the fixed
//    key set actually emits it (a key the renderer omits is silently dropped).
for (const key of ["autopilot_guard_interval", "autopilot_guard_quiescent"]) {
  assert.ok(configFields.includes(`toml:"${key}"`), `config declares ${key}`);
  assert.ok(renderTable.includes(key), `render table emits ${key}`);
}
assert.ok(configAccessors.includes("func (c *Config) AutopilotGuardIntervalMinutes()"), "interval accessor exists");
assert.ok(configAccessors.includes("func (c *Config) AutopilotGuardQuiescentPolicy()"), "policy accessor exists");

// 2. Setters: bounds are rejected rather than clamped into a schedule the user
//    did not ask for, and the policy enum is closed.
assert.ok(settingsApp.includes("func (a *App) SetDesktopAutopilotGuardInterval(minutes int) error"), "interval setter exists");
assert.ok(settingsApp.includes("func (a *App) SetDesktopAutopilotGuardQuiescent(policy string) error"), "policy setter exists");
assert.ok(settingsApp.includes('case "disable", "standby", "destroy":'), "policy enum is closed to the three documented gears");
assert.ok(settingsApp.includes("minutes < 1 || minutes > 1440"), "interval range is enforced");

// 3. SettingsView carries both, and the panel controls are wired to the setters.
for (const field of dials) {
  assert.ok(viewTypes.includes(field), `SettingsView declares ${field}`);
}
assert.ok(panel.includes("app.SetDesktopAutopilotGuardInterval("), "panel writes the interval dial through its setter");
assert.ok(panel.includes("app.SetDesktopAutopilotGuardQuiescent("), "panel writes the policy dial through its setter");
assert.ok(panel.includes('t("settings.autopilotGuardInterval")'), "interval control is labeled from the dictionary");
assert.ok(panel.includes('t("settings.autopilotGuardQuiescent")'), "policy control is labeled from the dictionary");

// 4. Bridge: the binding and the browser-dev mock agree with Go's signature.
for (const setter of setters) {
  assert.ok(bridge.includes(`${setter}(`), `bridge exposes ${setter}`);
}
assert.ok(bridge.includes("SetDesktopAutopilotGuardInterval(minutes: number)"), "interval binding takes a number");
assert.ok(bridge.includes("settings.autopilotGuardInterval"), "mock stores the interval");
assert.ok(bridge.includes("settings.autopilotGuardQuiescent"), "mock stores the policy");

// 5. Three locales, seven keys each — a missing translation would fall back to
//    the raw key on screen.
const locales = ["en.ts", "zh.ts", "zh-TW.ts"].map((name) => ({
  name,
  text: read(resolve(frontendRoot, "src/locales", name)),
}));
const keys = [
  "settings.autopilotGuardInterval",
  "settings.autopilotGuardIntervalHint",
  "settings.autopilotGuardQuiescent",
  "settings.autopilotGuardQuiescentHint",
  "settings.autopilotGuardQuiescent.disable",
  "settings.autopilotGuardQuiescent.standby",
  "settings.autopilotGuardQuiescent.destroy",
];
for (const locale of locales) {
  for (const key of keys) {
    assert.ok(locale.text.includes(`"${key}"`), `${locale.name} is missing ${key}`);
  }
}

// 6. The guard engine itself is on the other side of these dials.
const guard = read(resolve(repoRoot, "desktop/autopilot_guard.go"));
assert.ok(guard.includes("func autopilotGuardInterval(minutes int) string"), "engine turns the dial into an interval");
assert.ok(guard.includes('ApprovalMode:   "ask"') || guard.includes('"ask"'), "the guard records the strictest approval gear");
assert.ok(guard.includes("func (e *HeartbeatEngine) ReconcileAutopilotGuards()"), "the sweep exists");

console.log("  PASS  task 326 guard dials reach config, setter, renderer, view, bridge, panel and all three locales");
