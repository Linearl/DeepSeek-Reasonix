// Run: npx tsx src/__tests__/task722-lab-layout-mechanism.test.ts
// 任务 722/724/727 验收测试——实验室布局 yaml 化机制 + 第一版分组定义 +
// 安全/成本控制细粒度子开关 + 心跳轮换桥接。
//  ① 724 机制：合法 yaml → yaml 源；缺失/损坏/越界引用 → 回退内置默认
//     （warnings 带原因），任何异常都不产生半份数据；
//  ② 724 安全网：解析器子集之外的语法（tab、重复键、未知键引用、白名单外
//     entry id、字典外 labelKey）全部打回默认；
//  ③ 722 六点：yaml 与内置默认同构（分组树/成员/档位/onKeys 逐项一致）；
//  ④ 722 点6：三子键（nil=跟随总开关）前端 wire 齐全（视图字段/桥接/行）；
//  ⑤ 727：heartbeatRotation 桥接 heartbeat-rotation.json（桥接声明 + mock +
//     卡内行 + 布局成员登记），三语键齐全。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { parseSimpleYaml, YamlParseError } from "../lab/parseSimpleYaml";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { resolveLabLayout, railTiersForDefault } from "../lab/labLayout";
import { LAB_LIGHT_KEY_IDS } from "../lab/labLightKeys";
import { LAB_GROUP_KEYS } from "../lab/labLayoutTypes";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;
const yamlText = readFileSync(`${frontendRoot}/src/lab/lab-layout.yaml`, "utf8");
const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8");
const styles = readFileSync(`${frontendRoot}/src/styles.css`, "utf8");
const view = readFileSync(`${frontendRoot}/src/lib/settingsViewTypes.ts`, "utf8");
const types = readFileSync(`${frontendRoot}/src/lib/types.ts`, "utf8");
const bridge = readFileSync(`${frontendRoot}/src/lib/bridge.ts`, "utf8");

console.log("\ntask 722/724 lab layout mechanism + first-version grouping");

// 白名单与 SettingsPanel 一致（pane 白名单来自组件源码）。
const paneIds = new Set<string>();
{
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "SettingsPanel declares the LAB_PANE_IDS whitelist");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
}

// ── ① 合法 yaml 通过全部校验，源=yaml ──
{
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  ok(resolved.source === "yaml" && resolved.warnings.length === 0,
    `shipped lab-layout.yaml resolves as yaml source (warnings: ${JSON.stringify(resolved.warnings)})`);
  // 与内置默认同构同值（安全网回退后界面一致）。
  ok(JSON.stringify(resolved.layout) === JSON.stringify(LAB_LAYOUT_DEFAULT_DATA),
    "yaml structure equals the built-in default (fallback is seamless)");
}

// ── ② 安全网：各类损坏都回退默认 ──
{
  const r0 = resolveLabLayout(null, { allowedEntryIds: paneIds });
  ok(r0.source === "default" && r0.layout.version === 1, "missing yaml falls back to the default");
  const r1 = resolveLabLayout("version: 1\ngroups: [", { allowedEntryIds: paneIds });
  ok(r1.source === "default" && r1.warnings.length > 0, "parse error falls back with a reason");
  const r2 = resolveLabLayout("version: 2\ngroups: []", { allowedEntryIds: paneIds });
  ok(r2.source === "default", "unsupported version falls back");
  const r3 = resolveLabLayout(yamlText.replace("experimentalTabCompress", "experimentalTabCompressTypo"), { allowedEntryIds: paneIds });
  ok(r3.source === "default", "unknown light-key reference falls back");
  // 任务 742：tabCompress/tabModeTint 条目合并为 tabManagement——安全网
  // needle 随迁到新卡 id（needle 不存在时 replace 是 no-op，测试会假绿）。
  const r4 = resolveLabLayout(yamlText.replace("id: tabManagement", "id: notAPaneBranch"), { allowedEntryIds: paneIds });
  ok(r4.source === "default", "entry id outside the pane whitelist falls back");
  const r5 = resolveLabLayout(yamlText.replace("labelKey: settings.tabManagement", "labelKey: settings.noSuchKey"), { allowedEntryIds: paneIds });
  ok(r5.source === "default", "labelKey outside the dictionary falls back (文案必须走 locale 键)");
  // 回退结果与内置默认逐位一致——不白屏、不半渲染。
  ok(r1.layout === LAB_LAYOUT_DEFAULT_DATA && r3.layout === LAB_LAYOUT_DEFAULT_DATA,
    "every fallback returns the built-in default object");
}

