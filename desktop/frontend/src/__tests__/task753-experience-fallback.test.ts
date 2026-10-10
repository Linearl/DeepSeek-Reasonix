// Run: LANG=en node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/task753-experience-fallback.test.ts
//
// Task 753 — 根因①：experience 启动兜底不得物理覆写持久层。
//   AC1  启动同步失败（无快照）+ mirror=concise：兜底后 effective tier 仍是
//        concise，localStorage 的 canonical 键一个都不被改写、衍生键不被发明。
//   AC1b mirror 缺失：兜底落到安全默认 standard，且不"发明"任何持久键。
//   AC2  快照 sessionExperience 字段缺失（旧后端/迁移窗口）：不得把 mirror
//        归一化成 standard；显式合法值正常应用并写 mirror。
//   AC3  失败窗口 → 快照到达（同值 concise）：effective tier 全程恒定，
//        不出现 standard 语义的展开-折叠翻转（值恒定即组件 modeChanged
//        永不触发）。
//   源码钉：useDesktopPreferences 失败分支不再调用 mirror 写入式 hydrate。
//
// 每个 createTranscriptHarness 拥有独立 vite server（模块状态互不可见）与
// 独立 storage 注入（覆盖 globalThis.localStorage），场景按创建顺序串行，
// 断言一律在下一个 harness 创建之前完成。

import { readFileSync } from "node:fs";
import { createTranscriptHarness } from "./transcript-dom-harness";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}
const localStorage = () => (globalThis as { localStorage?: Storage }).localStorage!;

console.log("\ntask 753-1: experience fallback never rewrites the persisted tier");

// Source pin: the failed branch must hydrate from the mirror (memory only),
// never call the mirror-writing hydrate again.
{
  const source = readFileSync(new URL("../app-runtime/useDesktopPreferences.ts", import.meta.url), "utf8");
  const failedBranch = source.slice(source.indexOf("const failed = useCommittedCommand"), source.indexOf("const reload ="));
  ok(failedBranch.includes("markSessionExperienceHydratedFromMirror()"), "source pin: failed branch marks hydrated from the mirror");
  ok(!failedBranch.includes("hydrateSessionExperience"), "source pin: failed branch no longer calls the mirror-writing hydrate");
}

// AC1 + AC3: the exact startup-failure scene in one isolated module graph —
// mirror says concise, the backend snapshot never arrived, the fallback runs,
// then the late agreeing snapshot lands.
{
  const harness = await createTranscriptHarness({
    storage: { "reasonix-session-experience": "concise" },
  });
  const sessionExperience = await harness.loadModule<{
    getSessionExperience: () => string;
    hydrateSessionExperience: (v: unknown) => void;
    isSessionExperienceHydrated: () => boolean;
    markSessionExperienceHydratedFromMirror: () => string;
    resolveWorkProcessPresentation: (v: string) => { showWhileRunning: boolean };
  }>("/src/lib/sessionExperience.ts");

  ok(sessionExperience.isSessionExperienceHydrated() === false, "starts un-hydrated");
  const tier = sessionExperience.markSessionExperienceHydratedFromMirror();
  ok(tier === "concise", "AC1: fallback keeps the mirrored concise tier");
  ok(sessionExperience.getSessionExperience() === "concise", "AC1: effective tier stays concise after the fallback");
  ok(sessionExperience.resolveWorkProcessPresentation(sessionExperience.getSessionExperience()).showWhileRunning === false,
    "AC1: concise semantics (no live expansion) hold after the fallback");
  ok(localStorage().getItem("reasonix-session-experience") === "concise", "AC1: the canonical mirror key is NOT rewritten");
  ok(localStorage().getItem("reasonix-display-mode") === null && localStorage().getItem("reasonix-process-fold") === null && localStorage().getItem("reasonix-reasoning-summary") === null,
    "AC1: no compatibility mirror keys are invented by the fallback");
  // AC3: the snapshot finally arrives and agrees (concise). The effective tier
  // never changed across the whole failure window, so no reasoning panel sees
  // a modeChanged flip and no geometry consumer sees a fold flip.
  sessionExperience.hydrateSessionExperience("concise");
  ok(sessionExperience.getSessionExperience() === "concise", "AC3: the late agreeing snapshot changes nothing");
}

// AC1b: no mirror at all (first install / privacy mode / previously erased).
{
  const harness = await createTranscriptHarness({});
  const sessionExperience = await harness.loadModule<{
    getSessionExperience: () => string;
    markSessionExperienceHydratedFromMirror: () => string;
  }>("/src/lib/sessionExperience.ts");
  const tier = sessionExperience.markSessionExperienceHydratedFromMirror();
  ok(tier === "standard" && sessionExperience.getSessionExperience() === "standard",
    "AC1b: missing mirror falls back to the safe default");
  ok(localStorage().getItem("reasonix-session-experience") === null,
    "AC1b: the fallback does not invent a persisted value");
}

// AC2: the snapshot adapter must not treat an ABSENT field as "standard",
// and an explicit value stays authoritative.
{
  const harness = await createTranscriptHarness({
    storage: { "reasonix-session-experience": "concise" },
  });
  const sessionExperience = await harness.loadModule<{
    getSessionExperience: () => string;
    isSessionExperienceHydrated: () => boolean;
  }>("/src/lib/sessionExperience.ts");
  const adapter = await harness.loadModule<{
    applyPreferencesAppearance: (settings: Record<string, unknown>) => unknown;
  }>("/src/app-runtime/desktopPreferencesAdapter.ts");

  adapter.applyPreferencesAppearance({ desktopTheme: "system", desktopLayoutStyle: "workbench", sessionExperience: undefined });
  ok(sessionExperience.isSessionExperienceHydrated() === false, "AC2: an absent snapshot field leaves the module un-hydrated");
  ok(sessionExperience.getSessionExperience() === "concise", "AC2: the mirror stays authoritative when the field is absent");
  ok(localStorage().getItem("reasonix-session-experience") === "concise",
    "AC2: an absent snapshot field does not overwrite the mirror");

  adapter.applyPreferencesAppearance({ desktopTheme: "system", desktopLayoutStyle: "workbench", sessionExperience: "deep" });
  ok(sessionExperience.getSessionExperience() === "deep", "AC2: an explicit snapshot value still applies");
  ok(localStorage().getItem("reasonix-session-experience") === "deep",
    "AC2: an explicit snapshot value rewrites the mirror (authoritative path)");
}

if (failed > 0) {
  throw new Error(`${failed} task 753-1 checks failed`);
}
console.log(`  ${passed} checks passed`);
