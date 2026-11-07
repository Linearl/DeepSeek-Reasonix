// Run: tsx src/__tests__/task783-usage-monitoring.test.tsx
//
// 任务 783 验收套——可观测性组「用量监测」合并卡 + OpenCode Go 用量侧栏化 +
// 编辑读回（readBack）转正：
//  ① 布局：usageMonitoring 卡在 observability 组，tier=recommended，成员
//     glmPlanUsage（推荐，只读展示行）+ opencodeGoUsage（推荐，展示行——
//     tl1487 四组「不提供开关、始终打开」后开关行退役为展示行）；卡灯键
//     清空（tl1487）；ts 默认与 yaml 同构同值（含扩展点注释）。
//  ② 侧栏化：ContextPanel 在 PlanUsageCard 之后挂 OpenCodeGoUsageSidebarCard；
//     渲染门 = labFlags 的 opencodeGoUsage 旗标（关 = 不渲染、零网络请求，
//     163 前端门延伸到边栏——铁律 2）；开启后与 GLM 卡同套 plan-usage 视觉
//     （同类名、60s 轮询、30s 倒计时、手动刷新）。
//  ③ readBack 转正：前端注册表 toolOptimizations=recommended，Go 渲染表
//     render.go 同步 LabTierRecommended（562 两侧一致断言的源码侧钉）。
//  ④ 配置键零变化：experimentalOpenCodeGoUsage 键名/setter 原样保留。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntask 783 usage monitoring card + OpenCode Go sidebar + readBack promotion");

// ── ① 布局：用量监测卡结构（ts 默认 + yaml 同构）──
{
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const obs = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "observability");
  const card = obs!.entries.find((e: { id: string }) => e.id === "usageMonitoring") as
    | { id: string; tier?: string; onKeys: string[]; members?: Array<{ id: string; tier: string }> }
    | undefined;
  ok(Boolean(card), "usageMonitoring entry exists in the observability group");
  ok(card!.tier === "recommended", "usageMonitoring container tier is recommended");
  ok(JSON.stringify(card!.onKeys) === JSON.stringify([]),
    "tl1487: the card light is empty — the OpenCode Go switch (family's single gate key) is retired");
  const members = card!.members ?? [];
  ok(members.length === 2, "usageMonitoring carries exactly two members");
  ok(members[0]?.id === "glmPlanUsage" && members[0]?.tier === "recommended",
    "member 1 glmPlanUsage is recommended (live, read-only row)");
  // c52a73ccd(783 同任务后半)裁决:readBack(编辑读回)tier 未稳定→推荐,
  // 前端注册表+Go 渲染表同步翻;内置默认与 yaml 均为 recommended。
  ok(members[1]?.id === "opencodeGoUsage" && members[1]?.tier === "recommended",
    "member 2 opencodeGoUsage carries the readBack row at recommended tier (783 final ruling)");
  // 782④ 的另一半：opencodeGoUsage 不再出现在界面组。
  const ui = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "ui");
  ok(!(ui!.entries.some((e: { id: string }) => e.id === "opencodeGoUsage")),
    "opencodeGoUsage is gone from the ui group");

  const yamlText = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const yamlBlock = yamlText.slice(yamlText.indexOf("# 任务 783：「用量监测」合并卡"));
  ok(yamlBlock.includes("tier: recommended") && yamlBlock.includes("id: glmPlanUsage") && yamlBlock.includes("id: opencodeGoUsage"),
    "yaml mirrors the same usageMonitoring card (同构同值)");
  ok(yamlBlock.includes("preset 家族"), "yaml carries the coding-plan preset family extension-point note");
}

