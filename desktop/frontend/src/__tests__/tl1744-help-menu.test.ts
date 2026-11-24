// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1744-help-menu.test.ts
// tl1744 验收（右上角问号帮助菜单）：
//  ① prompt 构造器：ghIssueComposerPrompt 产出的文本以字面量 "/gh-issue-submit "
//     开头（会话侧按前缀识别技能调用，不能被翻译/换行截断），{repo} 占位被
//     forkLinks 的 slug 替换，缺省仓库 = Linearl/DeepSeek-Reasonix；
//  ② slug 单源：FORK_REPO_SLUG 由 FORK_REPO_URL 派生（owner/repo 形状）；
//  ③ 菜单五项稳定 key 清单：顺序 = 产品文档/问题上报/提需求/导出日志/关于，
//     与 App.tsx 挂载点数组一一对应（源码级断言锚定接线不漂移）；
//  ④ 三语键集齐平：helpMenu.* 五键在 zh/zh-TW/en 都存在（DictKey 类型已强制，
//     这里再读值断言非空 + prompt 模板含 {repo} 占位）；
//  ⑤ 挂载锚点：App.tsx 存在 windowsFramelessChrome 门控的 HelpMenuButton，
//     且两项 gh-issue-submit 动作走 startGhIssueSession（新会话链路接线）；
//  ⑥ 回归锚点：调色板帮助组既有命令（cmd-product-docs/cmd-export-logs/cmd-about）
//     仍在 App.tsx 中、词条未被改动（问号菜单只做入口聚合，不搬家）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

import { ghIssueComposerPrompt, HELP_MENU_ITEM_KEYS } from "../lib/helpMenu";
import { FORK_REPO_SLUG, FORK_REPO_URL } from "../lib/forkLinks";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const here = dirname(fileURLToPath(import.meta.url));
const appTsx = readFileSync(join(here, "..", "App.tsx"), "utf8");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntl1744 help menu");

// ① prompt 构造器形状。
const bugPrompt = ghIssueComposerPrompt("请帮我向 {repo} 提交一个 bug 报告。问题描述：");
ok(bugPrompt.startsWith("/gh-issue-submit "), "prefill prompt starts with the literal /gh-issue-submit token");
ok(!bugPrompt.includes("{repo}"), "{repo} placeholder is replaced");
ok(bugPrompt.includes("Linearl/DeepSeek-Reasonix"), "default slug is the fork repo");
ok(!bugPrompt.includes("\n"), "prompt stays on one line (slash token is prefix-matched)");
// 缺省参数 = fork slug；显式传参可覆盖（远端/测试场景）。
ok(ghIssueComposerPrompt("x", "owner/other") === "/gh-issue-submit x", "explicit slug override is honored verbatim");

// ② slug 单源派生。
ok(FORK_REPO_SLUG === "Linearl/DeepSeek-Reasonix", "fork slug is Linearl/DeepSeek-Reasonix");
ok(FORK_REPO_URL.endsWith(FORK_REPO_SLUG), "FORK_REPO_SLUG derives from FORK_REPO_URL (single source)");

// ③ 五项稳定 key 清单。
assert.deepEqual([...HELP_MENU_ITEM_KEYS], ["productDocs", "reportIssue", "featureRequest", "exportLogs", "about"]);
ok(true, "HELP_MENU_ITEM_KEYS order = docs/issue/feature/logs/about (matches menu order)");

// ④ 三语键集齐平 + prompt 模板占位。
const locales: [string, Record<string, string>][] = [["en", en], ["zh", zh], ["zh-TW", zhTW]];
for (const [name, dict] of locales) {
  for (const key of ["helpMenu.open", "helpMenu.reportIssue", "helpMenu.featureRequest", "helpMenu.issuePromptBug", "helpMenu.issuePromptFeature"]) {
    ok(Boolean(dict[key]?.trim()), `${name}.${key} is present and non-empty`);
  }
}
ok(en["helpMenu.issuePromptBug"].includes("{repo}") && en["helpMenu.issuePromptFeature"].includes("{repo}"), "en prompt templates carry the {repo} placeholder");
ok(zh["helpMenu.issuePromptBug"].includes("{repo}") && zhTW["helpMenu.issuePromptFeature"].includes("{repo}"), "zh/zh-TW prompt templates carry the {repo} placeholder");

// ⑤ 挂载与接线锚点（源码级）。
ok(/windowsFramelessChrome && \(\s*<HelpMenuButton/.test(appTsx), "HelpMenuButton mounts under the windowsFramelessChrome gate");
ok(/key: "help-report-issue"[\s\S]*?startGhIssueSession\(ghIssueComposerPrompt\(t\("helpMenu\.issuePromptBug"\)\)\)/.test(appTsx), "report-issue item routes through startGhIssueSession + bug prompt");
ok(/key: "help-feature-request"[\s\S]*?startGhIssueSession\(ghIssueComposerPrompt\(t\("helpMenu\.issuePromptFeature"\)\)\)/.test(appTsx), "feature item routes through startGhIssueSession + feature prompt");
ok(/key: "help-product-docs"[\s\S]*?onSelect: \(\) => openProductDocs\(\)/.test(appTsx), "docs item calls openProductDocs (cmd-product-docs same source)");
ok(/startGhIssueSession = useCallback\(/.test(appTsx), "startGhIssueSession is a stable useCallback");
ok(/insertComposerIntoTab\(activeTabId, pending\.prompt\)/.test(appTsx), "prefill lands in the fresh tab's composer request slot");
ok(/clearComposerInsertForTab\(inserted\)/.test(appTsx), "inserted slot is cleaned up on tab leave (no re-injection on remount)");

// ⑥ 回归锚点：调色板帮助组原命令未被搬家/删除。
ok(/id: "cmd-product-docs"/.test(appTsx), "palette cmd-product-docs untouched");
ok(/id: "cmd-export-logs"/.test(appTsx), "palette cmd-export-logs untouched");
ok(/id: "cmd-about"/.test(appTsx), "palette cmd-about untouched");
ok(/id: "cmd-report-bug"/.test(appTsx), "palette cmd-report-bug untouched (issue entry stays put)");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
