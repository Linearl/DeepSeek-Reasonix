// Task 359 acceptance (lab navigation, four requirements):
//  Q1-A  intro = collapsed top banner (default closed), single entry — the old
//        bottom section is gone;
//  Q2①   groups collapsed by default, header is a toggle with an on-count;
//  Q2③   sticky group directory: click jumps to the group AND auto-expands it;
//  ③     "disabled items last" top switch — on = enabled first (stable sort),
//        off = source order; persisted in localStorage (page preference only,
//        no experimental_* chain); switch semantics untouched;
//  ④     task 353 sticky-rail guards stay green (run alongside this file).
//
// Run: npx tsx src/__tests__/task359-lab-navigation.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const intro = readFileSync(fileURLToPath(new URL("../components/ForkFeaturesIntro.tsx", import.meta.url)), "utf8");

console.log("\ntask 359 lab navigation");

// Q1-A: entry default-collapsed, single entry point (task 379: the open state
// now hosts the large dialog instead of the inline banner).
ok(/const \[introOpen, setIntroOpen\] = useState\(false\)/.test(panel), "intro entry state defaults to CLOSED (dialog not open)");
ok(panel.includes("aria-expanded={introOpen}"), "entry exposes aria-expanded");
ok(panel.includes("{introOpen ? <ForkFeaturesIntroDialog t={t} onClose={() => setIntroOpen(false)} /> : null}"), "intro dialog mounts only while open");
{
  const calls = panel.match(/<ForkFeaturesIntro/g) ?? [];
  ok(calls.length === 1, `exactly ONE ForkFeaturesIntro mount point (got ${calls.length}) — no double entry`);
  const idx = panel.indexOf("<ForkFeaturesIntro");
  const chipsIdx = panel.indexOf('experimental-lab__chips');
  const navIdx = panel.indexOf("</nav>");
  ok(idx > 0 && idx < chipsIdx && chipsIdx < navIdx, "mount sits above the chips (top banner position)");
}
ok(!/<input\b|<select\b|type="checkbox"|type="radio"/.test(intro), "ForkFeaturesIntro itself still has no form controls (task 282 contract)");

// Q2①: groups collapsed by default + header toggle with count.
ok(/useState<ReadonlySet<LabGroupKey>>\(\(\) => new Set\(\)\)/.test(panel), "expandedGroups starts EMPTY = every group collapsed by default");
ok(panel.includes("aria-expanded={expanded}"), "group header is a real toggle (aria-expanded)");
ok(panel.includes("const expanded = expandedGroups.has(g.key);") && panel.includes("{expanded ? items.map((feature)"), "items render only while the group is expanded");
ok(panel.includes('t("settings.labGroup.onCount"') && panel.includes("const onCount ="), "group header shows the enabled count via the onCount key");

// Q2③: sticky directory — click jumps AND auto-expands.
ok(panel.includes("experimental-rail__toc"), "group directory rendered inside the rail (sticky via task 132/353 rail)");
ok(panel.includes("onClick={() => jumpToGroup(g.key)}"), "directory item click routes to jumpToGroup");
ok(panel.includes("setExpandedGroups((prev) => (prev.has(key) ? prev : new Set(prev).add(key)));"), "jump auto-expands the target group (user-annotated requirement)");
ok(panel.includes('scrollIntoView({ block: "start", behavior: "smooth" })') && panel.includes("`lab-group-${key}`"), "jump scrolls to the group element by id");

// ③: enabled-first switch — display order only, persisted, bidirectional.
ok(panel.includes('role="switch"') && panel.includes("aria-checked={enabledFirst}"), "order switch rendered with switch semantics");
ok(panel.includes("useState<boolean>(") && panel.includes('getItem("reasonix.lab.enabledFirst") === "1"'), "switch state initializes from localStorage (survives restart)");
ok(panel.includes('setItem("reasonix.lab.enabledFirst"'), "switch writes back to localStorage");
ok(panel.includes("const items = enabledFirst ? [...filtered].sort((a, b) => Number(b.on) - Number(a.on)) : filtered;"), "on = stable enabled-first sort, off = source order (bidirectional)");
{
  // Scope the check to this switch's own wiring (the panel legitimately calls
  // SetExperimental* for OTHER lab switches): the enabledFirst setter body and
  // the order-toggle element must not touch the experimental chain.
  const setterStart = panel.indexOf("const setEnabledFirstPref");
  const setterBody = setterStart >= 0 ? panel.slice(setterStart, setterStart + 400) : "";
  const toggleStart = panel.indexOf('className={`experimental-lab__order-toggle');
  const toggleBody = toggleStart >= 0 ? panel.slice(toggleStart, toggleStart + 500) : "";
  ok(
    setterBody.length > 0 && toggleBody.length > 0 && !setterBody.includes("SetExperimental") && !toggleBody.includes("SetExperimental"),
    "order switch does NOT ride the experimental_* chain (display-only preference)",
  );
}

// Locale keys in all three languages (task 359 set + task 361 split the
// order-switch caption into on/off state keys).
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const keys = ["settings.labGroup.onCount", "settings.lab.enabledFirst.on", "settings.lab.enabledFirst.off", "settings.lab.tocLabel"];
  const missing = keys.filter((k) => !(k in dict));
  ok(missing.length === 0, `locale ${name} carries all 4 task-359/361 keys`);
}
ok(String(zh["settings.labGroup.onCount"]) === "{n} 开", "zh count format keeps the {n} placeholder");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
