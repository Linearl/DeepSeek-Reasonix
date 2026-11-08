// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1413-rename-dual-entry-sync.test.ts
// tl1413: 改名同步双入口对称性契约（用户 20261105 报告：顶部链改会话名侧栏不
// 一定同步）。修法形态（wt-tl1413b，已随主线合入）= 顶部链/侧栏链/历史面板三
// 入口统一走 App.tsx 的同一 renameTopic handler：持久化（app.RenameTopic）→
// pushTopicTitlePatch 乐观回执（侧栏立即重画）→ refreshProjectsAndTabs
// （tabMetas 刷新，顶部标题立即）→ refreshHistoryView（历史面板快照同步）。
//
// 本钉三层：
//   A. 行为钉（纯函数）——projectTreeWithTopicTitle 的立即重画/同值恒等/未知 id
//      no-op；topicDisplayTitle 对刷新后 tabMetas 的立即投影（侧栏→顶部方向的
//      显示面）。
//   B. 源码契约钉（App.tsx 顶部链）——顶部入口接线、提交走同一 handler（非裸调
//      app.RenameTopic）、回执单调 seq、Enter+blur 双发 once-guard、Escape 不提
//      交、目标 id 在清编辑态前捕获（切 tab 不误落新会话）、空名守卫。
//   C. 源码契约钉（ProjectTree 侧栏链 + 补丁消费）——补丁 seq 门 + 立即重画、
//      侧栏自身入口 handler 优先（onRenameTopic 缺席才裸调）、同款乐观画。
//   D. 上限钉——App.tsx 的 app.RenameTopic 直调恒为 2（历史面板 + 共享
//      handler）；新增直调即红，逼新增入口说明为何不走路由（同机制收敛）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreeWithTopicTitle } from "../lib/projectTreeTopic";
import { topicDisplayTitle, topicTitle } from "../lib/sessionTitles";
import type { ProjectNode, TabMeta } from "../lib/types";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(here, "../App.tsx"), "utf8");
const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
const treePropsSource = readFileSync(resolve(here, "../components/ProjectTreeProps.ts"), "utf8");

// ── A. 行为钉：纯函数面 ────────────────────────────────────────────────

console.log("\nSidebar paint helper (projectTreeWithTopicTitle)");
const projectRow = (label: string, children: ProjectNode[]): ProjectNode => ({
  key: `project:${label}`, kind: "project", label, root: "/repo", children,
});
const topicRow = (key: string, topicId: string, label: string): ProjectNode => ({
  key, kind: "topic", topicId, label,
});
const tree: ProjectNode[] = [
  projectRow("Alpha", [topicRow("a1", "t1", "old name"), topicRow("a2", "t2", "sibling")]),
  projectRow("Beta", [topicRow("b1", "t3", "other project")]),
];

const renamed = projectTreeWithTopicTitle(tree, "t1", "new name");
ok(renamed !== tree, "a real rename produces a new tree identity so the sidebar repaints");
ok(renamed[0]?.children?.[0]?.label === "new name", "the renamed topic row paints the new label immediately (no changed-v2 round-trip)");
ok(renamed[0]?.children?.[1] === tree[0]?.children?.[1], "untouched sibling rows keep their node identity");
ok(renamed[1] === tree[1], "untouched projects keep their node identity");
ok(renamed[0] !== tree[0], "the ancestor folder of the renamed row is cloned so React descends into it");

const sameValue = projectTreeWithTopicTitle(renamed, "t1", "new name");
ok(sameValue === renamed, "same-value repaint is identity-stable: the shared-handler receipt never disturbs the tree the sidebar's own entry already painted");

const unknown = projectTreeWithTopicTitle(tree, "t-missing", "whatever");
ok(unknown === tree, "a receipt for an unknown topic id is a no-op (stale receipts cannot fabricate rows)");

const sessionTree: ProjectNode[] = [
  projectRow("Alpha", [{ key: "s1", kind: "session", topicId: "t9", label: "session label" }]),
];
const renamedSession = projectTreeWithTopicTitle(sessionTree, "t9", "renamed session");
ok(renamedSession[0]?.children?.[0]?.label === "renamed session", "runtime session rows carrying the topic id repaint too (same logical row identity)");

console.log("\nTop-bar title projection (sessionTitles)");
const tabBase: TabMeta = {
  id: "tab-1", scope: "project", workspaceRoot: "/repo", workspaceName: "Repo",
  topicId: "t1", topicTitle: "old name", label: "Repo / old name",
  ready: true, running: false, mode: "normal", active: true, cwd: "/repo",
};
ok(topicDisplayTitle(tabBase) === "old name", "before the refresh lands the title bar shows the resident name");
ok(topicDisplayTitle({ ...tabBase, topicTitle: "new name" }) === "new name", "after refreshProjectsAndTabs lands the refreshed meta the title bar shows the new name immediately");
ok(topicTitle({ ...tabBase, topicTitle: "new name" }) === "Repo / new name", "the composite (hover) title carries the same rename");
ok(topicDisplayTitle({ ...tabBase, topicTitle: "" }) === "Untitled", "a stale empty title falls back to Untitled instead of blanking the bar");

