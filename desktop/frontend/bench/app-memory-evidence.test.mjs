import assert from "node:assert/strict";
import { test } from "node:test";
import { ABSENT_LIFECYCLE_PROBE, attributeRetention, BASELINE_NOT_SETTLED_REASON, evidenceIntegrity, retainedCohorts, screeningBlockers, settledMajority, SETTLED_NOT_SETTLED_REASON, RESTING_CONTEXT_MISMATCH_REASON, summarizeHeap, TRANSIENT_EXCURSION_REASON } from "./app-memory-evidence.mjs";

const readings = (...triples) => triples.map(([nodes, jsEventListeners, activeSubscriptions]) => ({ nodes, jsEventListeners, activeSubscriptions }));

const sample = (ids, roundTrips) => ({ phase: "full", roundTrips, lifecycle: {
  liveRenderTokenIds: ids, liveRenderTokens: ids.length,
  activeOperations: 0, activeSubscriptions: 6, invariantViolations: 0, overflow: false,
} });
test("deliberately retained cohorts remain detectable even when totals are constant", () => {
  const samples = [sample([1, 2], 0), sample([3, 4], 32), sample([3, 5], 64), sample([3, 6], 96)];
  assert.equal(evidenceIntegrity(samples), true);
  assert.deepEqual(retainedCohorts(samples).at(-1).retainedPostBaseline, [3]);
});
test("probe overflow, duplicate IDs and cleanup underflow invalidate evidence", () => {
  for (const mutation of [{ overflow: true }, { invariantViolations: 1 }, { activeSubscriptions: -1 }, { liveRenderTokenIds: [1, 1] }]) {
    const value = sample([1, 2], 0);
    Object.assign(value.lifecycle, mutation);
    assert.equal(evidenceIntegrity([value]), false);
  }
});
test("stable owner counters alone cannot establish whole-App retention attribution", () => {
  const samples = [
    { ...sample([1, 2], 0), dom: { nodes: 6000, jsEventListeners: 500 } },
    { ...sample([1, 3], 32), dom: { nodes: 6024, jsEventListeners: 512 } },
    { ...sample([1, 4], 64), dom: { nodes: 6024, jsEventListeners: 512 } },
    { ...sample([1, 5], 96), dom: { nodes: 6024, jsEventListeners: 512 } },
  ];
  assert.equal(attributeRetention(samples).status, "needs-attribution");
  assert.ok(attributeRetention(samples).reasons.includes("heap-retainer-and-control-evidence-required"));
});
test("a stable tail cannot hide growth earlier in the post-GC sequence", () => {
  const samples = [6000, 6024, 6100, 6100, 6100].map((nodes, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes, jsEventListeners: 500 },
  }));
  assert.ok(attributeRetention(samples).reasons.includes("post-gc-dom-or-listener-drift"));
});
test("missing or non-finite native counters are invalid evidence", () => {
  for (const dom of [undefined, {}, { nodes: NaN, jsEventListeners: 500 }, { nodes: 6000, jsEventListeners: -1 }]) {
    const samples = Array.from({ length: 4 }, (_, index) => ({ ...sample([1, index + 2], index * 32), dom }));
    assert.ok(attributeRetention(samples).reasons.includes("invalid-native-counters"));
  }
});
test("subscriptions retained after a round trip require attribution", () => {
  const samples = Array.from({ length: 4 }, (_, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes: 6000, jsEventListeners: 500 },
  }));
  samples[1].lifecycle.activeSubscriptions++;
  assert.ok(attributeRetention(samples).reasons.includes("subscription-population-drift"));
});
test("persistent post-baseline cohorts remain a qualification blocker", () => {
  const samples = [
    { ...sample([1, 2], 0), dom: { nodes: 6000, jsEventListeners: 500 } },
    { ...sample([1, 3], 32), dom: { nodes: 6024, jsEventListeners: 512 } },
    { ...sample([1, 3], 64), dom: { nodes: 6024, jsEventListeners: 512 } },
    { ...sample([1, 3], 96), dom: { nodes: 6024, jsEventListeners: 512 } },
  ];
  assert.equal(attributeRetention(samples).status, "needs-attribution");
});
test("the automated gate blocks on screening failures, not the offline attribution duty", () => {
  const clean = Array.from({ length: 4 }, (_, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes: 6024, jsEventListeners: 512 },
  }));
  assert.deepEqual(screeningBlockers(attributeRetention(clean).reasons), []);
  const drift = [6000, 6024, 6100, 6100].map((nodes, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes, jsEventListeners: 500 },
  }));
  assert.deepEqual(screeningBlockers(attributeRetention(drift).reasons), ["post-gc-dom-or-listener-drift"]);
  assert.deepEqual(screeningBlockers(["missing-attribution"]), ["missing-attribution"]);
});
test("a mid-sequence excursion that fully returns to baseline is an observation, not a blocker", () => {
  // The real Linux/Chromium soak shows 614 listeners at phase transitions
  // settling back to the 512 baseline; freed counters are not retention.
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: index === 6 || index === 13 ? 614 : 512 },
  }));
  const result = attributeRetention(samples);
  assert.ok(result.reasons.includes(TRANSIENT_EXCURSION_REASON));
  assert.deepEqual(screeningBlockers(result.reasons), []);
});
test("a displaced final tail remains a persistent blocker", () => {
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: index === 20 ? 614 : 512 },
  }));
  const result = attributeRetention(samples);
  assert.ok(result.reasons.includes("post-gc-dom-or-listener-drift"));
  assert.deepEqual(screeningBlockers(result.reasons), ["post-gc-dom-or-listener-drift"]);
});
test("an explicit settled tail sample is the authoritative resting state", () => {
  // Round 5 CI data: the blip can land on the final round checkpoint; the
  // quiescent sample after it proves recovery, so this must not block.
  const blipBeforeSettled = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: index === 20 ? 614 : 512 },
  }));
  blipBeforeSettled.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 512 }, settledReadings: readings([6049, 512, 6], [6049, 512, 6], [6049, 614, 6]) });
  const recovered = attributeRetention(blipBeforeSettled);
  assert.ok(recovered.reasons.includes(TRANSIENT_EXCURSION_REASON));
  assert.deepEqual(screeningBlockers(recovered.reasons), []);
  // A settled tail that stays displaced is still a persistent blocker.
  const stuck = blipBeforeSettled.map((sample_) => ({ ...sample_ }));
  stuck[21] = { ...stuck[21], dom: { nodes: 6049, jsEventListeners: 614 }, settledReadings: readings([6049, 614, 6], [6049, 614, 6], [6049, 512, 6]) };
  assert.deepEqual(screeningBlockers(attributeRetention(stuck).reasons), ["post-gc-dom-or-listener-drift"]);
});
test("an unsettled baseline is reported instead of being judged as displacement", () => {
  // Round 5 CI data: the baseline sample itself caught the cleanup blip (616),
  // every later reading rested at 514, and the gate misreported the series as
  // persistent drift.
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: index === 0 ? 616 : 512 },
  }));
  samples[0] = { ...samples[0], baselineStable: false, baselineReadings: [{ nodes: 6049, jsEventListeners: 616 }, { nodes: 6049, jsEventListeners: 512 }] };
  samples.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 512 }, settledReadings: readings([6049, 512, 6], [6049, 512, 6], [6049, 512, 6]) });
  const result = attributeRetention(samples);
  assert.ok(result.reasons.includes(BASELINE_NOT_SETTLED_REASON));
  assert.deepEqual(screeningBlockers(result.reasons), [BASELINE_NOT_SETTLED_REASON]);
});
test("an unsettled baseline still blocks sustained growth away from a low point", () => {
  // 616 -> 400 -> 450 -> 500 -> 550 ends below the baseline but keeps growing;
  // skipping the displacement check must not hide it.
  const listeners = [616, 400, 450, 500, 550];
  const samples = listeners.map((value, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: value },
  }));
  samples[0] = { ...samples[0], baselineStable: false };
  const result = attributeRetention(samples);
  assert.ok(result.reasons.includes("post-gc-dom-or-listener-drift"));
  assert.deepEqual(screeningBlockers(result.reasons), [BASELINE_NOT_SETTLED_REASON, "post-gc-dom-or-listener-drift"]);
});
test("a settled baseline keeps the displacement check", () => {
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32),
    dom: { nodes: 6049, jsEventListeners: index === 0 ? 512 : 514 },
  }));
  samples[0] = { ...samples[0], baselineStable: true };
  samples.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 514 }, settledReadings: readings([6049, 514, 6], [6049, 514, 6], [6049, 514, 6]) });
  assert.deepEqual(screeningBlockers(attributeRetention(samples).reasons), ["post-gc-dom-or-listener-drift"]);
});

