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
// home per the task body), not scattered elsewhere. Task 427: the cold-cache
// entry merged into the shared compress-opt card (light reads either switch).
ok(
  panel.includes('{ id: "compressOpt", group: "efficiency",') &&
    panel.includes("Boolean(s.experimentalColdCacheCompact)"),
  "render table carries the merged entry in the storage group",
);
ok(panel.includes('| "compressOpt"'), "the detail-card union includes the merged id");

// Detail card: switch + two knobs, each writing through its own setter.
ok(panel.includes("app.SetExperimentalColdCacheCompact(on)"), "switch writes through its setter");
ok(panel.includes("app.SetColdCacheCompactMinBytes(kb * 1024)"), "size floor converts KB -> bytes through its setter");
ok(panel.includes("app.SetColdCacheCompactIdleMinutes(h * 60)"), "idle knob converts hours -> minutes through its setter");

// Task 427: both switches live inside ONE merged detail card; within the card
// the proactive-compact block mounts before the cold-cache block (same order
// as the former standalone cards).
{
  const start = panel.indexOf('{selected === "compressOpt" && (');
  const next = panel.indexOf('{selected === "compactionParallel" && (');
  ok(start >= 0 && next > start, "merged compress-opt card located in the detail area");
  const card = panel.slice(start, next);
  ok(
    card.indexOf('app.SetExperimentalProactiveCompact(') < card.indexOf('app.SetExperimentalColdCacheCompact('),
    "proactive-compact switch mounts before the cold-cache switch inside the merged card",
  );
  ok(card.includes('settings.coldCacheCompact') && card.includes('settings.proactiveCompact'), "both original label families render in the merged card");
}

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
