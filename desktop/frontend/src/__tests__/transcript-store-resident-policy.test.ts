// Run: tsx src/__tests__/transcript-store-resident-policy.test.ts
//
// Task 192 — experimental_active_tab_resident (default off):
//   off  = today's semantics byte-for-byte (unbounded pins, prune on switch);
//   on   = the exemption is capped at the two most recently active tabs
//          (oldest released first), switch-away retains an active/running
//          tab's state (zero-reload), and budget overruns are named in the
//          log instead of growing silently (190 collaboration).
// Source guards pin the render table, the settings card, the three locales
// and the preferences hydration (铁律 2 full chain, 81/123 lesson).

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { TranscriptStore } from "../lib/transcriptStore";
import type {
  HistoryEntry,
  HistoryMessage,
  HistorySlice,
  HistorySliceRequest,
} from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

class FakeBackend {
  sliceCalls = 0;
  async HistorySliceForTab(_tabID: string, _req: HistorySliceRequest): Promise<HistorySlice> {
    this.sliceCalls += 1;
    const messages: HistoryMessage[] = [{ role: "user", content: "hello" }];
    const entries: HistoryEntry[] = messages.map((message, index) => ({
      entryId: `s:r0:m${index}:o0`,
      turn: 1,
      order: index,
      message,
      refs: [],
    }));
    return {
      entries,
      nextCursor: "",
      hasOlder: false,
      totalTurns: 1,
      startTurn: 1,
      endTurn: 1,
      stale: false,
      revision: 1,
      revisionKnown: true,
      digest: "digest-1",
    };
  }

  async HistoryContentForTab(): Promise<never> {
    throw new Error("not expected in this suite");
  }
}

const FULL_PAGE = { turns: 9999 };

function newStore(activeTabResident: boolean): TranscriptStore {
  return new TranscriptStore(new FakeBackend(), {
    historyBodyBudgetBytes: 64 * 1024 * 1024,
    maxResidentSessions: 10,
    evictCooldownMs: 0,
    recentActiveExemptMs: 0, // isolate the policy: no 190-M1 window shielding
    activeTabResident,
  });
}

async function threeLiveTabs(store: TranscriptStore): Promise<void> {
  // Realistic order per tab: it becomes active first (a recency stamp the
  // cap ranking needs), then holds a live pin, then loads. Pasting the three
  // loads before any hop leaves every tab without a timestamp, and the cap
  // degenerates to insertion order — which killed the third tab on load.
  store.noteActiveTab("t1");
  store.setPinned("t1", true);
  await store.loadLatest("t1", "/s/1.jsonl", FULL_PAGE);
  await sleep(2); // distinct recency stamps: same-ms ties would be arbitrary
  store.noteActiveTab("t2", "t1");
  store.setPinned("t2", true);
  await store.loadLatest("t2", "/s/2.jsonl", FULL_PAGE);
  await sleep(2);
  store.noteActiveTab("t3", "t2");
  store.setPinned("t3", true);
  await store.loadLatest("t3", "/s/3.jsonl", FULL_PAGE);
}

// ── off: unbounded pins, exactly today's semantics (zero regression) ────────
{
  const store = newStore(false);
  await threeLiveTabs(store);
  // Force enforcement by loading another session (runs the budget loops).
  await store.loadLatest("t4", "/s/4.jsonl", FULL_PAGE);
  ok(store.isResident("t1", "/s/1.jsonl"), "off: the oldest live-pinned tab stays resident (today's unbounded pin)");
  ok(store.isResident("t3", "/s/3.jsonl"), "off: the newest live-pinned tab stays resident");
  ok(store.shouldRetainOnSwitch("t3") === false, "off: switch-away releases exactly as today");
}

// ── on: the exemption is capped at two, oldest released first ───────────────
{
  // A 1-byte budget forces the budget loop on the next load, so "released"
  // is observable as an eviction instead of an idle pass over a big budget.
  const store = new TranscriptStore(new FakeBackend(), {
    historyBodyBudgetBytes: 1,
    maxResidentSessions: 10,
    evictCooldownMs: 0,
    recentActiveExemptMs: 0,
    activeTabResident: true,
  });
  await threeLiveTabs(store); // t3 newest (active), t2 second, t1 oldest
  await store.loadLatest("t4", "/s/4.jsonl", FULL_PAGE);
  ok(store.isResident("t3", "/s/3.jsonl"), "on: the newest exemption survives the cap");
  ok(store.isResident("t2", "/s/2.jsonl"), "on: the second-newest exemption survives the cap");
  ok(!store.isResident("t1", "/s/1.jsonl"), "on: exceeding the cap releases the oldest exemption");
  ok(store.residentSessionCount() <= 4, "on: the resident set stays bounded");
}

// ── on: switch-away retains the active/running tab (zero-reload return) ─────
{
  const store = newStore(true);
  await store.loadLatest("t1", "/s/1.jsonl", FULL_PAGE);
  store.setPinned("t1", true); // running
  ok(store.shouldRetainOnSwitch("t1") === true, "on: a running tab is retained across a switch-away");
  store.setPinned("t1", false);
  ok(store.shouldRetainOnSwitch("t1") === false, "on: without a pin there is nothing to retain");
}

