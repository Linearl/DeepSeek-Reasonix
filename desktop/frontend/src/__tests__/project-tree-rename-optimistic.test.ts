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
// converge on the same renameTopic handler, and that handler must refetch the
// sidebar right after the backend rename resolves. The backend half of the
// contract lives in Go (finishTopicRename pushes the title into the catalog
// synchronously, bounded, before RenameTopic returns — pinned by
// Test718RenameReachesSidebarWithoutWaitingForMetadataTick and
// Test1413RenameReturnsFreshCatalogUnderMetadataStorm), so this post-return
// refetch reads the fresh title even while the project-tree:changed-v2 event
// is still sitting in the 400ms batch window: the entries no longer depend on
// the event round-trip for consistency.
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
  /const renameTopic = useCallback\(async \(topicId: string, title: string\) => \{[\s\S]*?await app\.RenameTopic\(topicId, nextTitle\);\s*await refreshProjectsAndTabs\(\);/.test(appSource),
  true,
  "renameTopic refetches the sidebar immediately after the backend rename returns (fresh catalog guaranteed by the synchronous backend push)",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
