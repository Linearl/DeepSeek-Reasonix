// Task 439: the built-in zcode task bus lab entry must survive a settings
// save — the render table is a fixed key set and a missing entry silently
// drops the save (81/123 lost-save lesson). This harness pins the frontend
// half of the contract: the feature id exists, the entry light reads the
// switch, the setter call shape matches the backend binding, the status card
// reads the App binding, and the three locales all carry the card copy.
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
const settingsApp = fs.readFileSync(
  path.join(repoRoot, "desktop/settings_app.go"),
  "utf8",
);
const settingsPrefs = fs.readFileSync(
  path.join(repoRoot, "desktop/settings_preferences.go"),
  "utf8",
);
const host = fs.readFileSync(path.join(repoRoot, "desktop/zcode_task_bus.go"), "utf8");
const render = fs.readFileSync(
  path.join(repoRoot, "internal/config/render.go"),
  "utf8",
);

// 1. Render table: the entry exists and reads the boot-snapshot switch.
ok(
  panel.includes('{ id: "zcodeTaskBus", group: "infra"'),
  "lab rail hosts the zcodeTaskBus entry in the misc group",
);
ok(
  panel.includes("on: Boolean(s.experimentalZcodeTaskBus)"),
  "entry light reads s.experimentalZcodeTaskBus",
);
ok(
  panel.includes('| "zcodeTaskBus"'),
  "ExperimentFeatureId union carries zcodeTaskBus",
);

// 2. The detail card wires the toggle to the backend setter, raises the
// restart banner (the listener arms at boot), and renders the status +
// enrolled roles from the App binding (serve 状态灯 + roles 可视化).
ok(
  panel.includes('selected === "zcodeTaskBus" && ('),
  "detail card renders for zcodeTaskBus",
);
ok(
  panel.includes("app.SetExperimentalZcodeTaskBus(on)") &&
    panel.includes("setRestartNeeded(true)"),
  "toggle persists through SetExperimentalZcodeTaskBus and raises restartNeeded",
);
ok(
  panel.includes("app.ZcodeTaskBusStatus()"),
  "card loads live status through ZcodeTaskBusStatus",
);
ok(
  panel.includes("zcodeBusStatus.roles.map") && panel.includes("zcode-"),
  "card visualizes the enrolled roles as zcode- contacts",
);

// 3. Bridge contract: declared, stubbed.
ok(
  bridge.includes("SetExperimentalZcodeTaskBus(enabled: boolean): Promise<void>;"),
  "bridge declares SetExperimentalZcodeTaskBus",
);
ok(
  bridge.includes("ZcodeTaskBusStatus(): Promise<ZcodeTaskBusStatusView>;"),
  "bridge declares ZcodeTaskBusStatus",
);
ok(
  bridge.includes("async SetExperimentalZcodeTaskBus() {}"),
  "browser mock stubs SetExperimentalZcodeTaskBus",
);

// 4. Status view type carries the exact wire keys the Go status map emits.
for (const key of ["enabled", "running", "addr", "endpoint", "roles", "err"]) {
  ok(
    new RegExp(`ZcodeTaskBusStatusView[\\s\\S]*?\\b${key}:`).test(view),
    `ZcodeTaskBusStatusView declares ${key}`,
  );
}

// 5. Backend face: both settings views carry the flag, the App wrapper and
// the config setter exist, and the host file arms nothing when the flag is
// off (zero-behaviour gate is inside startZcodeTaskBus).
ok(
  settingsApp.includes('ExperimentalZcodeTaskBus bool `json:"experimentalZcodeTaskBus"`'),
  "backend views carry experimentalZcodeTaskBus",
);
ok(
  settingsPrefs.includes("func (a *App) SetExperimentalZcodeTaskBus(enabled bool) error"),
  "App exposes SetExperimentalZcodeTaskBus",
);
ok(
  host.includes("!cfg.Desktop.ExperimentalZcodeTaskBus {\n\t\treturn\n\t}"),
  "flag-off path returns before any network work",
);
ok(
  host.includes("127.0.0.1:8787"),
  "default endpoint is the fixed loopback 8787",
);

// 6. Persistence chain: the config renderer lists the key (fixed key set).
ok(
  render.includes("experimental_zcode_task_bus = %v"),
  "config render table lists experimental_zcode_task_bus",
);

// 7. Locales: the card copy exists in all three languages.
for (const locale of ["zh", "en", "zh-TW"]) {
  const data = fs.readFileSync(path.join(frontendRoot, `src/locales/${locale}.ts`), "utf8");
  ok(
    data.includes('"settings.zcodeTaskBus":') &&
      data.includes('"settings.zcodeTaskBusHint":') &&
      data.includes('"settings.zcodeTaskBus.roles":'),
    `locale ${locale} carries the zcodeTaskBus card copy`,
  );
}

// 8. Wails bindings: the generated surface exposes both methods. The file is
// generated locally (gitignored); when absent (fresh checkout) the compile-time
// drift check is disabled too, so skip instead of failing on the environment.
const wailsDts = path.join(frontendRoot, "wailsjs/go/main/App.d.ts");
if (fs.existsSync(wailsDts)) {
  const bindings = fs.readFileSync(wailsDts, "utf8");
  ok(
    bindings.includes("SetExperimentalZcodeTaskBus") && bindings.includes("ZcodeTaskBusStatus"),
    "wailsjs bindings expose SetExperimentalZcodeTaskBus + ZcodeTaskBusStatus",
  );
} else {
  process.stdout.write("  SKIP  wailsjs bindings not generated in this checkout\n");
}

process.stdout.write(`\n${passed} passed\n`);
if (process.exitCode) process.exit(1);
