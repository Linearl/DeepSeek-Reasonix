// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1097-log-management.test.tsx
//
// tl 1097 验收测试——日志分级保留（B 方案）前端面：
//  ① 布局：logManagement 参数卡在 observability 组，kind=param，两个 segment
//     字段（告警日志保留时间 + 空间上限）；ts 默认与 yaml 同构同值；
//  ② 值语义：labParamFields 注册表两字段——0 = 内置默认（7 天 / 100MB）、
//     -1 = 永久 / 无限制，写入走 app 桥 setter；
//  ③ SettingsView / bridge：四个新键（desktopAlertLogRetentionDays /
//     desktopAlertLogMaxTotalMB / AlertLogUsage / OpenLogDir）接线齐全；
//  ④ 三语文案：16 个新键（区块说明 + 2 标签 + 2 hint + 4+4 选项 + 警示行 +
//     占用行 + 打开目录×2）三语齐全，且文案含「警告与错误」限定语（不得泛指
//     「日志保留时间」）；
//  ⑤ 永久+无限制语义：仅两者同时为 -1 时警示行条件成立（foreverUnlimited
//     判定），只选其一不成立。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { resolveLabLayout } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { LAB_PARAM_FIELDS, LAB_PARAM_FIELD_IDS } from "../lab/labParamFields";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const yamlText = readFileSync(`${frontendRoot}/src/lab/lab-layout.yaml`, "utf8");
const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8");
const registry = readFileSync(`${frontendRoot}/src/lab/labParamFields.ts`, "utf8");
const view = readFileSync(`${frontendRoot}/src/lib/settingsViewTypes.ts`, "utf8");
const types = readFileSync(`${frontendRoot}/src/lib/types.ts`, "utf8");
const bridge = readFileSync(`${frontendRoot}/src/lib/bridge.ts`, "utf8");

console.log("\ntl 1097 日志分级保留——前端验收");

// 白名单与 SettingsPanel 一致。
const paneIds = new Set<string>();
{
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "SettingsPanel declares the LAB_PANE_IDS whitelist");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
}

// ── ① 布局：logManagement 卡进可观测性组，yaml ≡ 内置默认 ──
{
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  ok(resolved.source === "yaml" && resolved.warnings.length === 0,
    `shipped lab-layout.yaml (with logManagement card) resolves as yaml source (warnings: ${JSON.stringify(resolved.warnings)})`);
  ok(JSON.stringify(resolved.layout) === JSON.stringify(LAB_LAYOUT_DEFAULT_DATA),
    "yaml structure equals the built-in default (logManagement card included)");
  const obs = LAB_LAYOUT_DEFAULT_DATA.groups.find((g) => g.key === "observability");
  const card = obs?.entries.find((e) => e.id === "logManagement") as
    | { id: string; kind?: string; tier?: string; fields?: Array<{ id: string; input: string }> }
    | undefined;
  ok(Boolean(card), "logManagement entry exists in the observability group");
  ok(card?.kind === "param", "logManagement is a kind: param card");
  ok(card?.tier === "optional", "logManagement container tier is optional");
  ok(JSON.stringify(card?.fields) === JSON.stringify([
    { id: "alertLogRetentionDays", input: "segment" },
    { id: "alertLogMaxTotalMB", input: "segment" },
  ]), "logManagement carries exactly two segment fields (retention + size)");
  ok(paneIds.has("logManagement"), "logManagement is in the pane whitelist");
}

// ── ② 值语义：注册表两字段 ──
{
  ok(LAB_PARAM_FIELD_IDS.has("alertLogRetentionDays") && LAB_PARAM_FIELD_IDS.has("alertLogMaxTotalMB"),
    "registry registers both tl1097 field ids");
  const r = LAB_PARAM_FIELDS.alertLogRetentionDays;
  const sz = LAB_PARAM_FIELDS.alertLogMaxTotalMB;
  ok(r.input === "segment" && sz.input === "segment", "both bindings are segment inputs");
  ok(JSON.stringify(r.options?.map((o) => o.value)) === JSON.stringify([0, 30, 90, -1]),
    "retention options are 7d(=0 default)/30d/90d/forever(-1)");
  ok(JSON.stringify(sz.options?.map((o) => o.value)) === JSON.stringify([0, 300, 1024, -1]),
    "size options are 100MB(=0 default)/300MB/1GB/unlimited(-1)");
  ok(registry.includes("app.SetDesktopAlertLogRetentionDays(Number(value))") &&
     registry.includes("app.SetDesktopAlertLogMaxTotalMB(Number(value))"),
    "registry wires the tl1097 app bridge setters");
  // ⑤ 永久+无限制判定：pane 源码里两者同时为 -1 才成立。
  ok(panel.includes("(s.desktopAlertLogRetentionDays ?? 0) === -1 && (s.desktopAlertLogMaxTotalMB ?? 0) === -1"),
    "foreverUnlimited requires BOTH dials set to -1 (只选其一不触发警示)");
}

