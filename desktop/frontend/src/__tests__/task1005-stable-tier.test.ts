// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1005-stable-tier.test.ts
// 任务 1005 acceptance harness — 实验室新增「稳定」档：批1 四项转档
// （autopilot / sessionCollab / fullAccess / runtimeReuse）+ 禁用态两分支显示
// + 开发者模式单项解锁：
//  ① 档位与徽章（C1/C5 数据面）：四项在注册表/Go labFeatureTiers/布局数据
//     （yaml + 内置默认）三处同为 stable；LAB_TIER_ORDER 稳定居首（最强档）；
//     稳定档永不触发「建议开启」角标；rail 卡头聚合出单枚稳定徽章。
//  ② C4 kill switch（Go 面）：Default() 四项默认开（autopilot 伴生
//     max_runtime 8h，防 desktopAutopilotDefaults 拒绝降级致默认开空转），
//     显式 false 不被覆盖的验收测试在案（internal/config/lab_stable_tier_1005_test.go）。
//  ③ 禁用态两分支文案（定稿 ⑤）：四个 pane 开关禁用态挂 labStableLocked，
//     解释行按存储值分支（值开=默认开启说明 / 值关=曾手动关闭说明）。
//  ④ 开发者模式（定稿 ②⑥）：单项 toggle（localStorage reasonix.lab.devMode，
//     作用域仅稳定档），标签显示当前态；「检测到你手动关闭了 N 个稳定能力」
//     小字仅开发者模式开且 N>0 时可见（只提示不弹窗）。
//  ⑤ 三语 locale 全量在位。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { EXPERIMENT_FEATURE_TIERS, LAB_TIER_COUNTS, LAB_TIER_ORDER, suggestEnable, LAB_TIER_LABEL_KEYS, type TierFeatureId } from "../lib/experimentTiers";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { labEntryBadgeTier, resolveLabLayout } from "../lab/labLayout";
import { en } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;
const read = (p: string) => readFileSync(`${frontendRoot}/${p}`, "utf8");
const readRepo = (p: string) => readFileSync(`${repoRoot}/${p}`, "utf8");

console.log("\ntask 1005 lab stable tier (batch-1 transfer + locked display + developer-mode unlock)");

// ── ① 档位与徽章（C1/C5 数据面）─────────────────────────────────
{
  const four: TierFeatureId[] = ["autopilot", "sessionCollab", "fullAccess", "runtimeReuse"];
  ok(four.every((id) => EXPERIMENT_FEATURE_TIERS[id] === "stable"),
    "register: the batch-1 four carry the stable tier");
  ok(LAB_TIER_ORDER[0] === "stable" && LAB_TIER_ORDER.length === 5,
    "LAB_TIER_ORDER puts stable first (strongest tier, 5 tiers)");
  // 任务 959：① tableWrap + ② sendTargetSelect 入表——可选 34→36（计数
  // pin 随表A 步进，562 同步；1024→37 后 1072 renderArtifact 移除回 36；
  // 1069 stalledIntentNudge 叠加入表→37；1070 standardContinuation → 38；
  // 1097 logManagement 叠加入表→39，主线漏跟本 pin；tl 1125 tabManagement +
  // viewLayout 两容器「可选 → 推荐」——推荐 18→20、可选 39→37、总数不变
  // 75，合并态实测真值一并收口）。tl 1132：startupSplash（可选）+
  // startupLoading（纯前端容器，同 tabManagement 豁免）叠加入表——
  // 可选 37→39、总数 75→77。
  ok(LAB_TIER_COUNTS.stable === 4 && LAB_TIER_COUNTS.recommended === 20 && LAB_TIER_COUNTS.optional === 39 && LAB_TIER_COUNTS.unstable === 13 && LAB_TIER_COUNTS.retired === 1,
    "LAB_TIER_COUNTS pins 4 稳定/20 推荐/39 可选/13 未稳定/1 退役 (tl 1132 步进)");
  // 稳定档永不 nag（「建议开启」只归推荐档——稳定项默认开，无需劝开）。
  ok(four.every((id) => !suggestEnable(EXPERIMENT_FEATURE_TIERS[id], false) && !suggestEnable(EXPERIMENT_FEATURE_TIERS[id], true)),
    "stable never suggests (off and on both constructed)");
  // Go 侧同源：四项 LabTierStable（562 双侧一致门禁的数据面抽查）。
  const goRender = readRepo("internal/config/render.go");
  ok([
    '{"autopilot", LabTierStable,',
    '{"sessionCollab", LabTierStable,',
    '{"fullAccess", LabTierStable,',
    '{"runtimeReuse", LabTierStable,',
  ].every((s) => goRender.includes(s)) && goRender.includes('LabTierStable      LabTier = "stable"'),
    "Go labFeatureTiers registers the four as LabTierStable");
  // 布局数据两份同源：四项 tier stable（yaml 与内置默认同构由 722 锚守护，
  // 这里钉四项本身）。
  const yamlText = read("src/lab/lab-layout.yaml");
  const paneIds = new Set(LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g) => g.entries.map((e) => e.id)));
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  const flatTiers = (layout: typeof LAB_LAYOUT_DEFAULT_DATA) =>
    layout.groups.flatMap((g) => g.entries.map((e) => [e.id, e.tier ?? null] as const)).filter(([id]) => four.includes(id as TierFeatureId));
  assert.deepEqual(flatTiers(resolved.layout), flatTiers(LAB_LAYOUT_DEFAULT_DATA));
  ok(resolved.source === "yaml" && flatTiers(resolved.layout).every(([, tier]) => tier === "stable"),
    "yaml and built-in default carry the four at stable (resolved from yaml)");
  // 徽章聚合：autopilot 卡头单枚稳定；sessionCollab 卡头稳定压过可选子项。
  ok(labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, "autopilot") === "stable" &&
     labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, "sessionCollab") === "stable" &&
     labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, "fullAccess") === "stable" &&
     labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, "runtimeReuse") === "stable",
    "rail aggregation yields the single stable badge for all four cards");
  // TierBadge 渲染 stable（轻渲染：类名 + 文案 + tooltip 键）。
  const dom = new JSDOM("<div id='root'></div>");
  Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
  const { TierBadge } = await import("../components/TierBadge");
  const root = createRoot(document.getElementById("root")!);
  const { act } = await import("react");
  const translator = (key: keyof typeof en) => en[key];
  await act(async () => {
    root.render(createElement(TierBadge, { tier: "stable", translator: translator as never }));
  });
  const badge = document.querySelector(".lab-tier-badge--stable");
  ok(badge !== null && badge.getAttribute("data-lab-tier") === "stable" && badge.textContent === en["settings.labTier.stable"],
    "TierBadge renders the stable pill with label + data tier");
  // CSS：stable 徽章配色在案（562 徽章域）。
  ok(read("src/styles.css").includes(".lab-tier-badge--stable"),
    "styles.css carries the stable badge pill");
}

