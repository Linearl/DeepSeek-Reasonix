// Run: npx tsx src/__tests__/task562-experiment-tiers.test.ts
// 任务 562 acceptance harness (lab tier badges):
//  ① the tier register mirrors xlsx 表A exactly — 稳定 4 / 推荐 18 / 可选 34 /
//     未稳定 13 / 已退役 1 = 70 (acceptance ④; 任务 1005 批1 四项转稳定档后，
//     见下方 ① 内演进注);
//  ② the wall picks are the 16 curated 表B W1 items, every pick carries a
//     tier (12 recommended + 4 optional) (acceptance ② data half);
//  ③ the frontend register and the Go labFeatureTiers registry in
//     internal/config/render.go agree item by item — two sides, one source;
//  ④ rail wiring: every rail entry renders its SINGLE aggregated badge
//     (任务 774 徽章聚合：卡头 = max(卡 tier, members tiers)，推荐>可选>
//     未稳定>已退役；成员徽章只在卡内详情行可见); merged cards cover all
//     member features; non-表A ids (preapproveManagedPaths) get none;
//  ⑤ pane wiring: every 表A feature's switch label is wrapped in labLabel
//     (or the standalone usage card), i.e. 62/63 pane badges;
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
// 任务 774：rail 徽章聚合为单枚最高档（labEntryBadgeTier），多枚去重清单
// 仅作测试诊断面（railTiersForDefault）。
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import type { LabLayoutData } from "../lab/labLayoutTypes";
import { findLabLayoutDefaultRegisterDrift, labEntryBadgeTier, railTiersForDefault } from "../lab/labLayout";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 562 lab three-tier badges");

