// Run: tsx src/__tests__/work-status-running-duration.test.tsx
//
// Task 341: the fold header's "Working Xm Ys" duration must keep ticking while
// the turn runs, even when no start timestamp exists — long quiet stretches
// (no new events) previously froze the label on the static segment.durationMs
// snapshot. Covers the resolver arithmetic (unit), the component integration
// (simulated tick advance), and the visibility-return repaint.

import { act } from "react";
import { createTranscriptHarness, type TranscriptHarness } from "./transcript-dom-harness";
import { resolveRunningDurationMs } from "../lib/workStatus";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  const match = actual === expected;
  if (!match) process.stdout.write(`        expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
  ok(match, label);
}

// Parses "Working 1m 5s" / "Working 1m" / "Working 24s" into total seconds.
// Whole minutes render without a seconds part (formatWorkDuration drops it).
function labelSeconds(text: string | null | undefined): number {
  const match = /Working\s+(?:(\d+)m)?\s*(?:(\d+)s)?/.exec(text ?? "");
  if (!match || (match[1] === undefined && match[2] === undefined)) return -1;
  return Number(match[1] ?? 0) * 60 + Number(match[2] ?? 0);
}

console.log("\nwork status running duration (task 341)");

// ── Unit: resolver arithmetic ─────────────────────────────────────────────────
{
  const key = "unit-seg";
  const t0 = 1_000_000;
  // First sighting anchors at `now` and grows from the static snapshot.
  eq(resolveRunningDurationMs({ now: t0, running: true, segmentKey: key, staticDurationMs: 60_000 }), 60_000, "first sighting returns the static snapshot");
  eq(resolveRunningDurationMs({ now: t0 + 5_000, running: true, segmentKey: key, staticDurationMs: 60_000 }), 65_000, "start-missing duration grows with the clock (+5s)");
  eq(resolveRunningDurationMs({ now: t0 + 6_000, running: true, segmentKey: key, staticDurationMs: 60_000 }), 66_000, "anchor persists across renders (+1s per tick, no restart)");
  // A still-larger static snapshot (parallel/outer work) is not rewound.
  eq(resolveRunningDurationMs({ now: t0 + 7_000, running: true, segmentKey: key, staticDurationMs: 120_000 }), 127_000, "larger static snapshot becomes the new growth base");

  // Settling releases the anchor; a later run re-anchors at its own first sight.
  eq(resolveRunningDurationMs({ now: t0 + 8_000, running: false, segmentKey: key, staticDurationMs: 120_000 }), 120_000, "settled segment returns the static snapshot");
  eq(resolveRunningDurationMs({ now: t0 + 99_000, running: true, segmentKey: key, staticDurationMs: 120_000 }), 120_000, "a new run re-anchors instead of resuming the old clock");

  // Known starts keep the historical max() semantics (no rewind, no freeze).
  eq(resolveRunningDurationMs({ now: t0, running: true, segmentKey: "k2", staticDurationMs: 60_000, turnStartAt: t0 - 1_000 }), 60_000, "start present + larger static: max keeps the static duration");
  eq(resolveRunningDurationMs({ now: t0, running: true, segmentKey: "k2", staticDurationMs: 60_000, turnStartAt: t0 - 90_000 }), 90_000, "start present + larger live count: max takes the live count");
  eq(resolveRunningDurationMs({ now: t0, running: true, segmentKey: "k3", staticDurationMs: 10_000, reasoningStartedAt: t0 - 30_000 }), 30_000, "reasoning start drives the count when the turn start is missing");
  eq(resolveRunningDurationMs({ now: t0, running: true, segmentKey: "k4", staticDurationMs: 5_000, turnStartAt: 0 }), 5_000, "turnStartAt=0 counts as missing and falls to the first-seen anchor");

  // A rewound clock cannot push the count backwards.
  eq(resolveRunningDurationMs({ now: t0 - 60_000, running: true, segmentKey: "k5", staticDurationMs: 60_000 }), 60_000, "clock rewind re-anchors instead of going negative");
}

// ── Component: running fold header integration ───────────────────────────────
// Completed tool (static 60s) + a running tool, no turn start timestamp: the
// exact production shape of the frozen "工作中 X分X秒" screenshot.
const runningItems: Item[] = [
  { kind: "user", id: "u-341", text: "run long" },
  { kind: "tool", id: "t-341a", name: "read_file", args: "{}", readOnly: true, status: "done", durationMs: 60_000 },
  { kind: "tool", id: "t-341b", name: "bash", args: "{}", readOnly: false, status: "running" },
];

{
  const harness = await createTranscriptHarness();
  const container = harness.container;
  const label = () => container.querySelector(".turn-collapse__label")?.textContent;
  let fakeNow = 1_000_000;
  const originalNow = Date.now;
  Date.now = () => fakeNow;
  try {
    await harness.render(runningItems, { running: true });
    eq(labelSeconds(label()), 60, "start-missing running fold counts from the static snapshot");

    // Visibility return: hidden webviews throttle setInterval, so the repaint
    // on visibilitychange must immediately catch the count up to the clock.
    fakeNow += 5_000;
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await harness.flush();
    eq(labelSeconds(label()), 65, "visibilitychange repaint catches the duration up immediately");

    // Simulated tick advance: the 1s interval fires, the label grows.
    fakeNow += 2_100;
    await harness.waitFor(() => {
      const seconds = labelSeconds(label());
      return seconds > 65;
    }, "the running duration to tick past 1m 5s without new events");
    ok(labelSeconds(label()) >= 67, "duration keeps ticking during a quiet stretch (no freeze)");
  } finally {
    Date.now = originalNow;
    await harness.unmount();
    await harness.close();
  }
}

// Reverse case (acceptance ④): a known turn start plus a larger static
// snapshot keeps the max() semantics — the static duration wins and is not
// rewound by the fresh live count.
{
  const harness = await createTranscriptHarness();
  const container = harness.container;
  const label = () => container.querySelector(".turn-collapse__label")?.textContent;
  let fakeNow = 1_000_000;
  const originalNow = Date.now;
  Date.now = () => fakeNow;
  try {
    await harness.render(runningItems, { running: true, turnStartAt: fakeNow - 1_000 });
    eq(labelSeconds(label()), 60, "start present + larger static: label shows the static duration");
    fakeNow += 3_000;
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await harness.flush();
    eq(labelSeconds(label()), 60, "live count under the static snapshot does not rewind the label");
  } finally {
    Date.now = originalNow;
    await harness.unmount();
    await harness.close();
  }
}

console.log(`\n${passed} passed, ${failed} failed`);
// This suite unmounts folds mid-run, so a live 1s tick interval and the React
// scheduler handles it fed keep the event loop from draining naturally — exit
// explicitly like fold-toggle-button.test.tsx does.
process.exit(failed > 0 ? 1 : 0);
