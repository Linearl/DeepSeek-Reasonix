// S1 (design 2026-09-30 §7 R4): the experimental_base_process lab entry must
// survive a settings save — the render table is a fixed key set and a missing
// entry silently drops the save (81/123 lost-save lesson). Same harness shape
// as settings-cdp-debug-port.test.ts: the feature id exists, the entry light
// reads the switch, the toggle raises the restart banner, and the persistence
// chain is complete end to end.
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

let passed = 0;
function ok(cond: boolean, label: string) {
  if (!cond) {
    process.stderr.write(`FAIL ${label}\n`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  process.stdout.write(`  PASS  ${label}\n`);
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");
const repoRoot = path.resolve(frontendRoot, "..", "..");
const panel = fs.readFileSync(
  path.join(frontendRoot, "src/components/SettingsPanel.tsx"),
  "utf8",
);
const view = fs.readFileSync(path.join(frontendRoot, "src/lib/settingsViewTypes.ts"), "utf8");
const viewTypes = fs.readFileSync(path.join(frontendRoot, "src/lib/types.ts"), "utf8");
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const locales = ["en", "zh", "zh-TW"].map((l) =>
  fs.readFileSync(path.join(frontendRoot, `src/locales/${l}.ts`), "utf8"),
);
const settingsApp = fs.readFileSync(path.join(repoRoot, "desktop/settings_app.go"), "utf8");
const preferences = fs.readFileSync(path.join(repoRoot, "desktop/settings_preferences.go"), "utf8");
const render = fs.readFileSync(path.join(repoRoot, "internal/config/render.go"), "utf8");
const edit = fs.readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8");

// 1. Render table: the entry exists and reads the boot-snapshot switch.
ok(
  panel.includes('{ id: "baseProcess", group: "misc"'),
  "lab rail hosts the baseProcess entry in the misc group",
);
ok(
  panel.includes("on: Boolean(s.experimentalBaseProcess)"),
  "entry light reads s.experimentalBaseProcess",
);

// 2. The detail card wires the toggle to the backend setter and raises the
// restart banner (the base client section is built once per process).
ok(
  panel.includes('selected === "baseProcess" && ('),
  "detail card renders for baseProcess",
);
ok(
  panel.includes("app.SetExperimentalBaseProcess(on)") &&
    panel.includes("setRestartNeeded(true)"),
  "toggle persists through SetExperimentalBaseProcess and raises restartNeeded",
);

// 3. Bridge contract: declared in the interface and stubbed in the mock.
ok(
  bridge.includes("SetExperimentalBaseProcess(enabled: boolean): Promise<void>;"),
  "bridge declares SetExperimentalBaseProcess",
);
ok(
  bridge.includes("async SetExperimentalBaseProcess() {}"),
  "bridge mock stubs SetExperimentalBaseProcess",
);

// 4. Persistence chain: both settings views carry the flag (a view without
// the field silently reads off — task 81 lesson), the renderer lists the key
// (fixed key set), and the Go setters exist.
ok(
  settingsApp.includes('ExperimentalBaseProcess bool `json:"experimentalBaseProcess"`'),
  "backend views carry experimentalBaseProcess",
);
ok(
  preferences.includes("func (a *App) SetExperimentalBaseProcess(enabled bool) error"),
  "App.SetExperimentalBaseProcess exists",
);
ok(
  edit.includes("func (c *Config) SetExperimentalBaseProcess(enabled bool) error"),
  "config edit.go defines SetExperimentalBaseProcess",
);
ok(
  render.includes("experimental_base_process = %v"),
  "config render table lists experimental_base_process",
);
ok(
  view.includes("experimentalBaseProcess?: boolean;"),
  "SettingsView type carries experimentalBaseProcess",
);
ok(
  viewTypes.includes("experimentalBaseProcess?: boolean;"),
  "DesktopStartupSettingsView type carries experimentalBaseProcess",
);

// 5. Locales: all three languages carry the four keys (DictKey = keyof en,
// so a missing zh/zh-TW key is a type error — this is the runtime mirror).
for (const [i, l] of ["en", "zh", "zh-TW"].entries()) {
  for (const key of [
    "settings.baseProcess",
    "settings.baseProcessHint",
    "settings.baseProcess.on",
    "settings.baseProcess.off",
  ]) {
    ok(locales[i].includes(`"${key}"`), `${l} locale carries ${key}`);
  }
}

assert.ok(true);
process.stdout.write(`\n${passed} passed\n`);
if (process.exitCode) process.exit(1);
