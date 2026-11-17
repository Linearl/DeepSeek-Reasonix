// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1665-update-page-governance.test.ts
//
// tl1665（2026-11-18）：更新页治理——删两项遥测开关 + 核实官网/issue 链接。
//
// 验收钉：
//  ① 更新页（设置 > 更新）不再有「匿名启动统计」「共享聚合质量指标」两项开关：
//     组件源码、三个 locale、SettingsView 类型、Wails 绑定、浏览器 mock 五处
//     同时干净（只删组件不算完——那四处是同机制的第二~第五处真相源）。
//  ② 删开关不破坏更新检查本身：「启动时检测新版本」开关原样保留。
//  ③ 四个对外链接都指 fork（Linearl/DeepSeek-Reasonix），且「查看已有问题」
//     带 open 过滤；更新页里不得再出现上游 esengine 的地址。
//  ④ 链接常量收口在 lib/forkLinks.ts 一处（宪法判据 D2：同语义只允许一处
//     权威源），更新页从该模块取址而不是自己写字面量。
//  ⑤ 后端上报面已断：internal/config 的两个闸门恒为 false（删开关 ≠ 停上报，
//     这一条钉住「上报真的停了」）。

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  FORK_CHANGELOG_URL,
  FORK_DOCS_SITE_URL,
  FORK_ISSUES_URL,
  FORK_NEW_ISSUE_URL,
  FORK_OPEN_ISSUES_URL,
  FORK_RELEASES_URL,
  FORK_REPO_URL,
} from "../lib/forkLinks";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const testDir = dirname(fileURLToPath(import.meta.url));
const srcRoot = join(testDir, "..");
const repoRoot = join(srcRoot, "..", "..", "..");
const read = (...parts: string[]) => readFileSync(join(...parts), "utf8");

const panel = read(srcRoot, "components", "SettingsPanel.tsx");
const bridge = read(srcRoot, "lib", "bridge.ts");
const viewTypes = read(srcRoot, "lib", "settingsViewTypes.ts");
const prefsMock = read(srcRoot, "lib", "desktopPreferencesMock.ts");
const en = read(srcRoot, "locales", "en.ts");
const zh = read(srcRoot, "locales", "zh.ts");
const zhTW = read(srcRoot, "locales", "zh-TW.ts");
const configGo = read(repoRoot, "internal", "config", "config.go");
const updaterGo = read(repoRoot, "desktop", "updater.go");

console.log("\ntl1665 update-page governance");

// ① 两项遥测开关从更新页整块移除（组件 + 三个 locale + 类型 + 绑定 + mock）。
const retiredKeys = [
  "settings.telemetryLabel",
  "settings.telemetryHint",
  "settings.metricsLabel",
  "settings.metricsHint",
];
for (const key of retiredKeys) {
  ok(!panel.includes(key), `the update page no longer renders ${key}`);
  ok(!en.includes(key) && !zh.includes(key) && !zhTW.includes(key), `${key} is gone from every locale`);
}
ok(!panel.includes("app.SetDesktopTelemetry"), "the update page no longer calls app.SetDesktopTelemetry");
ok(!panel.includes("app.SetDesktopMetrics"), "the update page no longer calls app.SetDesktopMetrics");
ok(!/\btelemetry=\{/.test(panel), "the update section no longer takes a telemetry prop");
ok(!/\bmetrics=\{/.test(panel), "the update section no longer takes a metrics prop");
ok(!/^\s*telemetry: boolean;/m.test(viewTypes), "SettingsView no longer carries a telemetry flag");
ok(!/^\s*metrics: boolean;/m.test(viewTypes), "SettingsView no longer carries a metrics flag");
ok(!bridge.includes("SetDesktopTelemetry"), "the Wails binding surface no longer exposes SetDesktopTelemetry");
ok(!bridge.includes("SetDesktopMetrics"), "the Wails binding surface no longer exposes SetDesktopMetrics");
ok(!prefsMock.includes("SetDesktopTelemetry") && !prefsMock.includes("SetDesktopMetrics"),
  "the browser dev mock mirrors the retired binding surface");

// ② 删开关不破坏更新检查本身：启动时检测新版本开关原样保留。
ok(panel.includes('label={t("updater.autoCheckLabel")}'), "the startup update-check switch is still rendered");
ok(panel.includes("app.SetDesktopCheckUpdates(enabled)"), "the startup update-check switch still persists its choice");
ok(panel.includes('t("updater.officialDownload")'), "the official-download button is still rendered");
ok(panel.includes("openDownload"), "the official-download button still opens the download page");

// ③ 四个链接的指向（常量层）。
ok(FORK_REPO_URL === "https://github.com/Linearl/DeepSeek-Reasonix", "the fork repo URL points at Linearl/DeepSeek-Reasonix");
ok(FORK_RELEASES_URL === "https://github.com/Linearl/DeepSeek-Reasonix/releases", "the download URL is the fork releases page");
ok(FORK_ISSUES_URL === "https://github.com/Linearl/DeepSeek-Reasonix/issues", "the issue list URL is the fork repo");
ok(FORK_NEW_ISSUE_URL === "https://github.com/Linearl/DeepSeek-Reasonix/issues/new/choose", "the new-issue URL is the fork repo template chooser");
ok(FORK_OPEN_ISSUES_URL === "https://github.com/Linearl/DeepSeek-Reasonix/issues?q=is%3Aissue%20state%3Aopen",
  "the view-issues URL filters to open issues");
ok(FORK_DOCS_SITE_URL === "https://linearl.github.io/DeepSeek-Reasonix/", "the docs site URL is the fork Pages site");
ok(FORK_CHANGELOG_URL === "https://linearl.github.io/DeepSeek-Reasonix/changelog/", "the changelog URL is the fork docs site changelog");

// ③' 更新页从常量取址（不再自己写字面量），且页内不得出现上游地址。
ok(panel.includes("openExternal(FORK_CHANGELOG_URL)"), "the update page opens the changelog through the shared constant");
ok(panel.includes("openExternal(FORK_NEW_ISSUE_URL)"), "the update page opens the new-issue page through the shared constant");
ok(panel.includes("openExternal(FORK_OPEN_ISSUES_URL)"), "the update page opens the open-issue list through the shared constant");
ok(!panel.includes("esengine"), "the update page contains no upstream (esengine) URL");
ok(!panel.includes("linearl.github.io/DeepSeek-Reasonix/changelog/"), "the update page hardcodes no changelog URL of its own");

// ④ 「前往官网下载」落在 fork 发布页（Go 侧 downloadPageURL），不是上游。
ok(/downloadPageURL\s*=\s*"https:\/\/github\.com\/Linearl\/DeepSeek-Reasonix\/releases"/.test(updaterGo),
  "the Go download page constant points at the fork releases page");

// ⑤ 后端上报面已断：两个闸门恒为 false（删开关 ≠ 停上报）。
ok(/func \(c \*Config\) DesktopTelemetry\(\) bool \{\s*return false\s*\}/m.test(configGo),
  "DesktopTelemetry() is hard-wired to false — the launch ping and crash diagnostics never go out");
ok(/func \(c \*Config\) DesktopMetrics\(\) bool \{\s*return false\s*\}/m.test(configGo),
  "DesktopMetrics() is hard-wired to false — aggregate quality metrics never go out");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
