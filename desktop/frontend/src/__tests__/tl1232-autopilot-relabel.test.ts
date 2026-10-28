// Run: tsx src/__tests__/tl1232-autopilot-relabel.test.ts
//
// tl1232 复现：yolo→autopilot→yolo 之后 autopilot 档位再也选不上。
//
// desktop.log 实证（tab_316871668…，2026-10-29 05:34）：
//   05:34:45.083 autopilot toggle applied=true   ← 第二次 autopilot 后端已进档
//   05:34:49.245 approval switch entry=tier tier_yolo_leave=true ← 用户点 yolo 离开
//   此后再无任何该 tab 的后端模式调用               ← 后续 autopilot 点击被前端吞掉
//
// 根因（前端呈现层，引擎无恙——Go 侧四段序列测试全绿）：
//   App.tsx applyToolApprovalMode 的 autopilotDrops 分支只 patch
//   {toolApprovalMode, autopilot}，把合成标签 collaborationMode 留在 "autopilot"，
//   且不标记 pending。Composer.chooseTaskMode 的守卫是
//   `if (nextMode !== collaborationMode)` —— 标签卡在 "autopilot" 时，第四档
//   点击被整段吞掉；而纠正标签的 wire meta 是 fire-and-forget + seq 竞态 +
//   仅活跃 tab 水合，一旦丢失/迟到，且 tab-strip 陈旧列表回灌（档位切换不推
//   tab 事件，saveTabsLocked 只落盘），标签就永久卡死 = 「再也选不上」。
//
// 本文件用 lib/composerProfile.ts 的真实纯函数重放 App.tsx 处理器语义，
// 三条通道逐一断言；末尾对 App.tsx 源码做锚断言防接线漂移。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import {
  composerProfileFromMeta,
  hydrateComposerProfileFromMeta,
  hydrateComposerProfilesFromTabs,
  patchComposerProfile,
  type ComposerProfile,
  type ComposerProfileField,
  type ComposerProfilesByTab,
} from "../lib/composerProfile";
import type { Meta, TabMeta } from "../lib/types";

const testDir = dirname(fileURLToPath(import.meta.url));

function meta(overrides: Partial<Meta>): Meta {
  return {
    label: "ai-passport",
    ready: true,
    runtime: { phase: "ready" } as Meta["runtime"],
    startupErr: "",
    eventChannel: "agent:event",
    cwd: "D:/1.workspace/ai-passport",
    autoApproveTools: true,
    bypass: true,
    collaborationMode: "normal",
    toolApprovalMode: "yolo",
    tokenMode: "full",
    goal: "",
    goalStatus: "stopped",
    ...overrides,
  } as Meta;
}

function tabMeta(overrides: Partial<TabMeta>): TabMeta {
  return {
    id: "tab-tl1232",
    scope: "project",
    workspaceRoot: "D:/1.workspace/ai-passport",
    topicId: "topic-1",
    topicTitle: "转线对话",
    label: "ai-passport",
    ready: true,
    running: false,
    mode: "normal-yolo",
    collaborationMode: "autopilot",
    toolApprovalMode: "yolo",
    autopilot: true,
    goal: "",
    goalStatus: "stopped",
    active: true,
    cwd: "D:/1.workspace/ai-passport",
    ...overrides,
  } as TabMeta;
}

// Composer.chooseTaskMode 的点击守卫（Composer.tsx 原样）：
// `if (nextMode !== collaborationMode) onSetCollaborationMode(nextMode);`
// collaborationMode prop = displayedComposerProfileCollaborationMode(profile)，
// goalDraftMode 恒 false 本测试不涉足，取 profile.collaborationMode。
function autopilotClickDispatches(profile: ComposerProfile): boolean {
  return "autopilot" !== profile.collaborationMode;
}