test("settled majority absorbs a single-sample blip (layer c, task 117-D)", () => {
  // One noisy reading (614) among two resting readings (512) must not flip
  // the verdict: c down-samples single-sample noise while keeping the strict
  // majority requirement (>=2 of >=3).
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes: 6049, jsEventListeners: index === 20 ? 614 : 512 },
  }));
  samples.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 512 },
    settledReadings: readings([6049, 512, 6], [6049, 512, 6], [6049, 614, 6]) });
  const result = attributeRetention(samples);
  assert.deepEqual(screeningBlockers(result.reasons), []);
});

test("a settled tail without a majority reading is its own blocker", () => {
  // Three disagreeing readings cannot anchor a verdict; report it instead of
  // silently falling back to whichever sample landed last.
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes: 6049, jsEventListeners: 512 },
  }));
  samples.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 512 },
    settledReadings: readings([6049, 512, 6], [6049, 513, 6], [6049, 514, 6]) });
  assert.deepEqual(screeningBlockers(attributeRetention(samples).reasons), [SETTLED_NOT_SETTLED_REASON]);
});

test("a systematically displaced settled majority keeps the gate red (blocking ability, task 117-D)", () => {
  // Real drift survives the majority vote: every settled reading sits above
  // the baseline, so the gate must still block — c never loosens the verdict.
  const samples = Array.from({ length: 21 }, (_, index) => ({
    ...sample([1, index + 2], index * 32), dom: { nodes: 6049, jsEventListeners: 512 },
  }));
  samples.push({ ...sample([1, 23], 512), phase: "settled", dom: { nodes: 6049, jsEventListeners: 614 },
    settledReadings: readings([6049, 614, 6], [6049, 614, 6], [6049, 614, 6]) });
  const result = attributeRetention(samples);
  assert.ok(result.reasons.includes("post-gc-dom-or-listener-drift"));
  assert.deepEqual(screeningBlockers(result.reasons), ["post-gc-dom-or-listener-drift"]);
});

