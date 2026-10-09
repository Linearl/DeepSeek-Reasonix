// Run: npx tsx src/__tests__/task563-lab-picks-wall.test.ts
// 任务 563 acceptance harness (lab picks wall: three card elements + detail
// dialog + suggest badge):
//  ① 16 cards, each carrying the 表B three elements — tier badge (562) /
//     one-line effect (col 6) / live on/off state read from the features
//     render table (never hardcoded) (acceptance 1);
//  ② the detail dialog opens/closes, its copy is 表B col 7 VERBATIM, and the
//     layer portals to body so the scrolling host can never clip it
//     (acceptance 2);
//  ③ the 「建议开启」badge fires ONLY for recommended-tier picks that are
//     currently off — both states constructed (acceptance 3);
//  ④ a new lab item (added to the tier register in a simulation) never joins
//     the wall — the pick list is the human-curated LAB_WALL_PICKS tuple, and
//     the yaml labPicks section carries words, never cards (acceptance 4);
//  ⑤ locale keys for the state/suggest labels exist in all three dialects.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  EXPERIMENT_FEATURE_TIERS,
  LAB_WALL_PICKS,
  LAB_TIER_ORDER,
  suggestEnable,
  type LabTier,
  type LabWallPickId,
  type TierFeatureId,
} from "../lib/experimentTiers";
import { parseForkFeaturesYaml, labPickCopyFor } from "../lib/forkFeaturesYaml";
import { labWallOnFor, LabWallOnContext } from "../lib/labWallOn";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const wallSource = readFileSync(fileURLToPath(new URL("../components/LabPicksWall.tsx", import.meta.url)), "utf8");
const dialogSource = readFileSync(fileURLToPath(new URL("../components/LabPickDetailDialog.tsx", import.meta.url)), "utf8");
const panelSource = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const yamlText = readFileSync(fileURLToPath(new URL("../../public/fork-features.yaml", import.meta.url)), "utf8");

console.log("\ntask 563 lab picks wall");

// ① 16 cards × three elements, live state.
{
  ok(LAB_WALL_PICKS.length === 16, `wall keeps exactly 16 picks (got ${LAB_WALL_PICKS.length})`);
  ok(wallSource.includes("lab-picks-wall__card-effect"), "card renders the effect line (表B col 6)");
  ok(wallSource.includes("lab-picks-wall__card-state") && wallSource.includes('data-on={on ? "true" : "false"}'),
    "card renders the live state pill");
  ok(wallSource.includes("useLabWallOn()") && wallSource.includes("labWallOnFor(wallOn, id)"),
    "card state is read live through the wall-on context (not hardcoded)");
  ok(!/sessionWall:\s*(true|false)/.test(wallSource), "no hardcoded per-pick boolean in the wall component");
  // The provider side derives from the features render table…
  ok(panelSource.includes("LabWallOnContext.Provider value={labWallOnById}"),
    "ExperimentalSection provides the wall-on map");
  ok(panelSource.includes("features.find((f) => f.id === pick)") && panelSource.includes("map[pick] = direct.on"),
    "wall-on map reads the live features render table");
  // …and the four 561-merged members read their OWN keys, never the merged OR-light.
  for (const [pick, expr] of [
    ["budgetControl", "Boolean(s.experimentalContextBudget) || Boolean(s.experimentalResearchBudget)"],
    ["compressOpt", "Boolean(s.experimentalProactiveCompact) || Boolean(s.experimentalColdCacheCompact)"],
    ["restartUpdate", "Boolean(s.experimentalRestartUpdate)"],
    ["subagentPanel", "Boolean(s.experimentalSubagentPanel)"],
  ] as const) {
    ok(panelSource.includes(`case "${pick}":`) && panelSource.includes(expr),
      `merged member ${pick} reads its own keys`);
  }
}

