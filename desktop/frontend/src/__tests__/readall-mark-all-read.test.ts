// Run: npx --no-install tsx src/__tests__/readall-mark-all-read.test.ts
// wt-zcode-288：「全部已读」核心逻辑 —— tab / 项目分组 / 会话三处右键菜单共用
// 的 readActivity 标记层。覆盖：标记后未读判定清零、三种上下文的 key 收集、
// 存档读写与变更广播（测状态层，不开窗口）。

import {
  markReadKeysRead,
  markTabsAllRead,
  loadReadActivityStore,
  persistReadActivity,
  readActivityEquals,
  readActivityKeyFromTab,
  readActivityKeysInScope,
  readActivityKeysInSubtree,
  READ_ACTIVITY_STORAGE_KEY,
  READ_ACTIVITY_CHANGED_EVENT,
} from "../lib/readActivity";
import { projectTreeTopicHasUnreadActivity } from "../lib/projectTreeTopic";
import type { ProjectNode } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function ok(value: boolean, label: string) {
  eq(value, true, label);
}

const NOW = 1_800_000_000_000;
const EARLIER = 1_700_000_000_000;

const project: ProjectNode = {
  key: "p1",
  kind: "project",
  label: "P1",
  root: "/repo",
  children: [
    { key: "t1", kind: "topic", label: "T1", topicId: "t1", root: "/repo", lastActivityAt: EARLIER },
    { key: "s1", kind: "session", label: "S1", topicId: "s1", root: "/repo", sessionPath: "/repo/s1.jsonl", lastActivityAt: EARLIER + 1 },
  ],
};
const otherProject: ProjectNode = {
  key: "p2",
  kind: "project",
  label: "P2",
  root: "/other",
  children: [
    { key: "t2", kind: "topic", label: "T2", topicId: "t2", root: "/other", lastActivityAt: EARLIER + 2 },
  ],
};
const globalFolder: ProjectNode = {
  key: "g",
  kind: "global_folder",
  label: "Global",
  children: [
    { key: "gt", kind: "global_topic", label: "GT", topicId: "gt", lastActivityAt: EARLIER + 3 },
  ],
};

console.log("\nreadActivity key collection");

eq(
  readActivityKeysInSubtree([project, otherProject, globalFolder]),
  ["project\u001f/repo\u001ft1", "project\u001f/repo\u001fs1", "project\u001f/other\u001ft2", "global\u001f\u001fgt"],
  "subtree walk collects every topic/session key (project + global), folders skipped",
);
eq(
  readActivityKeysInSubtree([globalFolder]),
  ["global\u001f\u001fgt"],
  "group-scoped walk only collects that group's sessions",
);
eq(
  readActivityKeysInScope([project, otherProject, globalFolder], "project", "/repo"),
  ["project\u001f/repo\u001ft1", "project\u001f/repo\u001fs1"],
  "scope walk only collects sessions of the same workspace",
);
eq(
  readActivityKeysInScope([project, globalFolder], "global", ""),
  ["global\u001f\u001fgt"],
  "global scope walk collects global sessions with empty workspaceRoot",
);

console.log("\nreadActivityKeyFromTab (tab menu context)");

eq(
  readActivityKeyFromTab({ scope: "project", workspaceRoot: "/repo", topicId: "t1" }),
  "project\u001f/repo\u001ft1",
  "project tab maps to the same key shape as the tree node",
);
eq(
  readActivityKeyFromTab({ scope: "global", workspaceRoot: "", topicId: "gt" }),
  "global\u001f\u001fgt",
  "global tab maps to the global key shape",
);
eq(
  readActivityKeyFromTab({ scope: "file", workspaceRoot: "/repo", topicId: "" }),
  null,
  "file tab without topicId yields no key",
);

console.log("\nmarkReadKeysRead (pure)");

const fresh = markReadKeysRead({ "project\u001f/repo\u001ft1": NOW + 5 }, ["project\u001f/repo\u001ft1"], NOW);
eq(fresh, { "project\u001f/repo\u001ft1": NOW + 5 }, "existing newer readAt wins (never regresses)");
const untouchedArchive = { "project\u001f/repo\u001ft1": 1 };
ok(
  markReadKeysRead(untouchedArchive, [], NOW) === untouchedArchive,
  "empty key set returns the same reference (callers can skip setState/persist)",
);
eq(
  markReadKeysRead({ "project\u001f/repo\u001ft1": 1 }, ["project\u001f/repo\u001ft1"], NOW),
  { "project\u001f/repo\u001ft1": NOW },
  "stale readAt is bumped to now",
);

console.log("\nacceptance: unread badge clears after markAllRead");

const unreadBefore = (node: ProjectNode) => projectTreeTopicHasUnreadActivity(node, {}, "project", "/repo", "active", "/repo/active.jsonl");
ok(unreadBefore(project.children![0] as ProjectNode), "topic with activity is unread against an empty read archive");
ok(unreadBefore(project.children![1] as ProjectNode), "session with activity is unread against an empty read archive");