// App.tsx applyToolApprovalMode 的 autopilotDrops 分支语义（修前/修后两版）。
function leavePatchBefore(profile: ComposerProfile): { patch: Partial<ComposerProfile>; pending: ComposerProfileField[] } {
  void profile;
  return { patch: { toolApprovalMode: "yolo", autopilot: false }, pending: ["toolApprovalMode", "autopilot"] };
}
function leavePatchAfter(profile: ComposerProfile): { patch: Partial<ComposerProfile>; pending: ComposerProfileField[] } {
  // 修法（收窄版）：只有标签确实卡在 "autopilot" 时才随旗归位 "normal" 并标
  // pending；goal/plan 标签不因 autopilot 产生，不动也不标 pending（免增
  // 无谓粘性）。与 App.tsx autopilotDrops 分支逐字段同构。
  const dropsLabelStuck = profile.autopilot && profile.collaborationMode === "autopilot";
  return {
    patch: {
      toolApprovalMode: "yolo",
      autopilot: false,
      ...(dropsLabelStuck ? { collaborationMode: "normal" as const } : {}),
    },
    pending: dropsLabelStuck
      ? ["toolApprovalMode", "autopilot", "collaborationMode"]
      : ["toolApprovalMode", "autopilot"],
  };
}

function applyPatch(
  store: ComposerProfilesByTab,
  tabId: string,
  base: ComposerProfile,
  fn: (p: ComposerProfile) => { patch: Partial<ComposerProfile>; pending: ComposerProfileField[] },
): ComposerProfilesByTab {
  const decided = fn(base);
  return patchComposerProfile(store, tabId, base, decided.patch, decided.pending);
}

// ── 复盘起点：05:34:45 第二次 engage 落定后的 profile ─────────────────────
// engage patch {cm:"autopilot", tam:"yolo", ap:true} + wire meta 一致 → pending 清空。
const engagedProfile = composerProfileFromMeta(
  meta({ collaborationMode: "autopilot", toolApprovalMode: "yolo", autoApproveTools: true, bypass: true }),
);

assert.equal(engagedProfile.collaborationMode, "autopilot", "起点：engage 后 profile 标签 = autopilot");
assert.equal(engagedProfile.autopilot, true, "起点：engage 后 profile 裸旗 = true");

// 通道 C（决定性，对齐日志）：yolo 离档后 wire 一直没来——点击必须仍能发出。
{
  process.stdout.write("通道 C：yolo 离档后 wire 未到\n");
  const tabId = "tab-tl1232";
  // 引擎已在 :49 离开 autopilot（后端日志 tier_yolo_leave=true），此后无任何
  // meta/strip 刷新到达（tab 空闲，用户持续点第四档）。
  let store: ComposerProfilesByTab = {};

  // 修前语义：标签留在 "autopilot"。
  store = applyPatch(store, tabId, engagedProfile, leavePatchBefore);
  let profile = store[tabId];
  assert.equal(profile.collaborationMode, "autopilot", "修前：离档 patch 后标签仍 = autopilot（卡死源）");
  assert.equal(profile.autopilot, false, "修前：离档 patch 后裸旗 = false（档位条回到 yolo 点亮）");
  assert.equal(autopilotClickDispatches(profile), false, "修前：第四档点击被 chooseTaskMode 守卫吞掉（复现）");

  // 修后语义：标签随旗离档。
  store = applyPatch({}, tabId, engagedProfile, leavePatchAfter);
  profile = store[tabId];
  assert.equal(profile.collaborationMode, "normal", "修后：离档 patch 后标签 = normal");
  assert.equal(autopilotClickDispatches(profile), true, "修后：第四档点击正常发出");
}

// 通道 B（回灌）：陈旧 tab-strip 列表不得把标签盖回 autopilot。
{
  process.stdout.write("通道 B：陈旧 tab-strip 回灌\n");
  const tabId = "tab-tl1232";
  // 档位切换不推 tab 事件：strip 仍是 engage 期间抓的旧列表（autopilot:true）。
  const staleTabs = [tabMeta({})];

  // 修前：离档 patch（tam/ap 已 pending，旗不会回灌）→ 但 cm 不在 pending 里，
  // 陈旧 strip 把标签盖回 "autopilot"（旧值相等无冲突）→ 点击仍被吞。
  let store = applyPatch({}, tabId, engagedProfile, leavePatchBefore);
  store = hydrateComposerProfilesFromTabs(store, staleTabs);
  assert.equal(store[tabId].collaborationMode, "autopilot", "修前：陈旧 strip 维持/盖回标签 autopilot（继续卡死）");
  assert.equal(store[tabId].autopilot, false, "修前：裸旗因 pending 不回灌（保持 false）");
  assert.equal(autopilotClickDispatches(store[tabId]), false, "修前：回灌后第四档点击仍被吞（卡死只需标签）");

  // 修后：cm 带 pending，reconcile 对陈旧后端值保持本地 "normal"。
  store = applyPatch({}, tabId, engagedProfile, leavePatchAfter);
  store = hydrateComposerProfilesFromTabs(store, staleTabs);
  assert.equal(store[tabId].collaborationMode, "normal", "修后：pending 护住标签，陈旧 strip 盖不回来");
  assert.equal(autopilotClickDispatches(store[tabId]), true, "修后：回灌场景点击仍正常发出");

  // 新鲜 strip 到达后 pending 解除，收敛到引擎真值。
  store = hydrateComposerProfilesFromTabs(store, [
    tabMeta({ collaborationMode: "normal", autopilot: false, toolApprovalMode: "yolo" }),
  ]);
  assert.equal(store[tabId].collaborationMode, "normal", "修后：新鲜 strip 到达后仍 = normal（收敛）");
  assert.equal(store[tabId].autopilot, false, "修后：新鲜 strip 到达后裸旗 = false（收敛）");
}