// ① 表A distribution.
{
  const counts: Record<LabTier, number> = { stable: 0, recommended: 0, optional: 0, unstable: 0, retired: 0 };
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
  // 任务 782：界面组三张条目级合并卡容器 id 入表——searchHistory（三成员
  // 两推荐一可选 → 推荐档）、inputSelection / viewLayout（全可选成员 →
  // 可选档）——推荐 15→16、可选 24→26、总数 53→56（Go 渲染表无条目，
  // 纯前端容器同 tabManagement 豁免 → Go 侧仍 51）。
  // 任务 783：「用量监测」卡容器 + GLM Plan 用量子项入表（推荐×2）→
  // 推荐 16→18、总数 56→58；同任务④编辑读回（toolOptimizations）随 22.5
  // 交付转正，未稳定 13→12、推荐 18→19、总数不变 58。
  // 任务 785：S1 常驻底座开关随底座退役出表——未稳定
  // 12→11、总数 58→57（合并线依 Go 表A 12/50 同源实跑对齐）。
  // 任务 815（统一登记窗口收口）：sidebarHover×2（826）+ editEvidenceRelaxed
  // （789/815）+ homePointer（846）此前已入表而计数 pin 尚钉在收口前口径
  // （19/26/12/1=58），本窗口一并跳到 19/29/14/1=63。
  // 任务 686：workflowPanel（工作流可视化面板，未稳定档）入表——未稳定
  // 14→15、总数 63→64（Go 侧同步 56→57）。
  // 任务 943：multiEditInjection（multi_edit 直注，工具优化族拆分独立卡）
  // 按可选档入表——可选 29→30、总数 64→65（Go 侧同步 57→58）。任务 940：
  // approvalSelect（composer 权限选择器形态二选一，默认关）按可选档入表
  // ——可选 30→31、总数 65→66（Go 侧同步 58→59；两件先后入表各 +1）。任务
  // 944：renderArtifact（对话内产物卡片）按可选档入表——可选 31→32、
  // 总数 66→67（Go 侧同步 59→60）。任务 961：smartAutomation（智能自动化
  // 合并卡，纯前端容器 id）可选档入表 + interactionPresentation（交互与
  // 呈现合并卡，纯前端容器 id）推荐档入表——可选 32→33、推荐 19→20、
  // 总数 67→69（Go 渲染表无条目，同 tabManagement 豁免）。任务 979：
  // capabilityGroups（能力组披露+session_hub 聚合件，953/954/955 共用一键）
  // 按可选档入表（自 Go labNonFeatureKeys 豁免表转正）——可选 33→34、
  // 总数 69→70（Go 侧同步 60→61）。
  // 任务 979：capabilityGroups（能力组披露+session_hub 聚合件）按可选档入表
  // ——可选 33→34、总数 69→70（Go 侧同步 60→61）。任务 1005：autopilot /
  // sessionCollab / fullAccess / runtimeReuse 四项升「稳定」（新增第四档，
  // Go labFeatureTiers 同动作 18→14 推荐 / +4 稳定，总数 61 不变）——前端
  // 此前口径 20/34/15/1：推荐 -3+1（orphanHandling 992 补升）、可选 -1+1
  // （runtimeReuse 原挂可选 + editEvidenceRelaxed 992 补升）、未稳定 -2
  // （orphanHandling/editEvidenceRelaxed）→ 4 稳定/18 推荐/34 可选/13 未稳定
  // /1 退役，总数不变 70。
  // 任务 959：① tableWrap（对话表格按栏宽自动换行，默认关）+ ②
  // sendTargetSelect（composer 发送目标形态二选一，默认关）按可选档入表
  // ——可选 34→36、总数 70→72（Go 侧同步 61→63）。
  // 任务 1024：draftRewrite（composer 草稿优化）在其上叠加——可选 36→37、
  // 总数 72→73（Go 侧同步 63→64）。任务 1072：renderArtifact（对话内产物
  // 卡片）整功能移除出表——可选 37→36、总数 73→72（Go 侧同步 64→63）。
  // 任务 1069：stalledIntentNudge（模型提前结束判定器续轮提醒，默认关）按
  // 可选档入表——可选 36→37、总数 72→73（Go 侧同步 63→64）。
  // 任务 1070：standardContinuation（Standard 流续跑总闸）按可选档入表——
  // 可选 37→38、总数 73→74（Go 侧同步 64→65）。
  // tl 1097：logManagement（日志管理，纯前端容器 id，无配置键）按可选档入表
  // ——可选 38→39、总数 74→75（Go 渲染表无条目，同 tabManagement 豁免）。
  // tl 1125：tabManagement（标签页管理合并卡）+ viewLayout（视图布局合并卡）
  // 两特性档位「可选 → 推荐」（用户 2026-10-24 指令：推荐 = 核心特性，不开启
  // 体验有缺口）——推荐 18→20、可选 39→37、总数不变 75（两 id 均为纯前端
  // 容器 id，Go 渲染表无条目，registry 计数不变仍 66，同 tabManagement 豁免）。
  // tl 1132：startupSplash（启动遮罩，可选）+ startupLoading（「启动与加载」
  // 纯前端容器 id，无配置键，同 tabManagement 豁免）按可选档入表——
  // 可选 37→39、总数 75→77（Go 侧同步 +startupSplash 一条 → 67）。
  // tl 1165：smoothScrollFollow（流式滚动平滑跟随，DSH ScrollFollow 同款四条
  // 防线，默认关）按可选档入表——可选 39→40、总数 77→78（Go 侧同步 67→68）。
  ok(counts.stable === 4 && counts.recommended === 20 && counts.optional === 40 && counts.unstable === 13 && counts.retired === 1,
    `register counts are 4 稳定/20/40/13/1 (got ${JSON.stringify(counts)})`);
  ok(Object.keys(EXPERIMENT_FEATURE_TIERS).length === 78, `register holds exactly 78 features (got ${Object.keys(EXPERIMENT_FEATURE_TIERS).length})`);
  ok(LAB_TIER_COUNTS.stable === 4 && LAB_TIER_COUNTS.recommended === 20 && LAB_TIER_COUNTS.optional === 40 && LAB_TIER_COUNTS.unstable === 13 && LAB_TIER_COUNTS.retired === 1,
    "LAB_TIER_COUNTS pins 4 稳定/20/40/13/1");
  // 任务 722/724：默认布局的档位与注册表逐 id 一致（漂移=测试红，不静默）。
  ok(findLabLayoutDefaultRegisterDrift().length === 0,
    `default layout tiers agree with the register (drift: ${JSON.stringify(findLabLayoutDefaultRegisterDrift())})`);
}