// 项目分组菜单路径：该分组子树全部标记已读 → 未读清零。
const groupKeys = readActivityKeysInSubtree([project]);
const afterGroup = markReadKeysRead({}, groupKeys, NOW);
ok(
  !projectTreeTopicHasUnreadActivity(project.children![0] as ProjectNode, afterGroup, "project", "/repo", "active", "/repo/active.jsonl"),
  "after markAllRead on the group subtree the topic badge is cleared",
);
ok(
  !projectTreeTopicHasUnreadActivity(project.children![1] as ProjectNode, afterGroup, "project", "/repo", "active", "/repo/active.jsonl"),
  "after markAllRead on the group subtree the session badge is cleared",
);
ok(
  projectTreeTopicHasUnreadActivity(otherProject.children![0] as ProjectNode, afterGroup, "project", "/repo", "active", "/repo/active.jsonl"),
  "sessions outside the marked group stay unread",
);

// 会话行菜单路径：同工作区（scope+root）全部标记已读。
const scopeKeys = readActivityKeysInScope([project, otherProject, globalFolder], "project", "/repo");
const afterScope = markReadKeysRead({}, scopeKeys, NOW);
ok(
  !projectTreeTopicHasUnreadActivity(project.children![0] as ProjectNode, afterScope, "project", "/repo", "active", "/repo/active.jsonl"),
  "after markAllRead on the session's workspace the badge is cleared",
);
ok(
  projectTreeTopicHasUnreadActivity(globalFolder.children![0] as ProjectNode, afterScope, "project", "/repo", "active", "/repo/active.jsonl"),
  "global sessions are untouched by a project-scope markAllRead",
);

console.log("\nreadActivityEquals");

ok(readActivityEquals({}, {}), "empty archives are equal");
ok(readActivityEquals({ a: 1 }, { a: 1 }), "same keys and values are equal");
ok(!readActivityEquals({ a: 1 }, { a: 2 }), "different values are not equal");
ok(!readActivityEquals({ a: 1 }, { a: 1, b: 2 }), "different key sets are not equal");

console.log("\npersistence: load/persist roundtrip + change broadcast");

// Node 下没有 localStorage/window —— 桩在 globalThis 上，验证存档层真实读写。
const backing = new Map<string, string>();
(globalThis as unknown as { localStorage: unknown }).localStorage = {
  getItem: (key: string) => (backing.has(key) ? backing.get(key)! : null),
  setItem: (key: string, value: string) => void backing.set(key, value),
};
const dispatched: unknown[] = [];
(globalThis as unknown as { window: unknown }).window = {
  dispatchEvent: (event: unknown) => {
    dispatched.push(event);
    return true;
  },
};

eq(loadReadActivityStore(), {}, "empty storage loads as an empty archive");
backing.set(READ_ACTIVITY_STORAGE_KEY, JSON.stringify({ "project\u001f/repo\u001ft1": 42, bad: "not-a-number" }));
eq(loadReadActivityStore(), { "project\u001f/repo\u001ft1": 42 }, "non-numeric entries are dropped on load");

persistReadActivity({ "global\u001f\u001fgt": NOW });
eq(JSON.parse(backing.get(READ_ACTIVITY_STORAGE_KEY)!), { "global\u001f\u001fgt": NOW }, "persist writes the archive to localStorage");
eq(
  (dispatched[0] as CustomEvent | undefined)?.type,
  READ_ACTIVITY_CHANGED_EVENT,
  "persist broadcasts the change event (ProjectTree reloads and clears badges)",
);

// TabBar 菜单路径：markTabsAllRead 从存档读档 → 标记 → 写回并广播。
backing.set(READ_ACTIVITY_STORAGE_KEY, JSON.stringify({}));
dispatched.length = 0;
markTabsAllRead(
  [
    { scope: "project", workspaceRoot: "/repo", topicId: "t1" },
    { scope: "global", workspaceRoot: "", topicId: "gt" },
    { scope: "file", workspaceRoot: "/repo", topicId: "" },
  ],
  NOW,
);
eq(
  loadReadActivityStore(),
  { "project\u001f/repo\u001ft1": NOW, "global\u001f\u001fgt": NOW },
  "markTabsAllRead marks every open tab's session read (file tabs skipped)",
);
eq(dispatched.length, 1, "markTabsAllRead broadcasts exactly one change event");

const tabKeys = [readActivityKeyFromTab({ scope: "project", workspaceRoot: "/repo", topicId: "t1" })].filter((key): key is string => Boolean(key));
const afterTabs = markReadKeysRead(loadReadActivityStore(), tabKeys, NOW);
ok(
  !projectTreeTopicHasUnreadActivity(project.children![0] as ProjectNode, afterTabs, "project", "/repo", "active", "/repo/active.jsonl"),
  "after markTabsAllRead the tab's session badge is cleared",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
