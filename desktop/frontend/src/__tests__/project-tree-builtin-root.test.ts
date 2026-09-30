// Run: tsx src/__tests__/project-tree-builtin-root.test.ts
// Task 186: the host's own directories are not projects. The sidebar must not
// render a project node for the builtin workspace root even if a backend path
// forgets the whitelist, and the open-path scope normalization keeps builtin
// roots out of the project registry in the first place.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { projectTreeWithoutBuiltinWorkspaceNodes } from "../lib/projectTreePresentation";
import type { ProjectNode } from "../lib/types";

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

function projectNode(overrides: Partial<ProjectNode>): ProjectNode {
  return { key: "project_x", kind: "project", label: "X", root: "C:/x", children: [], ...overrides };
}

const GLOBAL_ROOT = "C:/users/me/appdata/reasonix/global-workspace";

console.log("\nTask 186: builtin workspace never renders as a project node");

const withGlobal = projectTreeWithoutBuiltinWorkspaceNodes([
  projectNode({ key: "global_folder", kind: "global_folder", label: "Global", root: GLOBAL_ROOT }),
  projectNode({ key: `project_${GLOBAL_ROOT}`, label: "global-workspace", root: GLOBAL_ROOT }),
  projectNode({ key: "project_real", label: "Real", root: "D:/code/real" }),
]);
ok(withGlobal.length === 2, "a project node on the Global folder's root is dropped");
ok(withGlobal.some((node) => node.kind === "global_folder"), "the Global folder itself survives");
ok(withGlobal.some((node) => node.root === "D:/code/real"), "ordinary projects survive");

const untouched = projectTreeWithoutBuiltinWorkspaceNodes([
  projectNode({ key: "project_a", label: "A", root: "D:/a" }),
  projectNode({ key: "project_b", label: "B", root: `${GLOBAL_ROOT}-backup` }),
]);
ok(untouched.length === 2, "prefix neighbours of the builtin root are not dropped");
ok(
  projectTreeWithoutBuiltinWorkspaceNodes([projectNode({ key: "project_a", label: "A", root: "D:/a" })]).length === 1,
  "a snapshot without a Global folder passes through unchanged",
);
ok(
  projectTreeWithoutBuiltinWorkspaceNodes([]).length === 0,
  "an empty snapshot passes through unchanged",
);

console.log("\nTask 186: wiring contract");
const here = dirname(fileURLToPath(import.meta.url));
const treeSource = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");
ok(
  treeSource.includes("projectTreeWithoutBuiltinWorkspaceNodes(asArray(snapshot.projects))"),
  "the shell snapshot passes through the builtin-root filter before painting",
);

const goWhitelist = readFileSync(resolve(here, "../../../builtin_roots.go"), "utf8");
ok(
  goWhitelist.includes("func normalizeWorkspaceScope") && goWhitelist.includes("func stripBuiltinProjects"),
  "the backend keeps the scope normalizer and the project-list whitelist",
);
const tabsSource = readFileSync(resolve(here, "../../../tabs.go"), "utf8");
ok(
  tabsSource.split("normalizeWorkspaceScope(scope, workspaceRoot)").length >= 4,
  "every tab-open entry in tabs.go applies the builtin-root scope normalization",
);
const restoreSource = readFileSync(resolve(here, "../../../app.go"), "utf8");
ok(
  restoreSource.includes("normalizeWorkspaceScope(entry.Scope, entry.WorkspaceRoot)"),
  "the persisted-tab restore normalizes a builtin-root entry to Global scope",
);

process.stdout.write(`\n${passed}/${passed + failed} checks passed.\n`);
if (failed > 0) process.exit(1);
