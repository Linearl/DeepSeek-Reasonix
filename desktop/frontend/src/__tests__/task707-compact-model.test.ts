// Run: tsx src/__tests__/task707-compact-model.test.ts
// 任务 707: the economic-compaction switch + target must survive a settings
// save and reach the runtime. This harness pins the frontend half of the
// contract — lab render table (contextGovernance card, below compressOpt),
// model-preference companion row (fallbackModel precedent), bridge shape,
// tier registration (optional, mirrored on both sides), types, and the
// three-locale key set.
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
const panel = fs.readFileSync(path.join(frontendRoot, "src/components/SettingsPanel.tsx"), "utf8");
const view = fs.readFileSync(path.join(frontendRoot, "src/lib/settingsViewTypes.ts"), "utf8");
const types = fs.readFileSync(path.join(frontendRoot, "src/lib/types.ts"), "utf8");
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const tiers = fs.readFileSync(path.join(frontendRoot, "src/lib/experimentTiers.ts"), "utf8");
const zh = fs.readFileSync(path.join(frontendRoot, "src/locales/zh.ts"), "utf8");
const en = fs.readFileSync(path.join(frontendRoot, "src/locales/en.ts"), "utf8");
const zhTw = fs.readFileSync(path.join(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const settingsApp = fs.readFileSync(path.join(repoRoot, "desktop/settings_app.go"), "utf8");
const settingsPrefs = fs.readFileSync(path.join(repoRoot, "desktop/settings_preferences.go"), "utf8");
const goConfig = fs.readFileSync(path.join(repoRoot, "internal/config/config.go"), "utf8");
const goEdit = fs.readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8");
const goRender = fs.readFileSync(path.join(repoRoot, "internal/config/render.go"), "utf8");
const goSnapshot = fs.readFileSync(path.join(repoRoot, "internal/config/model_runtime_snapshot.go"), "utf8");
const goBoot = fs.readFileSync(path.join(repoRoot, "internal/boot/boot.go"), "utf8");
const goAgentModel = fs.readFileSync(path.join(repoRoot, "internal/agent/compact_model.go"), "utf8");
const goAgentCompact = fs.readFileSync(path.join(repoRoot, "internal/agent/compact.go"), "utf8");

console.log("\n任务 707 compact model: persistence + render + runtime contract");

// 1. Lab surface: the switch lives in the contextGovernance card (compressOpt
//    block, below the proactive-compact cooldown), wraps a tier label, and
//    lights the card when on.
ok(
  panel.includes('labLabel("compactModel", t("settings.compactModel"))') &&
    panel.includes("app.SetExperimentalCompactModel(on)"),
  "contextGovernance card renders the compactModel switch through its setter",
);
ok(
  /coldCacheCompact\.idleHours[\s\S]{0,1200}labLabel\("compactModel"/.test(panel),
  "the switch sits below the proactive-compact cooldown block (任务书「下方」)",
);
ok(
  panel.includes('Boolean(s.experimentalCompactModel) || Boolean(s.experimentalCacheTuning)'),
  "contextGovernance card lights when the compact-model switch is on",
);
ok(
  panel.includes("!Boolean(s.experimentalCompactModel)") && panel.includes('t("settings.compactModel.inactiveHint")'),
  "off state shows the inactive hint (关=压缩仍走对话模型)",
);

// 2. Model-preference companion row: only while the switch is on, persists
//    through SetCompactModel (fallbackModel row precedent).
ok(
  panel.includes("Boolean(s.experimentalCompactModel) && (") &&
    panel.includes("await app.SetCompactModel(ref)") &&
    panel.includes('t("settings.compactModelPreference.none")'),
  "model preferences host the compression-model row (fallbackModel companion shape)",
);

// 3. Bridge contract: declared + dev-mock stubbed (real calls proxy to the
//    wailsjs runtime binding; the gitignored bindings regenerate at build).
ok(
  bridge.includes("SetExperimentalCompactModel(enabled: boolean): Promise<void>;") &&
    bridge.includes("async SetExperimentalCompactModel() {}") &&
    bridge.includes("SetCompactModel(model: string): Promise<void>;") &&
    bridge.includes("async SetCompactModel() {}"),
  "bridge declares and stubs both setters",
);

// 4. Go chain: config keys (default false), setters, render lines, both
//    settings views, view mapping, and the App-level setters.
ok(goConfig.includes('ExperimentalCompactModel bool `toml:"experimental_compact_model"`'), "config key experimental_compact_model (default false)");
ok(goConfig.includes('CompactModel string `toml:"compact_model"`'), "config key compact_model");
ok(goEdit.includes("func (c *Config) SetExperimentalCompactModel(enabled bool) error") && goEdit.includes("func (c *Config) SetCompactModel(model string) error") && goEdit.includes("func (c *Config) CompactModelLive() string"), "config setters + CompactModelLive gate");
ok(goRender.includes("experimental_compact_model = %v") && goRender.includes(`compact_model = %q`), "render.go emits both keys (fixed-key-set rule)");
ok(
  (settingsApp.match(/ExperimentalCompactModel bool\s+`json:"experimentalCompactModel"`/g) ?? []).length === 2,
  "both settings views carry experimentalCompactModel (81/123 both-views lesson)",
);
ok(
  (settingsApp.match(/CompactModel\s+string `json:"compactModel"`/g) ?? []).length === 2 &&
    settingsApp.includes("CompactModel:             cfg.Agent.CompactModel"),
  "both settings views carry compactModel and map the config value",
);
ok(settingsPrefs.includes("func (a *App) SetExperimentalCompactModel(enabled bool) error") && settingsPrefs.includes("func (a *App) SetCompactModel(model string) error"), "App setters persist switch + target");

// 5. Runtime reach: the boot assembly arms the destination switch-gated, and
//    the fingerprint carries both fields so a change re-applies at the next
//    run (model-preference family contract).
ok(goBoot.includes("CompactModel:       compactModelDestinationFromConfig(cfg)") && goBoot.includes("CompactModelPricing: compactModelPricingFromConfig(cfg)"), "boot arms CompactModel + pricing");
ok(goSnapshot.includes("c.Agent.ExperimentalCompactModel, c.Agent.CompactModel"), "model runtime fingerprint covers the switch + target");
ok(goAgentModel.includes("func (a *Agent) compactionDestination()"), "agent resolves the compaction destination");
ok(goAgentCompact.includes("summaryDest := a.compactionDestination()") && goAgentCompact.includes("ModelRef: summaryDest.ref"), "runSummaryRequest routes dest + usage attribution");

// 6. Tier registration: optional, mirrored on both sides, counts moved
//    19→20 / 48→49; the rail card covers it as a merged member.
ok(tiers.includes('| "compactModel"') && tiers.includes('compactModel: "optional"'), "frontend tier registry: compactModel = optional");
ok(goRender.includes('{"compactModel", LabTierOptional, []string{"experimental_compact_model", "compact_model"}}'), "Go labFeatureTiers: compactModel = optional with both render keys");
ok(tiers.includes("optional: 20,"), "LAB_TIER_COUNTS optional 19→20 (总数 48→49)");
ok(tiers.includes('"compressOpt", "compactModel", "cacheTuning"'), "contextGovernance rail card lists compactModel as a member");

// 7. Locales: all three languages carry the full key set.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTw]] as const) {
  const keys = [
    '"settings.compactModel"',
    '"settings.compactModelHint"',
    '"settings.compactModel.on"',
    '"settings.compactModel.off"',
    '"settings.compactModel.inactiveHint"',
    '"settings.compactModelPreference"',
    '"settings.compactModelPreferenceHelp"',
    '"settings.compactModelPreference.none"',
  ];
  const missing = keys.filter((k) => !dict.includes(k));
  ok(missing.length === 0, `${name} locale carries all eight keys (missing: ${JSON.stringify(missing)})`);
}

// 8. Frontend types mirror the settings view.
ok(
  types.includes("experimentalCompactModel?: boolean") && types.includes("compactModel?: string") &&
    view.includes("experimentalCompactModel?: boolean") && view.includes("compactModel?: string"),
  "SettingsView types expose the switch + target",
);

process.stdout.write(`\n${passed}/${passed} passed\n`);
if (process.exitCode) {
  process.exit(1);
}
process.stdout.write("task 707 contract complete\n");
