// Run: npx tsx src/__tests__/task562-experiment-tiers.test.ts
// 任务 562 acceptance harness (lab three-tier badges):
//  ① the tier register mirrors xlsx 表A exactly — 推荐 15 / 可选 20 /
//     未稳定 13 / 已退役 1 = 49 (acceptance ④; 任务 707 压缩模型指定入表后口径);
//  ② the wall picks are the 16 curated 表B W1 items, every pick carries a
//     tier (12 recommended + 4 optional) (acceptance ② data half);
//  ③ the frontend register and the Go labFeatureTiers registry in
//     internal/config/render.go agree item by item — two sides, one source;
//  ④ rail wiring: every rail entry renders badges; merged cards cover all
//     48 member features; non-表A ids (preapproveManagedPaths) get none;
//  ⑤ pane wiring: every 表A feature's switch label is wrapped in labLabel
//     (or the standalone usage card), i.e. 48/48 pane badges;
//  ⑥ locale keys exist in all three dialects.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  EXPERIMENT_FEATURE_TIERS,
  LAB_TIER_COUNTS,
  LAB_TIER_ORDER,
  LAB_RAIL_ENTRY_MEMBERS,
  LAB_WALL_PICKS,
  railTiersFor,
  isTierFeatureId,
  type LabTier,
  type TierFeatureId,
} from "../lib/experimentTiers";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 562 lab three-tier badges");

// ① 表A distribution.
{
  const counts: Record<LabTier, number> = { recommended: 0, optional: 0, unstable: 0, retired: 0 };
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    counts[EXPERIMENT_FEATURE_TIERS[id]] += 1;
  }
  // 任务 621 修正：562 原钉 15/20/10/1=46，漏收 toolOptimizations（任务 603，
  // 已在 562 合入后追加）与 tabModeTint（任务 504，Go 侧一直有档）——未稳定
  // 10→12、总数 46→48，两侧（Go labFeatureTiers）同源对齐。
  // 任务 517：B1/B2/B3（可选×3）合并为 safetyCostControl（可选×1）——
  // 可选 20→18、总数 48→46，两侧同源对齐。
  // 任务 677：collabGroupView（群聊入口开关，409 交付漏挂铁律 2 开关）入表
  // ——未稳定 12→13、总数 46→47，两侧同源对齐。
  // 任务 705：sessionCollabAutoFold（超长跨会话消息自动折叠，默认关）按可选
  // 档入表——可选 18→19、总数 47→48，两侧同源对齐。
  // 任务 707：compactModel（压缩模型指定，默认关）按任务书建议可选档入表
  // ——可选 19→20、总数 48→49，两侧同源对齐。
  ok(counts.recommended === 15 && counts.optional === 20 && counts.unstable === 13 && counts.retired === 1,
    `register counts are 15/20/13/1 (got ${JSON.stringify(counts)})`);
  ok(Object.keys(EXPERIMENT_FEATURE_TIERS).length === 49, `register holds exactly 49 features (got ${Object.keys(EXPERIMENT_FEATURE_TIERS).length})`);
  ok(LAB_TIER_COUNTS.recommended === 15 && LAB_TIER_COUNTS.optional === 20 && LAB_TIER_COUNTS.unstable === 13 && LAB_TIER_COUNTS.retired === 1,
    "LAB_TIER_COUNTS pins 15/20/13/1");
}

// ② wall picks (表B W1).
{
  ok(LAB_WALL_PICKS.length === 16, `wall carries exactly 16 picks (got ${LAB_WALL_PICKS.length})`);
  const picks = LAB_WALL_PICKS as readonly string[];
  ok(picks.every((id) => isTierFeatureId(id)), "every pick is a registered 表A feature");
  const pickCounts: Record<LabTier, number> = { recommended: 0, optional: 0, unstable: 0, retired: 0 };
  for (const id of picks) pickCounts[EXPERIMENT_FEATURE_TIERS[id as TierFeatureId]] += 1;
  ok(pickCounts.recommended === 12 && pickCounts.optional === 4,
    `pick tier split is 推荐 12 + 可选 4 (got ${JSON.stringify(pickCounts)})`);
  ok(new Set(picks).size === 16, "no duplicate picks");
}

