// Task 364 acceptance → tl1741 换代（用户 20261121 三拍：autopilot 稳定卡随
// 「稳定档不再提供开关」移除——preapprove 子块随宿主卡退役；fullAccess 保留
// 在 automation 组）：
//  ① autopilot 卡与卡内 preapprove 子块不再在实验室面板渲染（语义面零改动：
//     bridge 五值 setter / persisted 字段名 / settingsViewTypes 字段全保留，
//     settings-preapprove-managed.test.ts 详钉）；
//  ② full access 仍归 automation 组（fullAccess 是 tl1741 后该组唯一卡）；
//  ③ render-table 现存清单：退役 id（autopilot/sessionCollab）出表，其余在位。
//
// Run: npx tsx src/__tests__/task364-lab-regroup.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");

console.log("\ntask 364 lab regroup (tl1741: autopilot pane retired with its preapprove sub-block)");

const labDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const viewTypes = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");

// ① single entry: no rail feature, no standalone pane branch.
ok(!panel.includes('{ id: "preapproveManagedPaths", group:'), "rail has no preapprove entry (feature removed from the array)");
ok(!panel.includes('{selected === "preapproveManagedPaths" &&'), "no standalone pane branch for preapprove (single entry)");

// ① tl1741: the autopilot card (and its preapprove sub-block) retired.
{
  ok(!labDefault.includes('id: "autopilot"'), "autopilot entry left the layout (tl1741)");
  ok(!panel.includes('{selected === "autopilot" && ('), "autopilot pane branch removed (tl1741)");
  ok(!panel.includes("autopilot-preapprove-subblock"), "pre-approval sub-block retired with the autopilot card (tl1741)");
  ok(!panel.includes('app.SetPreapproveManagedPaths('), "no panel call sites remain (card retired)");
}

// ② full access re-homed (unchanged): automation group, the only card after tl1741.
{
  const autoBlock = labDefault.slice(labDefault.indexOf('key: "automation"'), labDefault.indexOf('key: "efficiency"'));
  ok(autoBlock.includes('id: "fullAccess"'), `full access group = automation (got ${autoBlock.includes('id: "fullAccess"') ? "automation" : "missing"})`);
}

// ③ zero behaviour by scope (tl1741): the semantic surface is untouched —
//    bridge method, persisted field names and view-type fields all live on
//    (config 手写可调；settings-preapprove-managed.test.ts 详钉).
ok(bridge.includes("SetPreapproveManagedPaths(enabled: boolean"), "bridge method name unchanged");
ok(viewTypes.includes("experimentalPreapproveManagedPaths") && viewTypes.includes("preapproveSkills") && viewTypes.includes("preapproveSessionStores"), "persisted field names unchanged");
ok(viewTypes.includes("preapproveBashEscape"), "category reads unchanged (no field rename smuggled in)");
{
  const calls = panel.match(/app\.SetPreapproveManagedPaths\(/g) ?? [];
  ok(calls.length === 0, `panel has zero call sites after the card retired (got ${calls.length})`);
}
ok(!panel.includes("SetExperimentalFullAccess") === false, "full-access switch wiring untouched (presence check)");

// ④ remaining render-table entries all still present (no silent drop).
{
  // tl1741：autopilot/sessionCollab 随撤开关出布局；其余在位（no silent drop）。
  const ids = ["fullAccess", "splitView", "todoSidebar"];
  const missing = ids.filter((id) => !labDefault.includes(`id: "${id}"`));
  const retired = ["autopilot", "sessionCollab"].filter((id) => labDefault.includes(`id: "${id}"`));
  ok(missing.length === 0 && retired.length === 0,
    `render-table entries intact (missing: ${missing.join(", ") || "none"}; retired-still-present: ${retired.join(", ") || "none"})`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
