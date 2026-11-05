// Run: tsx src/__tests__/project-tree-rename-optimistic.test.ts

import { projectTreeWithTopicTitle } from "../lib/projectTreeTopic";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

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

console.log("\nproject tree optimistic rename");

eq(
  projectTreeWithTopicTitle(
    [
      {
        key: "p",
        kind: "project",
        label: "P",
        children: [
          { key: "topic_rename", kind: "topic", label: "Old name", topicId: "topic_rename" },
          { key: "topic_keep", kind: "topic", label: "Keep me", topicId: "topic_keep" },
        ],
      },
    ],
    "topic_rename",
    "New name",
  ).map((node) => (node.children ?? []).map((child) => child.label)),
  [["New name", "Keep me"]],
  "a committed rename repaints the topic row label before the catalog event arrives",
);

eq(
  projectTreeWithTopicTitle(
    [{ key: "p", kind: "project", label: "P", children: [{ key: "t", kind: "topic", label: "Same", topicId: "t" }] }],
    "t",
    "Same",
  )[0].children?.[0].label,
  "Same",
  "an unchanged rename label leaves the tree identity stable",
);

eq(
  projectTreeWithTopicTitle(
    [{ key: "p", kind: "project", label: "P", children: [{ key: "t", kind: "topic", label: "T", topicId: "t" }] }],
    "topic_missing",
    "New name",
  )[0].children?.[0].label,
  "T",
  "a rename for a topic outside the loaded tree changes nothing",
);

const projectTreeSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../components/ProjectTree.tsx"), "utf8");
eq(
  projectTreeSource.includes("projectTreeWithTopicTitle(current, topicId, title)"),
  true,
  "commitRenameTopic paints the new label optimistically before the refresh round-trip",
);
eq(
  projectTreeSource.includes("project-tree__skeleton"),
  true,
  "an expanded folder with a loading first topic page renders skeleton rows",
);
eq(
  /projectTreeRevisionIsFresh\(latestRevisionRef\.current, event\.revision\)\) \{\s*void refresh\(\)/.test(projectTreeSource),
  true,
  "a stale project-tree revision falls back to a full snapshot refetch instead of dropping the event",
);

// tl1413 — rename entry symmetry. The sidebar entry (ProjectTree
// commitRenameTopic) and the top-bar entry (App commitActiveTopicRename) must
// converge on the same renameTopic handler. The backend half of the contract
// lives in Go (finishTopicRename pushes the title into the catalog
// synchronously, bounded, before RenameTopic returns — pinned by
// Test718RenameReachesSidebarWithoutWaitingForMetadataTick and
// Test1413RenameReturnsFreshCatalogUnderMetadataStorm), so the catalog is
// already fresh when the frontend resumes.
//
// Package-78 rework (tl1413b) corrected the tl1413 conclusion: the
// refreshSignal-driven refresh() does NOT reload topic pages (only pinned
// shells — see the projectTreeArchive pin below), so the refetch alone never
// painted expanded-folder rows and the top-bar entry still lagged seconds on
// the changed-v2 round-trip. The missing half is the receipt consumption:
// renameTopic now pushes the new title to the tree as topicTitlePatch, which
// paints it the same way the tree's own rename entry does.
const appSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../App.tsx"), "utf8");
eq(
  appSource.includes("onRenameTopic={renameTopic}"),
  true,
  "the sidebar tree receives the shared renameTopic handler as its rename entry",
);
eq(
  /const commitActiveTopicRename = useCallback\(async \(\) => \{[\s\S]*?await renameTopic\(topicId, nextTitle\);/.test(appSource),
  true,
  "the top-bar rename commit routes through the same shared renameTopic handler",
);
eq(
  /const renameTopic = useCallback\(async \(topicId: string, title: string\) => \{[\s\S]*?await app\.RenameTopic\(topicId, nextTitle\);\s*\/\/ tl1413b[\s\S]*?pushTopicTitlePatch\(topicId, nextTitle\);\s*await refreshProjectsAndTabs\(\);/.test(appSource),
  true,
  "renameTopic consumes the receipt (pushTopicTitlePatch) then refetches — the package-78 rework: the refetch alone only reloads pinned shells, so the patch is what paints expanded-folder rows immediately",
);

// tl1413b — the top-bar entry's missing half. The shared handler now pushes a
// rename receipt to the tree; these pins keep the whole consumption chain
// wired: App state → prop → tree effect (same optimistic paint as the
// tree's own entry), plus the history-panel entry riding the same patch.
eq(
  /const \[topicTitlePatch, setTopicTitlePatch\] = useState<\{ topicId: string; title: string; seq: number \} \| null>\(null\);/.test(appSource),
  true,
  "App owns the rename-receipt patch state (topicId/title/seq)",
);
eq(
  /const pushTopicTitlePatch = useCallback\(\(topicId: string, title: string\) => \{\s*topicTitlePatchSeqRef\.current \+= 1;/.test(appSource),
  true,
  "receipts carry a monotonic seq so the tree can drop stale patches",
);
eq(
  appSource.includes("topicTitlePatch={topicTitlePatch ?? undefined}"),
  true,
  "the sidebar tree receives the rename-receipt patch at the render site",
);
eq(
  /if \(session\.topicId\) \{[\s\S]*?await app\.RenameTopic\(session\.topicId, title\);[\s\S]*?pushTopicTitlePatch\(session\.topicId, title\.trim\(\)\);/.test(appSource),
  true,
  "the history-panel rename entry consumes the receipt the same way (same mechanism family)",
);
const propsSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../components/ProjectTreeProps.ts"), "utf8");
eq(
  propsSource.includes("topicTitlePatch?: { topicId: string; title: string; seq: number }"),
  true,
  "ProjectTreeProps declares the optional receipt patch channel",
);
eq(
  /appliedTitlePatchSeqRef\.current = topicTitlePatch\.seq;\s*setTree\(\(current\) => applyRuntimeProjection\(projectTreeWithTopicTitle\(current, topicTitlePatch\.topicId, topicTitlePatch\.title\)\)\);/.test(projectTreeSource),
  true,
  "the tree paints the receipt through the same projectTreeWithTopicTitle optimistic path its own rename entry uses, one-shot per seq",
);
// The mechanism root this patch exists for: refresh() with no options must NOT
// reload topic pages (only pinned shells) — if that ever changes to a default
// full reload, the patch becomes redundant and may be retired.
const archiveSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../lib/projectTreeArchive.ts"), "utf8");
eq(
  /const targets = projects\.filter\(\(project\) => options\?\.reloadAllTopics \|\| keys\.has\(project\.key\)\);/.test(archiveSource),
  true,
  "refresh() with no options reloads no topic pages (options?.reloadAllTopics is falsy and keys is empty) — the reason the receipt patch, not the refetch, paints the rename",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