// ② wall picks (表B W1).
{
  ok(LAB_WALL_PICKS.length === 16, `wall carries exactly 16 picks (got ${LAB_WALL_PICKS.length})`);
  const picks = LAB_WALL_PICKS as readonly string[];
  ok(picks.every((id) => isTierFeatureId(id)), "every pick is a registered 表A feature");
  const pickCounts: Record<LabTier, number> = { stable: 0, recommended: 0, optional: 0, unstable: 0, retired: 0 };
  for (const id of picks) pickCounts[EXPERIMENT_FEATURE_TIERS[id as TierFeatureId]] += 1;
  // 任务 1005：autopilot / sessionCollab / fullAccess 三 pick 转稳定档——
  // 图墙收录清单不变（W1 人工收录口径），pick 档位拆分 12+4 → 3 稳定 + 9 推荐 + 4 可选。
  ok(pickCounts.stable === 3 && pickCounts.recommended === 9 && pickCounts.optional === 4,
    `pick tier split is 稳定 3 + 推荐 9 + 可选 4 (got ${JSON.stringify(pickCounts)})`);
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
  // 任务 815：editEvidenceRelaxed（789 证据门宽松）随设置页入口落地自 Go
  // 豁免表转正入表——Go registry 51→56 条目；前端独有的 editEvidenceRelaxed
  // 转为两侧共享。feOnly 回到 8 个纯前端容器/展示/桥接豁免。
  // 任务 686：workflowPanel 入表（两侧共享）——Go registry 56→57。
  // 任务 943：multiEditInjection 入表（两侧共享，工具优化族拆分）——57→58。
  // 任务 940：approvalSelect 入表（两侧共享）——Go registry 58→59（两件各 +1）。
  // 任务 944：renderArtifact 入表（两侧共享）——Go registry 59→60。
  // 任务 961：smartAutomation/interactionPresentation 两张合并卡为纯前端
  // 容器 id（无配置键，Go 渲染表无条目）——Go registry 保持 60。
  // 任务 979：capabilityGroups 入表（两侧共享，自 Go 豁免表转正）——60→61。
  // 任务 959：① tableWrap + ② sendTargetSelect 入表（两侧共享）——61→63。
  // 任务 1024：draftRewrite 叠加入表——63→64。任务 1072：renderArtifact
  // 整功能移除出表——64→63。任务 1069：stalledIntentNudge（模型提前结束
  // 判定器续轮提醒，默认关）入表——63→64。
  // 任务 1024：draftRewrite 叠加入表——63→64。
  // 任务 1070：standardContinuation 叠加入表（两侧共享）——64→65。
  // 任务 1056：networkRetryBudget（网络重试预算分级，[network.retry_budget] 总开关）
  // 只在 Go 侧登记（labFeatureTiers 65→66），前端 experimentTiers 暂无该 id——
  // 同 sessionCwdFollow 口径计入 Go-only config-only 豁免（后续设置页入口补
  // 前端注册时两侧转共享，此处同步收回）。
  // tl 1097：logManagement（日志管理，纯前端容器 id，无配置键，同 tabManagement
  // 豁免）——Go 渲染表无条目，registry 计数不变（仍 66）。
  // tl 1132：startupSplash 入表（两侧共享）——Go registry 66→67。
  // tl 1105（c75109788 重组重交付，纯 Go 面单 commit）：interruptedAutoResume
  // （中断自动续轮，interrupted_auto_resume 键，Optional 档）只在 Go 侧登记
  // ——Go registry 67→68；前端 experimentTiers 暂无该 id，同 networkRetryBudget
  // 口径计入 Go-only config-only 豁免（后续设置页入口补前端注册时两侧转共享，
  // 此处同步收回）。
  // tl 1165：smoothScrollFollow 入表（两侧共享）——Go registry 68→69。
  ok(Object.keys(goTiers).length === 69, `Go registry parses to 69 entries (got ${Object.keys(goTiers).length})`);
  const feIds = Object.keys(EXPERIMENT_FEATURE_TIERS).sort();
  const goIds = Object.keys(goTiers).sort();
  const goOnly = goIds.filter((id) => !feIds.includes(id));
  const feOnly = feIds.filter((id) => !goIds.includes(id));
  ok(JSON.stringify(goOnly) === JSON.stringify(["interruptedAutoResume", "networkRetryBudget", "sessionCwdFollow"]),
    `Go-only ids are exactly the config-only exemption (got ${JSON.stringify(goOnly)})`);
  // tl 1132：startupLoading（「启动与加载」纯前端容器 id，Go 渲染表无条目，
  // 同 tabManagement 豁免）加入 feOnly。
  ok(JSON.stringify(feOnly) === JSON.stringify(["glmPlanUsage", "heartbeatRotation", "inputSelection", "interactionPresentation", "logManagement", "modelCapabilityFilter", "searchHistory", "smartAutomation", "startupLoading", "tabManagement", "usageMonitoring", "viewLayout"]),
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
  // 任务 782：viewLayout(+2)、searchHistory(+3)、inputSelection(+2)——
  // 成员总数 19→26。任务 783：用量监测卡 glmPlanUsage + opencodeGoUsage
  // ——成员总数 26→28。任务 815（登记窗口收口）：toolOptimizations 卡
  // editEvidenceRelaxed（789）+ viewLayout 卡 sidebarHover×2（826）计入
  // ——成员总数 28→31。任务 940：viewLayout 卡 approvalSelect 计入
  // ——成员总数 31→32。任务 961：smartAutomation 三成员（dream/
  // decisionPatterns/heartbeatBackground）+ interactionPresentation 三成员
  // （quickCommands/traceAsState/outputStyle）+ viewLayout 卡 renderArtifact
  // 计入——成员总数 32→39。任务 971：multiEditInjection 独立卡退役为聚合页
  // 成员行（restartUpdate 自 updateFeedback 迁入 toolOptimizations，成员
  // 计数净零）——成员总数 39→40。任务 979：capabilityGroups（能力组披露+
  // session_hub 聚合件）入聚合页成员行——成员总数 40→41。
  // 任务 959：viewLayout 卡 tableWrap + sendTargetSelect 计入——41→43。
  // 任务 1024：draftRewrite 并入 viewLayout 卡——43→44。任务 1072：
  // renderArtifact 成员行随功能移除出卡——44→43。任务 1069：
  // stalledIntentNudge 并入 smartAutomation 卡——43→44。
  // 任务 1070：smartAutomation 卡补 standardContinuation 成员行——44→45。
  // tl 1132：startupLoading 卡补 startupSplash 成员行——45→46。
  // tl 1165：viewLayout 卡补 smoothScrollFollow 成员行——46→47。
  ok(members.length === 47, `merged cards carry 47 member features (got ${members.length})`);  ok(members.every((id) => isTierFeatureId(id)), "every merged member is a registered 表A feature");
  const covered = new Set([...members, ...Object.keys(EXPERIMENT_FEATURE_TIERS).filter((id) => !members.includes(id as TierFeatureId))]);
  // 任务 782 三容器 → 56；任务 783 用量监测容器+子项 → 58；任务 815
  // 登记窗口收口（sidebarHover×2 + editEvidenceRelaxed + homePointer 计入）
  // → 63。任务 686：workflowPanel 独立 rail 入口计入 → 64。任务 943：
  // multiEditInjection 独立 rail 入口计入 → 65。任务 940：approvalSelect
  // 入表计入 → 66（943/940 两件先后入表各 +1）。任务 944：renderArtifact
  // 独立 rail 入口计入 → 67。任务 961：smartAutomation +
  // interactionPresentation 两容器 id 入表计入 → 69。任务 979：
  // capabilityGroups 入表计入 → 70。
  // 任务 959：① tableWrap + ② sendTargetSelect 入表计入 → 72。
  // 任务 1024：draftRewrite 叠加入表 → 73。任务 1072：renderArtifact 移除
  // → 72。任务 1069：stalledIntentNudge 叠加入表 → 73。
  // 任务 1024：draftRewrite 叠加入表 → 73。
  // 任务 1070：standardContinuation 叠加入表 → 74。
  // tl 1132：startupSplash + startupLoading 叠加入表 → 77。
  // tl 1165：smoothScrollFollow 叠加入表 → 78。
  ok(covered.size === 78, "rail entries cover all 78 features");
  // 任务 774 徽章聚合（设计稿 §4.3）：rail 行/卡头只渲染一枚最高档 =
  // max(卡 tier, members tiers)（推荐 > 可选 > 未稳定 > 已退役）。断言结构
  // 成对：railTiersForDefault 钉「聚合输入」（卡上实际存在的档位清单，含
  // 子项档位——它们仍在布局里，只是不上卡头），labEntryBadgeTier 钉「卡头
  // 输出」（单枚=清单首位）。合并卡的多档成员不再让卡头出现两枚徽章。
  const agg = (id: string) => labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, id);
  ok(JSON.stringify(railTiersForDefault("contextGovernance")) === JSON.stringify(["recommended", "optional"]) && agg("contextGovernance") === "recommended",
    `contextGovernance: input [推荐,可选] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("contextGovernance"))} → ${String(agg("contextGovernance"))})`);
  // 任务 1005：autopilot 独立卡转稳定档——单枚稳定徽章（LAB_TIER_ORDER
  // 首位，聚合取最高档）。
  ok(JSON.stringify(railTiersForDefault("autopilot")) === JSON.stringify(["stable"]) && agg("autopilot") === "stable", "standalone entry badges itself");
  ok(railTiersForDefault("preapproveManagedPaths").length === 0 && agg("preapproveManagedPaths") === null, "non-表A id (task-364 domain) shows no badge");
  // 任务 722 点2：「已退役」徽章自模型策略卡移除——只余 highSpeedModel 的可选档。
  ok(JSON.stringify(railTiersForDefault("modelStrategy")) === JSON.stringify(["optional"]) && agg("modelStrategy") === "optional",
    `modelStrategy drops the retired badge (got ${JSON.stringify(railTiersForDefault("modelStrategy"))})`);
  // 任务 722 点5/6 + 774：compactModel（可选）/messageMerge（推荐）/心跳轮换
  // （可选）并入后，卡头只聚合出推荐一枚。
  ok(JSON.stringify(railTiersForDefault("safetyCostControl")) === JSON.stringify(["recommended", "optional"]) && agg("safetyCostControl") === "recommended",
    `safetyCostControl: input [推荐,可选] aggregates to the single 推荐 badge after the 722 folds (got ${String(agg("safetyCostControl"))})`);
  // 任务 722 点3 + 774 验收件① + 任务 1005：跨会话协作卡（卡=稳定 + 折叠
  // 子项=可选）卡头只显示「稳定」一枚（1005 批1 转档后推荐>可选、稳定>推荐）。
  ok(JSON.stringify(railTiersForDefault("sessionCollab")) === JSON.stringify(["stable", "optional"]) && agg("sessionCollab") === "stable",
    `sessionCollab shows exactly one 稳定 badge, its auto-fold member's 可选 stays in-card (got ${String(agg("sessionCollab"))})`);
  // 任务 774 验收件②：子代理套件（无自有档，成员 可选×3+未稳定）按最高档
  // 单枚——可选压过未稳定。
  ok(JSON.stringify(railTiersForDefault("subagentSuite")) === JSON.stringify(["optional", "unstable"]) && agg("subagentSuite") === "optional",
    `subagentSuite aggregates to the single 可选 badge over its unstable member (got ${String(agg("subagentSuite"))})`);
  // 其余合并卡逐卡钉聚合终值（722 布局现值）。
  // 任务 971：restartUpdate 迁至工具优化聚合页——updateFeedback 仅余 feedback
  // （可选档），聚合终值 推荐→可选。任务 992（用户 1018 21:34）：restartUpdate
  // 归位回本卡——聚合终值回升 推荐（推荐压过可选；本断言 2026-10-19 随 1005
  // 窗口一并补同步，992 落地时本 pin 漏跟）。
  ok(agg("updateFeedback") === "recommended", `updateFeedback aggregates to 推荐 after the 992 move-back (got ${String(agg("updateFeedback"))})`);
  // 任务 971：toolOptimizations 聚合页（readBack 推荐 + 证据门未稳定 + multi_edit
  // 直注可选 + restart_update 推荐）——卡头聚合单枚 推荐。
  ok(agg("toolOptimizations") === "recommended", `toolOptimizations hub aggregates to 推荐 (got ${String(agg("toolOptimizations"))})`);
  ok(agg("sessionStore") === "optional", `sessionStore aggregates to 可选 over its unstable member (got ${String(agg("sessionStore"))})`);
  ok(agg("devDebug") === "unstable", `devDebug aggregates to 未稳定 (got ${String(agg("devDebug"))})`);
  // 任务 782：界面组三张合并卡逐卡钉聚合终值——容器徽章 = 成员最高档
  // （searchHistory 三成员 [推荐,可选] → 推荐；inputSelection 全可选成员 → 可选）。
  // tl 1125：viewLayout 容器档位「可选 → 推荐」（用户指令），聚合输入
  // [推荐,可选×8]，卡头终值 单枚 推荐（推荐压过成员可选）。
  ok(JSON.stringify(railTiersForDefault("searchHistory")) === JSON.stringify(["recommended", "optional"]) && agg("searchHistory") === "recommended",
    `searchHistory: input [推荐,推荐,可选] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("searchHistory"))} → ${String(agg("searchHistory"))})`);
  ok(JSON.stringify(railTiersForDefault("viewLayout")) === JSON.stringify(["recommended", "optional"]) && agg("viewLayout") === "recommended",
    `viewLayout: tl 1125 推荐 container + optional members aggregate to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("viewLayout"))} → ${String(agg("viewLayout"))})`);
  ok(JSON.stringify(railTiersForDefault("inputSelection")) === JSON.stringify(["optional"]) && agg("inputSelection") === "optional",
    `inputSelection: all-optional members aggregate to 可选 (got ${JSON.stringify(railTiersForDefault("inputSelection"))})`);
  // 任务 961：automation/efficiency 两张合并卡钉聚合终值——容器徽章 =
  // 成员最高档（smartAutomation 全可选 → 可选；interactionPresentation
  // [推荐,可选,可选] → 推荐）。viewLayout 并入 renderArtifact（可选）后
  // 仍全可选 → 可选（上方 pin 不变）。
  ok(JSON.stringify(railTiersForDefault("smartAutomation")) === JSON.stringify(["optional"]) && agg("smartAutomation") === "optional",
    `smartAutomation: all-optional members aggregate to 可选 (got ${JSON.stringify(railTiersForDefault("smartAutomation"))})`);
  ok(JSON.stringify(railTiersForDefault("interactionPresentation")) === JSON.stringify(["recommended", "optional"]) && agg("interactionPresentation") === "recommended",
    `interactionPresentation: input [推荐,可选] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("interactionPresentation"))})`);
  // 任务 783：用量监测卡——GLM Plan 用量（推荐）+ OpenCode Go 用量（未稳定）
  // 聚合出单枚推荐徽章。
  ok(JSON.stringify(railTiersForDefault("usageMonitoring")) === JSON.stringify(["recommended", "unstable"]) && agg("usageMonitoring") === "recommended",
    `usageMonitoring: input [推荐,未稳定] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("usageMonitoring"))} → ${String(agg("usageMonitoring"))})`);
  // 已退役垫底：合成布局钉「已退役」永不顶掉更强档位（默认布局已无 retired
  // 成员卡——722 点2 移除；此规则由合成数据守护，防止未来回填时翻转）。
  const synthetic: LabLayoutData = {
    version: 1,
    groups: [
      {
        key: "ui",
        labelKey: "settings.labGroup.ui",
        entries: [
          {
            id: "sessionWall",
            labelKey: "settings.sessionWall",
            tier: "optional",
            onKeys: ["experimentalSessionWall"],
            members: [
              { id: "modelCapabilityFilter", labelKey: "settings.modelCapabilityFilter", tier: "retired" },
            ],
          },
        ],
      },
    ],
  };
  ok(labEntryBadgeTier(synthetic, "sessionWall") === "optional",
    `retired never out-ranks a stronger tier (got ${String(labEntryBadgeTier(synthetic, "sessionWall"))})`);
  ok(labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, "preapproveManagedPaths") === null,
    "unknown id aggregates to no badge (null)");
}

