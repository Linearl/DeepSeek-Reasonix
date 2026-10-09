// Run: tsx src/__tests__/plan-usage-card.test.tsx
//
// Task 287 — render the right-dock overview plan card against the wire states
// the store can hold: unsupported (hidden entirely — the fallback rule; since
// task 666 this is also the "current model is not plan-capable" state),
// no-key (setup note, no crash), a normal two-window payload (bars + percents
// + countdown), and the exhausted state (warning line + critical tone).
// Task 666 — the query targets the visible tab: the bridge call carries the
// reported tab id and a tab switch re-queries with the new tab. Zero skips:
// every state must render without throwing.

import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";
import { PlanUsageCard } from "../components/PlanUsageCard";
import { LocaleProvider } from "../lib/i18n";
import { stopPlanUsagePollingForTest, usePlanUsageStore } from "../store/planUsage";
import type { PlanUsageResult } from "../lib/planUsage";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  IS_REACT_ACT_ENVIRONMENT: true,
});

let queries = 0;
let seenTabs: (string | undefined)[] = [];
Object.assign(window, {
  go: {
    main: {
      App: {
        GetProviderPlanUsage: async (tabID?: string) => {
          queries += 1;
          seenTabs.push(tabID);
          return { supported: false, windows: [], note: "unsupported", queriedAt: 0 };
        },
      },
    },
  },
});

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

async function renderCard(state: PlanUsageResult | null, tabId?: string) {
  usePlanUsageStore.setState({ view: state, loading: false });
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <PlanUsageCard tabId={tabId} />
      </LocaleProvider>,
    );
  });
  await act(async () => {
    await Promise.resolve();
  });
  return {
    card: container.querySelector('[data-testid="plan-usage-card"]'),
    rows: container.querySelectorAll(".plan-usage__row"),
    fills: container.querySelectorAll(".plan-usage__fill"),
    notes: container.querySelectorAll(".plan-usage__note"),
    exhausted: container.querySelectorAll(".plan-usage__exhausted"),
    refresh: container.querySelector(".plan-usage__refresh"),
    dispose: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

const clean: PlanUsageResult = {
  supported: true,
  provider: "GLM",
  region: "cn",
  windows: [
    { window: "five_hour", percent: 42.5, resetsAt: "2026-10-09T20:00:00Z" },
    { window: "weekly", percent: 7, resetsAt: "2026-10-12T00:00:00Z" },
  ],
  note: "",
  queriedAt: Date.now(),
};

// Unsupported: hidden entirely (task 287 fallback rule — never an error).
// Since task 666 the backend returns this whenever the tab's CURRENT model is
// not plan-capable, so this state IS the display condition's hidden branch.
{
  const r = await renderCard({ supported: false, provider: "", region: "", windows: [], note: "unsupported", queriedAt: 0 });
  ok(r.card === null, "unsupported (current model not plan-capable) renders nothing");
  await r.dispose();
}

// Never-queried: hidden too.
{
  const r = await renderCard(null);
  ok(r.card === null, "null view renders nothing");
  await r.dispose();
}

// no-key: the card shows the setup note.
{
  const r = await renderCard({ supported: true, provider: "GLM", region: "cn", windows: [], note: "no-key", queriedAt: 0 });
  ok(r.card !== null, "supported provider renders the card");
  ok(r.notes.length === 1, "no-key shows the setup note");
  ok(r.rows.length === 0, "no-key has no window rows");
  ok(r.refresh !== null, "refresh button is present even in the note state");
  await r.dispose();
}

// Normal payload: two window rows with bars and countdowns.
{
  const r = await renderCard(clean);
  ok(r.card !== null, "clean payload renders the card");
  ok(r.rows.length === 2, "two windows render two rows");
  ok(r.fills.length === 2, "two bars render");
  const fill = r.fills[0] as HTMLElement;
  ok(fill.style.width === "42.5%", "first bar width tracks the percent");
  ok(r.card?.textContent?.includes("43%"), "five-hour percent shows rounded");
  ok(r.card?.textContent?.includes("5 小时"), "window label localized (zh)");
  ok(r.card?.textContent?.includes("GLM · CN"), "provider + region label shows");
  ok(r.exhausted.length === 0, "no exhaustion warning below 100%");
  await r.dispose();
}

// Exhausted: warning line + critical fill tone.
{
  const r = await renderCard({
    ...clean,
    windows: [{ window: "five_hour", percent: 100, resetsAt: "2026-10-09T20:00:00Z" }, { window: "weekly", percent: 60, resetsAt: "" }],
  });
  ok(r.exhausted.length === 1, "exhausted state shows the warning line");
  const fill = r.fills[0] as HTMLElement;
  ok(fill.className.includes("plan-usage__fill--critical"), "exhausted bar uses the critical tone");
  await r.dispose();
}

// Manual refresh goes through the store bridge call.
{
  const r = await renderCard(clean);
  const before = queries;
  await act(async () => {
    (r.refresh as HTMLButtonElement).click();
    await Promise.resolve();
  });
  ok(queries > before, "refresh button issues a bridge call");
  await r.dispose();
}

// Task 666: the query targets the visible tab, and a tab switch re-queries
// with the new tab — the surfaces must never show the previous tab's quota.
{
  seenTabs = [];
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <PlanUsageCard tabId="tab-a" />
      </LocaleProvider>,
    );
  });
  await act(async () => {
    await Promise.resolve();
  });
  ok(seenTabs.includes("tab-a"), "the bridge call carries the visible tab id");
  await act(async () => {
    root.render(
      <LocaleProvider>
        <PlanUsageCard tabId="tab-b" />
      </LocaleProvider>,
    );
  });
  await act(async () => {
    await Promise.resolve();
  });
  ok(seenTabs.includes("tab-b"), "a tab switch re-queries with the new tab");
  ok(seenTabs[seenTabs.length - 1] === "tab-b", "the latest query targets the newest tab");
  await act(async () => root.unmount());
  container.remove();
}

stopPlanUsagePollingForTest();
process.stdout.write(`\nplan-usage-card: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