// ── ② C4 kill switch（Go 面，源码锚）───────────────────────────
{
  const configGo = readRepo("internal/config/config.go");
  // Default() 四项默认开（autopilot 伴生 max_runtime 8h）。
  ok(configGo.includes("Autopilot:               true") && configGo.includes('AutopilotMaxRuntime:     "8h"') &&
     configGo.includes("ExperimentalSessionCollab: true") && configGo.includes("ExperimentalFullAccess:    true") && configGo.includes("ExperimentalRuntimeReuse:  true"),
    "config.Default() turns the four on (autopilot rides max_runtime 8h)");
  // C4 验收测试在案：显式 false 不覆盖 / 显式 true 保留 / 键缺省读默认 / 渲染往返。
  const goTest = readRepo("internal/config/lab_stable_tier_1005_test.go");
  ok(["TestLabStableTierDefaultsOn", "TestLabStableTierExplicitFalseSurvives", "TestLabStableTierExplicitTrueSurvives", "TestLabStableTierAbsentKeyReadsDefault", "TestLabStableTierDefaultRenderRoundTrip"]
      .every((name) => goTest.includes(`func ${name}(t *testing.T)`)),
    "the C1/C4 acceptance tests exist in internal/config");
}

// ── ③ 禁用态两分支文案（四个 pane）──────────────────────────────
// 1134（45381165f）实验室二级 chunk 拆分：ExperimentalSection 整体搬入
// ExperimentalLabSection.tsx，本段与 ④ 的源码锚随迁（1005 漏跟同步，
// wt-reds-cleanup 件5c 补）。
{
  const panel = read("src/components/ExperimentalLabSection.tsx");
  const panes = ["autopilot", "sessionCollab", "fullAccess", "runtimeReuse"] as const;
  for (const id of panes) {
    ok(panel.includes(`disabled={busy || labStableLocked("${id}")}`),
      `${id} pane: the switch disables while stable-locked`);
    ok(panel.includes(`labStableLocked("${id}") ? (\n                <p className="settings-field__hint-line">{t(Boolean(s.${id === "autopilot" ? "autopilot" : `experimental${id[0].toUpperCase()}${id.slice(1)}`}) ? "settings.lab.stable.lockedOn" : "settings.lab.stable.lockedOff")}</p>`),
      `${id} pane: the locked line branches on the stored value (lockedOn / lockedOff)`);
    ok(panel.includes(`labStableUnlocked("${id}") ? (\n                <p className="settings-field__hint-line experimental-lab__stable-warning">{t("settings.lab.stable.devWarning")}</p>`),
      `${id} pane: the unlocked state shows the no-guarantee warning`);
  }
}

// ── ④ 开发者模式（单项 toggle + N 计数小字）─────────────────────
{
  const panel = read("src/components/ExperimentalLabSection.tsx");
  ok(panel.includes('globalThis.localStorage?.getItem("reasonix.lab.devMode") === "1"') &&
     panel.includes('globalThis.localStorage?.setItem("reasonix.lab.devMode"'),
    "developer-mode preference persists in localStorage (reasonix.lab.devMode)");
  ok(panel.includes('aria-checked={labDevMode}') && panel.includes("settings.lab.devMode.on") && panel.includes("settings.lab.devMode.off"),
    "the toggle is a labeled switch whose label states the current state");
  // N 计数小字：仅开发者模式开且 N>0 时渲染（定稿 ⑥：普通用户零打扰）。
  ok(panel.includes('{labDevMode && labStableOffCount > 0 ? (') && panel.includes('t("settings.lab.devMode.disabledCount", { n: labStableOffCount })'),
    "the disabled-count notice renders only while developer mode is on and n > 0");
  // N 从 settings 状态直接算（批1 四项）。
  ok(panel.includes('["autopilot", Boolean(s.autopilot)]') && panel.includes('["runtimeReuse", Boolean(s.experimentalRuntimeReuse)]'),
    "the count derives from the live settings state of the batch-1 four");
}

// ── ⑤ 三语 locale ───────────────────────────────────────────────
{
  const keys = [
    "settings.labTier.stable",
    "settings.labTier.stable.desc",
    "settings.lab.devMode.on",
    "settings.lab.devMode.off",
    "settings.lab.devMode.disabledCount",
    "settings.lab.stable.lockedOn",
    "settings.lab.stable.lockedOff",
    "settings.lab.stable.devWarning",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = read(`src/locales/${locale}.ts`);
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all 1005 keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
