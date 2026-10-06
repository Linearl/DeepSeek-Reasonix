// Run: tsx src/__tests__/settings-heap-high-threshold.test.ts
// Task 528: the heap-high trigger threshold gets a real settings-panel input.
// Contract test over the full chain (config setter dual-write → render mirror
// row → view readback → App method → bridge → panel input → three locales).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const settings = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const settingsViewTypes = readFileSync(resolve(testDir, "../lib/settingsViewTypes.ts"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const configEdit = readFileSync(resolve(testDir, "../../../../internal/config/edit.go"), "utf8");
const configRender = readFileSync(resolve(testDir, "../../../../internal/config/render.go"), "utf8");
const settingsPrefs = readFileSync(resolve(testDir, "../../../settings_preferences.go"), "utf8");
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

console.log("\ntask 528 heap-high threshold UI contract");

// Config chain: the setter dual-writes [agent] + [desktop] mirror, and the
// render table lists the mirror (a mirror the renderer drops is a silent
// save-loss — the task 81/123 lesson).
ok(configEdit.includes("c.Desktop.PerfMonitorHeapHighThresholdMB = mb") && configEdit.includes("c.Agent.PerfMonitorHeapHighThresholdMB = mb"), "config setter dual-writes agent + desktop mirror");
ok(configRender.includes("perf_monitor_heap_high_threshold_mb = %d   # desktop: settings-view mirror"), "render table lists the desktop mirror row");

// Desktop chain: view readback helper + Wails App method exist.
ok(settingsPrefs.includes("func perfMonitorHeapHighThresholdForView"), "view readback helper exists (mirror wins, [agent] fallback)");
ok(settingsPrefs.includes("func (a *App) SetPerfMonitorHeapHighThresholdMB(mb int) error"), "Wails App setter exists");

// Frontend chain: bridge declaration + mock, view type, panel input.
ok(bridge.includes("SetPerfMonitorHeapHighThresholdMB(mb: number): Promise<void>;"), "AppBindings declares the task-528 method");
ok(bridge.includes("async SetPerfMonitorHeapHighThresholdMB() {}"), "Mock exists for the dev shell");
ok(settingsViewTypes.includes("perfMonitorHeapHighThresholdMB?: number;"), "Settings view type carries the readback field");
ok(settings.includes("const [perfHeapHighThreshold, setPerfHeapHighThreshold] = useState<number>(s.perfMonitorHeapHighThresholdMB ?? 6144)"), "Panel state starts at the built-in 6GB default");
ok(settings.includes("await app.SetPerfMonitorHeapHighThresholdMB(perfHeapHighThreshold)"), "Panel input saves through the App setter");
ok(settings.includes("disabled={busy || !Boolean(s.experimentalHeapHighProfile)}"), "Input is disabled while the 501 switch is off (zero behaviour when off)");
ok(settings.includes("min={1024}") && settings.includes("max={131072}"), "Input carries the clamped range 1024..131072");

// Locales: both new keys in all three languages, and the misleading
// "adjustable TOML key" phrasing is gone from the switch hint.
ok(
  locales.every((locale) => locale.includes('"settings.perfMonitor.heapHighThreshold"') && locale.includes('"settings.perfMonitor.heapHighThresholdHint"')),
  "zh, zh-TW and en each define the threshold label + hint",
);
ok(
  locales.every((locale) => !locale.includes("perf_monitor_heap_high_threshold_mb 可调") && !locale.includes("perf_monitor_heap_high_threshold_mb 可調")),
  "No locale hint still points at hand-editing the TOML key",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