// 通道 A（happy path 钉住）：wire meta 正常到达时两条版本都收敛——证明缺口只在丢 wire 通道。
{
  process.stdout.write("通道 A：wire meta 正常到达\n");
  const tabId = "tab-tl1232";
  const freshMeta = meta({ collaborationMode: "normal", toolApprovalMode: "yolo", autoApproveTools: true, bypass: true });

  // 修前：patch 无 cm pending → wire 一来即纠回（为何日志里偶发自愈）。
  let store = applyPatch({}, tabId, engagedProfile, leavePatchBefore);
  store = hydrateComposerProfileFromMeta(store, tabId, freshMeta);
  assert.equal(store[tabId].collaborationMode, "normal", "修前：wire 到达即纠回 normal");

  // 修后：pending 与 wire 同值 → pending 解除，同样收敛。
  store = applyPatch({}, tabId, engagedProfile, leavePatchAfter);
  store = hydrateComposerProfileFromMeta(store, tabId, freshMeta);
  assert.equal(store[tabId].collaborationMode, "normal", "修后：wire 到达 pending 解除（同样收敛）");
  assert.equal(store[tabId].autopilot, false, "修后：裸旗 = false");
}

// App.tsx 源码锚：autopilotDrops 分支必须随旗改标签并标记 pending（收窄版）。
{
  process.stdout.write("源码锚：App.tsx 接线\n");
  const src = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
  assert.match(src, /const dropsLabelStuck = autopilotDrops && composerProfile\.collaborationMode === "autopilot";/, "App.tsx：仅标签卡 autopilot 时归位");
  assert.match(src, /\.\.\.\(dropsLabelStuck \? \{ collaborationMode: "normal" as const \} : \{\}\)/, "App.tsx：离档 patch 条件携带标签归位");
  assert.match(src, /\["toolApprovalMode", "autopilot", "collaborationMode"\]/, "App.tsx：pendingFields 含 collaborationMode");
}

// goal × autopilot 离档：标签是 "goal"（goal>autopilot 优先级），不动 cm、
// 不标 cm pending；autopilot 点击照常能发出（"autopilot" !== "goal"）。
{
  process.stdout.write("通道 D：goal × autopilot 离档（标签非 autopilot，不牵连）\n");
  const tabId = "tab-tl1232";
  const goalEngaged = composerProfileFromMeta(
    meta({ collaborationMode: "goal", toolApprovalMode: "yolo", autoApproveTools: true, bypass: true, autopilot: true, goal: "objective", goalStatus: "running" }),
  );
  assert.equal(goalEngaged.collaborationMode, "goal", "起点：goal × autopilot 标签 = goal");
  assert.equal(goalEngaged.autopilot, true, "起点：裸旗 = true");

  let store = applyPatch({}, tabId, goalEngaged, leavePatchAfter);
  const profile = store[tabId];
  assert.equal(profile.collaborationMode, "goal", "修后：goal 标签原样保留");
  assert.equal(profile.autopilot, false, "修后：裸旗落 false");
  assert.equal(profile.pending.collaborationMode, undefined, "修后：cm 不标 pending（无粘性）");
  assert.equal(autopilotClickDispatches(profile), true, "修后：autopilot 点击照常发出（本就不被吞）");
}

process.stdout.write("tl1232-autopilot-relabel: all passed\n");
