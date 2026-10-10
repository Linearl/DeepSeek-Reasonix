// Run: npx tsx src/__tests__/task742-tab-management-heartbeat-background.test.ts
// 任务 742 acceptance harness — 实验室「标签页管理」合并 +「心跳任务后台化」开关：
//  ① 合并：yaml + 内置默认把 tabCompress（506）+ tabModeTint（651）合并为
//     tabManagement 单入口（tier 可选）；两个旧 pane 退役；合并卡内两行各读
//     各的键、各写各的 setter（81/123 丢存铁律）；
//  ② 兼容：两个旧配置键原样保留（零迁移——条目级合并，键语义/setter 不变），
//     已存偏好升级后行为不突变；
//  ③ 心跳后台化：experimental_heartbeat_background 默认关（零值 bool，铁律 2），
//     渲染表固定键集落盘；引擎 call-time 读闸（244 B1 S4 契约），nil 闸 = 关；
//     park 走 264 detached 语义且用户打开过的标签永不收起（visibleBefore 守卫）；
//  ④ 徽章：两个新条目均可选档（任务书指定）；
//  ⑤ 三语：七个新键 zh/en/zh-TW 齐全。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";

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
const labDefault = readFileSync(`${frontendRoot}/src/lab/labLayoutDefault.ts`, "utf8");
const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8");
const tiers = readFileSync(`${frontendRoot}/src/lib/experimentTiers.ts`, "utf8");
const lightKeys = readFileSync(`${frontendRoot}/src/lab/labLightKeys.ts`, "utf8");
const view = readFileSync(`${frontendRoot}/src/lib/settingsViewTypes.ts`, "utf8");
const bridge = readFileSync(`${frontendRoot}/src/lib/bridge.ts`, "utf8");
const goHeartbeat = readFileSync(`${repoRoot}/desktop/heartbeat.go`, "utf8");
const goApp = readFileSync(`${repoRoot}/desktop/app.go`, "utf8");
const goTabs = readFileSync(`${repoRoot}/desktop/tabs.go`, "utf8");
const goSettingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
const goSettingsPrefs = readFileSync(`${repoRoot}/desktop/settings_preferences.go`, "utf8");
const goConfig = readFileSync(`${repoRoot}/internal/config/desktop_preferences.go`, "utf8");
const goEdit = readFileSync(`${repoRoot}/internal/config/edit.go`, "utf8");
const goRender = readFileSync(`${repoRoot}/internal/config/render.go`, "utf8");

console.log("\ntask 742 tab-management merge + heartbeat background mode");

// ── ① 合并：yaml + 默认同构，旧二入口退役 ──
{
  // yaml 中不再有独立 tabCompress / tabModeTint 条目。
  const yamlEntryIds = [...yamlText.matchAll(/^ {6}- id: (\w+)$/gm)].map((m) => m[1]);
  ok(!yamlEntryIds.includes("tabCompress") && !yamlEntryIds.includes("tabModeTint"),
    "yaml retires the standalone tabCompress/tabModeTint entries");
  ok(yamlEntryIds.includes("tabManagement"), "yaml registers the tabManagement merged entry");
  // 内置默认与 yaml 同构（722 ① 已钉全量同构，这里钉 742 面避免回归假绿）。
  ok(labDefault.includes('{ id: "tabManagement", labelKey: "settings.tabManagement", tier: "optional", onKeys: ["experimentalTabCompress", "tabModeTintNonDefault"] }'),
    "built-in default carries the tabManagement merged entry with the OR light");
  ok(!labDefault.includes('{ id: "tabCompress"') && !labDefault.includes('{ id: "tabModeTint"'),
    "built-in default retires the standalone entries too");
  // 合并卡灯 = 两键 OR（自适应压缩开 或 权限指示离默认档）。
  ok(yamlText.includes("onKeys: [experimentalTabCompress, tabModeTintNonDefault]"),
    "merged card light is the OR of both member keys");
  // rail pane 白名单随迁。
  const paneMatch = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  const paneIds = paneMatch ? paneMatch[1] : "";
  ok(paneIds.includes('"tabManagement"') && paneIds.includes('"heartbeatBackground"'),
    "pane whitelist registers tabManagement + heartbeatBackground");
  ok(!paneIds.includes('"tabCompress"') && !paneIds.includes('"tabModeTint"'),
    "pane whitelist drops the retired ids");
  // 两个旧 pane 分支退役，合并卡 pane 承载两行成员开关。
  ok(!panel.includes('{selected === "tabCompress" && (') && !panel.includes('{selected === "tabModeTint" && ('),
    "the two standalone pane branches are retired");
  const tmStart = panel.indexOf('{selected === "tabManagement" && (');
  const tmEnd = panel.indexOf('{selected === "heartbeatBackground" && (', tmStart);
  const tmCard = tmStart >= 0 && tmEnd > tmStart ? panel.slice(tmStart, tmEnd) : "";
  ok(tmCard.includes("app.SetExperimentalTabCompress(on)") && tmCard.includes("Boolean(s.experimentalTabCompress) === on"),
    "member row 1 (adaptive compression) reads + writes its own key");
  ok(tmCard.includes("app.SetTabPermissionIndicator(mode)") && tmCard.includes("s.tabPermissionIndicator ?? \"badge\""),
    "member row 2 (permission indicator) reads + writes its own key");
  // 成员行徽章保留（621：每特性一枚——合并不降级成员徽章）。
  ok(tmCard.includes('labLabel("tabCompress"') && tmCard.includes('labLabel("tabModeTint"'),
    "both member rows keep their own tier badges");
}

