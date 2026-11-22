// Task 364 acceptance → tl1741r2 再换代（用户 20261122 打回裁定「stable 清主
// 选项（常开）、子选项保留配置」：autopilot 卡体回归 rail（无主开关行），卡内
// preapprove 子块随宿主 pane 回归——364 交付语义原样；fullAccess 仍在
// automation 组）：
//  ① autopilot 卡回归为无主开关卡，卡内 preapprove 子块回归渲染（语义面零
//     改动：bridge 五值 setter / persisted 字段名 / settingsViewTypes 字段全
//     保留，settings-preapprove-managed.test.ts 详钉）；
//  ② full access 仍归 automation 组；
//  ③ render-table 现存清单：回归 id（autopilot/sessionCollab）在表，其余在位。
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

console.log("\ntask 364 lab regroup (tl1741r2: autopilot card body restored with its preapprove sub-block)");

const labDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const viewTypes = readFileSync(fileURLToPath(new URL("../lib/settingsViewTypes.ts", import.meta.url)), "utf8");

// ① single entry: no rail feature, no standalone pane branch.
ok(!panel.includes('{ id: "preapproveManagedPaths", group:'), "rail has no preapprove entry (feature removed from the array)");
ok(!panel.includes('{selected === "preapproveManagedPaths" &&'), "no standalone pane branch for preapprove (single entry)");

// ① tl1741r2: the autopilot card body (and its preapprove sub-block) restored
//    as a master-switch-free card; the retired master key stays retired.
{
  ok(labDefault.includes('id: "autopilot"'), "autopilot entry is back in the layout (tl1741r2)");
  ok(!labDefault.includes('onKeys: ["autopilot"]'), "the autopilot light stays retired with the master key (tl1741 removal stands)");
  ok(panel.includes('{selected === "autopilot" && ('), "autopilot pane branch restored (tl1741r2)");
  ok(panel.includes("autopilot-preapprove-subblock"), "pre-approval sub-block rides the restored autopilot card (tl1741r2)");
  ok(panel.includes('app.SetPreapproveManagedPaths('), "panel call sites restored with the card (tl1741r2)");
  ok(!panel.includes("app.SetDesktopAutopilot("), "the autopilot master-switch setter stays retired (card body only)");
}

// ② full access re-homed (unchanged): automation group.
{
  const autoBlock = labDefault.slice(labDefault.indexOf('key: "automation"'), labDefault.indexOf('key: "efficiency"'));
  ok(autoBlock.includes('id: "fullAccess"'), `full access group = automation (got ${autoBlock.includes('id: "fullAccess"') ? "automation" : "missing"})`);
}

// ③ zero behaviour by scope (364 交付口径不变): the semantic surface is
//    untouched — bridge method, persisted field names and view-type fields
//    all live on (settings-preapprove-managed.test.ts 详钉).
ok(bridge.includes("SetPreapproveManagedPaths(enabled: boolean"), "bridge method name unchanged");
ok(viewTypes.includes("experimentalPreapproveManagedPaths") && viewTypes.includes("preapproveSkills") && viewTypes.includes("preapproveSessionStores"), "persisted field names unchanged");
ok(viewTypes.includes("preapproveBashEscape"), "category reads unchanged (no field rename smuggled in)");
{
  const calls = panel.match(/app\.SetPreapproveManagedPaths\(/g) ?? [];
  ok(calls.length >= 2, `panel wires the sub-block master + category writes (got ${calls.length} call sites)`);
}
ok(!panel.includes("SetExperimentalFullAccess") === false, "full-access switch wiring untouched (presence check)");

// ④ remaining render-table entries all still present (no silent drop).
{
  // tl1741r2：autopilot/sessionCollab 卡体回表；其余在位（no silent drop）。
  const ids = ["fullAccess", "splitView", "todoSidebar", "autopilot", "sessionCollab"];
  const missing = ids.filter((id) => !labDefault.includes(`id: "${id}"`));
  ok(missing.length === 0,
    `render-table entries intact (missing: ${missing.join(", ") || "none"})`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
