// Task 379 acceptance (+ rework, user 0929 additions) — fork features intro
// → large modal + card wall, copy externalized to yaml, badge slot reserved:
//  ① entry opens a LARGE dialog (portal, role=dialog, aria-modal; close button
//     + Escape + backdrop click live in the DIALOG layer so task 282's
//     pure-display component contract is untouched);
//  ② copy loads from public/fork-features.yaml — parser exercised on the real
//     file; per-field fallback keeps locale text for missing file/fields;
//  ③ recommended badge slot, shipped yaml carries ZERO badges;
//  ④ the card wall renders 3 columns driven by the yaml `columns` field
//     (change the file, no code edit) with vertical scrolling inside the
//     fixed-size dialog (content overflow never breaks the modal);
//  ⑤ rework entries appear in the wall model straight from yaml (project
//     grouping, autopilot+cross-session long-running dev, sandbox switch
//     group) — badge zero-default applies to them too;
//  ⑥ privacy: the yaml mirrors public locale copy only (no secret patterns).
//
// Run: npx tsx src/__tests__/task379-intro-dialog.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { FORK_FEATURE_INTRO_GROUPS, FORK_FEATURE_INTRO_COUNT } from "../lib/forkFeaturesIntro";
import { parseForkFeaturesYaml, forkFeatureCopyFor, FORK_FEATURES_DEFAULT_COLUMNS } from "../lib/forkFeaturesYaml";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const panel = read("../components/SettingsPanel.tsx");
const dialog = read("../components/ForkFeaturesIntroDialog.tsx");
const intro = read("../components/ForkFeaturesIntro.tsx");
const yamlText = read("../../public/fork-features.yaml");
const loader = read("../lib/forkFeaturesYaml.ts");

console.log("\ntask 379 intro dialog + yaml copy (rework)");

// ① dialog shape + single entry.
ok(dialog.includes("createPortal") && dialog.includes('role="dialog"') && dialog.includes('aria-modal="true"'), "dialog portals with role=dialog + aria-modal");
ok(dialog.includes('onClick={onClose}') && dialog.includes('"Escape"') && dialog.includes("modal-backdrop"), "close affordances: backdrop click + Escape + modal-backdrop styling");
ok(panel.includes("{introOpen ? <ForkFeaturesIntroDialog t={t} onClose={() => setIntroOpen(false)} /> : null}"), "panel mounts the dialog behind the intro entry");
ok(panel.includes('aria-haspopup="dialog"') && panel.includes("aria-expanded={introOpen}"), "entry button advertises the dialog (aria-haspopup + expanded state kept from 359)");
ok(!panel.includes("<ForkFeaturesIntro t={t} />"), "the old inline banner mount is gone (narrow-column form retired)");
{
  const mounts = panel.match(/<ForkFeaturesIntro/g) ?? [];
  ok(mounts.length === 1, `exactly one intro mount (got ${mounts.length}) — single entry preserved`);
}

// ② yaml parsing on the REAL file + per-field fallback.
{
  const copy = parseForkFeaturesYaml(yamlText);
  const allIds = FORK_FEATURE_INTRO_GROUPS.flatMap((g) => g.features.map((f) => f.id));
  ok(copy.lead.trim().length > 0, `yaml lead parsed: ${copy.lead.slice(0, 24)}…`);
  const missing = allIds.filter((id) => !copy.byId[id]);
  ok(missing.length === 0, `yaml covers every design-table feature (missing: ${missing.join(", ") || "none"})`);
  const blank = allIds.filter((id) => {
    const f = copy.byId[id];
    return !f || !f.title.trim() || !f.desc.trim() || !f.how.trim() || !f.icon.trim();
  });
  ok(blank.length === 0, `every feature has title/desc/how/icon (blank: ${blank.join(", ") || "none"})`);

  // Fallback ladder: a yaml missing one feature keeps the locale value for it.
  const partial = parseForkFeaturesYaml(yamlText.replace(/- id: classicLayout[\s\S]*?(?=      - id: groupFold)/, ""));
  const localeFor = (kind: "title" | "desc" | "how", id: string) => `LOCALE_${kind.toUpperCase()}_${id}`;
  const merged = forkFeatureCopyFor(partial, FORK_FEATURE_INTRO_GROUPS, localeFor);
  const classic = merged.groups.flatMap((g) => g.features).find((f) => f.id === "classicLayout");
  ok(Boolean(classic) && classic!.title.startsWith("LOCALE_TITLE_"), "missing yaml entry falls back to locale (no blank card)");
  // A blank field inside yaml also falls back per field.
  const blanked = parseForkFeaturesYaml(yamlText.replace("title: 经典布局", "title: "));
  const mergedBlank = forkFeatureCopyFor(blanked, FORK_FEATURE_INTRO_GROUPS, localeFor);
  const classicBlank = mergedBlank.groups.flatMap((g) => g.features).find((f) => f.id === "classicLayout");
  ok(Boolean(classicBlank) && classicBlank!.title.startsWith("LOCALE_TITLE_"), "blank yaml field falls back per field");
  // Total failure (no yaml at all) → everything from locale.
  const none = forkFeatureCopyFor(null, FORK_FEATURE_INTRO_GROUPS, localeFor);
  ok(none.groups.every((g) => g.features.every((f) => f.title.startsWith("LOCALE_")) && none.lead === ""), "no yaml at all → pure locale, panel alive");
}

// Loader wiring: fetch first, swallow failures → null (never throws up).
ok(loader.includes('fetch(YAML_URL') && loader.includes(".catch(() => null)"), "loader fetches the public file and swallows failures");

