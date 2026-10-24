// Run: tsx src/__tests__/topicbar-controls.test.ts

import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const moreMenuSource = [
  readFileSync(resolve(testDir, "../components/TopicbarMoreMenu.tsx"), "utf8"),
  readFileSync(resolve(testDir, "../components/TopicbarMoreMenuContent.tsx"), "utf8"),
].join("\n");

assert.doesNotMatch(appSource, /t\("shortcuts\.cheatsheetTitle"\)|t\("topicBar\.command"\)/);

const taskSummaryControlIndex = moreMenuSource.indexOf('t("summary.session")');
assert.ok(taskSummaryControlIndex >= 0, "topic bar renders the localized Session summary control");
assert.ok(!moreMenuSource.includes('aria-label="Session summary"'), "Session summary does not use a hard-coded English label");

// tl 1100：topicbar 右侧只剩「…」——移出的三项（分段控件/提问搜索/外部打开）
// 不再在 topicbar 渲染，能力全部并入 `…` 菜单。
assert.ok(!appSource.includes("<TopicbarSurfaceSwitch />"), "topicbar no longer renders the transcript|trajectory segment switch (moved into the … menu)");
assert.ok(!appSource.includes('aria-label={t("questionSearch.label")}'), "topicbar no longer renders the question-search button");
assert.ok(!appSource.includes("<ExternalOpener "), "topicbar no longer renders the yellow-folder external opener button");
assert.ok(moreMenuSource.includes('t("topicBar.openIn")'), "the … menu exposes Open in… (openers moved in)");
// tl 1119+1120：一级菜单收敛——原「复制 ▸ / 导出会话 ▸ / 转录 / 轨迹」四项合并进
// 「复制和导出 ▸」「会话视图 ▸」两个二级入口，独立的一级触发项已移除。
assert.ok(!moreMenuSource.includes('t("topicBar.copyInfo")'), "the … menu no longer exposes a standalone Copy ▸ trigger (merged into Copy and export)");
assert.ok(moreMenuSource.includes('t("topicBar.copyAndExport")'), "the … menu exposes the merged Copy and export ▸ trigger");
assert.ok(moreMenuSource.includes('t("topicBar.sessionView")'), "the … menu exposes the Session view ▸ trigger (transcript/trajectory merged in)");
assert.ok(moreMenuSource.includes('t("trajectory.view.transcript")') && moreMenuSource.includes('t("trajectory.view.trajectory")'), "the … menu exposes transcript/trajectory view items (switch moved in)");
assert.ok(moreMenuSource.includes('topicbar__more-submenu-indicator'), "submenu triggers render the ▶ indicator class");
assert.ok(moreMenuSource.includes('t("topicBar.exportMarkdown")') && moreMenuSource.includes('t("topicBar.exportImage")'), "export formats (markdown/json/pdf/image) stay reachable via the Copy and export submenu");
assert.ok(moreMenuSource.includes('t("summary.session")'), "session summary stays a direct first-level item");

process.stdout.write("topicbar static presentation contracts passed\n");
