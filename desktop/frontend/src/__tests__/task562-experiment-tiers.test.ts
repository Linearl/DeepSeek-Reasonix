// Run: npx tsx src/__tests__/task562-experiment-tiers.test.ts
// 任务 562 acceptance harness (lab three-tier badges):
//  ① the tier register mirrors xlsx 表A exactly — 推荐 15 / 可选 24 /
//     未稳定 13 / 已退役 1 = 53 (acceptance ④; 任务 742 合并卡容器+心跳后台化
//     入表后口径);
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
  LAB_WALL_PICKS,
  isTierFeatureId,
  type LabTier,
  type TierFeatureId,
} from "../lib/experimentTiers";
// 任务 722/724：合并卡成员表移交实验室布局数据（yaml 化内置默认），rail
// 徽章与成员覆盖从布局数据读（lib/experimentTiers 只保留档位注册表）。
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { findLabLayoutDefaultRegisterDrift, railTiersForDefault } from "../lab/labLayout";

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
  // 任务 704：trajectoryView（轨迹视图，默认关）按可选档入表——可选
  // 20→21、总数 49→50，两侧同源对齐。
  // 任务 727：heartbeatRotation（心跳会话轮换，桥接 JSON 键）按可选档入表
  // ——可选 21→22、总数 50→51。
  // 任务 742：tabManagement（标签页管理合并卡，纯前端容器 id，无配置键）与
  // heartbeatBackground（心跳任务后台化，默认关）按可选档入表——可选
  // 22→24、总数 51→53（Go 渲染表只收 heartbeatBackground → 51）。
  ok(counts.recommended === 15 && counts.optional === 24 && counts.unstable === 13 && counts.retired === 1,
    `register counts are 15/24/13/1 (got ${JSON.stringify(counts)})`);
  ok(Object.keys(EXPERIMENT_FEATURE_TIERS).length === 53, `register holds exactly 53 features (got ${Object.keys(EXPERIMENT_FEATURE_TIERS).length})`);
  ok(LAB_TIER_COUNTS.recommended === 15 && LAB_TIER_COUNTS.optional === 24 && LAB_TIER_COUNTS.unstable === 13 && LAB_TIER_COUNTS.retired === 1,
    "LAB_TIER_COUNTS pins 15/24/13/1");
  // 任务 722/724：默认布局的档位与注册表逐 id 一致（漂移=测试红，不静默）。
  ok(findLabLayoutDefaultRegisterDrift().length === 0,
    `default layout tiers agree with the register (drift: ${JSON.stringify(findLabLayoutDefaultRegisterDrift())})`);
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
// 任务 621/722/727/742 口径：两侧各留显式豁免——Go 独有 sessionCwdFollow（任务
// 545，纯 TOML 配置特性，无 desktop 绑定、无设置页渲染面，无处挂徽章）；前端
// 独有 modelCapabilityFilter（已退役只读展示行，Go 渲染表已移除该键，473/562
// 域）、heartbeatRotation（任务 727，桥接 heartbeat-rotation.json 的 enabled，
// 不落 config.toml，无 Go 渲染表条目）与 tabManagement（任务 742，标签页管理
// 合并卡的纯前端容器 id，卡内两行各写各的配置键，容器自身无键）。除这些显式
// 豁免外逐项一致。
{
  const goSrc = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
  const goTiers: Record<string, string> = {};
  for (const m of goSrc.matchAll(/\{"([a-zA-Z]+)", LabTier([A-Za-z]+), \[/g)) {
    goTiers[m[1]] = m[2].toLowerCase();
  }
  ok(Object.keys(goTiers).length === 51, `Go registry parses to 51 entries (got ${Object.keys(goTiers).length})`);
  const feIds = Object.keys(EXPERIMENT_FEATURE_TIERS).sort();
  const goIds = Object.keys(goTiers).sort();
  const goOnly = goIds.filter((id) => !feIds.includes(id));
  const feOnly = feIds.filter((id) => !goIds.includes(id));
  ok(JSON.stringify(goOnly) === JSON.stringify(["sessionCwdFollow"]),
    `Go-only ids are exactly the config-only exemption (got ${JSON.stringify(goOnly)})`);
  ok(JSON.stringify(feOnly) === JSON.stringify(["heartbeatRotation", "modelCapabilityFilter", "tabManagement"]),
    `frontend-only ids are exactly the display/json-bridge/container exemptions (got ${JSON.stringify(feOnly)})`);
  let mismatch = 0;
  for (const id of feIds) {
    if (id in goTiers && goTiers[id] !== EXPERIMENT_FEATURE_TIERS[id as TierFeatureId]) mismatch += 1;
  }
  ok(mismatch === 0, `frontend and Go registers agree on every shared tier (mismatches: ${mismatch})`);
}

// ④ rail wiring: merged cards cover their members; standalone entries badge
// themselves; unknown ids stay clean.
{
  const members = LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g) => g.entries.flatMap((e) => e.members?.map((m) => m.id) ?? []));
  // 任务 722：sessionCollabAutoFold 并入 sessionCollab、compactModel +
  // messageMerge 并入 safetyCostControl、727 heartbeatRotation 入卡——
  // 成员总数 17→19；点2 modelStrategy 成员表去掉 modelCapabilityFilter。
  ok(members.length === 19, `merged cards carry 19 member features (got ${members.length})`);
  ok(members.every((id) => isTierFeatureId(id)), "every merged member is a registered 表A feature");
  const covered = new Set([...members, ...Object.keys(EXPERIMENT_FEATURE_TIERS).filter((id) => !members.includes(id as TierFeatureId))]);
  // 任务 704：trajectoryView 入表 → 50；任务 727 heartbeatRotation → 51；
  // 任务 742 tabManagement + heartbeatBackground → 53。
  ok(covered.size === 53, "rail entries cover all 53 features");
  const gov = railTiersForDefault("contextGovernance");
  ok(gov[0] === "recommended" && gov[1] === "optional" && gov.length === 2, `contextGovernance shows [推荐, 可选] (got ${JSON.stringify(gov)})`);
  ok(JSON.stringify(railTiersForDefault("autopilot")) === JSON.stringify(["recommended"]), "standalone entry badges itself");
  ok(railTiersForDefault("preapproveManagedPaths").length === 0, "non-表A id (task-364 domain) shows no badge");
  // 任务 722 点2：「已退役」徽章自模型策略卡移除——只余 highSpeedModel 的可选档。
  ok(!railTiersForDefault("modelStrategy").includes("retired") && JSON.stringify(railTiersForDefault("modelStrategy")) === JSON.stringify(["optional"]),
    `modelStrategy drops the retired badge (got ${JSON.stringify(railTiersForDefault("modelStrategy"))})`);
  // 任务 722 点5/6：compactModel（可选）与 messageMerge（推荐）并入安全/成本控制卡。
  ok(JSON.stringify(railTiersForDefault("safetyCostControl")) === JSON.stringify(["recommended", "optional"]),
    `safetyCostControl shows [推荐, 可选] after the 722 folds (got ${JSON.stringify(railTiersForDefault("safetyCostControl"))})`);
  // 任务 722 点3：sessionCollab 卡多出折叠子项的可选档徽章。
  ok(JSON.stringify(railTiersForDefault("sessionCollab")) === JSON.stringify(["recommended", "optional"]),
    `sessionCollab covers its auto-fold member (got ${JSON.stringify(railTiersForDefault("sessionCollab"))})`);
}

// ⑤ pane wiring in SettingsPanel (46/46) + rail render site.
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
  const wrapped = new Set([...panel.matchAll(/labLabel\("([a-zA-Z]+)"/g)].map((m) => m[1]));
  const missing: string[] = [];
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    // opencodeGoUsage：独立用量卡自带徽章；modelCapabilityFilter：任务 722
    // 点2 移除「已退役」徽章（只读展示行保留，无徽章）。
    if (id !== "opencodeGoUsage" && id !== "modelCapabilityFilter" && !wrapped.has(id)) missing.push(id);
  }
  ok(missing.length === 0, `pane labels wrap every non-card id in SettingsPanel + the usage card covers opencodeGoUsage (missing: ${JSON.stringify(missing)})`);
  ok(panel.includes("labEntryBadgeTiers(labLayoutResolved.layout, feature.id).map((tier) => ("), "rail rows render tier badges from the resolved layout");
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
