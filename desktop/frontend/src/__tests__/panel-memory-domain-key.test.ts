// Run: tsx src/__tests__/panel-memory-domain-key.test.ts
// Task 245: Global-domain right-dock panel memory is keyed by DOMAIN, not by
// session. The old chain `activeTab?.workspaceRoot ?? state.meta?.cwd ?? ""`
// sharded the Global domain into per-session cwd keys (and per Global-tab
// ghost workspace paths), so switching tabs flipped the dock. The fix funnels
// every panel memory key through workspacePanelMemoryRoot: Global maps to the
// single base key, project roots keep their own keys, and the session cwd
// never leaks into a key.

import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\npanel memory domain key (task 245)");

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;

const layout = await import("../store/layout");
const { workspacePanelMemoryRoot } = layout;

// A Global tab carries the global-workspace dir as its workspaceRoot (the
// "ghost project" path); a project tab carries its own root.
const GLOBAL_ROOT = "C:\\Users\\u\\AppData\\Roaming\\reasonix\\global-workspace";
const GLOBAL_ROOT_MOVED = "D:\\redirected\\state\\global-workspace";
const PROJECT_A = "C:\\code\\project-a";
const PROJECT_B = "C:\\code\\project-b";
// The old bug: a tabless window fell back to the live session's cwd.
const SESSION_CWD = "C:\\Users\\u\\somewhere-else";

console.log("\nworkspacePanelMemoryRoot mapping");
eq(workspacePanelMemoryRoot("global", GLOBAL_ROOT), "", "global scope maps to the single base key (ghost path dropped)");
eq(workspacePanelMemoryRoot("global", GLOBAL_ROOT_MOVED), "", "global scope stays one key even if the global root relocates");
eq(workspacePanelMemoryRoot("global", ""), "", "global scope with empty root maps to the base key");
eq(workspacePanelMemoryRoot("global", undefined), "", "global scope with missing root maps to the base key");
eq(workspacePanelMemoryRoot("project", PROJECT_A), PROJECT_A, "project scope keeps its own root key");
eq(workspacePanelMemoryRoot("project", ""), "", "project scope with empty root falls to the base key");
eq(workspacePanelMemoryRoot(undefined, undefined), "", "tabless startup window no longer leaks the session cwd");
eq(workspacePanelMemoryRoot(undefined, ""), "", "empty root without scope falls to the base key");
eq(workspacePanelMemoryRoot(undefined, PROJECT_A), PROJECT_A, "root without scope stays root-keyed (defensive)");

// The fragmentation itself: two Global sessions used to resolve DIFFERENT keys
// (per-session cwd / per ghost path). After normalization they must resolve to
// the same key, so the panel memory survives the switch.
console.log("\nGlobal domain unification across sessions");
eq(
  workspacePanelMemoryRoot("global", GLOBAL_ROOT),
  workspacePanelMemoryRoot("global", GLOBAL_ROOT_MOVED),
  "any two Global sessions resolve to the same domain key",
);
eq(
  workspacePanelMemoryRoot("global", GLOBAL_ROOT) === workspacePanelMemoryRoot("project", PROJECT_A),
  false,
  "Global domain key never collides with a project root key",
);

console.log("\npanel state survives a Global session switch (dock open + tab)");
dom.window.localStorage.clear();
layout.saveWorkspacePanelOpen(false, workspacePanelMemoryRoot("global", GLOBAL_ROOT));
layout.saveRightDockMode("files", workspacePanelMemoryRoot("global", GLOBAL_ROOT));
eq(
  layout.loadWorkspacePanelOpen(workspacePanelMemoryRoot("global", GLOBAL_ROOT_MOVED)),
  false,
  "dock open/closed state read back from another Global session's key",
);
eq(
  layout.loadRightDockMode(workspacePanelMemoryRoot("global", GLOBAL_ROOT_MOVED)),
  "files",
  "selected dock tab read back from another Global session's key",
);

console.log("\nbackward compatibility");
dom.window.localStorage.clear();
dom.window.localStorage.setItem("reasonix.workspacePanel.open", "0");
dom.window.localStorage.setItem("reasonix.rightDockMode", "changed");
eq(layout.loadWorkspacePanelOpen(""), false, "legacy base key still serves the Global domain (dock open)");
eq(layout.loadRightDockMode(""), "changed", "legacy base key still serves the Global domain (dock tab)");
eq(
  layout.loadWorkspacePanelOpen(workspacePanelMemoryRoot("global", GLOBAL_ROOT)),
  false,
  "pre-fix base-key value flows into the normalized Global key via the legacy seed",
);
eq(
  layout.loadRightDockMode(workspacePanelMemoryRoot("global", GLOBAL_ROOT)),
  "changed",
  "pre-fix base-key tab flows into the normalized Global key via the legacy seed",
);

// Project-level per-root memory (task 241's confirmed design) must not regress:
// a fresh project still seeds from the legacy base key, and existing project
// keys are never touched by Global-domain writes.
dom.window.localStorage.clear();
layout.saveWorkspacePanelOpen(true, PROJECT_A);
layout.saveRightDockMode("changed", PROJECT_A);
layout.saveWorkspacePanelOpen(false, workspacePanelMemoryRoot("global", GLOBAL_ROOT));
layout.saveRightDockMode("context", workspacePanelMemoryRoot("global", GLOBAL_ROOT));
eq(layout.loadWorkspacePanelOpen(PROJECT_A), true, "project A dock state untouched by a Global-domain write");
eq(layout.loadRightDockMode(PROJECT_A), "changed", "project A dock tab untouched by a Global-domain write");
eq(layout.loadWorkspacePanelOpen(PROJECT_B), false, "fresh project still seeds from the legacy base key (open)");
eq(layout.loadRightDockMode(PROJECT_B), "context", "fresh project still seeds from the legacy base key (tab)");
layout.saveRightDockMode("files", PROJECT_B);
eq(layout.loadRightDockMode(PROJECT_B), "files", "project-specific value wins over the legacy seed after first write");

// Old per-session cwd keys are abandoned (they were the fragmentation itself);
// nothing may read them back as Global state.
dom.window.localStorage.clear();
dom.window.localStorage.setItem(`reasonix.workspacePanel.open.${SESSION_CWD}`, "0");
dom.window.localStorage.setItem(`reasonix.rightDockMode.${SESSION_CWD}`, "changed");
eq(
  layout.loadWorkspacePanelOpen(workspacePanelMemoryRoot("global", GLOBAL_ROOT)),
  true,
  "stale per-session cwd key is ignored by the Global domain (default open)",
);
eq(
  layout.loadRightDockMode(workspacePanelMemoryRoot("global", GLOBAL_ROOT)),
  "context",
  "stale per-session cwd tab is ignored by the Global domain (default context)",
);

// Invalid stored values keep falling through to the defaults.
dom.window.localStorage.clear();
dom.window.localStorage.setItem(`reasonix.rightDockMode.${PROJECT_A}`, "nope");
eq(layout.loadRightDockMode(PROJECT_A), "context", "invalid project mode still falls back to the default");

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
