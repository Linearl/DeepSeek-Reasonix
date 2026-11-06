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
// 任务724/1134:布局树在 lab/labLayoutDefault、union 在 lab/labPaneRegistry、
// 卡行在 ExperimentalLabSection(拼接读源,1134 判例)。
const panel = [
  "../components/SettingsPanel.tsx",
  "../components/ExperimentalLabSection.tsx",
  "../lab/labLayoutDefault.ts",
  "../lab/labLightKeys.ts",
  "../lab/labPaneRegistry.ts",
].map((rel) => readFileSync(resolve(testDir, rel), "utf8")).join("\n");
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

// Render table: task 427 merged the cold-cache entry into the compress-opt
// card; task 561 M3 folds that card into contextGovernance (efficiency) —
// the family light still reads the switch. 任务724 后布局数据驱动:条目在
// labLayoutDefault(efficiency 组),灯读数在 labLightKeys 谓词表。
ok(
  panel.includes('key: "efficiency"') &&
    panel.includes('id: "contextGovernance"') &&
    panel.includes('"experimentalColdCacheCompact"') &&
    panel.includes("experimentalColdCacheCompact: (s: SettingsView) => Boolean(s.experimentalColdCacheCompact)"),
  "render table carries the contextGovernance family entry in the efficiency group",
);
ok(panel.includes('| "contextGovernance"'), "the detail-card union includes the family id");
ok(!panel.includes('| "compressOpt"'), "the standalone compressOpt union id is gone (task 561 M3)");

// Detail card: switch + two knobs, each writing through its own setter.
ok(panel.includes("app.SetExperimentalColdCacheCompact(on)"), "switch writes through its setter");
ok(panel.includes("app.SetColdCacheCompactMinBytes(kb * 1024)"), "size floor converts KB -> bytes through its setter");
ok(panel.includes("app.SetColdCacheCompactIdleMinutes(h * 60)"), "idle knob converts hours -> minutes through its setter");

// Task 427: both switches live inside ONE merged detail card; within the card
// the proactive-compact block mounts before the cold-cache block (same order
// as the former standalone cards).
{
  const start = panel.indexOf('{selected === "contextGovernance" && (');
  const next = panel.indexOf('{selected === "devDebug" && (');
  ok(start >= 0 && next > start, "merged context-governance card located in the detail area");
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
