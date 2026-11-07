// Run: npx tsx src/__tests__/task775-lab-param-cards.test.ts
// 任务 775 验收测试——实验室参数迁移基建（kind:param）+ 首批两参数迁存储组。
//  ① 机制：kind/fields schema 走 resolver 校验；合法参数卡通过，引用越界
//     （未登记字段/形态不符/空 fields/feature 带 fields/param 带 members/
//     非法 kind）整份回退内置默认（724 安全网同款）；
//  ② 首批迁移：后台会话闲置释放（308-O4）+ Go 软内存上限（308-O3）以参数
//     卡登记进「存储」组，yaml 与内置默认同构同值；绑定注册表接线正确
//     （SettingsView 读键 + app 桥写方法 + number 边界）；
//  ③ SettingsPanel：两硬编码参数行自跨会话协作卡退役，pane 分派走通用
//     参数渲染器（已登记走数据，未登记硬编码兜底）；
//  ④ 三语文案：两参数标签/提示键三语齐全（复用既有键，零新增）。
//  tl1286 更新：软内存上限卡（goMemLimit，低设即 GC 满跑）退役，换 GC 策略
//     旋钮卡 gcPercent（debug.SetGCPercent；id/键/setter 同步换，机制断言
//     不变）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { resolveLabLayout, labEntryLightOn, findLabEntry } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { LAB_PARAM_FIELD_IDS, LAB_PARAM_FIELDS } from "../lab/labParamFields";

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
const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8") + "\n" + readFileSync(`${frontendRoot}/src/components/ExperimentalLabSection.tsx`, "utf8") + "\n" + readFileSync(`${frontendRoot}/src/lab/labPaneRegistry.ts`, "utf8");
const registry = readFileSync(`${frontendRoot}/src/lab/labParamFields.ts`, "utf8");
const view = readFileSync(`${frontendRoot}/src/lib/settingsViewTypes.ts`, "utf8");

console.log("\ntask 775 lab param-card infrastructure + first two migrations");

// 白名单与 SettingsPanel 一致（pane 白名单来自组件源码，722 同款抽取）。
const paneIds = new Set<string>();
{
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "SettingsPanel declares the LAB_PANE_IDS whitelist");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
}

// ── ② 首批迁移 → tl1309 卡③ 并卡：两参数（闲置释放/GC 策略）以成员行落
//    「资源治理」卡（infra 组），字段绑定不变（值语义单源在 labParamFields），
//    yaml ≡ 内置默认 ──
{
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  ok(resolved.source === "yaml" && resolved.warnings.length === 0,
    `shipped lab-layout.yaml (with param cards) resolves as yaml source (warnings: ${JSON.stringify(resolved.warnings)})`);
  ok(JSON.stringify(resolved.layout) === JSON.stringify(LAB_LAYOUT_DEFAULT_DATA),
    "yaml structure equals the built-in default (param cards included)");

  const storage = LAB_LAYOUT_DEFAULT_DATA.groups.find((g) => g.key === "storage");
  // tl1309 卡③：GC 策略/闲置释放两参数卡迁出存储组——存储组回落
  // sessionStore/homePointer/jsonlZstd 三项。
  ok(storage?.entries.length === 3, "storage group carries sessionStore + homePointer + jsonlZstd (tl1309 card-3 move)");
  const infra = LAB_LAYOUT_DEFAULT_DATA.groups.find((g) => g.key === "infra");
  const gov = infra?.entries.find((e) => e.id === "resourceGovernance");
  // 「资源治理」合并卡：六成员（任务书③），两参数 id 以成员行承载（tier 随迁：
  // 闲置释放 recommended / GC 可选——与原独立参数卡 rail 徽章一致）。
  ok(gov?.tier === "stable" && gov.labelKey === "settings.resourceGovernance",
    "resourceGovernance is the stable-container merged card in the infra group");
  const idle = gov?.members?.find((m) => m.id === "idleReleaseMinutes");
  const mem = gov?.members?.find((m) => m.id === "gcPercent");
  ok(idle?.tier === "recommended" && idle.labelKey === "settings.detachedIdleRelease",
    "idleReleaseMinutes rides resourceGovernance as a recommended member (tier/label unchanged)");
  ok(mem?.tier === "recommended" && mem.labelKey === "settings.gcPercent",
    "gcPercent rides resourceGovernance as a recommended member (tl1487 七组①, label unchanged)");
  ok(gov?.members?.length === 6,
    "resourceGovernance carries exactly six members (runtimeReuse/orphanHandling/idleReleaseMinutes/gcPercent/acpListen/localServer)");
  ok(Boolean(findLabEntry(LAB_LAYOUT_DEFAULT_DATA, "resourceGovernance")) &&
     !findLabEntry(LAB_LAYOUT_DEFAULT_DATA, "idleReleaseMinutes") && !findLabEntry(LAB_LAYOUT_DEFAULT_DATA, "gcPercent"),
    "the merged card is addressable through findLabEntry; the two param ids are members, not entries");
}

