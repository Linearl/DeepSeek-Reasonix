// Run: tsx src/__tests__/tl1450-layout-retirement.test.ts
//
// tl1450 (2026-11-06 ruling, CONSTITUTION N5/Q5): the creation layout is
// retired — three layouts become two (classic + workbench). This harness pins
// the retirement contract end to end:
//   1. the layout enum is exactly two values, everywhere it lives;
//   2. the legacy raw value folds to workbench (one-version read compat; the
//      authoritative mapping + startup rewrite live in Go — see
//      internal/config/desktop_layout_upgrade_test.go);
//   3. the retired surface is gone (no mode sheet, no layout symbols, no dead
//      locale keys) while the SURVIVING creation.* namespaces (the shell
//      tool-group feature, the workbench empty-hero welcome) stay intact;
//   4. the four fork classic-compatibility points stay guarded (Q5: classic
//      is permanently retained; its compat code must not be cleaned away by
//      a future "finish the upstream retirement" pass).

import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = resolve(here, "..");
const repoRoot = resolve(srcRoot, "..", "..", "..");
const read = (p: string) => readFileSync(resolve(srcRoot, p), "utf8");

const layoutLib = read("lib/desktopLayoutStyle.ts");
const adapter = read("app-runtime/desktopPreferencesAdapter.ts");
const picker = read("components/SettingsPanel.tsx");
const bridge = read("lib/bridge.ts");
const goConfig = readFileSync(resolve(repoRoot, "internal/config/config.go"), "utf8");
const goEdit = readFileSync(resolve(repoRoot, "internal/config/edit.go"), "utf8");
const goPricing = readFileSync(resolve(repoRoot, "internal/config/pricing.go"), "utf8");
const goUpgradeTest = readFileSync(resolve(repoRoot, "internal/config/desktop_layout_upgrade_test.go"), "utf8");
const locales = ["en", "zh", "zh-TW"].map((name) => read(`locales/${name}.ts`));

console.log("\ntl1450 layout retirement (three layouts -> two)");

