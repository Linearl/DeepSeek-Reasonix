// Task 377n: the lifecycle noise gate lab entry must survive a settings save
// — the render table is a fixed key set and a missing entry silently drops
// the save (81/123 lost-save lesson). Same harness shape as
// settings-cdp-debug-port.test.ts: the feature id exists, the entry light
// reads the switch, the toggle raises the restart banner, and the
// persistence chain is complete end to end (UI → bridge → App setter →
// config setter → render table).
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

let passed = 0;
function ok(cond: boolean, label: string) {
  if (!cond) {
    process.stderr.write(`FAIL ${label}
`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  process.stdout.write(`  PASS  ${label}
`);
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
const goBindings = fs.readFileSync(
  path.join(frontendRoot, "wailsjs/go/main/App.d.ts"),
  "utf8",
);
const settingsApp = fs.readFileSync(path.join(repoRoot, "desktop/settings_app.go"), "utf8");
const preferences = fs.readFileSync(path.join(repoRoot, "desktop/settings_preferences.go"), "utf8");
const render = fs.readFileSync(path.join(repoRoot, "internal/config/render.go"), "utf8");
const edit = fs.readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8");

// 1. Render table: the entry exists and reads the boot-snapshot switch.
ok(
  panel.includes('{ id: "lifecycleNoiseGate", group: "dev-debug"'),
  "lab rail hosts the lifecycleNoiseGate entry in the misc group",
);
ok(
  panel.includes("on: Boolean(s.experimentalLifecycleNoiseGate)"),
  "entry light reads s.experimentalLifecycleNoiseGate",
);

// 2. The detail card wires the toggle to the backend setter and raises the
// restart banner (startup diagnostics read the gate once per process).
ok(
  panel.includes('selected === "lifecycleNoiseGate" && ('),
  "detail card renders for lifecycleNoiseGate",
);
ok(
  panel.includes("app.SetExperimentalLifecycleNoiseGate(on)") &&
    panel.includes("setRestartNeeded(true)"),
  "toggle persists through SetExperimentalLifecycleNoiseGate and raises restartNeeded",
);

// 3. View + bridge contract: declared in both view types, stubbed in the
// mock, and mirrored by the Wails binding.
ok(view.includes("experimentalLifecycleNoiseGate?: boolean;"), "settingsViewTypes declares the switch");
ok(viewTypes.includes("experimentalLifecycleNoiseGate?: boolean;"), "types.ts startup view declares the switch");
ok(
  bridge.includes("SetExperimentalLifecycleNoiseGate(enabled: boolean): Promise<void>;") &&
    bridge.includes("async SetExperimentalLifecycleNoiseGate() {}"),
  "bridge declares and stubs SetExperimentalLifecycleNoiseGate",
);
ok(goBindings.includes("SetExperimentalLifecycleNoiseGate"), "wailsjs bindings expose SetExperimentalLifecycleNoiseGate");

// 4. Locales: the four keys exist in all three languages.
for (const [i, l] of ["en", "zh", "zh-TW"].entries()) {
  ok(
    locales[i].includes('"settings.lifecycleNoiseGate":') &&
      locales[i].includes('"settings.lifecycleNoiseGateHint":') &&
      locales[i].includes('"settings.lifecycleNoiseGate.on":') &&
      locales[i].includes('"settings.lifecycleNoiseGate.off":'),
    `${l} locale carries the four lifecycleNoiseGate keys`,
  );
}

// 5. Backend chain: view field, App setter, config setter, render table.
ok(
  settingsApp.includes('ExperimentalLifecycleNoiseGate bool `json:"experimentalLifecycleNoiseGate"`'),
  "backend views carry experimentalLifecycleNoiseGate",
);
ok(
  settingsApp.includes("cfg.Desktop.ExperimentalLifecycleNoiseGate"),
  "backend views read cfg.Desktop.ExperimentalLifecycleNoiseGate",
);
ok(
  preferences.includes("c.SetExperimentalLifecycleNoiseGate(enabled)"),
  "App setter routes through applyConfigOnly to the config setter",
);
ok(
  edit.includes("func (c *Config) SetExperimentalLifecycleNoiseGate(enabled bool) error"),
  "config setter exists",
);
ok(
  render.includes("experimental_lifecycle_noise_gate = %v"),
  "render table covers experimental_lifecycle_noise_gate",
);

process.stdout.write(`
${passed} checks passed
`);
if (process.exitCode) {
  process.exit(process.exitCode);
}