// ── ② 已存偏好兼容：旧键零迁移 ──
{
  ok(goConfig.includes('TabPermissionIndicator string `toml:"tab_permission_indicator"`') &&
     goConfig.includes('ExperimentalTabCompress bool `toml:"experimental_tab_compress"`'),
    "both legacy config keys survive unchanged (zero-migration merge)");
  ok(goRender.includes('"experimental_tab_compress = %v') && goRender.includes('"tab_permission_indicator'),
    "render table still emits both legacy keys (fixed-key-set rule)");
  ok(goEdit.includes("func (c *Config) SetExperimentalTabCompress(enabled bool) error {") &&
     goEdit.includes("func (c *Config) SetTabPermissionIndicator(mode string) error {"),
    "both legacy setters survive (member rows keep their own write path)");
}

// ── ③ 心跳任务后台化：默认关 + call-time 闸 + 264 detached park ──
{
  // 条目登记：automation 组独立卡，可选档（任务书指定）。
  const autoBlock = yamlText.slice(yamlText.indexOf("key: automation"), yamlText.indexOf("key: efficiency"));
  ok(autoBlock.includes("- id: heartbeatBackground") && autoBlock.includes("labelKey: settings.heartbeatBackground"),
    "yaml registers heartbeatBackground in the automation group");
  ok(labDefault.includes('{ id: "heartbeatBackground", labelKey: "settings.heartbeatBackground", tier: "optional", onKeys: ["experimentalHeartbeatBackground"] }'),
    "built-in default registers the same entry (optional tier)");
  ok(lightKeys.includes("experimentalHeartbeatBackground: (s: SettingsView) => Boolean(s.experimentalHeartbeatBackground)"),
    "the card light reads its own settings key");
  // Go 配置：零值 false 默认（铁律 2）+ 固定键集渲染 + setter 链。
  ok(goConfig.includes('ExperimentalHeartbeatBackground bool `toml:"experimental_heartbeat_background"`'),
    "Go config ships experimental_heartbeat_background off (zero-value bool)");
  ok(goRender.includes('"experimental_heartbeat_background = %v'),
    "render table emits the key (unlisted key would flip back on save — 81/123)");
  ok(goEdit.includes("func (c *Config) SetExperimentalHeartbeatBackground(enabled bool) error {"),
    "config setter exists");
  ok(goSettingsPrefs.includes("func (a *App) SetExperimentalHeartbeatBackground(enabled bool) error {"),
    "desktop bridge setter exists");
  // 双视图读数（81/123 both-views lesson）。
  ok((goSettingsApp.match(/ExperimentalHeartbeatBackground bool `json:"experimentalHeartbeatBackground"`/g) ?? []).length === 2,
    "both Go settings views carry the field");
  ok(goSettingsApp.includes("view.ExperimentalHeartbeatBackground = cfg.Desktop.ExperimentalHeartbeatBackground") &&
     goSettingsApp.includes("ExperimentalHeartbeatBackground: cfg.Desktop.ExperimentalHeartbeatBackground,"),
    "both readback paths fill the field");
  // 引擎闸：字段 + nil=off 契约 + call-time 接线（244 B1 S4：保存后下一次运行生效）。
  ok(goHeartbeat.includes("heartbeatBackground func() bool") &&
     goHeartbeat.includes("e != nil && e.heartbeatBackground != nil && e.heartbeatBackground()"),
    "engine gate field + nil-reads-off contract");
  ok(goApp.includes("a.heartbeat.heartbeatBackground = func() bool {") &&
     goApp.includes("cfg.Desktop.ExperimentalHeartbeatBackground"),
    "startup wires the call-time config gate");
  // park 路径：264 detached 语义 + 用户已开标签守卫。
  ok(goHeartbeat.includes("e.app.heartbeatTopicTabVisible(scope, workspaceRoot, topicID)") &&
     goHeartbeat.includes("if background && !visibleBefore {"),
    "park is guarded by the visible-before snapshot (user-opened tabs never park)");
  ok(goHeartbeat.includes("e.app.parkTabAsDetached(tabMeta.ID)") && goTabs.includes("func (a *App) parkTabAsDetached(tabID string) error {"),
    "park reuses the task-264 detached semantics");
  ok(goTabs.includes("func (a *App) openHeartbeatTabInactive(") && goTabs.includes("openTopicTabPreferLiveActivation"),
    "background open promotes a previously parked runtime via prefer-live");
  // 前端 wire：视图字段 + bridge。
  ok(view.includes("experimentalHeartbeatBackground?: boolean;") &&
     bridge.includes("SetExperimentalHeartbeatBackground(enabled: boolean): Promise<void>;"),
    "frontend view type + bridge declare the switch");
  ok(panel.includes("app.SetExperimentalHeartbeatBackground(on)") && panel.includes("Boolean(s.experimentalHeartbeatBackground) === on"),
    "the pane row reads + writes its own key");
}

// ── ④ 徽章：两新条目均可选档 ──
{
  ok(tiers.includes('tabManagement: "optional"') && tiers.includes('heartbeatBackground: "optional"'),
    "tier register pins both new ids to optional (任务书指定)");
}

// ── ⑤ 三语 ──
{
  const keys = [
    "settings.tabManagement", "settings.tabManagementHint", "settings.tabManagement.subHint",
    "settings.heartbeatBackground", "settings.heartbeatBackgroundHint",
    "settings.heartbeatBackground.on", "settings.heartbeatBackground.off",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all seven 742 keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
