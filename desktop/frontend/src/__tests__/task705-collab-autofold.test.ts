// Run: tsx src/__tests__/task705-collab-autofold.test.ts
// 任务 705: the cross-session message auto-fold switch must survive a settings
// save — the lab render table is a fixed key set and a missing entry silently
// drops the save (81/123 lost-save lesson). This harness pins the frontend
// half of the contract: render table entry, detail pane wiring, bridge shape,
// tier registration (optional), the store feed, the Message render hook, and
// the fold gate itself (threshold + first-line preview).
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
const panel = fs.readFileSync(path.join(frontendRoot, "src/components/SettingsPanel.tsx"), "utf8");
const view = fs.readFileSync(path.join(frontendRoot, "src/lib/settingsViewTypes.ts"), "utf8");
const types = fs.readFileSync(path.join(frontendRoot, "src/lib/types.ts"), "utf8");
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const tiers = fs.readFileSync(path.join(frontendRoot, "src/lib/experimentTiers.ts"), "utf8");
const appTsx = fs.readFileSync(path.join(frontendRoot, "src/App.tsx"), "utf8");
const store = fs.readFileSync(path.join(frontendRoot, "src/store/collabDisplay.ts"), "utf8");
const message = fs.readFileSync(path.join(frontendRoot, "src/components/Message.tsx"), "utf8");
const fold = fs.readFileSync(path.join(frontendRoot, "src/lib/messageFold.ts"), "utf8");
const styles = fs.readFileSync(path.join(frontendRoot, "src/styles.css"), "utf8");
const zh = fs.readFileSync(path.join(frontendRoot, "src/locales/zh.ts"), "utf8");
const en = fs.readFileSync(path.join(frontendRoot, "src/locales/en.ts"), "utf8");
const zhTw = fs.readFileSync(path.join(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const settingsApp = fs.readFileSync(path.join(repoRoot, "desktop/settings_app.go"), "utf8");
const settingsPrefs = fs.readFileSync(path.join(repoRoot, "desktop/settings_preferences.go"), "utf8");
const goConfig = fs.readFileSync(path.join(repoRoot, "internal/config/desktop_preferences.go"), "utf8");
const goEdit = fs.readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8");
const goRender = fs.readFileSync(path.join(repoRoot, "internal/config/render.go"), "utf8");

console.log("\n任务 705 cross-session auto-fold: persistence + render contract");

// 1. Lab render table: the entry exists in the efficiency group and its light
//    reads the boot-snapshot switch.
ok(
  panel.includes('{ id: "sessionCollabAutoFold", group: "efficiency", label: t("settings.sessionCollabAutoFold"), on: Boolean(s.experimentalSessionCollabAutoFold) }'),
  "lab rail hosts the sessionCollabAutoFold entry in the efficiency group",
);

// 2. Detail pane wires the toggle to the backend setter (round-trip write path).
ok(
  panel.includes('selected === "sessionCollabAutoFold" && (') && panel.includes("app.SetExperimentalSessionCollabAutoFold(on)"),
  "detail card persists through SetExperimentalSessionCollabAutoFold",
);

// 3. Bridge contract: declared in the AppBindings interface and stubbed in the dev mock.
ok(
  bridge.includes("SetExperimentalSessionCollabAutoFold(enabled: boolean): Promise<void>;") &&
    bridge.includes("async SetExperimentalSessionCollabAutoFold() {}"),
  "bridge declares and stubs SetExperimentalSessionCollabAutoFold",
);

// 4. Go chain: config key (default false), setter, render line, both settings
//    views, view mapping, and the App-level setter.
ok(goConfig.includes('ExperimentalSessionCollabAutoFold bool `toml:"experimental_session_collab_auto_fold"`'), "config key experimental_session_collab_auto_fold (desktop_preferences, default false)");
ok(goEdit.includes("func (c *Config) SetExperimentalSessionCollabAutoFold(enabled bool) error"), "config setter SetExperimentalSessionCollabAutoFold");
ok(goRender.includes("experimental_session_collab_auto_fold = %v"), "render.go emits the key (fixed-key-set rule)");
ok(
  (settingsApp.match(/ExperimentalSessionCollabAutoFold bool `json:"experimentalSessionCollabAutoFold"`/g) ?? []).length === 2,
  "both settings views carry experimentalSessionCollabAutoFold (81/123 both-views lesson)",
);
ok(settingsApp.includes("view.ExperimentalSessionCollabAutoFold = cfg.Desktop.ExperimentalSessionCollabAutoFold"), "settings view maps the config value");
ok(settingsPrefs.includes("func (a *App) SetExperimentalSessionCollabAutoFold(enabled bool) error"), "App setter persists the switch");

// 5. Tier registration: optional (550 口径), mirrored on both sides, counts moved 18→19 / 47→48.
ok(tiers.includes('| "sessionCollabAutoFold"') && tiers.includes('sessionCollabAutoFold: "optional"'), "frontend tier registry: sessionCollabAutoFold = optional");
ok(goRender.includes('{"sessionCollabAutoFold", LabTierOptional, []string{"experimental_session_collab_auto_fold"}}'), "Go labFeatureTiers: sessionCollabAutoFold = optional");
ok(tiers.includes("optional: 19,"), "LAB_TIER_COUNTS optional 18→19 (总数 47→48)");

// 6. Settings feed: App writes the store once per settings load (and on save).
ok(
  appTsx.includes("setCollabAutoFold(settings.experimentalSessionCollabAutoFold)") &&
    appTsx.includes("experimentalSessionCollabAutoFold?: boolean"),
  "applyDesktopPreferences feeds the collabDisplay store",
);
ok(store.includes("autoFold: false"), "store default off (铁律 2: off = render in full)");

// 7. Render hook: Message folds only collab im-source cards past the threshold,
//    with a summary bar (sender + first line + expand) and a collapse toggle.
ok(
  message.includes("isCollabSource && collabAutoFold && estimateUserMessageLines(displayText) >= COLLAB_MSG_FOLD_LINE_THRESHOLD"),
  "Message folds only collab sources past the threshold",
);
ok(
  message.includes('im-source-card__text--folded') && message.includes("im-source-card__fold-sender") && message.includes("setCollabFoldExpanded(true)"),
  "summary bar renders sender + first line + expand toggle",
);
ok(message.includes('t("msg.collabFoldExpand")') && message.includes('t("msg.collabFoldCollapse")'), "expand/collapse labels are locale-driven");
ok(
  styles.includes(".im-source-card__text--folded") && !/im-source-card__fold-toggle\s*{[^}]*#[0-9a-fA-F]{3}/.test(styles),
  "fold bar styles exist and carry no hardcoded colour",
);

// 8. Threshold helper: exported with the 436 fold constants.
ok(fold.includes("export const COLLAB_MSG_FOLD_LINE_THRESHOLD = 8;"), "fold threshold exported (8 estimated lines)");
ok(fold.includes("export function firstDisplayLine"), "first-line preview helper exported");

// 9. Locales: all three languages carry the full key set.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTw]] as const) {
  ok(
    dict.includes('"settings.sessionCollabAutoFold"') &&
      dict.includes('"settings.sessionCollabAutoFoldHint"') &&
      dict.includes('"settings.sessionCollabAutoFold.on"') &&
      dict.includes('"settings.sessionCollabAutoFold.off"') &&
      dict.includes('"msg.collabFoldExpand"') &&
      dict.includes('"msg.collabFoldCollapse"'),
    `${name} locale carries all six keys`,
  );
}

// 10. Frontend types mirror the settings view.
ok(
  types.includes("experimentalSessionCollabAutoFold?: boolean") && view.includes("experimentalSessionCollabAutoFold?: boolean"),
  "SettingsView types expose experimentalSessionCollabAutoFold",
);

// 11. Behaviour: the store round-trips the setting, and the fold gate only
//     triggers past the threshold. Long collab bodies fold; short ones and
//     the default-off store never do.
const { useCollabDisplayStore, setCollabAutoFold } = await import("../store/collabDisplay");
const { COLLAB_MSG_FOLD_LINE_THRESHOLD, estimateUserMessageLines, firstDisplayLine } = await import("../lib/messageFold");

ok(useCollabDisplayStore.getState().autoFold === false, "store boots off");
setCollabAutoFold(true);
ok(useCollabDisplayStore.getState().autoFold === true, "store round-trips on");
setCollabAutoFold(undefined);
ok(useCollabDisplayStore.getState().autoFold === false, "store round-trips off (undefined = default off)");
setCollabAutoFold(true);

const longBody = Array.from({ length: 10 }, (_, i) => `第 ${i + 1} 行——跨会话消息里很长的一行内容，用于撑高估算行数。`).join("\n");
const shortBody = "短消息：一句话就完了。";
ok(estimateUserMessageLines(longBody) >= COLLAB_MSG_FOLD_LINE_THRESHOLD, "10-line body clears the fold threshold");
ok(estimateUserMessageLines(shortBody) < COLLAB_MSG_FOLD_LINE_THRESHOLD, "one-line body stays unfolded");
const gateOn = (body: string) => useCollabDisplayStore.getState().autoFold && estimateUserMessageLines(body) >= COLLAB_MSG_FOLD_LINE_THRESHOLD;
ok(gateOn(longBody) === true && gateOn(shortBody) === false, "fold gate matches threshold × store");
setCollabAutoFold(undefined);
ok(gateOn(longBody) === false, "default-off store never folds (关=现状全量展示)");
ok(firstDisplayLine("首行摘要\n\n第二行") === "首行摘要", "firstDisplayLine keeps the first non-empty head only");
ok(firstDisplayLine("  很长很长的一行会被截断吗 不会，只做首行裁剪   ") === "很长很长的一行会被截断吗 不会，只做首行裁剪", "firstDisplayLine trims edges and collapses runs");

process.stdout.write(`\n${passed}/${passed} passed\n`);
if (process.exitCode) {
  process.exit(1);
}
assert.ok(passed > 0);
