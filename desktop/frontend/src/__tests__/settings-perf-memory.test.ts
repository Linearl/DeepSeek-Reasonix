// Run: tsx src/__tests__/settings-perf-memory.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const section = readFileSync(resolve(testDir, "../components/PerfMemorySection.tsx"), "utf8");
const settings = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const backend = readFileSync(resolve(testDir, "../../../perf_read_app.go"), "utf8");
const perfMonitor = readFileSync(resolve(testDir, "../../../perf_monitor_test.go"), "utf8");
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

console.log("\ntask 338 memory observability contract");

// Bridge contract: three methods, four views, mocks with honest empty states.
const bridgeMethods = [
  "PerfTimeSeries(windowMinutes: number): Promise<PerfTimeSeriesView>;",
  "SampleHeapBreakdown(): Promise<HeapBreakdownView>;",
  "HeapBreakdownPath(): Promise<string>;",
];
ok(bridgeMethods.every((m) => bridge.includes(m)), "AppBindings declares the three task-338 methods");
ok(
  bridge.includes("export interface PerfTimeSeriesView") &&
    bridge.includes("export interface HeapBreakdownView") &&
    bridge.includes("export interface PerfPoint") &&
    bridge.includes("export interface HeapCategory"),
  "Series/breakdown/point/category views are exported for the panel",
);
ok(
  bridge.includes("async PerfTimeSeries()") && bridge.includes("async SampleHeapBreakdown()") && bridge.includes("async HeapBreakdownPath()"),
  "Mocks exist with empty-state shapes",
);

// Mount point: monitoring detail card, after the heap export button.
ok(settings.includes("import { PerfMemorySection } from \"./PerfMemorySection\""), "SettingsPanel imports the section");
ok(
  settings.includes("<PerfMemorySection busy={busy} apply={apply} />") &&
    settings.indexOf("<PerfMemorySection") > settings.indexOf('selected === "monitoring"') &&
    settings.indexOf("<PerfMemorySection") < settings.indexOf('selected === "sessionStore"'),
  "Section mounts inside the monitoring detail card",
);
ok(settings.includes("await app.SaveHeapProfile();"), "Original export-only heap button is preserved");

// Section behavior markers.
ok(section.includes("] as const;") && section.includes("settings.perfMonitor.window.48h"), "Window selector carries the four 1h..48h steps as const");
ok(section.includes("app.PerfTimeSeries(minutes)") || section.includes("app.PerfTimeSeries(windowMinutes)"), "Chart reads the series API");
ok(section.includes("await app.SampleHeapBreakdown()"), "Sample button runs the breakdown API");
ok(section.includes("app.HeapBreakdownPath()"), "Export path is surfaced on first load");
ok(section.includes("function wsPolyline") && section.includes("<polyline"), "WS chart renders an SVG polyline");
ok(section.includes("function pieArc") && section.includes("<path key={category.key}"), "Heap pie renders SVG arcs per category");
ok(
  ['var(--accent)', 'var(--ok)', 'var(--warn)', 'var(--danger)', 'var(--fg-dim)'].every((token) => section.includes(token)),
  "Pie palette uses only existing theme tokens",
);
ok(section.includes("settings.perfMonitor.chartNoSamples") && section.includes("settings.perfMonitor.chartSamplerOff"), "Empty and sampler-off states carry guidance keys");
ok(section.includes("type HeapCatKey") && section.includes("as HeapCatKey"), "Category locale lookup stays a typed union");

// Backend: read-only core + on-demand sampling, no writes on the read path.
ok(
  backend.includes("func readPerfSamples(") && backend.includes("func (a *App) PerfTimeSeries("),
  "WS series API exists with an injectable reader",
);
ok(backend.includes("func (a *App) SampleHeapBreakdown(") && backend.includes("profile.ParseData"), "Heap breakdown samples then parses pprof");
ok(
  !backend.includes("os.WriteFile") && !backend.includes("os.Create") && !backend.includes("os.MkdirAll"),
  "Read core performs no writes (zero-overhead contract)",
);
ok(backend.includes("github.com/google/pprof/profile"), "pprof parser dependency is wired");
ok(perfMonitor.includes("TestPerfMonitorNotStartedWritesNothing") || perfMonitor.includes("Closed = zero cost"), "182 closed-contract test text still present");

// Locales: every key present in all three languages.
const requiredKeys = [
  "settings.perfMonitor.chartTitle",
  "settings.perfMonitor.chartRefresh",
  "settings.perfMonitor.chartSamplerOff",
  "settings.perfMonitor.chartNoSamples",
  "settings.perfMonitor.window.1h",
  "settings.perfMonitor.window.6h",
  "settings.perfMonitor.window.24h",
  "settings.perfMonitor.window.48h",
  "settings.perfMonitor.heapPieTitle",
  "settings.perfHeap.sampleAndChart",
  "settings.perfHeap.sampleFailed",
  "settings.perfHeap.noData",
  "settings.perfHeap.exported",
  "settings.perfHeap.cat.transcript",
  "settings.perfHeap.cat.snapshot",
  "settings.perfHeap.cat.dag",
  "settings.perfHeap.cat.frontendCache",
  "settings.perfHeap.cat.other",
];
ok(
  locales.every((locale) => requiredKeys.every((key) => locale.includes(`"${key}"`))),
  "zh, zh-TW and en each define all 18 task-338 keys",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
