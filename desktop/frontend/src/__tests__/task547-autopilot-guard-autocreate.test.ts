// Run: tsx src/__tests__/task547-autopilot-guard-autocreate.test.ts
//
// Task 547 — guard auto-creation becomes an opt-in sub-option of the autopilot
// experiment feature (default off). The switch has to travel the whole chain:
// config key → setter → render table → SettingsView → bridge → panel control →
// three locales. The renderer and the view are the two places a preference has
// historically vanished (an unlisted key is dropped on save and the control
// flips itself back), so each hop is asserted from the source rather than
// assumed. The off state must keep the rendered config byte-identical: the key
// is emitted only while the opt-in is on.

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
const guardEngine = read(resolve(repoRoot, "desktop/autopilot_guard.go"));

const key = "experimental_autopilot_guard_autocreate";
const field = "experimentalAutopilotGuardAutocreate";
const setter = "SetDesktopAutopilotGuardAutocreate";

// 1. Config layer: the field exists, the reader fails closed, and the fixed
//    key set emits it — but only while the opt-in is on, so an untouched
//    config stays byte-identical (off = no line at all).
assert.ok(configFields.includes(`toml:"${key}"`), `config declares ${key}`);
assert.ok(configAccessors.includes("func (c *Config) AutopilotGuardAutocreateEnabled()"), "reader exists");
assert.ok(configAccessors.includes("func (c *Config) SetExperimentalAutopilotGuardAutocreate("), "config setter exists");
assert.ok(renderTable.includes(`if c.Desktop.ExperimentalAutopilotGuardAutocreate {`), "render table emits the key only while on");
assert.ok(renderTable.includes(key), "render table knows the key");

// 2. Creation gate: the App-side ensure entry and the engine-side sweep both
//    read the sub-option, the edge becomes a no-op while off, and the sweep
//    converges legacy guards (disabled, logged) instead of leaving them armed.
assert.ok(guardEngine.includes("func (a *App) autopilotGuardAutocreate() bool"), "app-side read exists");
assert.ok(guardEngine.includes("func (e *HeartbeatEngine) autopilotGuardAutocreate() bool"), "engine-side read exists");
assert.ok(guardEngine.includes("if !a.autopilotGuardAutocreate() {"), "ensure entry is gated (off = do not create)");
assert.ok(guardEngine.includes("guard auto-creation sub-option is off (task 547)"), "sweep logs the opt-out convergence");
assert.ok(guardEngine.includes("if !autocreate {"), "sweep gates the create half");

// 3. App setter: persists through the config-only path and reconciles right
//    away, so flipping the switch never waits up to a tick to take effect.
assert.ok(settingsApp.includes(`func (a *App) ${setter}(enabled bool) error`), "app setter exists");
assert.ok(settingsApp.includes("a.heartbeat.ReconcileAutopilotGuards()"), "app setter reconciles immediately");

// 4. SettingsView carries it, and the panel control is wired to the setter —
//    with the guard dials disabled while the opt-in is off (they are
//    meaningless without it, same rule as the task-477 wait dial).
assert.ok(viewTypes.includes(field), `SettingsView declares ${field}`);
assert.ok(panel.includes(`app.${setter}(`), "panel writes the opt-in through its setter");
assert.ok(panel.includes('t("settings.autopilotGuardAutocreate")'), "opt-in control is labeled from the dictionary");
assert.ok(panel.includes("busy || !Boolean(s.experimentalAutopilotGuardAutocreate)"), "guard dials disable while the opt-in is off");

// 5. Bridge: the binding and the browser-dev mock agree with Go's signature.
assert.ok(bridge.includes(`${setter}(enabled: boolean): Promise<void>`), "bridge binding takes a boolean");
assert.ok(bridge.includes(`async ${setter}(enabled: boolean)`), "mock implements the setter");
assert.ok(bridge.includes("experimentalAutopilotGuardAutocreate: false"), "mock defaults to off");

// 6. Three locales, four keys each — a missing translation would fall back to
//    the raw key on screen.
const locales = ["en.ts", "zh.ts", "zh-TW.ts"].map((name) => ({
  name,
  text: read(resolve(frontendRoot, "src/locales", name)),
}));
const keys = [
  "settings.autopilotGuardAutocreate",
  "settings.autopilotGuardAutocreateHint",
  "settings.autopilotGuardAutocreate.on",
  "settings.autopilotGuardAutocreate.off",
];
for (const locale of locales) {
  for (const k of keys) {
    assert.ok(locale.text.includes(`"${k}"`), `${locale.name} declares ${k}`);
  }
}

console.log("task547-autopilot-guard-autocreate: all chain assertions passed");
