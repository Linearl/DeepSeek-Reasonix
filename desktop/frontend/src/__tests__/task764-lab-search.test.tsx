// Task 764 acceptance (lab usability: settings search covers lab entries +
// double chip-row dedup):
//  ①  the settings nav search matches lab entry/member labels — searching
//      「心跳任务后台化」 (settings.heartbeatBackground) hits the lab tab
//      (searchTerms fed from the SAME resolved layout the rail renders);
//  ②  clicking the matched tab reaches the entry: the query rides onSelect,
//      findLabEntryIdByQuery maps it to the pane id, ExperimentalSection
//      preselects it, expands its group and scrolls the rail row into view;
//  ③  matching prefers layout order; member hits resolve to their parent
//      card; empty query / no hit → null (zero behavior);
//  ④  (dedup, user 0325 ruling) exactly one chip row remains on the lab page
//      — the rail toc (jump); the counted filter chips are gone with their
//      labFilter state and their dead CSS;
//  ⑤  zero new locale keys: search words come from the existing labelKeys in
//      all three languages (settings.heartbeatBackground pinned below).
//
// Run: npx tsx src/__tests__/task764-lab-search.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";
import { findLabEntryIdByQuery } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import type { DictKey } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const nav = readFileSync(fileURLToPath(new URL("../components/SettingsNavigation.tsx", import.meta.url)), "utf8");
const styles = readFileSync(fileURLToPath(new URL("../styles.css", import.meta.url)), "utf8");

/** Locale-backed translator matching the app's t() for dict keys. */
function translate(dict: Record<string, string>) {
  return (key: DictKey): string => dict[key] ?? key;
}

console.log("\ntask 764 lab search + chips dedup");

// ── ② wiring: query rides the nav click into a lab-entry preselect ─────────
ok(/onSelect: \(tab: SettingsTab, query\?: string\) => void;/.test(nav), "SettingsNavigation onSelect carries the query (764 signature)");
ok(nav.includes("onClick={() => onSelect(id, query)}"), "nav item click forwards the current query");
ok(panel.includes("const [pendingLabEntry, setPendingLabEntry] = useState<ExperimentFeatureId | null>(null);"), "pending lab entry state exists in SettingsPanel");
ok(panel.includes("findLabEntryIdByQuery(LAB_LAYOUT_FOR_SEARCH, query, t) as ExperimentFeatureId | null"), "selectTab resolves the query to a lab entry via the shared layout");
ok(panel.includes("focusEntry={pendingLabEntry}"), "ExperimentalSection receives the focus entry");
ok(panel.includes("focusEntry?: ExperimentFeatureId | null }"), "ExperimentalSection accepts focusEntry (optional, zero behavior when absent)");
ok(panel.includes("id={`lab-entry-${feature.id}`}"), "rail rows carry per-entry anchor ids (scroll target for the focus jump)");
ok(panel.includes("document.getElementById(`lab-entry-${focusEntry}`)?.scrollIntoView({ block: \"nearest\", behavior: \"smooth\" })"),
  "focus jump scrolls the rail row into view");

// ① search terms share the rail's layout source (single source, yaml-fallback
// included) and feed the experimental tab's searchTerms.
ok(/const LAB_LAYOUT_FOR_SEARCH = resolveLabLayout\(labLayoutYaml, \{ allowedEntryIds: LAB_PANE_IDS \}\)\.layout;/.test(panel),
  "search layout resolves from the same yaml + pane whitelist as the rail");
ok(panel.includes(': id === "experimental" ? labSearchTerms : ""'), "experimental tab searchTerms = lab search terms");
ok(panel.includes("for (const member of entry.members ?? []) parts.push(t(member.labelKey), member.id);"),
  "search terms include member labels (merged-card members are searchable)");

// ── ③ matcher behavior on the built-in default layout ──────────────────────
{
  const tZh = translate(zh as Record<string, string>);
  const tEn = translate(en as Record<string, string>);
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "心跳任务后台化", tZh) === "heartbeatBackground",
    "「心跳任务后台化」 resolves to the heartbeatBackground card (zh)");
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "Heartbeat background mode", tEn) === "heartbeatBackground",
    "English label resolves to the same card (locale-driven, no extra word list)");
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "指定压缩模型", tZh) === "safetyCostControl",
    "member hit (指定压缩模型) resolves to its parent merged card");
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "  心跳任务后台化  ", tZh) === "heartbeatBackground",
    "query is trimmed (nav input whitespace tolerated)");
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "", tZh) === null && findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "   ", tZh) === null,
    "empty/blank query → null (zero behavior)");
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "绝对不存在的条目词", tZh) === null,
    "no hit → null (nav falls back to plain tab navigation)");
  // Disambiguation by layout order: a unique-but-common-word query must land
  // on the rail-first card mentioning it (sessionCollab leads automation).
  ok(findLabEntryIdByQuery(LAB_LAYOUT_DEFAULT_DATA, "会话协作", tZh) === "sessionCollab", "first rail match wins for ambiguous queries");
}

// ── ④ dedup: one chip row left, and it is the toc ──────────────────────────
ok(!panel.includes("experimental-lab__chip"), "no filter-chip markup left in SettingsPanel");
ok(!panel.includes("setLabFilter") && !panel.includes('labFilter === "all"'), "labFilter state fully removed (no zombie guard)");
ok(!styles.includes("experimental-lab__chip"), "dead chip CSS rules removed (no orphan selectors)");
ok(panel.includes("experimental-rail__toc") && panel.includes("onClick={() => jumpToGroup(g.key)}"),
  "the surviving chip row is the rail toc (jump + auto-expand, task 359 contract)");

// ── ⑤ zero new locale keys; the acceptance word exists in all three ────────
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  ok(typeof dict["settings.heartbeatBackground"] === "string", `locale ${name} carries settings.heartbeatBackground (search word source)`);
}
ok(!("settings.labGroup.all" in zh) && !("settings.labGroup.all" in en) && !("settings.labGroup.all" in zhTW),
  "dead settings.labGroup.all key removed from all three locales (764)");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
