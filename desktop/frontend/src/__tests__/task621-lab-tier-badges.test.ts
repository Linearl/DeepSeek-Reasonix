// Run: npx tsx src/__tests__/task621-lab-tier-badges.test.ts
// 任务 621 acceptance harness (badge placement + 「（实验）」suffix cleanup):
//  ① pane side shows EXACTLY ONE badge per 表A feature — on the master switch
//     row only; sibling switches / dials never carry one (用户口径
//     2026-10-08：单特性多设置项时徽章只挂主控开关行);
//  ② the acceptance sample rows are pinned verbatim: autopilot secondaries
//     (ask 超时/自动续跑/守护自建/守护间隔) and the multi-switch siblings
//     (perfMonitor / researchBudget / coldCacheCompact / collabGuidanceMerge)
//     render bare labels, while their master switch rows keep the badge;
//  ③ the 「（实验）/（實驗）/(experimental)」 marker is GONE from all three
//     locales — the tier badge replaces it;
//  ④ the badge tooltip (任务 621 ①): TierBadge renders a `title` from
//     LAB_TIER_DESC_KEYS and all four desc keys exist in all three dialects;
//  ⑤ the stripped labels keep their keys in all three dialects (no key loss).

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  EXPERIMENT_FEATURE_TIERS,
  LAB_TIER_DESC_KEYS,
  LAB_TIER_ORDER,
  type TierFeatureId,
} from "../lib/experimentTiers";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 621 badge placement + suffix cleanup");

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const card = readFileSync(fileURLToPath(new URL("../components/SettingsOpenCodeGoUsageCard.tsx", import.meta.url)), "utf8");
const badge = readFileSync(fileURLToPath(new URL("../components/TierBadge.tsx", import.meta.url)), "utf8");

// ① exactly one pane badge per 表A feature (the usage card covers opencodeGoUsage).
{
  const counts = new Map<string, number>();
  for (const m of panel.matchAll(/labLabel\("([a-zA-Z]+)"/g)) {
    counts.set(m[1], (counts.get(m[1]) ?? 0) + 1);
  }
  const dupes: string[] = [];
  const missing: string[] = [];
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    const n = counts.get(id) ?? 0;
    if (id === "opencodeGoUsage") {
      // 独立用量卡自带徽章，pane 内不重复挂。
      if (n !== 0) dupes.push(`${id}×${n}`);
    } else if (id === "modelCapabilityFilter") {
      // 任务 722 点2：「已退役」徽章移除（rail 成员表 + 卡内只读行都不挂）。
      if (n !== 0) dupes.push(`${id}×${n}`);
    } else if (n === 0) missing.push(id);
    else if (n > 1) dupes.push(`${id}×${n}`);
  }
  ok(missing.length === 0, `every non-card 表A feature keeps its pane badge (missing: ${JSON.stringify(missing)})`);
  ok(dupes.length === 0, `no feature carries more than one pane badge (duplicates: ${JSON.stringify(dupes)})`);
}

// ② acceptance sample: badge on the master switch row, bare sibling rows.
{
  const badged = [
    'labLabel("autopilot", t("settings.autopilot"))',
    'labLabel("monitoring", t("settings.sessionMonitor"))',
    'labLabel("budgetControl", t("settings.contextBudget"))',
    'labLabel("compressOpt", t("settings.proactiveCompact"))',
    'labLabel("messageMerge", t("settings.collabInboxMerge"))',
  ];
  const bare = [
    // autopilot 附属行（抽查清单：ask 超时提帧/超时秒数、守护检查间隔…）
    'label={t("settings.autopilotMaxRuntime")}',
    'label={t("settings.autopilotApprovalGrace")}',
    'label={t("settings.autopilotAskTimeout")}',
    'label={t("settings.autopilotAskWaitSeconds")}',
    'label={t("settings.autopilotAskAutoContinue")}',
    'label={t("settings.autopilotGuardAutocreate")}',
    'label={t("settings.autopilotGuardInterval")}',
    // 多开关特性的兄弟开关行
    'label={t("settings.perfMonitor")}',
    'label={t("settings.researchBudget")}',
    'label={t("settings.coldCacheCompact")}',
    'label={t("settings.collabGuidanceMerge")}',
    // 非表A/域内附属块
    'label={t("settings.perfMonitor.heapHigh")}',
    'label={t("settings.activeTabResident")}',
    'label={t("settings.preapproveManagedPaths")}',
  ];
  const missingBadge = badged.filter((s) => !panel.includes(s));
  const badBare = bare.filter((s) => !panel.includes(s));
  ok(missingBadge.length === 0, `master switch rows keep their badges (missing: ${JSON.stringify(missingBadge)})`);
  ok(badBare.length === 0, `sibling/dial rows render bare labels (violations: ${JSON.stringify(badBare)})`);
  ok(panel.includes("labEntryBadgeTiers(labLayoutResolved.layout, feature.id).map((tier) => ("),
    "rail rows keep their tier badges (non-regression; 任务724 徽章改由布局数据驱动)");
}

// ③ the 「（实验）」 marker is gone from all three locales.
for (const locale of ["zh", "en", "zh-TW"]) {
  const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
  const hits = ["（实验）", "（實驗）", "(experimental)"].filter((s) => dict.includes(s));
  ok(hits.length === 0, `${locale} carries no experimental text marker (found: ${JSON.stringify(hits)})`);
}

// ④ badge tooltip: TierBadge renders the tier description; keys exist ×3.
{
  ok(badge.includes("title={t(LAB_TIER_DESC_KEYS[tier])}"), "TierBadge renders a tooltip from LAB_TIER_DESC_KEYS");
  ok(LAB_TIER_ORDER.every((tier) => LAB_TIER_DESC_KEYS[tier] === `settings.labTier.${tier}.desc`),
    "desc key register covers every tier with the uniform suffix");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = LAB_TIER_ORDER.map((tier) => `"settings.labTier.${tier}.desc"`).filter((k) => !dict.includes(k));
    ok(missing.length === 0, `${locale} carries all four tier desc keys (missing: ${JSON.stringify(missing)})`);
  }
}

// ⑤ stripped labels keep their keys in all three dialects (no key loss).
{
  const stripped = [
    "settings.autopilotAskTimeout",
    "settings.autopilotAskAutoContinue",
    "settings.autopilotGuardAutocreate",
    "settings.perfMonitor.heapHigh",
    "settings.sessionStorage.v4",
    "settings.preapproveManagedPaths",
    "settings.activeTabResident",
    "settings.opencodeGoUsage",
    "settings.lifecycleNoiseGate",
    "settings.cdpDebugPort",
    "settings.zcodeTaskBus",
    "settings.outputStyle",
    "settings.coldCacheCompact",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = stripped.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} keeps every stripped label key (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