// ── B. 源码契约钉：App.tsx 顶部链 + 双入口统一 ────────────────────────

console.log("\nApp.tsx top-bar chain (tl1413 顶部链)");
const commitTopicSlice = appSource.match(
  /const commitActiveTopicRename = useCallback\(\s*async \(\) => \{[\s\S]*?\n  \}, \[renameTopic, renamingTopicId, topicTitleDraft\]\);/,
)?.[0] ?? "";
ok(commitTopicSlice !== "", "commitActiveTopicRename exists with renameTopic in its closure deps (dual entries share one handler)");
ok(/await renameTopic\(topicId, nextTitle\);/.test(commitTopicSlice), "the top-bar commit routes through the shared renameTopic handler");
ok(!/app\.RenameTopic/.test(commitTopicSlice), "the top-bar commit never calls the bridge directly (the reported bare-call regression stays dead)");
ok(/const topicId = renamingTopicId;\s*setRenamingTopicId\(null\);/.test(commitTopicSlice), "the rename target id is captured before the editor state clears (a tab switch during commit cannot retarget another session)");
ok(/if \(topicRenameCommitHandledRef\.current\) return;\s*topicRenameCommitHandledRef\.current = true;/.test(commitTopicSlice), "Enter-then-blur double dispatch commits exactly once (once-guard)");
ok(/if \(topicRenameSkipCommitRef\.current\) \{[\s\S]*?topicRenameSkipCommitRef\.current = false;[\s\S]*?setRenamingTopicId\(null\);/.test(commitTopicSlice), "Escape arms the skip-commit ref so the trailing blur is inert");
ok(/const nextTitle = topicTitleDraft\.trim\(\);\s*if \(!nextTitle\) return;/.test(commitTopicSlice), "an empty draft never submits a rename");

const renameTopicSlice = appSource.match(
  /const renameTopic = useCallback\(async \(topicId: string, title: string\) => \{[\s\S]*?\n  \}, \[pushTopicTitlePatch, refreshHistoryView, refreshProjectsAndTabs, showToast\]\);/,
)?.[0] ?? "";
ok(renameTopicSlice !== "", "renameTopic exists with the receipt pusher + both refresh faces in its closure deps");
{
  const persist = renameTopicSlice.indexOf("await app.RenameTopic(topicId, nextTitle);");
  const receipt = renameTopicSlice.indexOf("pushTopicTitlePatch(topicId, nextTitle);");
  const tabs = renameTopicSlice.indexOf("await refreshProjectsAndTabs();");
  const history = renameTopicSlice.indexOf("await refreshHistoryView();");
  ok(persist >= 0, "the shared handler persists through app.RenameTopic (rename survives restart / reopen)");
  ok(receipt > persist, "the sidebar receipt is pushed after persistence succeeds (optimistic paint ordered behind the authoritative write)");
  ok(tabs > receipt, "refreshProjectsAndTabs follows the receipt so tabMetas (top-bar face) converges");
  ok(history > tabs, "refreshHistoryView follows so the history panel snapshot reopens in sync");
}
ok(/const nextTitle = title\.trim\(\);\s*if \(!topicId \|\| !nextTitle\) return;/.test(renameTopicSlice), "the shared handler guards blank topic ids and blank titles");

const patchSlice = appSource.match(
  /const pushTopicTitlePatch = useCallback\(\(topicId: string, title: string\) => \{[\s\S]*?\n  \}, \[\]\);/,
)?.[0] ?? "";
ok(/topicTitlePatchSeqRef\.current \+= 1;[\s\S]*?setTopicTitlePatch\(\{ topicId, title, seq: topicTitlePatchSeqRef\.current \}\);/.test(patchSlice), "rename receipts are monotonic seq-stamped (an older receipt can never repaint over a newer one)");

const guardEffectSlice = appSource.match(
  /useEffect\(\(\) => \{\s*if \(!renamingTopicId \|\| activeTab\?\.topicId === renamingTopicId\) return;[\s\S]*?\n  \}, \[activeTab\?\.topicId, renamingTopicId\]\);/,
)?.[0] ?? "";
ok(guardEffectSlice !== "" && /setRenamingTopicId\(null\);\s*setTopicTitleDraft\(""\);/.test(guardEffectSlice), "switching resources releases the open rename draft (no cross-session rename leakage)");

const startSlice = appSource.match(
  /const startActiveTopicRename = useCallback\(\(\) => \{[\s\S]*?\n  \}, \[activeTab\?\.topicId, activeTab\?\.topicTitle\]\);/,
)?.[0] ?? "";
ok(/setRenamingTopicId\(activeTab\.topicId\);\s*setTopicTitleDraft\(activeTab\.topicTitle \|\| ""\);/.test(startSlice), "the top-bar entry seeds the draft from the active session's own title");

ok(/onClick=\{startActiveTopicRename\}/.test(appSource), "the topicbar title button opens the top-bar rename editor");
ok(/if \(event\.key === "Enter"\)\s*\{\s*event\.preventDefault\(\);\s*void commitActiveTopicRename\(\);/.test(appSource), "Enter commits the top-bar rename");
ok(/if \(event\.key === "Escape"\)\s*\{\s*event\.preventDefault\(\);\s*cancelActiveTopicRename\(\);/.test(appSource), "Escape cancels the top-bar rename");
ok(/onBlur=\{\(\) => void commitActiveTopicRename\(\)\}/.test(appSource), "blur commits the top-bar rename (the once-guard above keeps it single-fire)");
ok(/const topicbarTitle = sidebarImDetailConnection \? t\("botDetail\.title", \{ name: sidebarImDetailConnection\.title \}\) : topicDisplayTitle\(activeTab\);/.test(appSource), "the topicbar title projects the refreshed tab meta (topicDisplayTitle)");
ok(/onRenameTopic=\{renameTopic\}/.test(appSource), "the sidebar tree receives the SAME shared handler (dual entries, one implementation)");
ok(/topicTitlePatch=\{topicTitlePatch \?\? undefined\}/.test(appSource), "the optimistic receipt is threaded into the sidebar tree");

console.log("\nApp.tsx rename call-site budget (tl1413 上限钉)");
const directRenameCalls = (appSource.match(/app\.RenameTopic\(/g) ?? []).length;
ok(directRenameCalls === 2, `app.RenameTopic is called from exactly the two sanctioned sites (shared handler + history panel); found ${directRenameCalls} — a third direct call means a new rename entry bypassed the shared optimistic path`);

const historySlice = appSource.match(
  /const onRenameHistorySession = useCallback\([\s\S]*?\[pushTopicTitlePatch, state\.running, renameSession, listSessions\],\n  \);/,
)?.[0] ?? "";
ok(historySlice !== "" && /await app\.RenameTopic\(session\.topicId, title\);[\s\S]*?pushTopicTitlePatch\(session\.topicId, title\.trim\(\)\);/.test(historySlice), "the history-panel rename entry persists then pushes the same sidebar receipt (third entry, same mechanism)");

// ── C. 源码契约钉：ProjectTree 侧栏链 + 补丁消费 ──────────────────────

console.log("\nProjectTree sidebar chain (tl1413 侧栏链)");
const consumeSlice = treeSource.match(
  /const appliedTitlePatchSeqRef = useRef\(0\);[\s\S]*?\}, \[topicTitlePatch\]\);/,
)?.[0] ?? "";
ok(consumeSlice !== "", "the tree consumes outside-rename receipts");
ok(/if \(!topicTitlePatch \|\| topicTitlePatch\.seq <= appliedTitlePatchSeqRef\.current\) return;\s*appliedTitlePatchSeqRef\.current = topicTitlePatch\.seq;/.test(consumeSlice), "receipts are consumed once per seq (no replay; a later backend title wins on the next data application)");
ok(/setTree\(\(current\) => applyRuntimeProjection\(projectTreeWithTopicTitle\(current, topicTitlePatch\.topicId, topicTitlePatch\.title\)\)\);/.test(consumeSlice), "a top-bar rename repaints the sidebar row immediately via projectTreeWithTopicTitle");

const commitTreeSlice = treeSource.match(
  /const commitRenameTopic = async \(topicId: string\) => \{[\s\S]*?\n  \};/,
)?.[0] ?? "";
ok(/if \(onRenameTopic\) await onRenameTopic\(topicId, title\);\s*else await app\.RenameTopic\(topicId, title\);/.test(commitTreeSlice), "the sidebar's own rename entry prefers the shared handler (bare bridge call only as the handler-less fallback)");
ok(/setTree\(\(current\) => applyRuntimeProjection\(projectTreeWithTopicTitle\(current, topicId, title\)\)\);/.test(commitTreeSlice), "the sidebar's own rename entry paints its row optimistically before the catalog round-trip");
ok(/await refresh\(\);/.test(commitTreeSlice), "the sidebar entry re-syncs the tree after the rename");

ok(/topicTitlePatch\?: \{ topicId: string; title: string; seq: number \};/.test(treePropsSource), "the receipt prop contract stays declared on ProjectTreeProps (the App wiring depends on it)");

console.log(`\ntl1413 rename dual-entry sync: ${passed} passed, ${failed} failed`);
process.exitCode = failed > 0 ? 1 : 0;