// ③ recommended badge slot + zero badges by default (incl. rework entries).
ok(intro.includes("feature.recommended ?") && intro.includes("experimental-intro__card-badge"), "badge render slot exists, conditional on recommended");
ok(intro.includes('t("settings.forkFeaturesIntro.recommended")'), "badge label comes from a locale key");
{
  const copy = parseForkFeaturesYaml(yamlText);
  const flagged = Object.entries(copy.byId).filter(([, f]) => f.recommended);
  ok(flagged.length === 0, `shipped yaml ships ZERO recommended badges (flagged: ${flagged.map(([id]) => id).join(", ") || "none"})`);
  ok(loader.includes("recommended: feature.recommended === \"true\""), "parser understands the recommended field for when the user picks the list");
}

// ④ card wall grid + yaml-driven columns (3) + vertical scroll in the dialog.
ok(intro.includes("experimental-intro__cards--wall") && intro.includes("experimental-intro__card--wall"), "component renders the wall modifiers");
ok(intro.includes("gridTemplateColumns: `repeat(${copy.columns}, 1fr)`"), "grid columns come from the parsed yaml `columns` field (file-driven, no code edit)");
{
  const copy = parseForkFeaturesYaml(yamlText);
  ok(copy.columns === 3, `real yaml declares columns: 3 (got ${copy.columns})`);
  ok(FORK_FEATURES_DEFAULT_COLUMNS === 3, "loader default columns is 3 when the field is absent");
  // Change the file value → the model follows (the acceptance's no-code-edit path).
  const widened = parseForkFeaturesYaml(yamlText.replace("columns: 3", "columns: 2"));
  ok(widened.columns === 2, `editing yaml columns changes the model (got ${widened.columns})`);
  const nonsense = parseForkFeaturesYaml(yamlText.replace("columns: 3", "columns: 99"));
  ok(nonsense.columns === FORK_FEATURES_DEFAULT_COLUMNS, "nonsense columns value falls back to the default (never destroys the wall)");
}
{
  const css = read("../styles.css");
  ok(css.includes(".experimental-intro__cards--wall") && css.includes("repeat(auto-fill, minmax(236px, 1fr))"), "wall base grid remains responsive (inline style overrides)");
  ok(css.includes(".fork-features-dialog {") && css.includes("min(980px, 94vw)"), "large dialog sizing present");
  ok(css.includes("max-height: 88vh") && css.includes("overflow: hidden"), "dialog box itself never stretches beyond the viewport");
  // The scroll lives in the body rule of the stylesheet (the dialog component
  // itself carries no inline overflow) — assert THAT rule specifically.
  const bodyRule = css.slice(css.indexOf(".fork-features-dialog__body {"), css.indexOf(".fork-features-dialog__body {") + 120);
  ok(bodyRule.includes("overflow: auto"), "dialog body scrolls vertically when the wall is long");
}
ok(intro.includes("experimental-intro__card-icon") && intro.includes("feature.icon"), "tiles carry the icon slot");

// ② component fallback surface (no white screen): copy null → locale per field.
ok(intro.includes("copy.lead.trim() || t(\"settings.forkFeaturesIntro.lead\")") && intro.includes("localeFor"), "component falls back to locale for lead and every field");

// ⑤ rework entries: present in yaml, rendered through the yaml-driven wall model.
{
  const copy = parseForkFeaturesYaml(yamlText);
  const reworkIds = ["projectGroupingManage", "longRunningDev", "sandboxSwitches"];
  const missing = reworkIds.filter((id) => !copy.byId[id]);
  ok(missing.length === 0, `rework entries exist in yaml (missing: ${missing.join(", ") || "none"})`);
  const blank = reworkIds.filter((id) => {
    const f = copy.byId[id];
    return !f || !f.title.trim() || !f.desc.trim() || !f.how.trim() || !f.icon.trim();
  });
  ok(blank.length === 0, `rework entries carry full copy (blank: ${blank.join(", ") || "none"})`);
  const localeFor = (kind: "title" | "desc" | "how", id: string) => reworkIds.includes(id) ? "" : `LOCALE_${kind.toUpperCase()}_${id}`;
  const model = forkFeatureCopyFor(copy, FORK_FEATURE_INTRO_GROUPS, localeFor);
  const allWallIds = model.groups.flatMap((g) => g.features.map((f) => f.id));
  const absent = reworkIds.filter((id) => !allWallIds.includes(id));
  ok(absent.length === 0, `rework entries reach the WALL MODEL (absent: ${absent.join(", ") || "none"}) — yaml-driven groups`);
  const longRunning = model.groups.flatMap((g) => g.features).find((f) => f.id === "longRunningDev");
  ok(Boolean(longRunning) && longRunning!.title.includes("自动驾驶") && longRunning!.desc.includes("心跳"), "long-running dev copy comes from yaml verbatim");
  // New groups ride with labels for the renderer.
  const automation = model.groups.find((g) => g.id === "automation");
  ok(Boolean(automation) && automation!.yamlLabel === "自动化与协作", "yaml-only group carries its display label");
  // Design-table groups still route through locale for their labels (component
  // does the lookup) — model keeps the id for that.
  ok(model.groups.some((g) => g.id === "layout"), "design-table groups still present in the wall model");
}

// ⑥ privacy: the yaml mirrors public copy only.
{
  const forbidden = /api[_-]?key|secret|token|password|credential|BEGIN [A-Z ]*PRIVATE KEY/i;
  ok(!forbidden.test(yamlText), "yaml carries no secret-shaped content");
  ok(!forbidden.test(yamlText) && yamlText.includes("设置 →"), "yaml text mirrors the public how-to surface");
}

// Group/icon count sanity vs the design table.
ok(FORK_FEATURE_INTRO_COUNT >= 6, `design table still carries the entries (${FORK_FEATURE_INTRO_COUNT})`);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
