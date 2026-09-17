// Task 151 (round 3): per-tab DOM residency. These are the rules that decide which
// transcript panes keep their DOM, and the surface key a resident pane keys off —
// both have to hold exactly, because getting them wrong either leaks panes (memory)
// or changes a pane's surface key on switch (the cost residency exists to avoid).
import assert from "node:assert/strict";
import { transcriptGeometryKeyFor } from "../lib/transcriptGeometryKey";
import {
  nextResidentTabs,
  TRANSCRIPT_RESIDENCY_DEFAULT,
  TRANSCRIPT_RESIDENCY_MAX,
  transcriptResidencyLimit,
} from "../lib/transcriptResidency";

let passed = 0;
function check(condition: boolean, label: string): void {
  if (!condition) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

// ── MRU ordering ───────────────────────────────────────────────────────────────

check(nextResidentTabs([], "a", 2).join(",") === "a", "the first activation opens a pane");
check(nextResidentTabs(["a"], "b", 2).join(",") === "b,a", "the incoming tab goes in front");
check(nextResidentTabs(["b", "a"], "a", 2).join(",") === "a,b", "switching back reorders and keeps both");
check(nextResidentTabs(["b", "a"], "b", 2).join(",") === "b,a", "an already-front tab is not shuffled");
check(nextResidentTabs(["a", "b"], "c", 2).join(",") === "c,a", "over the limit drops the oldest pane");
check(nextResidentTabs(["a", "b"], "c", 1).join(",") === "c", "limit 1 is the single-pane behaviour");
check(nextResidentTabs(["a", "b"], "a", 1).join(",") === "a", "limit 1 keeps only the visible tab");
check(nextResidentTabs(["a", "b"], undefined, 2).join(",") === "a,b", "no active tab keeps the list");
check(nextResidentTabs(["a", "b"], "c", 0).join(",") === "c", "a limit below one still keeps the visible tab");

// ── the opt-out ────────────────────────────────────────────────────────────────

const restoreWindow = (globalThis as { window?: unknown }).window;
const withStorage = (value: string | null): void => {
  (globalThis as { window?: unknown }).window = { localStorage: { getItem: () => value } };
};

withStorage(null);
check(transcriptResidencyLimit() === TRANSCRIPT_RESIDENCY_DEFAULT, "no stored preference uses the default");
withStorage("4");
check(transcriptResidencyLimit() === 4, "a stored preference is honoured");
withStorage("99");
check(transcriptResidencyLimit() === TRANSCRIPT_RESIDENCY_MAX, "above the maximum clamps");
withStorage("0");
check(transcriptResidencyLimit() === 1, "residency off floors at the visible pane");
withStorage("not-a-number");
check(transcriptResidencyLimit() === TRANSCRIPT_RESIDENCY_DEFAULT, "an unparseable value falls back to the default");
(globalThis as { window?: unknown }).window = undefined;
check(transcriptResidencyLimit() === TRANSCRIPT_RESIDENCY_DEFAULT, "no window (plain node) falls back to the default");
(globalThis as { window?: unknown }).window = restoreWindow;

// ── the surface key a resident pane keys off ───────────────────────────────────

const sessionKey = transcriptGeometryKeyFor({ sessionPath: "/sessions/a.jsonl", sessionGeneration: 3, tabId: "tab-a" });
check(sessionKey === transcriptGeometryKeyFor({ sessionPath: "/sessions/a.jsonl", sessionGeneration: 3, tabId: "tab-b" }),
  "a session identity is the same for every tab rendering it");
check(sessionKey !== transcriptGeometryKeyFor({ sessionPath: "/sessions/a.jsonl", sessionGeneration: 4, tabId: "tab-a" }),
  "a new session generation is a new surface");
check(sessionKey !== transcriptGeometryKeyFor({ sessionPath: "/sessions/b.jsonl", sessionGeneration: 3, tabId: "tab-a" }),
  "a different session path is a different surface");
check(transcriptGeometryKeyFor({ tabId: "tab-a" }).startsWith("topic"), "without a session path the topic identity is used");
check(transcriptGeometryKeyFor({ sessionPath: "   ", tabId: "tab-a" }) === transcriptGeometryKeyFor({ tabId: "tab-a" }),
  "a whitespace session path is not a session identity");
check(transcriptGeometryKeyFor({ sessionPath: "/sessions/a.jsonl", tabId: "tab-a" }) ===
  transcriptGeometryKeyFor({ sessionPath: "/sessions/a.jsonl", sessionGeneration: 0, tabId: "tab-a" }),
  "a missing generation matches an explicit zero");

console.log(`\n${passed} checks passed`);
