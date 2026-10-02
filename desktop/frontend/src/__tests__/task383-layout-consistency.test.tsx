// Task 383 acceptance — three-layout experience consistency, the user's
// ten-verdict table (tasklist = the authoritative spec). Four verdicts say
// "do" (#2 project-group entry, #5 classic footer icon-only, #8
// automation/heartbeat dedup, #10 types comment); #5's render face is already
// covered by task412-classic-rail-switch-timing (the v2058 rework proved the
// live path runs through App.tsx). This harness closes the remaining
// acceptance gaps: #2 menu wiring, #8 locale/button dedup, #10 enum-comment
// consistency against the Go normalizer (the actual source of the three
// values), plus the "don't do" guard for #1 (classic add menu keeps no
// blank-project item).
//
// Run: npx tsx src/__tests__/task383-layout-consistency.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 383 layout consistency (ten-verdict table)");

const projectTreeSrc = readFileSync(fileURLToPath(new URL("../components/ProjectTree.tsx", import.meta.url)), "utf8");
const addControlsSrc = readFileSync(fileURLToPath(new URL("../components/ProjectTreeAddControls.tsx", import.meta.url)), "utf8");
const appSrc = readFileSync(fileURLToPath(new URL("../App.tsx", import.meta.url)), "utf8");
const paletteSrc = readFileSync(fileURLToPath(new URL("../app-runtime/usePaletteCommands.tsx", import.meta.url)), "utf8");
const typesSrc = readFileSync(fileURLToPath(new URL("../lib/types.ts", import.meta.url)), "utf8");
const settingsTypesSrc = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");
const goConfigSrc = readFileSync(fileURLToPath(new URL("../../../../internal/config/config.go", import.meta.url)), "utf8");
const locale = (name: string) => readFileSync(fileURLToPath(new URL(`../locales/${name}.ts`, import.meta.url)), "utf8");
const locales = { zh: locale("zh"), "zh-TW": locale("zh-TW"), en: locale("en") };

