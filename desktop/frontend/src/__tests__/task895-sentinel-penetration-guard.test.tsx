// Run: npx tsx src/__tests__/task895-sentinel-penetration-guard.test.tsx
// 任务 895 验收⑤：哨兵穿透封堵开关的 UI 面——
//  ① 「安全 / 成本控制」卡（safetyCostControl，517/722 合并卡）内只加一条
//     开关条目：pane 内恰一个写入点、读生效值（缺省开）、开/关分段按钮；
//  ② 卡片结构不动：布局 yaml/内置默认的 safetyCostControl 成员与 onKeys
//     不含本键（条目级追加，不挂成员徽章、不参与卡灯）；
//  ③ 快照与桥接：settingsViewTypes + DesktopStartupSettingsView 双视图字段、
//     Go 侧 SettingsView/DesktopStartupSettingsView 双快照 + 构建器 +
//     App setter + config setter + render 回环 + boot 装配齐；
//  ④ 三语 i18n：label/hint/on/off 三个语种齐全。
// （后端判定面的回归矩阵在 internal/sentinel/penetration_test.go。）

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { resolveLabLayout } from "../lab/labLayout";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const frontendRoot = fileURLToPath(new URL("../..", import.meta.url));
const repoRoot = `${frontendRoot}/../..`;
const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");
const readRepo = (rel: string) => readFileSync(`${repoRoot}/${rel}`, "utf8");

const panel = read("src/components/SettingsPanel.tsx");
const bridge = read("src/lib/bridge.ts");
const viewTypes = read("src/lib/settingsViewTypes.ts");
const startupTypes = read("src/lib/types.ts");
const zh = read("src/locales/zh.ts");
const en = read("src/locales/en.ts");
const zhTW = read("src/locales/zh-TW.ts");
const yamlText = read("src/lab/lab-layout.yaml");
const goSettingsApp = readRepo("desktop/settings_app.go");
const goPreferences = readRepo("desktop/settings_preferences.go");
const goConfig = readRepo("internal/config/config.go");
const goEdit = readRepo("internal/config/edit.go");
const goRender = readRepo("internal/config/render.go");
const goBoot = readRepo("internal/boot/boot.go");

console.log("\n任务 895 sentinel 穿透封堵开关（UI 面）");

// ① 卡内单条开关条目。
{
  const start = panel.indexOf('{selected === "safetyCostControl" && (');
  const end = panel.indexOf('{selected === "sessionCollab" && (');
  ok(start >= 0 && end > start, "safetyCostControl pane branch located");
  const card = start >= 0 && end > start ? panel.slice(start, end) : "";
  ok(card.includes('t("settings.sentinelPenetrationGuard")'),
    "card carries the switch row label key");
  ok(card.includes('t("settings.sentinelPenetrationGuardHint")'),
    "card row keeps its own hint key");
  ok((card.match(/app\.SetSentinelPenetrationGuard\(on\)/g) ?? []).length === 1,
    "exactly ONE writer for the switch inside the card");
  ok(card.includes("s.sentinelPenetrationGuard ?? true"),
    "row reads the effective value with default-on fallback");
  ok(card.includes('"settings.sentinelPenetrationGuard.on"') &&
     card.includes('"settings.sentinelPenetrationGuard.off"'),
    "segment buttons carry on/off keys");
  ok(card.includes('selected === "safetyCostControl"') && panel.includes('{selected === "sessionCollab" && ('),
    "pane boundaries intact (row inserted inside the card, not around it)");
  // 即时生效：本行不写 setRestartNeeded（判定路径每次调用读开关）。
  const rowStart = card.indexOf('label={t("settings.sentinelPenetrationGuard")}');
  const rowEnd = card.indexOf("</SettingsField>", rowStart);
  const row = rowStart >= 0 && rowEnd > rowStart ? card.slice(rowStart, rowEnd) : "";
  ok(row.length > 0, "switch row block located");
  ok(!row.includes("setRestartNeeded"), "toggle applies live (no restart banner)");
}