// ② detail dialog: copy = 表B col 7 verbatim, open/close, unclipped layer.
{
  // 表B「实验室特性-档位与图墙-20261006」cols 6/7, pinned verbatim here so any
  // drift between yaml and the table fails this suite.
  const TABLE_B: Readonly<Record<LabWallPickId, [effect: string, detail: string]>> = {
    sessionWall: ["一屏总览所有会话，点开即跳转", "会话网格（搜索 / 按项目 / 按最近活跃）；命令面板另插「跳转会话」项"],
    tabCompress: ["标签再多也不挤，自动逐级降宽", "档位 176→148→122→100→84；tier ≥3 时隐藏 plan/goal/auto/yolo 徽章"],
    todoSidebar: ["待办、产物、引用全收进右侧面板", "todo 列表搬进 dock + Artifacts / References 只读标签"],
    promptHistoryPicker: ["点一下时钟图标，历史提问任你挑", "历史插入光标处；普通 ArrowUp 只在空 composer 才触发浏览"],
    monitoring: ["会话与性能异常，一眼看出", "合并入口（灯 = 会话监控 OR 性能监控）；perf 每 5s 采样写 logs/perf/、每 60s 落 heap profile"],
    restartUpdate: ["换新版本不用手动装", "门控发布 / 切换 / 删除版本整族操作；状态栏按钮入口"],
    budgetControl: ["长任务不再超预算", "每轮前置预算块 + 研究预算扩展（extend_research_budget 对模型可见）"],
    compressOpt: ["长会话越聊越顺", "主动压缩（用配置冷却取代硬编码 10 分钟）+ 冷缓存压缩；⚠️ 依赖 traceAsState"],
    messageMerge: ["多条引导消息不再刷屏", "inbox 合并（逐条 / 同发送者 / 全部）+ 引导消息合并"],
    autopilot: ["交给它自己干，你只管看", "非值守运行 + 截止时间 + 代理审批（破坏性 / 外联 / 凭据类自动拒绝）；⚠️ 需 YOLO 审批模式"],
    sessionCollab: ["多个会话之间能互相传话", "talk_to_session 等 5 个工具 + 通讯录提示块；桌面侧投递 pump"],
    fullAccess: ["不再反复点审批（⚠️ 安全权衡）", "写根集合解除边界 + bash 沙箱关闭；仍保留 session-data guard / ProtectedWriteRoots"],
    splitView: ["一个窗口并排看两个会话", "tab 右键菜单插入 / 移除分屏；⚠️ off 不会关闭已存在的分屏"],
    subagentPanel: ["谁在跑、跑到哪，右栏一目了然", "dock 的 Subagents 标签；ended 卡片默认折叠、running 自动展开"],
    selectionActions: ["选中一段文字，直接翻译或解释", "选区浮卡（Translate / Explain）+ 右键菜单项"],
    completionSummary: ["干完活自动给一张总结卡", "完成摘要卡；⚠️ 历史回放时不显示"],
  };
  const yaml = parseForkFeaturesYaml(yamlText);
  ok(yaml.labPicks.length === 16, `yaml labPicks parses 16 entries (got ${yaml.labPicks.length})`);
  ok(yaml.groups.length === 5 && Object.keys(yaml.byId).length === 9,
    "intro wall untouched by the labPicks section (groups 5 / byId 9)");
  let verbatim = true;
  for (const id of LAB_WALL_PICKS) {
    const copy = labPickCopyFor(yaml, id);
    const [effect, detail] = TABLE_B[id];
    if (!copy || copy.effect !== effect || copy.detail !== detail) { verbatim = false; break; }
  }
  ok(verbatim, "every pick's effect/detail is byte-equal to xlsx 表B cols 6/7");
  ok(wallSource.includes("{openPick ? (") && wallSource.includes("<LabPickDetailDialog") && wallSource.includes("onClose={() => setOpenPick(null)}"),
    "card click opens the dialog, onClose closes it");
  ok(dialogSource.includes("createPortal(") && dialogSource.includes("document.body"),
    "detail dialog portals to body (529 lesson: host overflow can never clip it)");
  ok(dialogSource.includes('addEventListener("keydown", onKey, true)') && dialogSource.includes("e.stopPropagation()"),
    "Escape is capture-phase and shielded: one Escape closes only the top layer");
  ok(dialogSource.includes('onClick={onClose}') && dialogSource.includes('onClick={(e) => e.stopPropagation()}'),
    "backdrop click closes, dialog body clicks stay");
  ok(dialogSource.includes("settings.forkFeaturesIntro.close"), "close button reuses the intro-dialog locale key");
}