// ── ③ SettingsView / types / bridge 接线 ──
{
  ok(view.includes("desktopAlertLogRetentionDays?: number") && view.includes("desktopAlertLogMaxTotalMB?: number"),
    "SettingsView carries both read keys");
  ok(types.includes("desktopAlertLogRetentionDays?: number") && types.includes("desktopAlertLogMaxTotalMB?: number"),
    "types.ts mirror carries both read keys");
  ok(bridge.includes("SetDesktopAlertLogRetentionDays(days: number): Promise<void>") &&
     bridge.includes("SetDesktopAlertLogMaxTotalMB(mb: number): Promise<void>") &&
     bridge.includes("AlertLogUsage(): Promise<{ bytes: number; files: number; dir: string }>"),
    "bridge AppBindings declares all three tl1097 methods");
  ok(bridge.includes("async SetDesktopAlertLogRetentionDays() {},") &&
     bridge.includes("async SetDesktopAlertLogMaxTotalMB() {},") &&
     bridge.includes("async AlertLogUsage() { return { bytes: 0, files: 0, dir: \"\" }; },"),
    "bridge mock provides the three tl1097 methods");
  // Pane 接线：专用组件渲染 + 卡内占用行 + 打开目录按钮。
  ok(panel.includes("{selected === \"logManagement\" && (") && panel.includes("<LogManagementLabPane"),
    "SettingsPanel has a dedicated logManagement pane branch");
  ok(panel.includes("function LogManagementLabPane"),
    "LogManagementLabPane component is defined");
  ok(panel.includes("t(\"settings.logManagement.foreverUnlimitedWarning\")"),
    "pane renders the forever+unlimited warning line via locale key");
  ok(panel.includes("t(\"settings.logManagement.openDir\")"),
    "pane renders the open-log-directory button via locale key");
  ok(panel.includes("app.OpenLogDir()"),
    "pane wires the open-directory button to app.OpenLogDir");
}

// ── ④ 三语文案（16 个新键，三语齐全；含「警告与错误」限定语）──
{
  const keys = [
    "settings.logManagement",
    "settings.logManagement.hint",
    "settings.alertLogRetention.label",
    "settings.alertLogRetention.hint",
    "settings.alertLogRetention.opt7d",
    "settings.alertLogRetention.opt30d",
    "settings.alertLogRetention.opt90d",
    "settings.alertLogRetention.optForever",
    "settings.alertLogMaxTotal.label",
    "settings.alertLogMaxTotal.hint",
    "settings.alertLogMaxTotal.opt100mb",
    "settings.alertLogMaxTotal.opt300mb",
    "settings.alertLogMaxTotal.opt1gb",
    "settings.alertLogMaxTotal.optUnlimited",
    "settings.logManagement.foreverUnlimitedWarning",
    "settings.logManagement.usage",
    "settings.logManagement.openDirLabel",
    "settings.logManagement.openDir",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all 18 tl1097 keys (missing: ${JSON.stringify(missing)})`);
  }
  // 「警告与错误」限定语：标签与 hint 不得泛指「日志保留时间」/「日志空间上限」。
  const zh = readFileSync(`${frontendRoot}/src/locales/zh.ts`, "utf8");
  ok(zh.includes("\"settings.alertLogRetention.label\": \"警告与错误日志保留时间\""),
    "zh retention label carries the 警告与错误 qualifier (逐字)");
  ok(zh.includes("\"settings.alertLogMaxTotal.label\": \"警告与错误日志空间上限\""),
    "zh size label carries the 警告与错误 qualifier (逐字)");
  ok(zh.includes("\"settings.logManagement.foreverUnlimitedWarning\": \"⚠️ 当前设置为「永久 + 无限制」，警告与错误日志将持续累积，不自动清理。\""),
    "zh forever+unlimited warning copy is verbatim per spec");
  ok(zh.includes("\"settings.logManagement.usage\": \"当前占用：{size} MB（{n} 个文件）\""),
    "zh usage placeholder copy is verbatim per spec");
  ok(zh.includes("\"settings.logManagement.openDir\": \"打开日志目录\""),
    "zh open-directory button copy is verbatim per spec");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
