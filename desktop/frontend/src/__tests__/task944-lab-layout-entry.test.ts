// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task944-lab-layout-entry.test.ts
//
// 任务 944 返修验收：「对话内产物卡片」实验室登记双面钉住——
//  ① rail 可见：出货 lab-layout.yaml（rail/搜索唯一数据源）ui 组含
//     renderArtifact 条目，且与内置默认同构同值同位次（944 交付时只登记
//     labLayoutDefault.ts 漏了 yaml——yaml 解析合法走 source=yaml，内置默认
//     永不兜底，条目因此在界面上凭空消失；722 机制的 JSON 全等已兜住全量
//     同构，这里按 944 验收口径单独再钉条目本身，失败信息直指本任务）；
//  ② 搜索可达：findLabEntryIdByQuery 对「对话内产物卡片」（zh）/
//     「In-chat artifact cards」（en）命中卡片（LAB_LAYOUT_FOR_SEARCH 与
//     rail 同源同 resolve，yaml 缺条目时搜索一并失效——返修前实测症状）。
//
// 任务 961：renderArtifact 由 ui 组独立卡改为「视图布局」（viewLayout）合并
// 卡成员行（条目级合并，782 先例——键/setter 零变化）。本测试双面口径不变，
// 钉住对象随迁：① 条目在 ui 组以 viewLayout 成员身份可见；② 搜索命中成员
// 返回父卡 id（成员没有独立 pane，父卡才是可打开面）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { findLabEntry, findLabEntryIdByQuery, resolveLabLayout } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { LAB_LIGHT_KEY_IDS } from "../lab/labLightKeys";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import type { DictKey } from "../locales/en";

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

// pane 白名单与 SettingsPanel 组件源码保持一致（722 测试同款抽取）。961 后
// renderArtifact 为成员行，白名单收父卡 id（viewLayout）。
const paneIds = new Set<string>();
{
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "SettingsPanel declares the LAB_PANE_IDS whitelist");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
}
ok(paneIds.has("viewLayout") && !paneIds.has("renderArtifact"),
  "viewLayout (the parent card) is registered in the pane whitelist; the folded member id is not");

const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });

// ── ① rail 可见：yaml 含成员条目且与默认同构 ──
ok(resolved.source === "yaml" && resolved.warnings.length === 0,
  `shipped lab-layout.yaml resolves as yaml source (warnings: ${JSON.stringify(resolved.warnings)})`);
const parentEntry = findLabEntry(resolved.layout, "viewLayout");
const yamlMember = parentEntry?.members?.find((m) => m.id === "renderArtifact");
ok(Boolean(yamlMember),
  "rail layout carries renderArtifact as a viewLayout member (ui group visible)");
ok(resolved.layout.groups.some((g) => g.key === "ui" && g.entries.some((e) => e.id === "viewLayout" && (e.members ?? []).some((m) => m.id === "renderArtifact"))),
  "renderArtifact sits in the ui group under the viewLayout card");
const defaultMember = findLabEntry(LAB_LAYOUT_DEFAULT_DATA, "viewLayout")?.members?.find((m) => m.id === "renderArtifact");
ok(Boolean(yamlMember) && Boolean(defaultMember) && JSON.stringify(yamlMember) === JSON.stringify(defaultMember),
  "yaml member equals the built-in default member (同构：labelKey/tier)");
ok(JSON.stringify(resolved.layout) === JSON.stringify(LAB_LAYOUT_DEFAULT_DATA),
  "yaml structure equals the built-in default wholesale (722 同构契约)");
ok(Boolean(defaultMember) && LAB_LIGHT_KEY_IDS.has("experimentalRenderArtifact"),
  "onKeys reference registered light keys");
ok(Boolean(parentEntry) && parentEntry!.onKeys.includes("experimentalRenderArtifact"),
  "the viewLayout card light follows the renderArtifact key (onKeys 跟随，灯谓词零回归)");

// ── ② 搜索可达：zh/en 查询命中卡片（成员命中 → 父卡）──
const tZh = (k: DictKey): string => (zh as Record<string, string>)[k];
const tEn = (k: DictKey): string => (en as Record<string, string>)[k];
ok(findLabEntryIdByQuery(resolved.layout, "对话内产物卡片", tZh) === "viewLayout",
  "「对话内产物卡片」resolves to the viewLayout card (zh)");
ok(findLabEntryIdByQuery(resolved.layout, "In-chat artifact cards", tEn) === "viewLayout",
  "「In-chat artifact cards」resolves to the viewLayout card (en)");

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
