// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/transcript-measurement-revision.test.tsx
// tl1416 (issue #82): the measurement revision drove TranscriptWindow's prepaint
// measurement pass, and EVERY MarkdownHistory row whose parsed blocks changed in
// one commit called geometryChanged — a bare counter increment — so W changing
// rows chained W synchronous re-render + measurement-sweep rounds inside one
// prepaint window (the write-back half of the 16.6s tab-switch freeze).
//
// The gate hook (useTranscriptMeasurementRevision) contracts pinned here:
//  1. same-pass signals collapse: W calls before the next measurement pass
//     cost ONE revision increment and ONE re-render;
//  2. beginMeasurementPass is the consumption point — after it, the next
//     signal increments again (one pass per commit, not per row);
//  3. signals arriving while the gate is still closed (pass not yet run) are
//     absorbed — the in-flight pass observes the whole committed DOM anyway;
//  4. bumpForPublishedSizes (the measurement effect's own publication
//     increment) is deliberately UNGATED — absorbing it would drop a published
//     batch's follow-up pass;
//  5. convergence bound: a W-row storm plus publication bumps costs a small
//     constant number of renders, and the revision STOPS growing once signals
//     and bumps stop — the old synchronous convergence ring cannot run away.

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { useTranscriptMeasurementRevision } from "../lib/useTranscriptMeasurementRevision";

const dom = new JSDOM("<!doctype html><html><body><div id='root'></div></body></html>", { url: "http://localhost/" });
(globalThis as unknown as { window: Window }).window = dom.window as unknown as Window;
(globalThis as unknown as { document: Document }).document = dom.window.document;
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
if (!("navigator" in globalThis)) {
  Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true, writable: true });
}

let renders = 0;
let latest: ReturnType<typeof useTranscriptMeasurementRevision> | undefined;
function Probe() {
  latest = useTranscriptMeasurementRevision();
  renders += 1;
  return null;
}

const container = document.createElement("div");
document.body.appendChild(container);
let root: Root | null = null;
async function start() {
  root = createRoot(container);
  await act(async () => root!.render(React.createElement(Probe)));
}
async function stop() {
  await act(async () => root!.unmount());
  container.remove();
}

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

async function main() {
  await start();
  const hook = () => latest!;
  const revision = () => hook().revision;
  eq(revision(), 0, "starts at revision 0");
  const rendersAtStart = renders;

  // Contract 1: W same-pass signals collapse into one increment + one render.
  await act(async () => {
    for (let row = 0; row < 10; row += 1) hook().signalGeometryChanged();
  });
  eq(revision(), 1, "ten same-pass geometryChanged signals cost one revision increment");
  eq(renders - rendersAtStart, 1, "the collapsed storm costs exactly one re-render");

  // Contract 2: beginMeasurementPass consumes; the next signal increments again.
  await act(async () => { hook().beginMeasurementPass(); });
  eq(renders - rendersAtStart, 1, "beginMeasurementPass itself triggers no render");
  await act(async () => { hook().signalGeometryChanged(); });
  eq(revision(), 2, "after consumption the next signal increments again");
  eq(renders - rendersAtStart, 2, "the next-window signal costs exactly one more render");

  // Contract 3a: consumption then a same-commit storm — the storm collapses.
  await act(async () => { hook().beginMeasurementPass(); });
  await act(async () => {
    for (let row = 0; row < 10; row += 1) hook().signalGeometryChanged();
  });
  eq(revision(), 3, "a consumed-then-stormed window still costs one increment");
  eq(renders - rendersAtStart, 3, "the storm costs exactly one render, not ten");

  // Contract 3b: signals while the gate is closed (pass not yet run) are
  // absorbed — the scheduled pass observes the whole committed DOM anyway.
  await act(async () => {
    for (let row = 0; row < 10; row += 1) hook().signalGeometryChanged();
  });
  eq(revision(), 3, "pre-consumption signals are absorbed entirely");
  eq(renders - rendersAtStart, 3, "absorbed signals add no render");

  // Contract 4: publication bumps are ungated — every call survives, React
  // batches the renders.
  await act(async () => { hook().beginMeasurementPass(); });
  const revisionBeforeBumps = revision();
  const rendersBeforeBumps = renders;
  await act(async () => {
    hook().bumpForPublishedSizes();
    hook().bumpForPublishedSizes();
  });
  eq(revision() - revisionBeforeBumps, 2, "publication bumps are not absorbed (ungated by design)");
  eq(renders - rendersBeforeBumps, 1, "React batches the ungated bumps into one render");

  // Contract 5: convergence bound. The full write-back cycle of the freeze:
  // a W-row commit storm (signals) plus the publication follow-up — then the
  // chain must STOP once nothing signals or publishes anymore.
  await act(async () => { hook().beginMeasurementPass(); });
  const revisionBeforeStorm = revision();
  const rendersBeforeStorm = renders;
  await act(async () => {
    for (let row = 0; row < 50; row += 1) hook().signalGeometryChanged();
    hook().bumpForPublishedSizes();
  });
  eq(revision() - revisionBeforeStorm, 2, "a 50-row storm + one publication = one collapsed signal + one ungated bump");
  eq(renders - rendersBeforeStorm, 1, "the whole storm re-renders once (was: once per row)");
  // Drain: two more passes with no signals and no publications — the revision
  // must be stable; a synchronous convergence ring would keep counting here.
  await act(async () => { hook().beginMeasurementPass(); });
  await act(async () => { hook().beginMeasurementPass(); });
  eq(revision(), revisionBeforeStorm + 2, "the revision chain terminates when signals and publications stop");

  await stop();
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main().catch((error) => {
  process.stderr.write(`${error}\n`);
  process.exit(1);
});
