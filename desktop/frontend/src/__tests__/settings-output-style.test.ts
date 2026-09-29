// Task 385a: the lab 回答风格 entry must survive a settings save — the render
// table is a fixed key set and a missing entry silently drops the save
// (81/123 lost-save lesson) — and the selector must be wired through the
// whole chain: config gate → App setter → settings readback → panel render.
// The broken-file report is part of the contract: a style .md with a bad
// frontmatter surfaces as an issue banner, never a silent skip.
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
const read = (...parts: string[]) => fs.readFileSync(path.join(...parts), "utf8");

const panel = read(frontendRoot, "src/components/SettingsPanel.tsx");
const view = read(frontendRoot, "src/lib/settingsViewTypes.ts");
const types = read(frontendRoot, "src/lib/types.ts");
const bridge = read(frontendRoot, "src/lib/bridge.ts");
const settingsApp = read(repoRoot, "desktop/settings_app.go");
const outputStyleApp = read(repoRoot, "desktop/output_style_app.go");
const edit = read(repoRoot, "internal/config/edit.go");
const render = read(repoRoot, "internal/config/render.go");

// 1. Render table + rail entry: the gate is a config key that renders, and
// the rail light reads both the gate and the persisted style.
ok(
  panel.includes('{ id: "outputStyle", group: "efficiency"'),
  "lab rail hosts the outputStyle entry in the efficiency group",
);
ok(
  panel.includes('Boolean(s.experimentalOutputStyleUI) || (s.outputStyle ?? "") !== ""'),
  "entry light reads the gate and the persisted style",
);
ok(
  render.includes("experimental_output_style_ui = %v"),
  "config render table lists experimental_output_style_ui",
);

// 2. Detail card: gate toggle + selector wired to the backend, selector value
// round-trips through the reloaded settings view, and unloadable files render.
ok(
  panel.includes('selected === "outputStyle" && ('),
  "detail card renders for outputStyle",
);
ok(
  panel.includes("app.SetExperimentalOutputStyleUI(on)"),
  "gate toggle persists through SetExperimentalOutputStyleUI",
);
ok(
  panel.includes("app.SetOutputStyle(next)"),
  "selector persists through SetOutputStyle",
);
ok(
  panel.includes('value={s.outputStyle ?? ""}'),
  "selector value reads back s.outputStyle (settings roundtrip)",
);
ok(
  panel.includes("app.ListOutputStyles()"),
  "the selector payload loads from ListOutputStyles",
);
ok(
  panel.includes("outputStyles.issues.length > 0"),
  "unloadable style files render an issue banner (bad frontmatter visible)",
);

// 3. Bridge contract: declared, stubbed for the browser mock, and mirrored by
// the backend views and TS types.
ok(
  bridge.includes("SetExperimentalOutputStyleUI(enabled: boolean): Promise<void>;"),
  "bridge declares SetExperimentalOutputStyleUI",
);
ok(
  bridge.includes("SetOutputStyle(name: string): Promise<void>;"),
  "bridge declares SetOutputStyle",
);
ok(
  bridge.includes("ListOutputStyles(): Promise<OutputStyleListView>;"),
  "bridge declares ListOutputStyles",
);
ok(
  bridge.includes("async ListOutputStyles()"),
  "browser mock stubs ListOutputStyles",
);
ok(
  settingsApp.includes('json:"experimentalOutputStyleUI"') &&
    settingsApp.includes('json:"outputStyle"'),
  "backend views carry experimentalOutputStyleUI + outputStyle",
);
ok(
  view.includes("experimentalOutputStyleUI?: boolean;") &&
    view.includes("outputStyle?: string;"),
  "SettingsView carries both fields",
);
ok(
  view.includes("export interface OutputStyleListView"),
  "the selector payload type exists",
);
ok(
  types.includes("experimentalOutputStyleUI?: boolean;"),
  "DesktopStartupSettingsView mirrors the gate",
);

// 4. Backend behaviour: persistence + validation + issue surfacing.
ok(
  outputStyleApp.includes("func (a *App) SetOutputStyle(name string) error"),
  "App.SetOutputStyle exists",
);
ok(
  outputStyleApp.includes("outputstyle.Resolve"),
  "App.SetOutputStyle validates the name against outputstyle.List",
);
ok(
  outputStyleApp.includes("func (a *App) ListOutputStyles()"),
  "App.ListOutputStyles exists",
);
ok(
  outputStyleApp.includes("outputstyle.ListReport"),
  "listing surfaces unloadable files through ListReport",
);
ok(
  edit.includes("func (c *Config) SetOutputStyle(name string) error"),
  "config setter exists",
);
ok(
  edit.includes("func (c *Config) SetExperimentalOutputStyleUI(enabled bool) error"),
  "config gate setter exists",
);

// 5. Locale parity: all three languages carry the section keys (the bundle
// check only runs on build; this pins the key set meanwhile).
for (const loc of ["zh.ts", "en.ts", "zh-TW.ts"]) {
  const text = read(frontendRoot, "src/locales", loc);
  for (const key of [
    "settings.outputStyle",
    "settings.outputStyle.on",
    "settings.outputStyle.off",
    "settings.outputStyle.selector",
    "settings.outputStyle.default",
    "settings.outputStyle.issues",
  ]) {
    ok(text.includes(`"${key}"`), `${loc} carries ${key}`);
  }
}

assert.ok(passed >= 30, `expected at least 30 assertions, got ${passed}`);
process.stdout.write(`\n${passed} passed\n`);
if (process.exitCode) process.exit(1);
