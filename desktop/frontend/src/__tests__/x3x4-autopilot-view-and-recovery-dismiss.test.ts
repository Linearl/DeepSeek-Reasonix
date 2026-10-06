// Run: tsx src/__tests__/x3x4-autopilot-view-and-recovery-dismiss.test.ts
//
// X4 断点 B（前端半）+ 任务 519 核实卡移除的两块前端锚：
// 1. X4 断点 B（前端半）：normalizeCollaborationMode 必须放行 "autopilot"——
//    此前它把后端（修复后）报来的 autopilot 归一成 normal，composer 的
//    autopilot 指示永远点不亮。
// 2. 任务 519（核实卡移除）：ToolRecoveryPanel 降级为不可交互记录行——
//    X3 的「忽略并不再提示」按钮随交互面整体退役（后端 dismiss 结算语义
//    保留，由 scripts/check-fork-integrity.mjs 的 X3 锚保护），面板不再
//    渲染任何按钮、不再出现「需要核实」措辞。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { normalizeCollaborationMode } from "../lib/types";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

// 1. 后端视图值原样通过（X4 前端半）。
assert.equal(normalizeCollaborationMode("autopilot"), "autopilot", "backend-reported autopilot must survive normalization");
// 旧三态不受影响（回归护栏）。
assert.equal(normalizeCollaborationMode("plan"), "plan");
assert.equal(normalizeCollaborationMode("goal"), "goal");
assert.equal(normalizeCollaborationMode("normal"), "normal");
// 未知值仍走 legacy 链：plan 轴优先，其次运行中 goal，最后 normal。
assert.equal(normalizeCollaborationMode(undefined, "", "plan-yolo"), "plan");
assert.equal(normalizeCollaborationMode(undefined, "objective"), "goal");
assert.equal(normalizeCollaborationMode(undefined), "normal");
// goal 存在时不被 autopilot 值遮蔽——preference 顺序 pin（goal 赢）。
assert.equal(normalizeCollaborationMode("autopilot", "objective"), "autopilot",
  "an explicit autopilot wire value wins over the goal fallback (backend already ordered plan>goal>autopilot)");

// 2. 任务 519：核实交互键三语退役（dismiss/inspect/confirm/reject/retry/resume）。
for (const [name, dict] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  for (const key of ["toolRecovery.dismiss", "toolRecovery.inspect", "toolRecovery.confirm",
    "toolRecovery.reject", "toolRecovery.retry", "toolRecovery.resume", "toolRecovery.resumePrompt"]) {
    assert.ok(!(key in dict), `${name} must no longer carry the review-interaction key ${key} (task 519)`);
  }
  // 记录行措辞不再是「需要核实」。
  const title = (dict as Record<string, string>)["toolRecovery.title"];
  assert.ok(typeof title === "string" && !title.includes("核实") && !title.includes("review"),
    `${name} panel title must read as a passive record, not a review request`);
}

// 3. 面板源码不再含任何动作按钮 / resolve 调用（519 降级为不可交互记录行）。
const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/ToolRecoveryPanel.tsx"), "utf-8");
assert.doesNotMatch(panel, /act\(call/, "panel must not wire any per-call action (task 519)");
assert.doesNotMatch(panel, /<button/, "panel must render no buttons (task 519)");
assert.doesNotMatch(panel, /ResolveToolRecoveryForTab/, "panel must not call the resolve action API (task 519)");
assert.doesNotMatch(panel, /onResume/, "panel must not take an onResume prop (task 519)");
// 记录行的只读展示件仍在：标题、结果未知行、操作详情展开。
for (const part of ['t("toolRecovery.title")', 't("toolRecovery.unknown")', 't("toolRecovery.details")']) {
  assert.ok(panel.includes(part), `panel must keep the passive record part ${part}`);
}

console.log("x3x4-autopilot-view-and-recovery-dismiss: all assertions passed");
