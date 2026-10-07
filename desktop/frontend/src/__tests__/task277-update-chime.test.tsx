// Task 277 acceptance (update-complete chime):
//  1. one-shot gate — first launch after a version swap plays once; later
//     launches of the same version never replay;
//  2. switch off ⇒ zero playback (and the seen version is still recorded, so
//     flipping the switch on later never replays a skipped version);
//  3. fail-closed — no storage / storage write failure / no active version ⇒
//     no playback (never a replay);
//  4. wiring guards — config field + setters + render table (2 settings views,
//     the 81/123 lesson) + lab switch + bridge surface + bundle path.
//
// System-mute note: playUpdateChime rides the same AudioContext → default
// output device path as the existing notification chimes, so a Windows system
// mute silences it exactly like every other app sound (no exclusive device).
//
// Run: npx tsx src/__tests__/task277-update-chime.test.tsx

import { maybePlayUpdateChime, UPDATE_CHIME_LAST_VERSION_KEY, type UpdateChimeOutcome } from "../lib/sound";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function fakeStorage(seed: Record<string, string> = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => { map.set(key, value); },
    dump: () => Object.fromEntries(map),
  };
}

const versions = (...entries: Array<[string, boolean]>) => async () =>
  entries.map(([version, active]) => ({ version, active }));

async function runCase(
  label: string,
  options: Parameters<typeof maybePlayUpdateChime>[0],
  expected: UpdateChimeOutcome,
  expectPlayed: boolean,
) {
  let played = 0;
  const outcome = await maybePlayUpdateChime({ ...options, play: () => { played += 1; } });
  ok(outcome === expected, `${label}: outcome=${outcome} (want ${expected})`);
  ok(expectPlayed ? played === 1 : played === 0, `${label}: ${expectPlayed ? "played once" : "zero playback"}`);
}

// 1. enabled + version swap → plays once.
{
  const storage = fakeStorage();
  await runCase("swap+enabled", { enabled: true, listVersions: versions(["v1.38.3", false], ["v1.38.4", true]), storage }, "played", true);
  ok(storage.dump()[UPDATE_CHIME_LAST_VERSION_KEY] === "v1.38.4", "swap+enabled: last-chimed version recorded");
}

// 2. same version on the next launch → never replays.
{
  const storage = fakeStorage({ [UPDATE_CHIME_LAST_VERSION_KEY]: "v1.38.4" });
  await runCase("relaunch+enabled", { enabled: true, listVersions: versions(["v1.38.4", true]), storage }, "already-chimed", false);
}

// 3. switch off on the swap launch → zero playback, but the version is recorded
//    so turning the switch on later never replays it (no surprise sound).
{
  const storage = fakeStorage();
  await runCase("swap+disabled", { enabled: false, listVersions: versions(["v1.38.4", true]), storage }, "disabled", false);
  ok(storage.dump()[UPDATE_CHIME_LAST_VERSION_KEY] === "v1.38.4", "swap+disabled: version still recorded (no later replay)");

  const nowEnabled = fakeStorage(storage.dump());
  await runCase("later-enabled", { enabled: true, listVersions: versions(["v1.38.4", true]), storage: nowEnabled }, "already-chimed", false);
}

// 4. no active version → no playback.
{
  await runCase("no-active", { enabled: true, listVersions: versions(["v1.38.4", false]), storage: fakeStorage() }, "no-active-version", false);
}

// 5. listVersions failure → no playback (fail closed).
{
  await runCase("list-fails", { enabled: true, listVersions: async () => { throw new Error("boom"); }, storage: fakeStorage() }, "no-active-version", false);
}

// 6. storage write failure → no playback (fail closed, never replays).
{
  const storage = {
    getItem: () => null,
    setItem: () => { throw new Error("quota"); },
  };
  await runCase("storage-write-fails", { enabled: true, listVersions: versions(["v1.38.4", true]), storage }, "no-storage", false);
}

// 7. no storage at all → no playback.
{
  await runCase("no-storage", { enabled: true, listVersions: versions(["v1.38.4", true]), storage: undefined }, "no-storage", false);
}

// ── wiring guards ────────────────────────────────────────────────────────────
{
  const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");

  const app = read("../App.tsx");
  ok(app.includes("maybePlayUpdateChime({"), "App startup gate wired");
  ok(app.includes("view.updateChime"), "App reads the config switch");
  ok(app.includes("app.ListInstalledVersions()"), "App uses the existing version detection");

  const sound = read("../lib/sound.ts");
  // Task 512 moved the update chime onto the dedicated nokia wav (1.25×, cut)
  // — the same AudioContext path remains (guard below).
  ok(sound.includes('"./sounds/nokia-tune.wav"'), "chime uses the bundled nokia wav (task 512 tune dial)");
  ok(sound.includes("new AudioContext()"), "same system-output audio path as notification chimes (system mute applies)");

  const panel = read("../components/SettingsPanel.tsx");
  ok(panel.includes("app.SetUpdateChime(on)"), "lab switch calls SetUpdateChime");
  // Task 561 M6: the update family folded into the updateFeedback card.
  ok(panel.includes('selected === "updateFeedback"'), "switch lives in the updateFeedback family card");

  const bridge = read("../lib/bridge.ts");
  ok(bridge.includes("SetUpdateChime(enabled: boolean)"), "bridge interface carries SetUpdateChime");
  ok(bridge.includes("async SetUpdateChime() {}"), "bridge mock carries SetUpdateChime");

  const config = read("../../../../internal/config/desktop_preferences.go");
  ok(config.includes('toml:"update_chime"'), "config field persisted");
  const edit = read("../../../../internal/config/edit.go");
  ok(edit.includes("func (c *Config) SetUpdateChime"), "config setter exists");
  const prefs = read("../../../settings_preferences.go");
  ok(prefs.includes("func (a *App) SetUpdateChime"), "App setter exists");

  const settingsApp = read("../../../settings_app.go");
  // \s+ (not a literal single space): gofmt re-aligns the struct as siblings
  // land, which had silently broken this counter into a pre-existing red.
  const viewFields = (settingsApp.match(/UpdateChime\s+bool\s+`json:"updateChime"`/g) ?? []).length;
  const viewAssigns = (settingsApp.match(/UpdateChime:?\s*=?\s*(cfg\.Desktop\.UpdateChime|view\.UpdateChime = cfg\.Desktop\.UpdateChime)/g) ?? []).length;
  ok(viewFields === 2, `render table: 2 view structs carry UpdateChime (got ${viewFields}) — 81/123 lesson`);
  ok(viewAssigns === 2, `render table: 2 assignments wired (got ${viewAssigns})`);

  for (const dialect of ["zh", "en", "zh-TW"]) {
    const locale = read(`../locales/${dialect}.ts`);
    ok(locale.includes('"settings.updateChime"'), `locale ${dialect}: switch label key`);
    ok(locale.includes('"settings.updateChimeHint"') && locale.includes('"settings.updateChime.on"') && locale.includes('"settings.updateChime.off"'), `locale ${dialect}: hint/on/off keys`);
  }
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