test("baseline and settled must name one resting context (layer a, task 117-D)", () => {
  // Comparing anchors taken in different fixture paths would misattribute a
  // phase mismatch to drift; a mismatch is reported instead.
  const build = (settledContext) => {
    const samples = Array.from({ length: 21 }, (_, index) => ({
      ...sample([1, index + 2], index * 32), dom: { nodes: 6049, jsEventListeners: 512 },
    }));
    samples[0] = { ...samples[0], context: "geometry->full" };
    samples.push({ ...sample([1, 23], 512), phase: "settled", context: settledContext, dom: { nodes: 6049, jsEventListeners: 512 },
      settledReadings: readings([6049, 512, 6], [6049, 512, 6], [6049, 512, 6]) });
    return attributeRetention(samples).reasons;
  };
  assert.deepEqual(screeningBlockers(build("geometry->full")), []);
  assert.deepEqual(screeningBlockers(build("windowed->full")), [RESTING_CONTEXT_MISMATCH_REASON]);
});

test("settledMajority requires three valid readings and two agreeing votes", () => {
  assert.equal(settledMajority(undefined), null);
  assert.equal(settledMajority(readings([1, 2, 3], [1, 2, 3])), null, "two readings never vote");
  assert.equal(settledMajority(readings([1, 2, 3], [4, 5, 6], [7, 8, 9])), null, "no majority");
  assert.equal(settledMajority(readings([1, 2, 3], [4, 5, 6], [{ nodes: 1 }])), null, "invalid reading");
  const winner = settledMajority(readings([6049, 512, 6], [6049, 614, 6], [6049, 512, 6]));
  assert.deepEqual(winner, { nodes: 6049, jsEventListeners: 512, activeSubscriptions: 6, agree: 2, total: 3 });
});
test("native objects are not automatically detached DOM", () => {
  const heap = { snapshot: { meta: {
    node_fields: ["type", "name", "id", "self_size", "detachedness"],
    node_types: [["native", "code"], [], [], [], []],
  } }, strings: ["HTMLDivElement", "compiled function"],
  nodes: [0, 0, 1, 64, 1, 0, 0, 2, 64, 2, 1, 1, 3, 128, 0] };
  const result = summarizeHeap(heap);
  assert.equal(result.categories.native.count, 2);
  assert.equal(result.categories.code.selfBytes, 128);
  assert.deepEqual(result.detached.HTMLDivElement.ids, [2]);
  heap.snapshot.meta.node_fields[4] = "unknown";
  assert.equal(summarizeHeap(heap).detachednessAvailable, false);
  assert.deepEqual(summarizeHeap(heap).detached, {});
});

test("absent-probe degradation keeps the evidence pipeline computable (task 751)", () => {
  // CI 实测（run 38043636852）：probe 缺席时旧代码在采集任何读数前 throw，
  // 三 shard 零数据 FAIL。降级契约：占位快照必须通过完整性校验、产出空 cohort、
  // attribution 只剩离线职责——聚合器（app-memory-shards.mjs aggregateShards）
  // 的硬性检查在降级档位下因此可满足，而不是 shard 绿聚合红。
  assert.equal(Object.isFrozen(ABSENT_LIFECYCLE_PROBE), true);
  assert.equal(ABSENT_LIFECYCLE_PROBE.published, false);
  const degraded = Array.from({ length: 4 }, (_, index) => ({
    phase: index === 0 ? "baseline" : "full", roundTrips: index * 32,
    lifecycle: ABSENT_LIFECYCLE_PROBE, dom: { nodes: 6024, jsEventListeners: 512 },
    lifecycleProbePublished: false,
  }));
  assert.equal(evidenceIntegrity(degraded), true);
  assert.deepEqual(retainedCohorts(degraded).at(-1), {
    phase: "full", roundTrips: 96, survivorsFromBaseline: [], retainedPostBaseline: [],
  });
  const attribution = attributeRetention(degraded);
  assert.equal(attribution.status, "needs-attribution");
  assert.deepEqual(screeningBlockers(attribution.reasons), []);
  assert.equal(degraded.every(s => s.lifecycle.activeOperations === 0), true, "aggregateShards hard check stays derivable");
});