// ── ②b 解析器子集 ──
{
  const doc = parseSimpleYaml(`# comment
version: 1
flow: [a, b, c]
quoted: "x: y"
nested:
  key: value
  list:
    - one
    - two
items:
  - id: first
    note: inline map item
  - id: second
    note: another
`);
  ok(doc.version === 1 && Array.isArray(doc.flow) && (doc.flow as string[]).length === 3,
    "parser reads scalars and flow sequences");
  ok((doc as { nested: { list: string[] } }).nested.list.length === 2, "parser reads nested maps and block lists");
  ok((doc as { items: Array<{ id: string }> }).items[1].note === "another",
    "parser reads list-item maps with aligned continuation keys");
  let threw = false;
  try { parseSimpleYaml("a: 1\n\ttab: 2"); } catch (e) { threw = e instanceof YamlParseError; }
  ok(threw, "tab indentation is rejected");
  threw = false;
  try { parseSimpleYaml("a: 1\na: 2"); } catch (e) { threw = e instanceof YamlParseError; }
  ok(threw, "duplicate keys are rejected");
  threw = false;
  try { parseSimpleYaml("a: [1, 2"); } catch (e) { threw = e instanceof YamlParseError; }
  ok(threw, "unclosed flow sequences are rejected");
}

// ── ③ 722 六点（结构面：yaml+默认；行面：SettingsPanel 源）──
{
  // 点1：备用模型行在 modelStrategy 卡、不在 sessionCollab 卡。
  const msStart = panel.indexOf('{selected === "modelStrategy" && (');
  const msEnd = panel.indexOf('{selected === "contextGovernance" && (', msStart);
  const scStart = panel.indexOf('{selected === "sessionCollab" && (');
  const scEnd = panel.indexOf('{selected === "optimisticParallel" && (', scStart);
  ok(panel.slice(msStart, msEnd).includes('t("settings.fallbackModelSwitch")'),
    "点1: the fallback-model switch row lives in the modelStrategy card");
  ok(!panel.slice(scStart, scEnd).includes("fallbackModelSwitch"),
    "点1: the sessionCollab card no longer hosts the fallback-model row");
  // 点2：卡内只读行不再挂徽章（labLabel）。
  ok(!panel.includes('labLabel("modelCapabilityFilter"'),
    "点2: the retired row keeps its read-only display without a tier badge");
  // 点4：邮箱化默认「默认投递通道」行——标签在左、下拉框在右、整行跨双列。
  ok(panel.includes('className="set-gates__item set-gates__item--wide"') &&
     panel.indexOf('set-gates__item--wide') < panel.indexOf('t("settings.sessionCollabDefaultDelivery")') &&
     styles.includes(".set-gates__item--wide") && styles.includes("grid-column: 1 / -1"),
    "点4: the delivery-channel row is label-left / select-right spanning both columns");
  // 点6 组迁移已由 task517/561 钉；这里钉 yaml 的组序（自动化 → 提效）。
  const auto = yamlText.indexOf("key: automation");
  const eff = yamlText.indexOf("key: efficiency");
  const safety = yamlText.indexOf("id: safetyCostControl");
  ok(auto < eff && eff < safety, "点6: the safety/cost card sits in the efficiency group (after automation)");
  // 全部 onKeys 引用都在注册表里。
  const onKeys = [...yamlText.matchAll(/^ {8}onKeys: \[([^\]]*)\]/gm)].flatMap((m) => m[1].split(",").map((s) => s.trim()));
  ok(onKeys.length > 0 && onKeys.every((k) => LAB_LIGHT_KEY_IDS.has(k)),
    `every yaml onKeys reference is a registered light key (${onKeys.length} refs)`);
  // 组键受控词表。
  const groupKeys = [...yamlText.matchAll(/^  - key: (\w[-\w]*)$/gm)].map((m) => m[1]);
  ok(groupKeys.length === LAB_GROUP_KEYS.length && groupKeys.every((k) => (LAB_GROUP_KEYS as readonly string[]).includes(k)),
    "yaml group keys are the controlled 8-key vocabulary");
}

