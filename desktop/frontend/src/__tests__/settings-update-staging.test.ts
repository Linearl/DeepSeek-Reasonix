// Run: npx tsx src/__tests__/settings-update-staging.test.ts
//
// Task 381: the fast-switch staging directory override — input + reset button
// in the restartUpdate card, wired through config → render → view → bridge.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const testDir = dirname(fileURLToPath(import.meta.url));
const root = join(testDir, "..");
const repoRoot = join(root, "..", "..", "..");
const panel = readFileSync(join(root, "components/SettingsPanel.tsx"), "utf8").replace(/\n\s*/g, " ");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8");
const viewTypes = readFileSync(join(root, "lib/settingsViewTypes.ts"), "utf8");
const goPrefs = readFileSync(join(repoRoot, "desktop", "settings_preferences.go"), "utf8");
const render = readFileSync(join(repoRoot, "internal", "config", "render.go"), "utf8");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

ok(panel.includes("app.SetStagingDir(e.target.value)"), "the staging input commits on blur through the bridge");
ok(panel.includes('app.SetStagingDir("")'), "the reset button clears the override back to the default");
ok(panel.includes("settings.stagingDirReset"), "the reset control carries its own label");
ok(viewTypes.includes("stagingDir?: string"), "SettingsView exposes stagingDir");
ok(bridge.includes("GetStagingDir(): Promise<string>") && bridge.includes("SetStagingDir(dir: string): Promise<void>"), "bridge exposes get/set for the staging override");
ok(goPrefs.includes("func (a *App) SetStagingDir(dir string) error") && goPrefs.includes("applyConfigOnly"), "the backend setter uses the light save path (no rebuild bounce)");
ok(render.includes("staging_dir = %q"), "the config renderer writes the override (empty stays byte-identical default)");
for (const [name, table] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  ok(table.includes('"settings.stagingDir"') && table.includes('"settings.stagingDirReset"'), `${name} ships the staging-dir label + reset pair`);
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