// ③ suggest badge: recommended AND off — both states constructed.
{
  ok(suggestEnable("recommended", false) === true, "recommended + off ⇒ badge shows");
  ok(suggestEnable("recommended", true) === false, "recommended + on ⇒ no badge");
  for (const tier of LAB_TIER_ORDER.filter((t) => t !== "recommended") as LabTier[]) {
    ok(suggestEnable(tier, false) === false && suggestEnable(tier, true) === false,
      `${tier} never suggests (off and on both constructed)`);
  }
  ok(wallSource.includes("suggestEnable(EXPERIMENT_FEATURE_TIERS[id], on)"),
    "card badge calls the one pure rule with the register tier + live state");
  ok(wallSource.includes("lab-picks-wall__card-suggest") && dialogSource.includes("lab-pick-dialog__suggest"),
    "badge renders on card and mirrors into the dialog");
}

// ④ new lab items never auto-join the wall.
{
  // Simulate a future register growth (what 任务562's gate forces to be tagged)
  // and prove the wall neither lists it nor grows.
  const grown = { ...EXPERIMENT_FEATURE_TIERS, demoFutureSwitch: "recommended" } as
    Readonly<Record<TierFeatureId | "demoFutureSwitch", LabTier>>;
  // 任务 603 grew the register to 47 (toolOptimizations); 任务 621 added
  // tabModeTint (504, Go-side tier all along) → 48; 任务 517 merged the three
  // B-group ids into safetyCostControl → 46; 任务 705 → 48（折叠开关）；任务
  // 707 → 49（压缩模型指定）；+1 simulated = 50.
  ok(Object.keys(grown).length === 50 && !LAB_WALL_PICKS.includes("demoFutureSwitch" as LabWallPickId),
    "simulated new tier item is absent from LAB_WALL_PICKS");
  ok(LAB_WALL_PICKS.every((id) => Object.prototype.hasOwnProperty.call(EXPERIMENT_FEATURE_TIERS, id)),
    "every wall pick is a registered tier item (no orphans)");
  const mapCalls = wallSource.split("LAB_WALL_PICKS.map").length - 1;
  ok(mapCalls === 1 && !wallSource.includes("Object.keys(EXPERIMENT_FEATURE_TIERS)"),
    "the wall renders LAB_WALL_PICKS.map and nothing else (yaml cannot grow it)");
  // The yaml side cannot smuggle a card in either: words only.
  const smuggled = parseForkFeaturesYaml(yamlText + "\n  - id: demoFutureSwitch\n    effect: 演示\n    detail: 演示详情\n");
  ok(smuggled.labPicks.length === 17 && labPickCopyFor(smuggled, "demoFutureSwitch") !== null,
    "an extra yaml labPick parses as words only — no code list change, no card");
}

// ⑤ locale keys for state + suggest labels, all three dialects.
{
  const keys = ["settings.labPicks.statusOn", "settings.labPicks.statusOff", "settings.labPicks.suggest"];
  for (const locale of ["en", "zh", "zh-TW"]) {
    const text = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = keys.filter((k) => !text.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries the state/suggest keys${missing.length ? ` (missing ${missing.join(",")})` : ""}`);
  }
  ok(LabWallOnContext !== undefined && typeof labWallOnFor({ sessionWall: true }, "sessionWall") === "boolean",
    "wall-on context module exports resolve");
  ok(labWallOnFor({}, "sessionWall") === false, "unknown/unwired pick reads off (honest default)");
}

process.stdout.write(`\ntask 563: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