// ── Verdict #2: project-group entry reaches workbench (menu) and creation
// (classic-form header); classic keeps its FolderInput header button ─────────
{
  const { projectTreeHeaderAddItems } = await import("../components/ProjectTreeAddControls");

  // Workbench call shape: group item present, wired to onGroup.
  let fired = false;
  const workbenchItems = projectTreeHeaderAddItems({
    blankLabel: "blank", localLabel: "local", remoteLabel: "remote",
    groupLabel: "projectGroup.createNew", disabled: false,
    onBlank: () => {}, onLocal: () => {}, onRemote: () => {}, onGroup: () => { fired = true; },
  });
  const groupItem = workbenchItems.find((item) => item.key === "new-project-group");
  ok(!!groupItem, "#2 workbench add menu carries a new-project-group item");
  ok(groupItem?.label === "projectGroup.createNew", "#2 group item uses the projectGroup.createNew copy (distinct from row-menu session groups)");
  groupItem?.onSelect();
  ok(fired, "#2 group item onSelect invokes the onGroup callback (opens NewGroupPanel)");

  // Classic call shape (ProjectTree's classicHeaderAddItems omits
  // groupLabel/onGroup): no group item, and no blank item (verdict #1 —
  // blank-project stays workbench-only).
  const classicItems = projectTreeHeaderAddItems({
    localLabel: "local", remoteLabel: "remote", disabled: false,
    onLocal: () => {}, onRemote: () => {},
  });
  ok(!classicItems.some((item) => item.key === "new-project-group"), "#2 classic add menu unchanged (no group item; classic uses its header FolderInput button)");
  ok(!classicItems.some((item) => item.key === "blank-project"), "#1 classic add menu stays without a blank-project entry (verdict: do NOT port)");

  // ProjectTree wiring: workbench menu items pass groupLabel + onGroup; the
  // classic header keeps the FolderInput button; creation rides the classic
  // header branch; one NewGroupPanel handles the confirm for every variant.
  ok(/groupLabel: t\("projectGroup\.createNew"\), onGroup: \(\) => setNewGroupOpen\(true\)/.test(projectTreeSrc), "#2 ProjectTree workbench add items wire projectGroup.createNew → setNewGroupOpen");
  ok(/aria-label=\{t\("projectGroup\.createNew"\)\}/.test(projectTreeSrc) && /<FolderInput size=\{14\} \/>/.test(projectTreeSrc), "#2 classic header keeps the FolderInput new-group button");
  ok(/compactTopics \? (\()?\s*\{?\s*renderProjectHeader\("workbench"\)/.test(projectTreeSrc.replace(/\n/g, " ")) || /renderProjectHeader\("workbench"\)/.test(projectTreeSrc), "#2 header branches exist for both modes");
  ok(/<NewGroupPanel open=\{newGroupOpen\} onClose=\{\(\) => setNewGroupOpen\(false\)\} onConfirm=\{handleAddGroup\} \/>/.test(projectTreeSrc), "#2 single NewGroupPanel confirms into handleAddGroup (variant-independent)");
  for (const [name, src] of Object.entries(locales)) {
    ok(new RegExp(`"projectGroup\\.createNew":\\s*"`).test(src), `#2 locale ${name} has projectGroup.createNew`);
  }
}

// ── Verdict #5: classic footer icon-only — render face owned by the task412
// harness (both App.tsx and SidebarRegion paths); here only the guard that
// this file's subject stayed put ─────────────────────────────────────────────
{
  ok(appSrc.includes('{(sidebarWorkbench || !sidebarCreation) ? (\n            <nav className="sidebar__nav sidebar__nav--footer">'), "#5 App.tsx footer ternary still sends classic down the utility-row path (task412 rework guard)");
}

// ── Verdict #8: automation/heartbeat dual-name gone ─────────────────────────
{
  for (const [name, src] of Object.entries(locales)) {
    ok(!/"sidebar\.automation"/.test(src), `#8 locale ${name} no longer defines sidebar.automation`);
    ok(/"heartbeat\.scheduler":\s*"/.test(src), `#8 locale ${name} keeps heartbeat.scheduler as the unified entry copy`);
  }
  // The sidebar footer utility-row (live classic+workbench path in App.tsx)
  // holds exactly one automation button — the dual-name was one button
  // answering to two labels, the dedupe is one label total.
  const footerStart = appSrc.indexOf('{(sidebarWorkbench || !sidebarCreation) ? (');
  const footerEnd = appSrc.indexOf('</nav>', footerStart);
  const footer = appSrc.slice(footerStart, footerEnd);
  ok((footer.match(/openPage\(\{ kind: "automation" \}\)/g) || []).length === 1, "#8 sidebar utility-row has exactly one automation button");
  ok(!footer.includes("sidebar.automation"), "#8 utility-row no longer references sidebar.automation");
  ok(/title: t\("heartbeat\.scheduler"\)[^\n]*openPage\(\{ kind: "automation" \}\)/.test(appSrc), "#8 palette command (cmd-automation) uses heartbeat.scheduler as its title");
  ok(/label=\{t\("heartbeat\.scheduler"\)\} onClick=\{props\.onOpenAutomation\}/.test(paletteSrc) || /t\("heartbeat\.scheduler"\)/.test(paletteSrc), "#8 palette source (unmounted composition half) stays on heartbeat.scheduler");
}

// ── Verdict #10: desktopLayoutStyle comment matches the three canonical
// values the Go normalizer actually produces ─────────────────────────────────
{
  const canonical = ["classic", "workbench", "creation"];
  const comment = (src: string) => src.match(/desktopLayoutStyle: string; \/\/ ([^\n]*)/)?.[1] ?? "";
  for (const [file, src] of [["lib/types.ts", typesSrc], ["lib/settingsViewTypes.ts", settingsTypesSrc]] as const) {
    const c = comment(src);
    ok(canonical.every((value) => c.includes(`"${value}"`)), `#10 ${file} comment carries all three canonical values`);
    ok(!/workspace/.test(c), `#10 ${file} comment omits the retired alias`);
  }
  // Go side is the source of truth: normalizeDesktopLayoutStyle returns exactly
  // classic / workbench / creation (workspace folds into workbench).
  const fn = goConfigSrc.match(/func normalizeDesktopLayoutStyle\(style string\) string \{[\s\S]*?\n\}/)?.[0] ?? "";
  ok(/case "classic":/.test(fn) && /case "workbench", "workspace":/.test(fn) && /case "creation":/.test(fn), "#10 Go normalizer emits exactly the three canonical values");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