// ⑤ pane wiring in SettingsPanel (46/46) + rail render site.
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../lab/labPaneRegistry.ts", import.meta.url)), "utf8");
  const wrapped = new Set([...panel.matchAll(/labLabel\("([a-zA-Z]+)"/g)].map((m) => m[1]));
  const missing: string[] = [];
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    // opencodeGoUsage：独立用量卡自带徽章；modelCapabilityFilter：任务 722
    // 点2 移除「已退役」徽章（只读展示行保留，无徽章）；
    // logManagement（tl 1097）：kind:param 参数卡的 pane 由专用
    // LogManagementLabPane 承接（当前占用行 + 打开目录按钮 + 永久+无限制
    // 警示行，超出通用 labLabel 包裹模式），徽章在组件内直接渲染——同
    // opencodeGoUsage 自带徽章的豁免口径。
    if (id !== "opencodeGoUsage" && id !== "modelCapabilityFilter" && id !== "logManagement" && !wrapped.has(id)) missing.push(id);
  }
  ok(missing.length === 0, `pane labels wrap every non-card id in SettingsPanel + the usage card covers opencodeGoUsage (missing: ${JSON.stringify(missing)})`);
  // 任务 774：rail 行渲染单枚聚合徽章（features 表预解析 badgeTier），
  // 多枚 .map 渲染必须消失——卡头不再出现两枚档位徽章。
  ok(panel.includes("badgeTier: labEntryBadgeTier(labLayoutResolved.layout, entry.id)"),
    "rail features table sources the aggregated badge tier from the resolved layout");
  // tl 1134：rail 行 memo 化后 TierBadge 落在 LabRailItem 子组件里，
  // translator prop 名随上下文改为 translator——断言按「单枚聚合徽章 + 无
  // .map 多枚渲染」的形态匹配（774 契约不变）。
  ok(panel.includes("{feature.badgeTier ? <TierBadge tier={feature.badgeTier}"),
    "rail rows render exactly one aggregated tier badge");
  ok(!panel.includes("labEntryBadgeTiers"), "the multi-badge rail mapping is gone from SettingsPanel");
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
