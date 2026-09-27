// Run: tsx src/__tests__/settings-opencode-go-usage-card.test.tsx
//
// opencodefix — render the OpenCode Go usage card against the three wire
// states the settings pane can hand it: a null tiers payload (the Go nil
// slice that used to marshal as "tiers": null and crash render with
// TypeError: null.find), an empty array (no data yet), and a normal
// three-window payload. Zero skips: every state must render without throwing.

import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { JSDOM } from "jsdom";
import { SettingsOpenCodeGoUsageCard } from "../components/SettingsOpenCodeGoUsageCard";
import { LocaleProvider } from "../lib/i18n";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  IS_REACT_ACT_ENVIRONMENT: true,
});

let payload: unknown;
let queries = 0;
Object.assign(window, {
  go: {
    main: {
      App: {
        GetOpenCodeGoUsage: async () => {
          queries += 1;
          return payload;
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

async function renderCard(state: { next: unknown; enabled?: boolean }) {
  payload = state.next;
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <SettingsOpenCodeGoUsageCard enabled={state.enabled ?? true} busy={false} onToggle={() => {}} />
      </LocaleProvider>,
    );
  });
  // Let the fetch effect's promise resolve into state.
  await act(async () => {
    await Promise.resolve();
  });
  return {
    card: container.querySelector('[data-testid="opencode-go-usage-card"]'),
    rows: container.querySelectorAll("[data-window]"),
    notes: container.querySelectorAll(".opencode-go-usage__note"),
    dispose: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

console.log("\nopencode-go usage card wire states");

// 1. null tiers — the crash state (Go nil slice marshalled to JSON null).
{
  const view = await renderCard({ next: { tiers: null, note: "no-key" } });
  ok(view.card !== null, "null tiers: the enabled card body renders instead of throwing");
  ok(view.rows.length === 0, `null tiers: no window rows render (${view.rows.length})`);
  ok(view.notes.length === 1 && (view.notes[0].textContent ?? "").length > 0,
    "null tiers: the degradation note renders as the placeholder");
  ok(queries === 1, `null tiers: the card queried the endpoint once (${queries})`);
  await view.dispose();
}

// 2. empty array — healthy wire with no window data (note empty branch).
{
  const before = queries;
  const view = await renderCard({ next: { tiers: [], note: "" } });
  ok(view.card !== null, "empty tiers: the card body renders");
  ok(view.rows.length === 0, `empty tiers: no window rows render (${view.rows.length})`);
  ok(view.notes.length === 1 && (view.notes[0].textContent ?? "").length > 0,
    "empty tiers: the empty-state note renders instead of a blank card");
  ok(queries === before + 1, "empty tiers: the card queried the endpoint once");
  await view.dispose();
}

// 3. normal three-window payload.
{
  const before = queries;
  const resetsAt = new Date(Date.now() + 3_600_000).toISOString();
  const view = await renderCard({
    next: {
      tiers: [
        { window: "rolling", percent: 12, resetsAt },
        { window: "weekly", percent: 55, resetsAt },
        { window: "monthly", percent: 80, resetsAt },
      ],
      note: "",
    },
  });
  ok(view.card !== null, "normal tiers: the card body renders");
  ok(view.rows.length === 3, `normal tiers: three window rows render (${view.rows.length})`);
  const windows = [...view.rows].map((row) => row.getAttribute("data-window"));
  ok(JSON.stringify(windows) === JSON.stringify(["rolling", "weekly", "monthly"]),
    `normal tiers: rows keep wire order (${windows.join(",")})`);
  ok([...view.rows].some((row) => (row.textContent ?? "").includes("12%")),
    "normal tiers: the percent cell renders");
  ok(queries === before + 1, "normal tiers: the card queried the endpoint once");
  await view.dispose();
}

// 4. switch off issues no query at all (the zero-regression gate, now across
//    a null payload as well).
{
  const before = queries;
  const view = await renderCard({ next: { tiers: null, note: "no-key" }, enabled: false });
  ok(view.card === null, "switch off: no card body renders");
  ok(queries === before, `switch off: no endpoint query (${queries - before})`);
  await view.dispose();
}

assert.ok(passed >= 12, `expected at least 12 checks, got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
