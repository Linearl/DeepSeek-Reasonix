// Run: npx tsx src/__tests__/settings-orphan-handling.test.ts
// Task 449: the two task-244 orphan switches (B5 lease reclaim + B4 recovery
// sweep) merge into ONE lab entry backed by ONE config key. This harness pins
// the whole chain: render-table entry, detail union id, the detail card writing
// through the merged setter, the effective-value view field, the bridge surface
// (merged setter declared; both pre-449 setters still declared because the
// generated bindings export them), all three locales, and — the actual point of
// the merge — that the two old entries and their locale keys are GONE so the
// lab panel shows exactly one switch.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const viewTypes = readFileSync(resolve(testDir, "../lib/settingsViewTypes.ts"), "utf8");
const startupTypes = readFileSync(resolve(testDir, "../lib/types.ts"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const en = readFileSync(resolve(testDir, "../locales/en.ts"), "utf8");
const zh = readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8");
const zhTW = readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8");

let passed = 0;
function ok(condition: unknown, label: string) {
  assert.ok(condition, label);
  passed++;
}

// Render table: exactly one misc-group entry, reading the merged view field.
ok(
  panel.includes('{ id: "orphanHandling", group: "misc",') &&
    panel.includes("Boolean(s.experimentalOrphanHandling)"),
  "render table carries the single merged entry in the misc group",
);
ok(panel.includes('| "orphanHandling"'), "the detail-card union includes the merged id");
ok(
  !panel.includes('"orphanLeaseReclaim"') && !panel.includes('"recoveryOrphanSweep"'),
  "the two pre-449 entries are gone from the render table (lab shows one switch)",
);

// Detail card: one card, writing through the merged setter.
ok(panel.includes("app.SetExperimentalOrphanHandling(on)"), "merged card writes through the merged setter");
ok(
  !panel.includes("app.SetExperimentalOrphanLeaseReclaim(") &&
    !panel.includes("app.SetExperimentalRecoveryOrphanSweep("),
  "the merged card does not call the pre-449 setters",
);
ok(
  panel.indexOf('selected === "orphanHandling"') >= 0 &&
    !panel.includes('selected === "orphanLeaseReclaim"') &&
    !panel.includes('selected === "recoveryOrphanSweep"'),
  "exactly one orphan detail card mounts",
);

// View fields (effective-value readout) on both view types + bridge surface.
ok(viewTypes.includes("experimentalOrphanHandling?: boolean;"), "SettingsView declares the merged switch");
ok(
  !viewTypes.includes("experimentalOrphanLeaseReclaim") && !viewTypes.includes("experimentalRecoveryOrphanSweep"),
  "SettingsView dropped the pre-449 fields",
);
ok(startupTypes.includes("experimentalOrphanHandling?: boolean;"), "DesktopStartupSettingsView declares the merged switch");
ok(
  !startupTypes.includes("experimentalOrphanLeaseReclaim") && !startupTypes.includes("experimentalRecoveryOrphanSweep"),
  "DesktopStartupSettingsView dropped the pre-449 fields",
);
ok(bridge.includes("SetExperimentalOrphanHandling(enabled: boolean)"), "bridge declares the merged setter");
// The generated bindings still export both pre-449 setters, so AppBindings must
// keep declaring them or the compile-time drift check fails (generated ⊆ App).
ok(
  bridge.includes("SetExperimentalOrphanLeaseReclaim(enabled: boolean)") &&
    bridge.includes("SetExperimentalRecoveryOrphanSweep(enabled: boolean)"),
  "bridge keeps the pre-449 setter declarations for the generated-binding drift check",
);

// Three locales ship the merged keys — and no longer ship the pre-449 keys.
const keys = [
  "settings.orphanHandling",
  "settings.orphanHandlingHint",
  "settings.orphanHandling.on",
  "settings.orphanHandling.off",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}
for (const stale of [
  "settings.orphanLeaseReclaim",
  "settings.orphanLeaseReclaimHint",
  "settings.recoveryOrphanSweep",
  "settings.recoveryOrphanSweepHint",
]) {
  ok(!en.includes(`"${stale}"`), `en dropped ${stale}`);
  ok(!zh.includes(`"${stale}"`), `zh dropped ${stale}`);
  ok(!zhTW.includes(`"${stale}"`), `zh-TW dropped ${stale}`);
}

console.log(`${passed} passed, 0 failed`);
