// Task 364 acceptance (two pure-UI regroup moves in the lab, same precedent
// as task 359: grouping/mounts only — config keys, persistence, switch
// semantics and approval behaviour are byte-identical):
//  ① managed-path pre-approval now lives INSIDE the autopilot card as a
//     sub-block (switch + four category checkboxes + risk line intact) and
//     has exactly ONE entry: no rail item, no standalone pane branch;
//  ② full access (YOLO) moved from the misc group to efficiency;
//  ③ zero behaviour: the config keys / bridge setter calls / persisted field
//     names are unchanged — asserted by scope (the moved block still calls
//     the same app.SetPreapproveManagedPaths signature);
//  ④ the task 359 group machinery (labGroups counts + sticky rail directory)
//     stays green — it derives from the features array, so the deleted entry
//     flows through automatically; run task359 alongside this file.
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

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");

console.log("\ntask 364 lab regroup");

// ① single entry: no rail feature, no standalone pane branch.
ok(!panel.includes('{ id: "preapproveManagedPaths", group:'), "rail has no preapprove entry (feature removed from the array)");
ok(!panel.includes('{selected === "preapproveManagedPaths" &&'), "no standalone pane branch for preapprove (single entry)");
ok(panel.includes('id: "autopilot", group: "efficiency"'), "autopilot stays in the efficiency group (the merge target)");

// ① sub-block inside the autopilot card, full anatomy preserved.
{
  const start = panel.indexOf('{selected === "autopilot" && (');
  const next = panel.indexOf('{selected === "highSpeedModel" && (');
  ok(start >= 0 && next > start, "autopilot pane branch located");
  const card = panel.slice(start, next);
  ok(card.includes("autopilot-preapprove-subblock"), "pre-approval renders as a named sub-block INSIDE the autopilot card");
  ok(card.includes('app.SetPreapproveManagedPaths('), "master switch setter still wired inside the card");
  ok(card.includes('type="checkbox"') && card.includes("preapproveSkills") && card.includes("preapproveBashEscape"), "the four category checkboxes moved along (skills/hooks/stores/bash)");
  ok(card.includes("settings.preapproveManagedPaths.warning"), "risk line moved along");
  ok(card.includes("settings.preapproveManagedPaths.on") && card.includes("settings.preapproveManagedPaths.off"), "on/off switch labels intact");
}

// ② full access re-homed.
{
  const m = panel.match(/\{ id: "fullAccess", group: "([a-z]+)"/);
  ok(Boolean(m) && m![1] === "efficiency", `full access group = efficiency (got ${m ? m[1] : "missing"})`);
}

// ③ zero behaviour by scope: the moved block still uses the same bridge
//    method, same persisted field names, same five-value single write.
ok(panel.includes("app.SetPreapproveManagedPaths"), "bridge method name unchanged");
ok(panel.includes("experimentalPreapproveManagedPaths") && panel.includes("preapproveSkills") && panel.includes("preapproveSessionStores"), "persisted field names unchanged");
ok(panel.includes("Boolean(s.preapproveSkills)") && panel.includes("Boolean(s.preapproveBashEscape)"), "category reads unchanged (no field rename smuggled in)");
// Exactly one call site family: the setter appears in the card only (one in
// the switch, one in the checkbox handler) — no duplicated parallel wiring.
{
  const calls = panel.match(/app\.SetPreapproveManagedPaths\(/g) ?? [];
  ok(calls.length === 2, `setter has exactly the two known call sites in the card (got ${calls.length})`);
}
ok(!panel.includes("SetExperimentalFullAccess") === false, "full-access switch wiring untouched (presence check)");

// ④ remaining render-table entries all still present (no silent drop).
{
  const ids = ["autopilot", "dream", "sessionCollab", "fullAccess", "splitView", "todoSidebar"];
  const missing = ids.filter((id) => !panel.includes(`id: "${id}"`));
  ok(missing.length === 0, `render-table entries intact (missing: ${missing.join(", ") || "none"})`);
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