// ── 1. the enum is exactly two values ────────────────────────────────────────
{
  ok(/export type DesktopLayoutStyle = "classic" \| "workbench";/.test(layoutLib),
    "the DesktopLayoutStyle union is exactly classic|workbench");
  ok(layoutLib.includes('(["workbench", "classic"] as const)') === false,
    "the picker tuple does not live in the layout lib (it lives in SettingsPanel)");
  ok(picker.includes('(["workbench", "classic"] as const)'), "the settings picker offers exactly workbench, classic");
  ok(!/["']creation["']/.test(picker.split("desktopLayoutStyleLabel")[0].split("tl1450")[1] ?? "") ||
    !picker.includes('"creation"'), "the settings picker carries no creation literal");
  ok(/"classic" \| "workbench"/.test(bridge) && !browserMockAcceptsCreation(bridge),
    "the browser mock reads only classic|workbench");
}

function browserMockAcceptsCreation(source: string): boolean {
  return /value === "classic" \|\| value === "creation"/.test(source);
}

// ── 2. legacy raw value folds to workbench (one-version read compat) ────────
{
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  const { normalizeDesktopLayoutStyle } = await import("../lib/desktopLayoutStyle");
  ok(normalizeDesktopLayoutStyle("creation") === "workbench", "normalize folds the legacy creation value to workbench");
  ok(normalizeDesktopLayoutStyle("classic") === "classic", "normalize keeps classic (fork-retained layout)");
  ok(normalizeDesktopLayoutStyle("workbench") === "workbench", "normalize keeps workbench");
  ok(normalizeDesktopLayoutStyle(undefined) === "workbench", "normalize defaults undefined to workbench");
  dom.window.close();
  ok(adapter.includes('return style === "classic" ? "classic" : "workbench";'),
    "the snapshot adapter folds every non-classic value to workbench");
  // Go side: the explicit one-version mapping + startup rewrite (authoritative).
  ok(/case "creation":\s*\n\s*return "workbench"/.test(goConfig),
    "Go normalizeDesktopLayoutStyle maps creation -> workbench (one-version compat)");
  ok(goPricing.includes("migrateRetiredCreationLayoutFileLocked"),
    "Go startup migration rewrites the stored creation value (ApplyUserConfigUpgradesOnStartup)");
  ok(goUpgradeTest.includes("TestApplyUserConfigUpgradesOnStartupRewritesLegacyCreationLayout"),
    "the Go rewrite has its own idempotency guard test");
}

// ── 3. the retired surface is gone; surviving namespaces stay ───────────────
{
  ok(!existsSync(resolve(srcRoot, "styles/mode-creation.css")), "mode-creation.css stays deleted");
  ok(!existsSync(resolve(srcRoot, "lib/useCreationTranscriptScrollbar.ts")), "the creation scrollbar hook stays deleted");
  const prodSources = [
    "App.tsx", "lib/desktopLayoutStyle.ts", "lib/transcriptRows.ts", "lib/projectTreeTopic.ts",
    "store/layout.ts", "components/ToolGroup.tsx", "components/ProjectTree.tsx",
    "app-shell/SidebarRegion.tsx", "app-runtime/useShellGeometry.ts",
  ].map(read).join("\n");
  ok(!/sidebarCreation|creationEmptyHero|creationTopics|creationMode|CREATION_[A-Z_]+|clampCreation|defaultCreation|isCreationGroupableTool|app--creation(?!-hero)|layout--creation-chrome-hidden|data-creation-label/.test(prodSources),
    "no retired layout symbol remains in the core production files");
  const deadKeys = [
    "creation.sidebar.newChat", "creation.sidebar.features", "creation.sidebar.skills", "creation.sidebar.messageChannels",
    "creation.reasoningLabel", "creation.toolCallsLabel",
    "creation.toolGroup.explore", "creation.toolGroup.modify", "creation.toolGroup.delegate",
    "creation.toolStat.read", "creation.toolStat.search", "creation.toolStat.write", "creation.toolStat.edit",
    "creation.toolStat.move", "creation.toolStat.delete", "creation.toolStat.task",
    "settings.desktopLayoutStyle.creation",
  ];
  for (const name of locales) {
    ok(deadKeys.every((key) => !name.includes(`"${key}"`)),
      "dead creation locale keys stay deleted (one check per locale file)");
  }
  // Surviving namespaces: the shell tool-group feature (used by classic and
  // workbench behind the task-668 grouping toggle) and the workbench
  // empty-session welcome (variant "creation" is a component variant name,
  // not a layout reference).
  ok(locales.every((name) => name.includes('"creation.toolGroup.shell"') && name.includes('"creation.toolStat.other"')),
    "the live shell tool-group keys survive in all three locales");
  ok(locales.every((name) => name.includes('"welcome.creation.title"')),
    "the workbench empty-hero welcome keys survive in all three locales");
  ok(read("components/Welcome.tsx").includes('variant?: "default" | "creation"'),
    "the Welcome creation variant (component variant, used by the workbench empty hero) survives");
}

// ── 4. classic compatibility points (Q5: classic is permanent) ──────────────
{
  // Point 1 — Go read side accepts classic (upstream retired the value).
  ok(/case "classic":\s*\n\s*return "classic"/.test(goConfig),
    "compat 1: Go normalizeDesktopLayoutStyle still accepts classic");
  // Point 2 — Go write side accepts classic (upstream removed the option).
  ok(/case "classic":\s*\n\s*c\.Desktop\.LayoutStyle = "classic"/.test(goEdit),
    "compat 2: Go SetDesktopLayoutStyle still accepts classic");
  // Point 3 — the startup upgrade deliberately does NOT rewrite classic
  // (the upstream classic->workbench migration stays unapplied in the fork).
  ok(goPricing.includes("upstream startup migration that rewrote layout_style") &&
    goUpgradeTest.includes("TestApplyUserConfigUpgradesOnStartupKeepsForkClassicDesktopLayout"),
    "compat 3: the startup upgrade keeps fork classic configs untouched (exemption + guard test)");
  // Point 4 — the frontend read side + rendering accept classic: the
  // normalizer's classic arm, the app--classic render branch, the classic
  // mode sheet and its boot prefetch.
  ok(layoutLib.includes('if (style === "classic") return "classic";'),
    "compat 4a: the frontend normalizer still accepts classic");
  ok(existsSync(resolve(srcRoot, "styles/mode-classic.css")),
    "compat 4b: mode-classic.css stays present");
  ok(layoutLib.includes('if (mode === "classic")') &&
    layoutLib.includes('import("../components/AppChrome")'),
    "compat 4c: the classic chrome chunk stays prefetched for classic users");
  const appSource = read("App.tsx");
  ok(appSource.includes('!sidebarWorkbench ? "app--classic"'), "compat 4d: App still renders app--classic for classic users");
  ok(read("components/AppChrome.tsx").length > 0, "compat 4e: the classic chrome chunk stays in the tree");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