// ── on: still inside 190's budget — overruns are named, not silent ──────────
{
  const store = newStore(true);
  await threeLiveTabs(store);
  store.noteActiveTab("t3", "t2");
  // The enforcement runs on every load; with the cap at two and the budgets
  // generous here the store stays under — the overrun path is the source
  // guard below (it needs a tiny budget plus exempt survivors, covered by
  // the noteResidentBudgetOver call site).
  await store.loadLatest("t4", "/s/4.jsonl", FULL_PAGE);
  ok(store.totalBodyBytes() > 0, "enforcement with the policy on leaves the store in a consistent state");
}

// ── source guards: 190 overrun log + 192 switch-away guard + 铁律 2 chain ──
{
  const thisDir = dirname(fileURLToPath(import.meta.url));
  const storeSource = readFileSync(join(thisDir, "../lib/transcriptStore.ts"), "utf8").replace(/\n\s*/g, " ");
  const monitorSource = readFileSync(join(thisDir, "../lib/sessionMonitor.ts"), "utf8");
  const controllerSource = readFileSync(join(thisDir, "../lib/useController.ts"), "utf8").replace(/\n\s*/g, " ");
  const prefsSource = readFileSync(join(thisDir, "../app-runtime/useDesktopPreferences.ts"), "utf8").replace(/\n\s*/g, " ");
  const panel = readFileSync(join(thisDir, "../components/SettingsPanel.tsx"), "utf8").replace(/\n\s*/g, " ");
  const en = readFileSync(join(thisDir, "../locales/en.ts"), "utf8");
  const zh = readFileSync(join(thisDir, "../locales/zh.ts"), "utf8");
  const zhTW = readFileSync(join(thisDir, "../locales/zh-TW.ts"), "utf8");

  ok(monitorSource.includes("resident budget exceeded by exempt sessions (task 192)"),
    "190 collaboration: an exempt overrun is logged loudly (noteResidentBudgetOver)");
  ok(storeSource.includes("noteResidentBudgetOver(total, this.historyBodyBudgetBytes)"),
    "store calls the overrun logger when the policy holds the budget over");
  ok(controllerSource.includes("shouldRetainOnSwitch(tabId)") && controllerSource.includes("switch-out:retained"),
    "switch-away release early-returns for a retained tab (zero-reload path)");
  ok(prefsSource.includes("getTranscriptStore().setResidentPolicy(Boolean((settings as { experimentalActiveTabResident?: boolean })"),
    "settings hydrate flips the policy at runtime (no restart, task 160 pattern)");
  ok(prefsSource.includes('import { getTranscriptStore } from "../lib/transcriptStore"'),
    "the hydration file imports the store");

  // Task 347 moved the switch into the cache-tuning page as its fourth
  // block: the standalone misc entry is gone and the two appear-points are
  // mutually exclusive - only the cache-tuning detail shows it.
  ok(!panel.includes('{ id: "activeTabResident", group: "misc",'), "misc group no longer lists the entry (task 347)");
  ok(!panel.includes('| "activeTabResident"'), "the standalone detail-card union id is gone (task 347)");
  ok(panel.includes("app.SetExperimentalActiveTabResident(on)"), "the cache-tuning detail card writes through the setter");
  ok(
    panel.includes('selected === "cacheTuning"') &&
      panel.slice(panel.indexOf('selected === "cacheTuning"')).includes("SetExperimentalActiveTabResident"),
    "the switch renders inside the cache-tuning detail (fourth block)",
  );
  // LRU capacity field (task 347 half 2, task 196fix2 contract).
  ok(panel.includes("app.SetDagGraphCacheCapacity(v)"), "capacity field writes through the 196fix2 setter");
  ok(panel.includes('t("settings.cacheTuning.dagCache")'), "capacity field carries its own label");
  const keys = ["settings.activeTabResident", "settings.activeTabResidentHint", "settings.activeTabResident.on", "settings.activeTabResident.off"];
  for (const key of keys) {
    ok(en.includes(`"${key}"`), `en ships ${key}`);
    ok(zh.includes(`"${key}"`), `zh ships ${key}`);
    ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
  }
}

// ── source guards: config → render → desktop chain (the Go side) ───────────
{
  const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..");
  const read = (rel: string) => readFileSync(join(repoRoot, rel), "utf8");
  ok(read("internal/config/config.go").includes('toml:"experimental_active_tab_resident"'),
    "config field exists with the toml key");
  ok(read("internal/config/render.go").includes("experimental_active_tab_resident = %v"),
    "render table writes the key (no silent drop on save)");
  ok(read("internal/config/edit.go").includes("func (c *Config) SetExperimentalActiveTabResident"),
    "config setter exists");
  ok(read("desktop/settings_app.go").includes('json:"experimentalActiveTabResident"'),
    "desktop view exposes the flag");
  ok(read("desktop/settings_preferences.go").includes("func (a *App) SetExperimentalActiveTabResident"),
    "wails setter exists");
}

assertNoFailure();
function assertNoFailure() {
  ok(passed >= 24, `expected at least 24 checks, got ${passed}`);
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}
