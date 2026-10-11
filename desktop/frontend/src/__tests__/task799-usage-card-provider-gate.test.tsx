// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task799-usage-card-provider-gate.test.tsx
//
// 任务 799 验收套——OpenCode Go 用量侧栏卡按当前 provider 条件显示（23 版
// 用户反馈回归：GLM 地址下侧栏也无条件渲染 OpenCode Go 卡，数据全 0%，无意义
// 占位）。两道门、四个分支：
//  ① 挂载：ContextPanel 在 PlanUsageCard 之后挂 <OpenCodeGoUsageSidebarCard
//     tabId={tabId} />（跟 tab，与 GLM 卡的任务 666 条件同构）；
//  ② 回归钉：family="zhipu"（GLM 地址）→ 无卡 DOM 且零用量请求（family 查询
//     照发——它本身就是零 I/O 的配置扫描）；
//  ③ opencode-go 地址：卡出现、三窗口渲染、用量查询照常；
//  ④ 其他 plan family（kimi / minimax / 无）与 lab 旗标关：一律不渲染；
//  ⑤ live 翻转：活跃 provider 从 opencode-go 切走（换 tab 触发重查）→ 卡消失。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntask 799 OpenCode Go usage card gates on the active plan family");

// ── ① 挂载点：跟 tab + 位于 PlanUsageCard 之后 ──
{
  const contextPanel = readFileSync(fileURLToPath(new URL("../components/ContextPanel.tsx", import.meta.url)), "utf8");
  const card = readFileSync(fileURLToPath(new URL("../components/OpenCodeGoUsageSidebarCard.tsx", import.meta.url)), "utf8");
  const planIdx = contextPanel.indexOf("<PlanUsageCard tabId={tabId} />");
  const goIdx = contextPanel.indexOf("<OpenCodeGoUsageSidebarCard tabId={tabId} />");
  ok(planIdx >= 0 && goIdx > planIdx, "ContextPanel mounts the tab-following sidebar card right after PlanUsageCard");
  ok(card.includes('family !== "opencode-go"') && card.includes("return null"),
    "the card renders nothing unless the active family is opencode-go");
  ok(card.includes("GetActivePlanFamily") && card.includes("GetOpenCodeGoUsage"),
    "the poll chains the family lookup before the usage request");
}

// ── 渲染面：JSDOM + 可控 family 桩桥 ──
const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  IS_REACT_ACT_ENVIRONMENT: true,
});
let familyAnswer = "";
let familyQueries = 0;
let usageQueries = 0;
let usagePayload: unknown = { tiers: [], note: "" };
Object.assign(window, {
  go: {
    main: {
      App: {
        GetActivePlanFamily: async () => {
          familyQueries += 1;
          return familyAnswer;
        },
        GetOpenCodeGoUsage: async () => {
          usageQueries += 1;
          if (usagePayload instanceof Error) throw usagePayload;
          return usagePayload;
        },
      },
    },
  },
});
usagePayload = {
  tiers: [
    { window: "rolling", percent: 42.4, resetsAt: new Date(Date.now() + 3600_000).toISOString() },
    { window: "weekly", percent: 10, resetsAt: "" },
    { window: "monthly", percent: 3.2, resetsAt: "" },
  ],
  note: "",
};

const { applyLabFlags } = await import("../lib/labFlags");
const { OpenCodeGoUsageSidebarCard } = await import("../components/OpenCodeGoUsageSidebarCard");
const { LocaleProvider } = await import("../lib/i18n");

const container = document.createElement("div");
document.body.appendChild(container);
let root: Root | null = null;
async function mount(tabId?: string) {
  root = createRoot(container);
  await act(async () => {
    root!.render(<LocaleProvider><OpenCodeGoUsageSidebarCard tabId={tabId} /></LocaleProvider>);
  });
  await act(async () => { await Promise.resolve(); });
}
async function unmount() {
  await act(async () => { await root!.unmount(); });
  root = null;
  container.innerHTML = "";
}
const sidebar = () => container.querySelector('[data-testid="opencode-go-usage-sidebar"]');

// lab 旗标默认关 → 连 family 查询都不发（163 零请求契约在 799 下保持）。
applyLabFlags({});
ok(familyQueries === 0 && usageQueries === 0, "before mounting: no gate runs while nothing is mounted");
await mount();
ok(familyQueries === 0 && usageQueries === 0, "lab off: zero family lookups and zero usage requests");
ok(sidebar() === null, "lab off: no DOM at all");
await unmount();

// ── ② 回归钉：GLM 地址（family=zhipu）下无卡、零用量请求 ──
applyLabFlags({ opencodeGoUsage: true });
familyAnswer = "zhipu";
familyQueries = 0; usageQueries = 0;
await mount("tab-a");
ok(familyQueries === 1, "GLM address: the family lookup runs (zero-I/O config scan)");
ok(usageQueries === 0, "GLM address: zero usage requests (the user-reported waste is gone)");
ok(sidebar() === null, "GLM address: the OpenCode Go card renders no DOM (regression pin)");
await unmount();

// ── ③ opencode-go 地址：卡出现、数据正常 ──
familyAnswer = "opencode-go";
familyQueries = 0; usageQueries = 0;
await mount("tab-a");
ok(usageQueries === 1, "opencode-go address: the usage request fires once on mount");
ok(sidebar() !== null, "opencode-go address: the card renders");
ok(sidebar()!.classList.contains("plan-usage"), "opencode-go address: same plan-usage visual system");
ok(container.querySelectorAll("[data-window]").length === 3, "opencode-go address: three usage windows render");
ok(container.querySelector('[data-window="rolling"] strong')?.textContent === "42%", "opencode-go address: percents render like before");
await unmount();

// ── ④ 其他 plan family 一律不渲染 ──
for (const other of ["kimi", "minimax", ""]) {
  familyAnswer = other;
  familyQueries = 0; usageQueries = 0;
  await mount("tab-a");
  ok(usageQueries === 0, `family=${JSON.stringify(other)}: zero usage requests`);
  ok(sidebar() === null, `family=${JSON.stringify(other)}: no card DOM`);
  await unmount();
}

// ── ⑤ live 翻转：provider 从 opencode-go 切走 → 卡消失 ──
familyAnswer = "opencode-go";
await mount("tab-a");
ok(sidebar() !== null, "flip setup: the card shows on the opencode-go tab");
familyAnswer = "zhipu";
await act(async () => {
  root!.render(<LocaleProvider><OpenCodeGoUsageSidebarCard tabId="tab-b" /></LocaleProvider>);
});
await act(async () => { await Promise.resolve(); });
ok(sidebar() === null, "provider flipped away: the card disappears on re-query");
familyAnswer = "opencode-go";
await act(async () => { await Promise.resolve(); });
ok(sidebar() === null, "the poll stays quiet until the next tick after a flip away");
await unmount();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