// ② 卡片结构不动：成员/灯不含本键（条目级追加）。
{
  // 白名单取自 SettingsPanel 的 LAB_PANE_IDS（与 722 验收同法）。
  const paneIds = new Set<string>();
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "SettingsPanel declares the LAB_PANE_IDS whitelist");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  ok(resolved.source === "yaml", "lab-layout.yaml still resolves as the layout source");
  const card = (resolved.layout.groups.flatMap((g) => g.entries).find((e) => e.id === "safetyCostControl") ?? null) as
    { members?: Array<{ id: string }>; onKeys?: string[] } | null;
  ok(Boolean(card), "safetyCostControl card present in the layout");
  if (card) {
    const memberIds = (card.members ?? []).map((m) => m.id);
    ok(!memberIds.includes("sentinelPenetrationGuard"), "switch is NOT a layout member (no rail badge added)");
    ok(!(card.onKeys ?? []).some((k) => k.toLowerCase().includes("penetration")),
      "switch does NOT join the card light keys");
    ok(JSON.stringify(memberIds) === JSON.stringify(["compactModel", "messageMerge", "heartbeatRotation"]),
      "card member list unchanged (722 structure preserved)");
  }
}

// ③ 视图字段与 Go 全链路。
for (const [name, src] of [["settingsViewTypes", viewTypes], ["types/DesktopStartup", startupTypes]] as const) {
  ok(src.includes("sentinelPenetrationGuard?: boolean;"), `${name} declares the snapshot field`);
}
{
  ok(/SentinelPenetrationGuard bool\s+`json:"sentinelPenetrationGuard"`/.test(goSettingsApp),
    "Go SettingsView carries the JSON field");
  ok((goSettingsApp.match(/SentinelPenetrationGuard bool\s+`json:"sentinelPenetrationGuard"`/g) ?? []).length === 2,
    "both Go snapshot structs (SettingsView + startup) carry the field");
  ok((goSettingsApp.match(/sentinelPenetrationGuardForView\(cfg\)/g) ?? []).length === 2,
    "both builders populate the effective value");
  ok(goSettingsApp.includes("func sentinelPenetrationGuardForView"), "effective-value helper exists");
  ok(goPreferences.includes("func (a *App) SetSentinelPenetrationGuard(enabled bool) error"),
    "App setter exists");
  ok(goPreferences.includes("sentinel.SetPenetrationGuard(enabled)"), "App setter applies live");
  ok(goConfig.includes('PenetrationGuard *bool `toml:"penetration_guard"`'),
    "config carries the user-global [sentinel] key");
  ok(goEdit.includes("func (c *Config) SetSentinelPenetrationGuard(enabled bool) error"),
    "config setter exists");
  ok(goRender.includes("penetration_guard = %v"), "render loop writes the explicit value (save keeps it)");
  ok(goRender.includes("# penetration_guard = false"), "render loop keeps a commented default line");
  ok(goBoot.includes("sentinel.SetPenetrationGuard(penetrationDefault)"), "boot wires the switch");
}

// ④ 桥接声明 + mock。
ok(bridge.includes("SetSentinelPenetrationGuard(enabled: boolean): Promise<void>;"),
  "bridge declares SetSentinelPenetrationGuard");
ok((bridge.match(/async SetSentinelPenetrationGuard\(\) \{\}/g) ?? []).length === 1,
  "bridge mock covers SetSentinelPenetrationGuard exactly once");

// ⑤ 三语 i18n。
for (const [name, src] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  for (const key of [
    "settings.sentinelPenetrationGuard",
    "settings.sentinelPenetrationGuardHint",
    "settings.sentinelPenetrationGuard.on",
    "settings.sentinelPenetrationGuard.off",
  ]) {
    ok(src.includes(`"${key}":`), `${name} locale carries ${key}`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