// ── ③ readBack 转正：两侧注册表同步 ──
{
  const { EXPERIMENT_FEATURE_TIERS } = await import("../lib/experimentTiers");
  ok(EXPERIMENT_FEATURE_TIERS.toolOptimizations === "recommended",
    "frontend register: toolOptimizations (readBack) = recommended");
  const goSrc = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
  ok(/\{"toolOptimizations", LabTierRecommended, \[\]/.test(goSrc),
    "Go render.go: toolOptimizations promotes to LabTierRecommended");
}

// ── ② 侧栏化：挂载点 + 渲染门 + 三态渲染 ──
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
  const contextPanel = readFileSync(fileURLToPath(new URL("../components/ContextPanel.tsx", import.meta.url)), "utf8");
  ok(panel.includes('{selected === "usageMonitoring" && ('), "usageMonitoring pane branch exists in SettingsPanel");
  ok(!panel.includes('{selected === "opencodeGoUsage" && ('), "the standalone opencodeGoUsage pane branch is gone");
  ok(!panel.includes("SetExperimentalOpenCodeGoUsage") && panel.includes("<SettingsOpenCodeGoUsageCard />"),
    "tl1487: the switch setter is gone — the member renders as a prop-less display card");
  const planIdx = contextPanel.indexOf("<PlanUsageCard tabId={tabId} />");
  const goIdx = contextPanel.indexOf("<OpenCodeGoUsageSidebarCard tabId={tabId} />");
  ok(planIdx >= 0 && goIdx > planIdx, "ContextPanel mounts the sidebar card right after PlanUsageCard (tab-following, task 799)");

  // 渲染面三态：JSDOM + 桩桥。
  const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
  Object.assign(globalThis, {
    window: dom.window,
    document: dom.window.document,
    localStorage: dom.window.localStorage,
    CustomEvent: dom.window.CustomEvent,
    IS_REACT_ACT_ENVIRONMENT: true,
  });
  let payload: unknown = { tiers: [], note: "" };
  let queries = 0;
  Object.assign(window, {
    go: {
      main: {
        App: {
          GetOpenCodeGoUsage: async () => {
            queries += 1;
            if (payload instanceof Error) throw payload;
            return payload;
          },
          // 任务 799 桩：783 的渲染面场景假定活跃 provider 就是 opencode-go
          // （provider 匹配门本身的分支覆盖在 task799 套里）。
          GetActivePlanFamily: async () => "opencode-go",
        },
      },
    },
  });
  const { OpenCodeGoUsageSidebarCard } = await import("../components/OpenCodeGoUsageSidebarCard");
  const { LocaleProvider } = await import("../lib/i18n");

  // tl1487 四组：用量查询「始终打开」——开关门退役，卡常显（仅剩 799 的
  // provider family 门，本场景桩返回 opencode-go）。挂载即查询一次、按
  // plan-usage 视觉渲染三窗口（与 GLM 卡同类名）。
  payload = {
    tiers: [
      { window: "rolling", percent: 42.4, resetsAt: new Date(Date.now() + 3600_000).toISOString() },
      { window: "weekly", percent: 10, resetsAt: "" },
      { window: "monthly", percent: 3.2, resetsAt: "" },
    ],
    note: "",
  };
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(<LocaleProvider><OpenCodeGoUsageSidebarCard /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); });
  ok(queries === 1, "always-on: queries once on mount (no lab switch gate)");
  const section = container.querySelector('[data-testid="opencode-go-usage-sidebar"]');
  ok(section !== null, "always-on: the sidebar card renders without any lab switch");
  ok(section!.classList.contains("plan-usage"), "always-on: same plan-usage visual system as the GLM card");
  ok(container.querySelectorAll("[data-window]").length === 3, "always-on: three usage windows render");
  ok(container.querySelector('[data-window="rolling"] strong')?.textContent === "42%", "always-on: percent rounds like the GLM card");

  // family 门（799）仍在：family 不对时不渲染、不发用量请求。
  {
    const before = queries;
    const hidden = document.createElement("div");
    document.body.appendChild(hidden);
    const hiddenRoot: Root = createRoot(hidden);
    const stub = (window as unknown as { go: { main: { App: { GetActivePlanFamily: () => Promise<string> } } } }).go.main.App;
    stub.GetActivePlanFamily = async () => "glm";
    await act(async () => {
      hiddenRoot.render(<LocaleProvider><OpenCodeGoUsageSidebarCard /></LocaleProvider>);
    });
    await act(async () => { await Promise.resolve(); });
    ok(hidden.querySelector('[data-testid="opencode-go-usage-sidebar"]') === null,
      "family gate (799) survives tl1487: non-opencode-go family renders nothing");
    ok(queries === before, "family gate (799): no usage query for a foreign family");
    await act(async () => { await hiddenRoot.unmount(); });
    hidden.remove();
    stub.GetActivePlanFamily = async () => "opencode-go";
  }

  // 线格式畸形（Go nil slice → tiers: null）不崩、走空态分支。
  payload = { tiers: null, note: "" };
  await act(async () => { await Promise.resolve(); });
  ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') !== null,
    "null tiers payload renders the empty branch without throwing");

  // 网络错误降级为 network note（不抛出）。
  payload = new Error("boom");
  await act(async () => {
    root.render(<LocaleProvider><OpenCodeGoUsageSidebarCard /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); });
  ok(container.querySelector('[data-testid="opencode-go-usage-sidebar"]') !== null,
    "network failure degrades to the note branch without throwing");

  await act(async () => { await root.unmount(); });
  container.remove();
}

// ── ④ 三语文案 ──
{
  const frontendRoot = fileURLToPath(new URL("../../", import.meta.url));
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const keys = ["settings.usageMonitoring", "settings.usageMonitoringHint", "settings.glmPlanUsage", "settings.glmPlanUsageHint"];
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries the usage-monitoring label/hint keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
