// Task 342: the CDP debug port lab entry must survive a settings save — the
// render table is a fixed key set and a missing entry silently drops the save
// (81/123 lost-save lesson). This harness pins the frontend half of the
// contract: the feature id exists, the entry light reads the switch, and the
// setter call shape matches the backend binding.
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
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const goBindings = fs.readFileSync(
  path.join(frontendRoot, "wailsjs/go/main/App.d.ts"),
  "utf8",
);
const settingsApp = fs.readFileSync(
  path.join(repoRoot, "desktop/settings_app.go"),
  "utf8",
);
const render = fs.readFileSync(
  path.join(repoRoot, "internal/config/render.go"),
  "utf8",
);

// 1. Render table: task 561 M5 folds the entry into the devDebug card; the
// family light still reads the boot-snapshot switch.
ok(
  panel.includes('{ id: "devDebug", group: "dev-debug"'),
  "lab rail hosts the devDebug family entry in the dev-debug group",
);
ok(
  panel.includes("on: Boolean(s.experimentalCDPDebugPort) || Boolean(s.experimentalLifecycleNoiseGate)"),
  "devDebug entry light reads s.experimentalCDPDebugPort",
);

// 2. The devDebug detail card wires the toggle to the backend setter and
// raises the restart banner (the WebView2 env is created once per process).
ok(
  panel.includes('selected === "devDebug" && ('),
  "detail card renders for the devDebug family",
);
ok(
  panel.includes("app.SetExperimentalCDPDebugPort(on)") &&
    panel.includes("setRestartNeeded(true)"),
  "toggle persists through SetExperimentalCDPDebugPort and raises restartNeeded",
);

// 3. Bridge contract: declared, stubbed, and mirrored by the Wails binding.
ok(
  bridge.includes("SetExperimentalCDPDebugPort(enabled: boolean): Promise<void>;"),
  "bridge declares SetExperimentalCDPDebugPort",
);
ok(
  goBindings.includes("SetExperimentalCDPDebugPort"),
  "wailsjs bindings expose SetExperimentalCDPDebugPort",
);
ok(
  settingsApp.includes('ExperimentalCDPDebugPort bool `json:"experimentalCDPDebugPort"`'),
  "backend views carry experimentalCDPDebugPort",
);

// 4. Persistence chain: the config renderer lists the key (fixed key set —
// an unlisted key is dropped on save) and the Go setter exists.
ok(
  render.includes('experimental_cdp_debug_port = %v'),
  "config render table lists experimental_cdp_debug_port",
);
ok(
  fs
    .readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8")
    .includes("func (c *Config) SetExperimentalCDPDebugPort(enabled bool) error"),
  "config edit.go defines SetExperimentalCDPDebugPort",
);
ok(
  view.includes("experimentalCDPDebugPort?: boolean;"),
  "SettingsView type carries experimentalCDPDebugPort",
);

process.stdout.write(`\n${passed} passed\n`);
if (process.exitCode) process.exit(1);
