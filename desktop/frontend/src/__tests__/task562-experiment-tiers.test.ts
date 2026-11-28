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
  // tl 1174：jsonlZstd（冷会话 transcript zstd 归档，可选，默认关）补登——
  // 可选 +1（Go 侧渲染表已随件登记）。unstable 13→12 为基线校正（表内
  // 实际 12 项，13 系过时期望，2026-10-28 合并线核对六基线一致后修正）。
  // tl-1218：multi_edit 三条增强（multiEditErrorDetail / multiEditEvidenceNarrow /
  // multiEditSkipFailed，toolOptimizations 聚合页成员行）按可选档入表——
  // 可选 +3（Go 侧同步 69→72）。
  // 合并线并集解回调（2026-10-28）：主线 jsonlZstd 补登（58c19737d，
  // optional）× 1218 三新增 ⇒ 并集态 4/20/44/12/1 = 81 项（zcode 预警的
  // 「81/13」中 13 系误算——jsonlZstd 非 unstable，已按实值钉 12）。
  // tl1193b：pinnedIndex（pinned 常驻层索引化，实验室新「记忆与上下文」组
  // 首卡）按未稳定档入表——未稳定 12→13、总数 81→82（Go 侧同步 72→73）。
  // tl1309 卡②：runtimeObservability（「运行可观测」合并卡，纯前端容器 id）
  // 按推荐档入表——推荐 21→22、总数 82→83（Go 渲染表无条目，同 tabManagement
  // 豁免）。tl1309 卡③：resourceGovernance（「资源治理」合并卡容器，稳定）
  // + gcPercent（可选）+ idleReleaseMinutes（推荐）三 id 入表——稳定 4→5、
  // 推荐 22→23、可选 44→45、总数 83→86（三 id Go 渲染表均无条目，同
  // tabManagement 豁免）。
  // tl1309 卡④：sessionTaskPanels（「会话与任务面板」合并卡容器）按推荐档
  // 入表——推荐 23→24、总数 86→87（同 tabManagement 豁免）。
  // tl1338：sessionStorage（会话存储档位面）未稳定→稳定（用户 2026-11-01 拍
  // 板：fork 已锁 v3，卡内余项皆迁移工具非实验能力）——稳定 5→6、未稳定
  // 12→11、总数不变 87；Go 渲染表同步 LabTierStable（条目数不变 73）。
  // tl1453：dream（可选档）随 dream/distill 实验退役（宪法 Q1）出表——
  // 可选 45→44、总数 87→86（Go 渲染表同步 73→72）。
  // tl1487 一组（20261107 八组清单）：modelCapabilityFilter（retired 独苗）
  // 出表（退役 1→0、总数 -1）+ coldCacheCompact（冷缓存压缩，可选）入表
  // （可选 +1、总数 +1）→ 总数 86 不变；collabHub 未稳定→稳定（稳定 +1、
  // 未稳定 -1）、outputStyle 可选→推荐（推荐 +1、可选 -1）。
  // tl1487 二组：workflowTemplates（工作流模板，推荐）入表（总数 +1→87）；
  // tabModeTint 未稳定→可选；workflowPanel 未稳定→稳定；completionSummary/
  // autoLoadOlder/tableWrap/approvalSelect/sidebarHover×2/smoothScrollFollow/
  // draftRewrite 八项可选→推荐——稳定 8、推荐 34、可选 37、未稳定 8。
  // tl1487 三组：subagentPanel/subagentDetail/draftPersistence/selectionActions
  // 可选→推荐——推荐 39（含 inputSelection 容器）、可选 32，总数不变 87。
  // tl1487 五/六/八组：cdpDebugPort 未稳定→稳定、jsonlZstd 可选→推荐、
  // pinnedIndex 未稳定→可选——稳定 9、推荐 40、可选 32、未稳定 6，总数不变 87。
  // tl1487 七组：gcPercent + 工具优化六键可选→推荐（推荐 47、可选 25）；
  // pathRules（未稳定）随 tl134 整卡移除出表——未稳定 5、总数 87→86。
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：localServer/
  // subagentPolicy 未稳定→可选、splitView/heartbeatBackground/feedback 可选→
  // 推荐——推荐 47→50、可选 25（出 3 进 2 后再 +3 净变 25）、未稳定 5→3。
  // tl1733：memoryIndexSlim（可选）入表——可选 25→26、总数 87→88。
  // tl1746：tasklistPanel（未稳定）入表——未稳定 3→4、总数 88→89。
  // tl1718：composerToolsFold（composer 工具组默认折叠，推荐档）入表——
  // 推荐 50→51、总数 89→90。tl1772：decisionPatternsLayered（决策模式注入
  // 分层伴生开关，可选档）入表——可选 26→27、总数 90→91（同件 memoryIndexSlim
  // 移组不改注册表计数）。tl1787 件⑤/A+续作：rangesCompact+modelRangesCompact+
  // prepairAutosplice 三条入未稳定档——未稳定 4→7、总数 91→94（并集解
  // 20261124 撞 tl1772，数字跑实测定稿）。
  // 案C（tl1265 级单键升档，2026-11-27 用户拍板 A1 同意、A2 highSpeedModel
  // 不升）：sessionCollabAutoFold 可选→推荐——推荐 51→52、可选 29→28，
  // 总数不变 96。
  // tl1844 换代（d2 报告候选 A，task704-switch⑦ tl1309 自洽钉同款）：原快照钉
  // 9/52/28/7/0 每次升档/登记窗口必人肉跟（案C 第 10 处漏跟实锤 848/849）→
  // 改「活注册表实分布 ≡ LAB_TIER_COUNTS」自洽断言：两侧均活取，计数值步进
  // 不再需要跟本文件；快照数值史由 git 承载，xlsx 表A 外部对账语义由 ③ 双侧
  // 一致断言把守（d2 报告候选 A 代价注）。
  ok(LAB_TIER_COUNTS.stable === counts.stable && LAB_TIER_COUNTS.recommended === counts.recommended && LAB_TIER_COUNTS.optional === counts.optional && LAB_TIER_COUNTS.unstable === counts.unstable && LAB_TIER_COUNTS.retired === counts.retired,
    `LAB_TIER_COUNTS matches the live register distribution (got live ${JSON.stringify(counts)} vs ${JSON.stringify(LAB_TIER_COUNTS)})`);
  // tl1746：tasklistPanel 入表 → 88→89。tl1718：composerToolsFold 入表 → 89→90。
  // tl1772：decisionPatternsLayered 入表 → 90→91。tl1787：三条入表 → 91→94。
  // tl1817：mimoTts（朗读 MiMo TTS 传输层，可选档）入表 → 94→95。
  // tl1816：fallbackModel（备用模型开关行，可选档）入表 → 95→96。
  // tl1844 换代：原快照钉「恰 96 项」登记窗口每件必跟 → 自洽断言「注册表总数
  // ≡ LAB_TIER_COUNTS 五档合计」——合计 < 总数即有档位值不在五档计数面内
  // （新档未接入计数/导出遗漏），条目增删不再需要跟本钉（总数史由 git 承载）。
  const registerTotal = Object.keys(EXPERIMENT_FEATURE_TIERS).length;
  const countedTotal = LAB_TIER_ORDER.reduce((sum, tier) => sum + LAB_TIER_COUNTS[tier], 0);
  ok(registerTotal === countedTotal, `register total (${registerTotal}) equals the five-tier LAB_TIER_COUNTS sum (${countedTotal})`);
  // tl1844：原 LAB_TIER_COUNTS 快照钉（9/52/28/7/0）与上方自洽断言同不变量，
  // 随换代合并退役（替代=上方「活分布 ≡ LAB_TIER_COUNTS」，同文件同覆盖）。
  // 案C 前它读常量字面：注册表单点突变时常量未跟即假绿（tl1844 突变实测）。
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
  // tl1487：completionSummary（二组⑤）+ subagentPanel/selectionActions
  // （三组）可选→推荐——pick 拆分 3 稳定 + 12 推荐 + 1 可选（splitView）。
  // tl1265 第2轮：splitView 可选→推荐（图墙 pick 随之变推荐）——
  // pick 拆分 3 稳定 + 13 推荐 + 0 可选。
  ok(pickCounts.stable === 3 && pickCounts.recommended === 13 && pickCounts.optional === 0,
    `pick tier split is 稳定 3 + 推荐 13 + 可选 0 (got ${JSON.stringify(pickCounts)})`);
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
  // tl-1218：multi_edit 三条增强入表（两侧共享，工具优化聚合页成员行）——
  // Go registry 69→72。tl1193b：pinnedIndex 入表（两侧共享，记忆与上下文组
  // 新组首卡）——Go registry 72→73。tl1309 卡②：runtimeObservability 为纯
  // 前端容器 id（Go 渲染表无条目，同 tabManagement 豁免），Go registry 仍 73。
  // tl1397：rangesCompact（ranges 小段增量压缩实验开关，experimental_ranges_compact，
  // 默认关）只在 Go 侧登记——73→74。tl1419：modelRangesCompact（模型驱动 ranges
  // 折叠实验开关+滑动窗口两家族子键，默认关）只在 Go 侧登记——74→75。两者前端
  // experimentTiers 均无该 id（grep 零引用，无处挂徽章），同 networkRetryBudget
  // 口径计入 Go-only config-only 豁免（后续设置页入口补前端注册时两侧转共享，
  // 此处同步收回）——tl1424② 预存红清理。
  // tl1453：dream（optional）随实验退役出 Go 表——75→74。
  // tl1487 一组：Go 表条目数不变 74（collabHub 翻档、outputStyle 翻档均
  // 不增减条目；coldCacheCompact 为前端独有新条目）。二组：翻档仍不增减
  // 条目；workflowTemplates（工作流模板）为前端独有新条目（Go 侧仍为
  // workflowPanel 家族子键）。四组：opencodeGoUsage 渲染表条目随键退役移除
  // ——Go 表 74→73（前端转 feOnly 展示行，用量查询常开无门）。tl1487 七组：
  // pathRules 条目随键退役移除——Go 表 73→72。tl1453 后 recallTool /
  // zeroSummaryCompact 两 unstable 条目入 Go 表而本 pin 漏跟（基点
  // c9bb09cd23 实值 75=预存红，20261121 tl1733 收口）；tl1733：
  // memoryIndexSlim（记忆注入瘦身，提效组，可选档）两侧共享入表——75→76。
  // tl1741 撤七条(autopilot/sessionCollab/collabHub/workflowPanel/cdpDebugPort/sessionStorage/runtimeReuse) + tl1746 加 tasklistPanel：76-7+1=70。
  // tl1718：composerToolsFold 两侧共享入表——70→71。
  // tl1772：decisionPatternsLayered 两侧共享入表——71→72。tl1787 续作：
  // prepairAutosplice 两侧共享入表——72→73（并集解 20261124）。tl1817：
  // mimoTts 两侧共享入表——73→74。
  // tl1844 换代：原快照钉「恰 74 条」跨语言登记窗口每件必跟 → 下界棘轮（811/
  // task705「计数不倒退」先例）：跌破即 Go 注册表解析丢条目（③ goOnly/feOnly
  // 精确清单同红双保险）；有意的退役批次在退役 commit 内随件下调本下界。
  // tl1855：startupSplash 条目随撤开关退役——74→73。
  ok(Object.keys(goTiers).length >= 73, `Go registry parses to >= 73 entries (tl1844 ratchet, exact 73 @ tl1855; got ${Object.keys(goTiers).length})`);

  const feIds = Object.keys(EXPERIMENT_FEATURE_TIERS).sort();
  const goIds = Object.keys(goTiers).sort();
  const goOnly = goIds.filter((id) => !feIds.includes(id));
  const feOnly = feIds.filter((id) => !goIds.includes(id));
  // 并集解回调（2026-10-28）：jsonlZstd 前端已随 58c19737d 注册 ⇒ Go-only
  // 收回 3 条（去 jsonlZstd）。tl1397/tl1419：rangesCompact/modelRangesCompact
  // 加入（同上 config-only 豁免口径，5 条）。tl1733：recallTool /
  // zeroSummaryCompact 两 Go-only 条目补入豁免名单（基点预存红收口，
  // 7 条）；memoryIndexSlim 两侧共享，不入名单。
  // tl1787 件⑤/A：rangesCompact/modelRangesCompact 设置页入口落地（上下文
  // 治理卡两行）——豁免收回两条（7→5），两侧转共享。
  ok(JSON.stringify(goOnly) === JSON.stringify(["interruptedAutoResume", "networkRetryBudget", "recallTool", "sessionCwdFollow", "zeroSummaryCompact"]),
    `Go-only ids are exactly the config-only exemption (got ${JSON.stringify(goOnly)})`);
  // tl 1132：startupLoading（「启动与加载」纯前端容器 id，Go 渲染表无条目，
  // 同 tabManagement 豁免）加入 feOnly。tl1309 卡②：runtimeObservability 加入。
  // tl1309 卡③：resourceGovernance（容器）+ gcPercent/idleReleaseMinutes（参数
  // 卡成员 id，字段绑定在 labParamFields，Go 渲染表无条目——775 参数卡同款
  // 豁免）三者加入。tl1487 一组：modelCapabilityFilter 出表（feOnly 收回）；
  // coldCacheCompact（冷缓存压缩，compressOpt 附属键族，Go 无独立条目）入表。
  // tl1487 二组：workflowTemplates（工作流模板，Go 侧为 workflowPanel 家族
  // 子键）入表。tl1487 四组：opencodeGoUsage（用量展示行，配置键整链退役）
  // 转入 feOnly。
  // tl1741：八项撤开关 id 转入 feOnly（Go 表条目退役，前端注册表标注保留
  // ——图墙/徽章展示面用，拍板条款②「注册表不动」）；idleReleaseMinutes/
  // gcPercent 随资源治理拆卡回散条 param 卡（仍 feOnly）。tl1816：
  // fallbackModel（备用模型开关行徽章，模型目标行在设置→模型偏好）入 feOnly。
  // tl1855：startupLoading（整卡退役）与 runtimeReuse（展示行下架出表）双
  // id 出 feOnly——两侧注册表同步退役，清单 27→25。随件收口预存红：tl1835
  // goalTokenBudgetM 入注册表时本清单漏跟（goal_token_budget 为裸 token 键、
  // Go 渲染表无条目，本属 feOnly——992 漏同步 drift 收口同款）。
  ok(JSON.stringify(feOnly) === JSON.stringify(["autopilot", "cdpDebugPort", "coldCacheCompact", "collabHub", "fallbackModel", "gcPercent", "glmPlanUsage", "goalTokenBudgetM", "heartbeatRotation", "idleReleaseMinutes", "inputSelection", "interactionPresentation", "logManagement", "opencodeGoUsage", "resourceGovernance", "runtimeObservability", "searchHistory", "sessionCollab", "sessionStorage", "sessionTaskPanels", "smartAutomation", "tabManagement", "usageMonitoring", "viewLayout", "workflowPanel", "workflowTemplates"]),
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
  // tl-1218：toolOptimizations 卡补 multi_edit 三条增强成员行——47→50。
  // tl1309 卡①：optimisticParallel 散条并入 contextGovernance 卡——50→51。
  // tl1309 卡②：monitoring 并 runtimeObservability（eventsRotation/
  // trajectoryView 为跨卡平移，成员总数净零）——51→52。tl1309 卡③：六成员并
  // 「资源治理」卡（复用池/孤儿回收/闲置释放/GC 四迁出独立卡 + 两参数卡）——
  // 52→58。
  // tl1309 卡④：todoSidebar 并 tabManagement、三面板并 sessionTaskPanels——58→62。
  // tl1453：dream 成员行随实验退役出 smartAutomation 卡——62→61。
  // tl1741：成员 61→52（sessionCollabAutoFold/workflowPanel/cdpDebugPort/
  // sessionStorage/runtimeReuse 五成员行随卡退役；resourceGovernance 五成员
  // 回散条出 members——orphanHandling/idleReleaseMinutes/gcPercent/acpListen/
  // localServer）。
  // tl1718 composerToolsFold +1；tl1741r2 打回返修：五成员行随卡体回归 +
  // resourceGovernance 六成员回卡（runtimeReuse=无开关展示行）——merged
  // cards 成员数以合并态实测为准（ok 值首猜 63，跑批校正）。
  // tl1772：decisionPatternsLayered 成员行入 smartAutomation 卡（+1）；
  // memoryIndexSlim 独立卡移入 safetyCostControl 卡成员行（+1）——63→65。
  // tl1772r2：decisionPatternsLayered 成员行 smartAutomation→safetyCostControl
  // 卡平移——成员总数 65 不变。tl1787 件⑤/A+续作：contextGovernance 卡补
  // ranges 两成员行+runtimeObservability 卡补 prepairAutosplice 成员行——
  // 65→68（并集解 20261124）。tl1817：interactionPresentation 卡补 mimoTts
  // 成员行——68→69。
  // // 65→68（并集解 20261124）。tl1816：pinnedIndex 独立卡移组 safetyCostControl
  // tl1844 换代：原快照钉「恰 70 成员」布局登记窗口每件必跟 → 下界棘轮（同
  // ③ Go 条目钉口径）：跌破即有卡成员行被删（有意退役批次随件下调）；成员
  // ⊆ 注册表由下方 covered 自洽断言把守。tl1855：startupSplash/runtimeReuse
  // 两成员行下架 + acpListen 行移组本地服务页——70→67。
  ok(members.length >= 67, `merged cards carry >= 67 member features (tl1844 ratchet, exact 67 @ tl1855; got ${members.length})`);  ok(members.every((id) => isTierFeatureId(id)), "every merged member is a registered 表A feature");
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
  // tl-1218：multi_edit 三条增强叠加入表 → 80（基线 77 口径 +3）；
  // 并集解回调（58c19737d jsonlZstd 已计入主线 78 口径）→ 78+3=81。
  // tl1193b：pinnedIndex 独立 rail 入口计入（记忆与上下文新组）→ 82。
  // tl1487 七组：pathRules 出表（union/registry 同撤）→ covered 87→86。
  // tl1265 第2轮：editResend（tl1544 入表，此前漏同步本计数）补齐 86→87。
  // tl1733：memoryIndexSlim rail 入口计入 → 87→88。
  // tl1746：tasklistPanel rail 入口计入 → 88→89。
  // tl1718：composerToolsFold 入表计入 → 89→90。
  // tl1772：decisionPatternsLayered 入表计入 → 90→91（memoryIndexSlim 移组
  // 不改 covered——rail 入口转成员行，id 仍覆盖）。tl1787：ranges×2+
  // prepairAutosplice 入表计入 → 91→94（并集解 20261124）。tl1817：
  // mimoTts 入表计入 → 94→95。
  // // prepairAutosplice 入表计入 → 91→94（并集解 20261124）。tl1816：
  // tl1844 换代：原快照钉「覆盖 96」→ 自洽断言「覆盖集 ≡ 注册表全集」——
  // 语义=每个注册 id 都有 rail 入口或卡成员位（成员混入未注册 id 即红，
  // members ⊆ registry），总数步进不再需要跟本钉。
  ok(covered.size === Object.keys(EXPERIMENT_FEATURE_TIERS).length, `rail entries cover the whole register (${covered.size}/${Object.keys(EXPERIMENT_FEATURE_TIERS).length})`);
  // 任务 774 徽章聚合（设计稿 §4.3）：rail 行/卡头只渲染一枚最高档 =
  // max(卡 tier, members tiers)（推荐 > 可选 > 未稳定 > 已退役）。断言结构
  // 成对：railTiersForDefault 钉「聚合输入」（卡上实际存在的档位清单，含
  // 子项档位——它们仍在布局里，只是不上卡头），labEntryBadgeTier 钉「卡头
  // 输出」（单枚=清单首位）。合并卡的多档成员不再让卡头出现两枚徽章。
  const agg = (id: string) => labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, id);
  // tl1787 件⑤/A：ranges 两成员行（未稳定档）入卡——聚合输入 [推荐,可选,
  // 未稳定]，卡头仍单枚推荐（推荐压过未稳定）。
  ok(JSON.stringify(railTiersForDefault("contextGovernance")) === JSON.stringify(["recommended", "optional", "unstable"]) && agg("contextGovernance") === "recommended",
    `contextGovernance: input [推荐,可选,未稳定] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("contextGovernance"))} → ${String(agg("contextGovernance"))})`);
  // 任务 1005 → tl1741r2：autopilot 卡体回归（无主开关）——fullAccess 仍是
  // 唯一带主开关的 stable 主卡（其余 stable 卡无开关行，见 561 M8 清单）。
  ok(JSON.stringify(railTiersForDefault("fullAccess")) === JSON.stringify(["stable"]) && agg("fullAccess") === "stable", "standalone entry badges itself (fullAccess, tl1741r2 后唯一带开关的 stable 主卡)");
  ok(railTiersForDefault("preapproveManagedPaths").length === 0 && agg("preapproveManagedPaths") === null, "non-表A id (task-364 domain) shows no badge");
  // 任务 722 点2：「已退役」徽章自模型策略卡移除——只余 highSpeedModel 的可选档。
  ok(JSON.stringify(railTiersForDefault("modelStrategy")) === JSON.stringify(["optional"]) && agg("modelStrategy") === "optional",
    `modelStrategy drops the retired badge (got ${JSON.stringify(railTiersForDefault("modelStrategy"))})`);
  // 任务 722 点5/6 + 774：compactModel（可选）/messageMerge（推荐）/心跳轮换
  // （可选）并入后，卡头只聚合出推荐一枚。
  ok(JSON.stringify(railTiersForDefault("safetyCostControl")) === JSON.stringify(["recommended", "optional"]) && agg("safetyCostControl") === "recommended",
    `safetyCostControl: input [推荐,可选] aggregates to the single 推荐 badge after the 722 folds (got ${String(agg("safetyCostControl"))})`);
  // tl1741：sessionCollab 卡（含 autoFold 成员）随撤开关移除——聚合断言随件退役。
  // 任务 774 验收件②：子代理套件（无自有档）按最高档单枚。tl1487 三组①：
  // 成员 [推荐×2, 可选, 未稳定] → 单枚 推荐。tl1265 第2轮：subagentPolicy
  // 未稳定→可选（dec-73872eee136e22de）——输入变 [推荐, 可选]，终值仍推荐。
  ok(JSON.stringify(railTiersForDefault("subagentSuite")) === JSON.stringify(["recommended", "optional"]) && agg("subagentSuite") === "recommended",
    `subagentSuite aggregates to the single 推荐 badge after tl1265 (got ${String(agg("subagentSuite"))})`);
  // 其余合并卡逐卡钉聚合终值（722 布局现值）。
  // 任务 971：restartUpdate 迁至工具优化聚合页——updateFeedback 仅余 feedback
  // （可选档），聚合终值 推荐→可选。任务 992（用户 1018 21:34）：restartUpdate
  // 归位回本卡——聚合终值回升 推荐（推荐压过可选；本断言 2026-10-19 随 1005
  // 窗口一并补同步，992 落地时本 pin 漏跟）。
  ok(agg("updateFeedback") === "recommended", `updateFeedback aggregates to 推荐 after the 992 move-back (got ${String(agg("updateFeedback"))})`);
  // 任务 971：toolOptimizations 聚合页（readBack 推荐 + 证据门未稳定 + multi_edit
  // 直注可选）——卡头聚合单枚 推荐。（restart_update 曾入聚合输入，992 归位
  // updateFeedback 卡后不再是本卡成员；tl1805 渲染面补同步，聚合输入不变。）
  ok(agg("toolOptimizations") === "recommended", `toolOptimizations hub aggregates to 推荐 (got ${String(agg("toolOptimizations"))})`);
  // tl1309 卡②：eventsRotation 迁出 sessionStore——仅余未稳定成员，聚合终值
  // 可选→未稳定。tl1338：唯一成员 sessionStorage 未稳定→稳定（用户 2026-11-01
  // 拍板），容器无自有档 ⇒ 聚合输入恰为 [稳定] 一枚、聚合终值 稳定；导航 rail
  // 与卡头同源 labEntryBadgeTier（ExperimentalLabSection 586 行）随之联动。
  // 断言输入清单精确等于 ["stable"]：若日后有人往本卡塞回未稳定成员致徽章
  // 回落，此处即红（连带①「无其他成员阻滞」钉死）。
  // tl1741r2：sessionStorage 成员行（稳定展示行）随卡体回归——sessionStore
  // 聚合输入恰为 [稳定] 一枚、聚合终值 稳定（916 面板锁语义由迁移工具面延续）。
  ok(JSON.stringify(railTiersForDefault("sessionStore")) === JSON.stringify(["stable"]) && agg("sessionStore") === "stable",
    `sessionStore: sessionStorage member restored by tl1741r2 ⇒ single stable badge (got ${JSON.stringify(railTiersForDefault("sessionStore"))} → ${String(agg("sessionStore"))})`);
  // tl1309 卡②：运行可观测卡聚合输入 [推荐(监控), 可选(events 瘦身/轨迹观测)]
  // → 卡头单枚 推荐。
  // tl1787 续作：prepairAutosplice（未稳定档）入卡——聚合输入 [推荐,可选,
  // 未稳定]，卡头仍单枚推荐。
  ok(JSON.stringify(railTiersForDefault("runtimeObservability")) === JSON.stringify(["recommended", "optional", "unstable"]) && agg("runtimeObservability") === "recommended",
    `runtimeObservability: input [推荐,可选,未稳定] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("runtimeObservability"))} → ${String(agg("runtimeObservability"))})`);
  // tl1741r2：资源治理卡卡体回归（无主开关卡）——六成员回卡。tl1855 三件
  // 减法（复用池展示行下架+ACP 移组本地服务页）后聚合输入
  // [推荐(孤儿/GC/闲置), 可选(本地服务)] → 卡头单枚 推荐（容器档随成员最高
  // 档聚合规则 稳定→推荐）。
  ok(JSON.stringify(railTiersForDefault("resourceGovernance")) === JSON.stringify(["recommended", "optional"]) && agg("resourceGovernance") === "recommended",
    `resourceGovernance: tl1855 input [推荐,可选] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("resourceGovernance"))} → ${String(agg("resourceGovernance"))})`);
  // tl1309 卡④：标签页管理（推荐容器+推荐成员）单枚推荐；会话与任务面板
  // 聚合输入 tl1487 二组⑤ 后 [稳定(工作流), 推荐(图墙/完成摘要)] → 卡头单枚 稳定。
  ok(JSON.stringify(railTiersForDefault("tabManagement")) === JSON.stringify(["recommended"]) && agg("tabManagement") === "recommended",
    `tabManagement: recommended container + recommended member stays the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("tabManagement"))})`);
  // tl1741r2：workflowPanel 成员行（稳定展示行）回归——sessionTaskPanels 输入
  // [稳定(工作流), 推荐(图墙/完成摘要)] → 卡头单枚 稳定。
  ok(JSON.stringify(railTiersForDefault("sessionTaskPanels")) === JSON.stringify(["stable", "recommended"]) && agg("sessionTaskPanels") === "stable",
    `sessionTaskPanels: tl1741r2 input [稳定,推荐] aggregates to the single 稳定 badge (got ${JSON.stringify(railTiersForDefault("sessionTaskPanels"))})`);
  // tl1487 五组 → tl1741r2：cdpDebugPort 成员行（稳定展示行）回归——devDebug
  // 聚合输入 [稳定, 未稳定] → 卡头单枚 稳定。
  ok(JSON.stringify(railTiersForDefault("devDebug")) === JSON.stringify(["stable", "unstable"]) && agg("devDebug") === "stable",
    `devDebug: tl1741r2 input [稳定,未稳定] aggregates to the single 稳定 badge (got ${JSON.stringify(railTiersForDefault("devDebug"))})`);
  // 任务 782：界面组三张合并卡逐卡钉聚合终值——容器徽章 = 成员最高档
  // （searchHistory 三成员 tl1487 二组⑥ 后全推荐 → 推荐；inputSelection
  // 全可选成员 → 可选）。
  // tl 1125：viewLayout 容器档位「可选 → 推荐」（用户指令），聚合输入
  // [推荐,可选×8]，卡头终值 单枚 推荐（推荐压过成员可选）。tl1487 二组②④
  // 后成员仅剩 splitView/sendTargetSelect 可选，输入仍 [推荐,可选]。
  ok(JSON.stringify(railTiersForDefault("searchHistory")) === JSON.stringify(["recommended"]) && agg("searchHistory") === "recommended",
    `searchHistory: tl1487 all-recommended input aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("searchHistory"))} → ${String(agg("searchHistory"))})`);
  ok(JSON.stringify(railTiersForDefault("viewLayout")) === JSON.stringify(["recommended", "optional"]) && agg("viewLayout") === "recommended",
    `viewLayout: tl 1125 推荐 container + optional members aggregate to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("viewLayout"))} → ${String(agg("viewLayout"))})`);
  ok(JSON.stringify(railTiersForDefault("inputSelection")) === JSON.stringify(["recommended"]) && agg("inputSelection") === "recommended",
    `inputSelection: tl1487 三组 all-recommended members aggregate to 推荐 (got ${JSON.stringify(railTiersForDefault("inputSelection"))})`);
  // 任务 961：automation/efficiency 两张合并卡钉聚合终值——容器徽章 =
  // 成员最高档。tl1265 第2轮：heartbeatBackground 可选→推荐（dec-73872eee136e22de）
  // ——smartAutomation 输入 [推荐,可选] → 终值 可选→推荐。
  ok(JSON.stringify(railTiersForDefault("smartAutomation")) === JSON.stringify(["recommended", "optional"]) && agg("smartAutomation") === "recommended",
    `smartAutomation: heartbeatBackground 升推荐后聚合到推荐 (got ${JSON.stringify(railTiersForDefault("smartAutomation"))})`);
  ok(JSON.stringify(railTiersForDefault("interactionPresentation")) === JSON.stringify(["recommended", "optional"]) && agg("interactionPresentation") === "recommended",
    `interactionPresentation: input [推荐,可选] aggregates to the single 推荐 badge (got ${JSON.stringify(railTiersForDefault("interactionPresentation"))})`);
  // 任务 783：用量监测卡——GLM Plan 用量（推荐）+ OpenCode Go 用量（20261030 用户裁升推荐）
  // 聚合出单枚推荐徽章。
  ok(JSON.stringify(railTiersForDefault("usageMonitoring")) === JSON.stringify(["recommended"]) && agg("usageMonitoring") === "recommended",
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
    // tl1741r2 卡体回归后 labLabel 包装断言恢复（含 sessionCollabAutoFold/
    // gcPercent/idleReleaseMinutes 成员行）；tasklistPanel=专用 pane 豁免
    // （tl1746 合并态条目）；opencodeGoUsage/modelCapabilityFilter/logManagement
    // 三真豁免保留。safetyCostControl=tl1816 豁免：总开关行（原 labLabel
    // 宿主）随方案A 退役，卡徽章由 rail 聚合面承载（labEntryBadgeTier）。
    // acpListen=tl1855 豁免：行移组「本地服务」页（设置面板常规 tab，
    // LocalServerPage 承接，下方专属断言把守徽章——opencodeGoUsage 卡先例）。
    // resourceGovernance=tl1855 豁免：卡头聚合说明行随件③①下架（组头已
    // 表达），卡徽章由 rail 聚合面承载（safetyCostControl 同款豁免）。
    if (["opencodeGoUsage", "modelCapabilityFilter", "logManagement",
         "tasklistPanel", "safetyCostControl", "acpListen", "resourceGovernance"].includes(id)) continue;
    if (!wrapped.has(id)) missing.push(id);

  }
  ok(missing.length === 0, `pane labels wrap every non-card id in SettingsPanel + the usage card covers opencodeGoUsage (missing: ${JSON.stringify(missing)})`);
  // tl1855：acpListen 徽章随「本地服务」页自带（opencodeGoUsage 卡同款契约
  // ——TierBadge 直读注册表，两视图同源）。
  const localServerPage = readFileSync(fileURLToPath(new URL("../components/LocalServerPage.tsx", import.meta.url)), "utf8");
  ok(localServerPage.includes("EXPERIMENT_FEATURE_TIERS.acpListen"), "local services page badges the resident ACP listener (tl1855)");
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