// ③ frontend register ↔ Go registry (render.go) agreement.
// 任务 621 口径：两侧各留一个显式豁免——Go 独有 sessionCwdFollow（任务 545，
// 纯 TOML 配置特性，无 desktop 绑定、无设置页渲染面，无处挂徽章）；前端独有
// modelCapabilityFilter（已退役只读展示行，Go 渲染表已移除该键，473/562 域）。
// 除这两个显式豁免外逐项一致。
{
  const goSrc = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
  const goTiers: Record<string, string> = {};
  for (const m of goSrc.matchAll(/\{"([a-zA-Z]+)", LabTier([A-Za-z]+), \[/g)) {
    goTiers[m[1]] = m[2].toLowerCase();
  }
  ok(Object.keys(goTiers).length === 49, `Go registry parses to 49 entries (got ${Object.keys(goTiers).length})`);
  const feIds = Object.keys(EXPERIMENT_FEATURE_TIERS).sort();
  const goIds = Object.keys(goTiers).sort();
  const goOnly = goIds.filter((id) => !feIds.includes(id));
  const feOnly = feIds.filter((id) => !goIds.includes(id));
  ok(JSON.stringify(goOnly) === JSON.stringify(["sessionCwdFollow"]),
    `Go-only ids are exactly the config-only exemption (got ${JSON.stringify(goOnly)})`);
  ok(JSON.stringify(feOnly) === JSON.stringify(["modelCapabilityFilter"]),
    `frontend-only ids are exactly the retired-display exemption (got ${JSON.stringify(feOnly)})`);
  let mismatch = 0;
  for (const id of feIds) {
    if (id in goTiers && goTiers[id] !== EXPERIMENT_FEATURE_TIERS[id as TierFeatureId]) mismatch += 1;
  }
  ok(mismatch === 0, `frontend and Go registers agree on every shared tier (mismatches: ${mismatch})`);
}

// ④ rail wiring: merged cards cover their members; standalone entries badge
// themselves; unknown ids stay clean.
{
  const members = Object.values(LAB_RAIL_ENTRY_MEMBERS).flat();
  ok(members.length === 17, `merged cards carry 17 member features (任务 707 compactModel 随上下文治理卡入成员表) (got ${members.length})`);
  ok(members.every((id) => isTierFeatureId(id)), "every merged member is a registered 表A feature");
  const covered = new Set([...members, ...Object.keys(EXPERIMENT_FEATURE_TIERS).filter((id) => !members.includes(id as TierFeatureId))]);
  ok(covered.size === 49, "rail entries cover all 49 features");
  const gov = railTiersFor("contextGovernance");
  ok(gov[0] === "recommended" && gov[1] === "optional" && gov.length === 2, `contextGovernance shows [推荐, 可选] (got ${JSON.stringify(gov)})`);
  ok(JSON.stringify(railTiersFor("autopilot")) === JSON.stringify(["recommended"]), "standalone entry badges itself");
  ok(railTiersFor("preapproveManagedPaths").length === 0, "non-表A id (task-364 domain) shows no badge");
  ok(railTiersFor("modelStrategy").includes("retired"), "retired member surfaces on the modelStrategy card");
  // 任务 517：safetyCostControl 是单键卡（无成员表）——自己作为表A id 挂徽章。
  ok(JSON.stringify(railTiersFor("safetyCostControl")) === JSON.stringify(["optional"]),
    `safetyCostControl badges itself as optional (got ${JSON.stringify(railTiersFor("safetyCostControl"))})`);
}

// ⑤ pane wiring in SettingsPanel (46/46) + rail render site.
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
  const wrapped = new Set([...panel.matchAll(/labLabel\("([a-zA-Z]+)"/g)].map((m) => m[1]));
  const missing: string[] = [];
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    if (id !== "opencodeGoUsage" && !wrapped.has(id)) missing.push(id);
  }
  ok(missing.length === 0, `pane labels wrap every non-card id in SettingsPanel + the usage card covers opencodeGoUsage (missing: ${JSON.stringify(missing)})`);
  ok(panel.includes("railTiersFor(feature.id).map((tier) => ("), "rail rows render tier badges");
  const card = readFileSync(fileURLToPath(new URL("../components/SettingsOpenCodeGoUsageCard.tsx", import.meta.url)), "utf8");
  ok(card.includes("EXPERIMENT_FEATURE_TIERS.opencodeGoUsage"), "opencodeGoUsage card badges its title");
}

// ⑥ wall component + dialog mount.
{
  const wall = readFileSync(fileURLToPath(new URL("../components/LabPicksWall.tsx", import.meta.url)), "utf8");
  ok(wall.includes("LAB_WALL_PICKS.map") && wall.includes("<TierBadge"), "picks wall renders badges from the register");
  const dialog = readFileSync(fileURLToPath(new URL("../components/ForkFeaturesIntroDialog.tsx", import.meta.url)), "utf8");
  ok(dialog.includes("<LabPicksWall t={t} />"), "intro dialog mounts the picks wall");
}

// ⑦ locale keys in all three dialects.
{
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = [
      ...LAB_TIER_ORDER.map((tier) => `settings.labTier.${tier}`),
      "settings.labPicks.title",
    ].filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all tier + picks keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