// ── ②b 绑定注册表：值语义收拢在代码 ──
{
  ok(LAB_PARAM_FIELD_IDS.has("detachedIdleReleaseMinutes") && LAB_PARAM_FIELD_IDS.has("gcPercent"),
    "registry registers both first-batch field ids");
  ok(LAB_PARAM_FIELDS.detachedIdleReleaseMinutes.input === "number" &&
     LAB_PARAM_FIELDS.gcPercent.input === "number",
    "both bindings are number inputs");
  ok(registry.includes("app.SetDetachedIdleReleaseMinutes(Number(value))") &&
     registry.includes("app.SetGCPercent(Number(value))"),
    "registry wires the existing app bridge setters (配置键/setter 零变化)");
  ok(view.includes("detachedIdleReleaseMinutes?: number") && view.includes("gcPercent?: number"),
    "SettingsView carries both read keys");
  ok(registry.includes('hintKey: "settings.detachedIdleReleaseHint"') &&
     registry.includes('hintKey: "settings.gcPercentHint"'),
    "bindings hint through dictionary keys (禁止内联文案)");
}

// ── ③ SettingsPanel：硬编码行退役 + 通用参数渲染接线 ──
{
  const scStart = panel.indexOf('{selected === "sessionCollab" && (');
  // tl1309：optimisticParallel 独立 pane 退役（并入 contextGovernance 卡）——
  // 边界 needle 随迁下一分支（needle 消失=indexOf -1=slice 退化=断言假绿，
  // 742 needle-随迁同款教训）。
  const scEnd = panel.indexOf('{selected === "autopilot" && (', scStart);
  const sessionCollabPane = panel.slice(scStart, scEnd);
  ok(!sessionCollabPane.includes("detachedIdleRelease") && !sessionCollabPane.includes("gcPercent"),
    "the sessionCollab card no longer hosts the two parameter rows");
  ok(!panel.includes("SetDetachedIdleReleaseMinutes(") && !panel.includes("SetGCPercent("),
    "the panel has no direct setter calls left (写入走注册表)");
  ok(panel.includes('selectedEntry?.kind === "param"') && panel.includes("<LabParamFieldsCard entry={selectedEntry"),
    "the pane dispatch routes param cards to the generic data-driven renderer");
  ok(panel.includes("import { LAB_PARAM_FIELDS } from \"../lab/labParamFields\";"),
    "the panel imports the param binding registry");
  // tl1309 卡③：两参数卡退役为「资源治理」成员行——pane 白名单/union id 摘除
  // （961 先例；字段值语义仍单源在 labParamFields，pane 行经注册表绑定渲染）。
  ok(!paneIds.has("idleReleaseMinutes") && !paneIds.has("gcPercent"),
    "both param card ids left the pane whitelist (tl1309 member fold)");
  ok(!panel.includes('| "idleReleaseMinutes"') && !panel.includes('| "gcPercent"'),
    "both param card ids left the pane union (tl1309 member fold)");
  ok(panel.includes('| "resourceGovernance"') && panel.includes('"resourceGovernance",'),
    "the merged container id entered the pane union + whitelist");
  ok(panel.includes('labLabel("idleReleaseMinutes"') && panel.includes('labLabel("gcPercent"'),
    "both param member rows wrap their labels in labLabel (行级徽章保留，562⑤/621 口径)");
  ok(panel.includes("LAB_PARAM_FIELDS.detachedIdleReleaseMinutes.write") && panel.includes("LAB_PARAM_FIELDS.gcPercent.write"),
    "the two param member rows read/write through the registry bindings (值语义单源)");
}

// ── ① 机制安全网：参数卡引用越界 ⇒ 整份回退默认 ──
{
  // tl1309 卡③：fixture 卡 id 换仍在白名单内的参数卡 logManagement（机制
  // 断言不变——resolver 只核 kind/fields/引用，不核具体 id；字段仍用
  // detachedIdleReleaseMinutes number 绑定核对注册表一致性）。
  const base = `version: 1
groups:
  - key: storage
    labelKey: settings.labGroup.storage
    entries:
      - id: logManagement
        labelKey: settings.logManagement
        kind: param
        fields:
          - id: detachedIdleReleaseMinutes
            input: number
`;
  const good = resolveLabLayout(base, { allowedEntryIds: paneIds });
  ok(good.source === "yaml", "a minimal valid param-card yaml resolves");

  const cases: Array<[string, string, string]> = [
    ["unknown field id falls back", base.replace("id: detachedIdleReleaseMinutes", "id: notAField"), "field"],
    ["input kind outside the four-shape set falls back", base.replace("input: number", "input: slider"), "input"],
    ["input mismatching the registry binding falls back", base.replace("input: number", "input: text"), "mismatch"],
    ["empty fields list falls back", base.replace("          - id: detachedIdleReleaseMinutes\n            input: number", ""), "empty"],
    ["missing fields on a param card falls back", base.replace("        kind: param\n        fields:\n          - id: detachedIdleReleaseMinutes\n            input: number", "        kind: param"), "absent"],
    ["fields on a feature card falls back", base.replace("        kind: param\n        fields:\n          - id: detachedIdleReleaseMinutes\n            input: number", "        fields:\n          - id: detachedIdleReleaseMinutes\n            input: number"), "feature+fields"],
    ["members on a param card falls back", base.replace("        kind: param\n        fields:", "        kind: param\n        members: []\n        fields:"), "param+members"],
    ["unknown kind value falls back", base.replace("kind: param", "kind: card"), "kind"],
  ];
  for (const [label, doc, note] of cases) {
    const r = resolveLabLayout(doc, { allowedEntryIds: paneIds });
    ok(r.source === "default" && r.layout === LAB_LAYOUT_DEFAULT_DATA, `${label} (${note})`);
  }
}

// ── ④ 三语文案（复用既有键，三语齐全）──
{
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const keys = ["settings.detachedIdleRelease", "settings.detachedIdleReleaseHint", "settings.gcPercent", "settings.gcPercentHint"];
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries the two param label/hint keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
