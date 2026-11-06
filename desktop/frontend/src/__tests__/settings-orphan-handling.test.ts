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
// 任务724/1134/tl1309卡③:orphanHandling 并入 infra 组 resourceGovernance 合并卡
// (成员行,union 在 labPaneRegistry、布局行在 labLayoutDefault、卡行在
// ExperimentalLabSection);拼接读源,「单一入口」语义不变。
const panel = [
  "../components/SettingsPanel.tsx",
  "../components/ExperimentalLabSection.tsx",
  "../lab/labLayoutDefault.ts",
  "../lab/labPaneRegistry.ts",
].map((rel) => readFileSync(resolve(testDir, rel), "utf8")).join("\n");
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

// Render table: exactly one entry for the merged switch, reading the merged
// view field. tl1309 卡③(d53ad7b29)后为 resourceGovernance 合并卡成员行:
// 布局条目 id=resourceGovernance、onKeys 含 experimentalOrphanHandling、
// 成员行 id=orphanHandling(infra 组)。
ok(
  panel.includes('id: "resourceGovernance"') &&
    panel.includes('{ id: "orphanHandling", labelKey: "settings.orphanHandling", tier: "recommended" }') &&
    panel.includes("Boolean(s.experimentalOrphanHandling)"),
  "render table carries the single merged entry in the infra resourceGovernance card",
);
// tl1309 卡③:独立 pane 退役,union id 同步摘除(961 monitoring 同款先例,
// labPaneRegistry 注释即规格);成员行承载(上方正钉)保证入口仍单一见可。
ok(!panel.includes('| "orphanHandling"'), "the standalone detail-card union id is gone (tl1309 卡③)");
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
// tl1309 卡③后独立 pane 分支退役(并入 resourceGovernance 卡,成员行承载):
// 「恰好一张孤儿卡」的保证改钉为分支不存在+卡行恰一处(上方 setter 钉)。
ok(
  !panel.includes('selected === "orphanHandling"') &&
    !panel.includes('selected === "orphanLeaseReclaim"') &&
    !panel.includes('selected === "recoveryOrphanSweep"'),
  "exactly one orphan card face: no standalone pane branches (merged member row only)",
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
