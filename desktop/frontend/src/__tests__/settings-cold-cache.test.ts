// Run: npx tsx src/__tests__/settings-cold-cache.test.ts
// Task 297: the cold-cache compact card ships the full chain — render-table
// entry on the lab storage group, detail union id, the detail card writing
// through the three setters, the effective-value view fields, and all three
// locales. Text assertions on the source, the house pattern for render tables.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const viewTypes = readFileSync(resolve(testDir, "../lib/settingsViewTypes.ts"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const zh = readFileSync(resolve(testDir, "../locales/zh.ts"), "utf8");
const en = readFileSync(resolve(testDir, "../locales/en.ts"), "utf8");
const zhTW = readFileSync(resolve(testDir, "../locales/zh-TW.ts"), "utf8");

let passed = 0;
function ok(condition: unknown, label: string) {
  assert.ok(condition, label);
  passed++;
}

// Render table: mounted on the lab STORAGE group (the "cost optimization"
// home per the task body), not scattered elsewhere.
ok(
  panel.includes('{ id: "coldCacheCompact", group: "storage",') &&
    panel.includes("Boolean(s.experimentalColdCacheCompact)"),
  "render table carries the entry in the storage group",
);
ok(panel.includes('| "coldCacheCompact"'), "the detail-card union includes the id");

// Detail card: switch + two knobs, each writing through its own setter.
ok(panel.includes("app.SetExperimentalColdCacheCompact(on)"), "switch writes through its setter");
ok(panel.includes("app.SetColdCacheCompactMinBytes(kb * 1024)"), "size floor converts KB -> bytes through its setter");
ok(panel.includes("app.SetColdCacheCompactIdleMinutes(h * 60)"), "idle knob converts hours -> minutes through its setter");

// The card lives inside the cache-tuning-adjacent detail area (mounted next
// to proactiveCompact, its same-domain neighbour).
ok(
  panel.indexOf('selected === "coldCacheCompact"') > panel.indexOf('selected === "proactiveCompact"'),
  "detail card mounts after the proactive-compact card",
);

// View fields (effective-value readout) + bridge surface.
ok(viewTypes.includes("experimentalColdCacheCompact?: boolean;"), "view type declares the switch");
ok(viewTypes.includes("coldCacheCompactMinBytes?: number;"), "view type declares the size floor");
ok(viewTypes.includes("coldCacheCompactIdleMinutes?: number;"), "view type declares the idle floor");
ok(bridge.includes("SetExperimentalColdCacheCompact(enabled: boolean)"), "bridge declares the switch setter");
ok(bridge.includes("SetColdCacheCompactMinBytes(bytes: number)"), "bridge declares the size setter");
ok(bridge.includes("SetColdCacheCompactIdleMinutes(minutes: number)"), "bridge declares the idle setter");

// Three locales carry every key the card renders.
const keys = [
  "settings.coldCacheCompact",
  "settings.coldCacheCompactHint",
  "settings.coldCacheCompact.on",
  "settings.coldCacheCompact.off",
  "settings.coldCacheCompact.minBytes",
  "settings.coldCacheCompact.minBytesHint",
  "settings.coldCacheCompact.idleHours",
  "settings.coldCacheCompact.idleHoursHint",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}

console.log(`${passed} passed, 0 failed`);
