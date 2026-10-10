// Run: npx tsx src/__tests__/task604-lab-picks-settings-path.test.ts
// 任务 604 acceptance harness (detail dialog settings-location hint):
//  ① the sampled paths are ACCURATE — three picks (one standalone, two 561
//     merged-card members) resolve to the exact rail location, cross-checked
//     against the SettingsPanel features render table (the rail's own data);
//  ② every current wall pick carries a location and the map covers nothing
//     outside LAB_WALL_PICKS;
//  ③ the label key exists in all three dialects and the dialog renders the
//     hint row only when a path is present (pure-display picks show no row).

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { LAB_SETTINGS_LOCATION, LAB_WALL_PICKS, type LabWallPickId } from "../lib/experimentTiers";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const wallSource = readFileSync(fileURLToPath(new URL("../components/LabPicksWall.tsx", import.meta.url)), "utf8");
const dialogSource = readFileSync(fileURLToPath(new URL("../components/LabPickDetailDialog.tsx", import.meta.url)), "utf8");
const panelSource = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");

console.log("\ntask 604 lab picks settings-location hint");

// ① sampled paths accurate — resolved against the REAL locale values, then
// cross-checked against the rail's features render table in SettingsPanel.
{
  const path = (pick: LabWallPickId): string => {
    const loc = LAB_SETTINGS_LOCATION[pick];
    if (!loc) return "";
    return [zh["settings.title"], zh["settings.tab.experimental"], zh[loc[0]], zh[loc[1]]].join(" → ");
  };
  ok(path("sessionWall") === "设置 → 实验室 → 界面 → 会话图墙",
    `sampled sessionWall resolves to 设置 → 实验室 → 界面 → 会话图墙 (got ${path("sessionWall")})`);
  ok(path("restartUpdate") === "设置 → 实验室 → 界面 → 更新与反馈",
    `sampled restartUpdate (561 merged member) resolves to 更新与反馈 card (got ${path("restartUpdate")})`);
  ok(path("budgetControl") === "设置 → 实验室 → 提效 → 上下文治理",
    `sampled budgetControl (561 merged member) resolves to 上下文治理 card (got ${path("budgetControl")})`);
  // Cross-source accuracy: every map entry must name a REAL rail entry —
  // 任务 722/724 起 render table 就是实验室布局默认数据（src/lab/
  // labLayoutDefault.ts）：卡 id 必须落在所指组的块内，labelKey 与卡键一致。
  const layoutSource = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  let anchored = true;
  for (const [pick, [groupKey, cardKey]] of Object.entries(LAB_SETTINGS_LOCATION)) {
    const card = cardKey.slice("settings.".length);
    const group = groupKey.slice("settings.labGroup.".length);
    const groupStart = layoutSource.indexOf(`key: "${group}"`);
    const groupEnd = layoutSource.indexOf('key: "', groupStart + 1);
    const block = layoutSource.slice(groupStart, groupEnd > groupStart ? groupEnd : undefined);
    const entryAt = block.indexOf(`id: "${card}"`);
    const entryText = entryAt >= 0 ? block.slice(entryAt, entryAt + 240) : "";
    if (entryAt < 0 || !entryText.includes(`labelKey: "${cardKey}"`)) {
      anchored = false;
      process.stdout.write(`    drift: ${pick} → ${group}/${card}\n`);
    }
  }
  ok(anchored, "every location names an existing layout entry (rail truth; 任务722 布局数据为准)");
}

// ② full coverage of current picks, nothing outside the pick list.
{
  const keys = Object.keys(LAB_SETTINGS_LOCATION);
  ok(keys.length === LAB_WALL_PICKS.length,
    `location map covers all ${LAB_WALL_PICKS.length} picks (got ${keys.length})`);
  ok(LAB_WALL_PICKS.every((id) => Object.prototype.hasOwnProperty.call(LAB_SETTINGS_LOCATION, id)),
    "every wall pick has a settings location today");
  ok(keys.every((k) => (LAB_WALL_PICKS as readonly string[]).includes(k)),
    "no stray entries outside LAB_WALL_PICKS (future pure-display picks stay unmapped)");
  // A simulated path-less pick (pure display, no lab switch) is absent from
  // the map — the mechanism the dialog degrades on.
  ok(!("demoFutureSwitch" in LAB_SETTINGS_LOCATION),
    "simulated pure-display pick has no location entry");
}

// ③ three dialects + render contract: row only when a path exists.
{
  ok(zh["settings.labPicks.settingsPath"] === "设置位置：", "zh carries 设置位置：");
  ok(zhTW["settings.labPicks.settingsPath"] === "設定位置：", "zh-TW carries 設定位置：");
  ok(en["settings.labPicks.settingsPath"] === "Settings location:", "en carries Settings location:");
  ok(wallSource.includes("function settingsPathFor") && wallSource.includes("settingsPath={settingsPathFor(t, openPick)}"),
    "wall builds the path from the register and passes it into the dialog");
  ok(wallSource.includes('if (!loc) return null;'),
    "path-less pick yields null (no fabricated route)");
  ok(dialogSource.includes("settingsPath?: readonly string[] | null"),
    "dialog takes settingsPath as an optional prop");
  ok(dialogSource.includes("{settingsPath && settingsPath.length > 0 ? (") && dialogSource.includes("lab-pick-dialog__path"),
    "dialog renders the hint row only when a path is present");
  ok(dialogSource.includes('t("settings.labPicks.settingsPath")'),
    "hint row label uses the three-dialect key");
}

process.stdout.write(`\ntask 604: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
