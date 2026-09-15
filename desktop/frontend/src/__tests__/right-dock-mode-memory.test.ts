// Run: tsx src/__tests__/right-dock-mode-memory.test.ts
// Task 120: right dock tab (文件/改动/概览) is remembered per workspaceRoot.

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

console.log("\nright dock mode memory");

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;

const layout = await import("../store/layout");

eq(layout.loadRightDockMode(""), "context", "no stored preference defaults to overview");
eq(layout.loadRightDockMode("/proj/a"), "context", "unknown project defaults to overview");

layout.saveRightDockMode("files", "/proj/a");
layout.saveRightDockMode("changed", "/proj/b");
eq(layout.loadRightDockMode("/proj/a"), "files", "project A remembers files");
eq(layout.loadRightDockMode("/proj/b"), "changed", "project B remembers changed");
eq(layout.loadRightDockMode("/proj/c"), "context", "project C still defaults to overview");

// Legacy global key seeds a project that has never been visited.
dom.window.localStorage.setItem("reasonix.rightDockMode", "changed");
eq(layout.loadRightDockMode("/proj/new"), "changed", "legacy global key seeds a fresh project");
layout.saveRightDockMode("files", "/proj/new");
eq(layout.loadRightDockMode("/proj/new"), "files", "project-specific value wins over legacy");

// Invalid stored values fall through to legacy, then overview.
dom.window.localStorage.setItem("reasonix.rightDockMode./proj/bad", "nope");
eq(layout.loadRightDockMode("/proj/bad"), "changed", "invalid project mode falls through to legacy global");
dom.window.localStorage.removeItem("reasonix.rightDockMode");
eq(layout.loadRightDockMode("/proj/bad"), "context", "invalid mode with no legacy falls back to overview");

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
