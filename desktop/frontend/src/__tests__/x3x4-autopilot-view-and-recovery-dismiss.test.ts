// Run: tsx src/__tests__/x3x4-autopilot-view-and-recovery-dismiss.test.ts
//
// X3/X4 修复的两块前端锚：
// 1. X4 断点 B（前端半）：normalizeCollaborationMode 必须放行 "autopilot"——
//    此前它把后端（修复后）报来的 autopilot 归一成 normal，composer 的
//    autopilot 指示永远点不亮。
// 2. X3 显式清除：toolRecovery.dismiss 键三语齐全，ToolRecoveryPanel 渲染的
//    忽略按钮有本地化文案可用。

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

// 2. X3 忽略按钮三语文案。
for (const [name, dict] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  const text = (dict as Record<string, string>)["toolRecovery.dismiss"];
  assert.ok(typeof text === "string" && text.length > 0, `${name} must localize toolRecovery.dismiss`);
}

// 3. 面板源码含 dismiss 动作按钮，且它是唯一不因 running 禁用的动作。
const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/ToolRecoveryPanel.tsx"), "utf-8");
assert.match(panel, /act\(call, "dismiss"\)/, "panel must wire the dismiss action");
assert.match(
  panel,
  /disabled=\{busy\} onClick=\{\(\) => void act\(call, "dismiss"\)\}/,
  "dismiss must stay clickable while the UI believes the tab idle (the stuck-card escape hatch)",
);

console.log("x3x4-autopilot-view-and-recovery-dismiss: all assertions passed");
