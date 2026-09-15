// Run: tsx src/__tests__/active-tab-persistence.test.ts
// Task 126: frontend-mirrored last-active tab id survives ListTabs races.

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

console.log("\nactive tab persistence");

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;

const prefs = await import("../lib/layoutPreferences");

eq(prefs.loadLastActiveTabId(), null, "starts empty");
prefs.saveLastActiveTabId("tab-b");
eq(prefs.loadLastActiveTabId(), "tab-b", "save then load");
prefs.saveLastActiveTabId("tab-b");
eq(prefs.loadLastActiveTabId(), "tab-b", "idempotent save keeps the same id");
prefs.saveLastActiveTabId("  ");
eq(prefs.loadLastActiveTabId(), "tab-b", "blank save is ignored");
prefs.saveLastActiveTabId("tab-c");
eq(prefs.loadLastActiveTabId(), "tab-c", "overwrite works");

const raw = dom.window.localStorage.getItem("reasonix.layoutPreferences.v1");
const parsed = raw ? JSON.parse(raw) : null;
eq(parsed?.activeTabId, "tab-c", "activeTabId is stored inside layoutPreferences.v1");
eq(typeof parsed?.sizes, "object", "sizes payload is preserved alongside activeTabId");

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