// ── ④ 722 点6：三子键 wire 面 ──
{
  ok(view.includes("safetyIdleTerminate?: boolean | null") && view.includes("safetyLoopStreakNote?: boolean | null") &&
     view.includes("safetyEventWaitRecheck?: boolean | null"),
    "SettingsView exposes the three sub-switch overrides (null = follow master)");
  ok(types.includes("safetyIdleTerminate?: boolean | null"), "config-mirror type carries the same trio");
  for (const b of [
    "SetSafetyIdleTerminate(enabled: boolean): Promise<void>;",
    "SetSafetyLoopStreakNote(enabled: boolean): Promise<void>;",
    "SetSafetyEventWaitRecheck(enabled: boolean): Promise<void>;",
    "async SetSafetyIdleTerminate() {}",
  ]) ok(bridge.includes(b), `bridge wire: ${b}`);
  // Go 双视图（81/123 both-views lesson）。
  const settingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
  // \s+ 容忍 gofmt 的字段对齐空格（锁死单空格会在 gofmt -w 后假红，742 实测）。
  ok((settingsApp.match(/SafetyIdleTerminate\s+\*bool\s+`json:"safetyIdleTerminate"`/g) ?? []).length === 2,
    "both Go settings views carry safetyIdleTerminate (JSON null = inherit)");
  ok((settingsApp.match(/SafetyEventWaitRecheck\s+\*bool\s+`json:"safetyEventWaitRecheck"`/g) ?? []).length === 2,
    "both Go settings views carry safetyEventWaitRecheck");
  // 卡内行：跟随读数 + 独立 setter。
  ok(panel.includes("s.safetyIdleTerminate ?? s.experimentalSafetyCostControl") &&
     panel.includes("app.SetSafetyIdleTerminate(on)") &&
     panel.includes("s.safetyLoopStreakNote ?? s.experimentalSafetyCostControl") &&
     panel.includes("app.SetSafetyLoopStreakNote(on)"),
    "sub-switch rows read (override ?? master) and write their own keys");
}

// ── ⑤ 727 heartbeatRotation ──
{
  ok(bridge.includes("HeartbeatRotationStatus(): Promise<{ enabled: boolean; path: string; err: string }>") &&
     bridge.includes("async HeartbeatRotationStatus()") && bridge.includes("async SetHeartbeatRotationEnabled() {}"),
    "bridge declares + stubs the heartbeat-rotation status/setter pair");
  ok(panel.includes("app.HeartbeatRotationStatus()") && panel.includes("app.SetHeartbeatRotationEnabled(on)"),
    "the safety/cost card hosts the rotation toggle (on-demand status, no second switch)");
  const rotationGo = readFileSync(`${repoRoot}/desktop/heartbeat_rotation_app.go`, "utf8");
  ok(rotationGo.includes("heartbeat-rotation.json") && rotationGo.includes("doc[\"enabled\"] = enabled"),
    "Go bridge reads/writes heartbeat-rotation.json in place (preserve other fields)");
  const memberIds = LAB_LAYOUT_DEFAULT_DATA.groups
    .flatMap((g) => g.entries.flatMap((e) => e.members?.map((m) => m.id) ?? []));
  ok(memberIds.includes("heartbeatRotation"), "layout default registers heartbeatRotation as a safetyCostControl member");
  ok(railTiersForDefault("safetyCostControl").join(",") === "recommended,optional",
    "safetyCostControl rail badges cover the folded members");
  // 三语键。
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const keys = ["settings.heartbeatRotation", "settings.heartbeatRotationHint", "settings.heartbeatRotation.on",
      "settings.heartbeatRotation.off", "settings.safetyCostControl.subHint"];
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries the 722/727 keys (missing: ${JSON.stringify(missing)})`);
    ok(!dict.includes("settings.safetyCostControl.memberState"), `${locale} drops the retired memberState key`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
