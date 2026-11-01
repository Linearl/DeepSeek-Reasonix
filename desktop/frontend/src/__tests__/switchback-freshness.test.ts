// Run: npx tsx src/__tests__/switchback-freshness.test.ts
//
// 任务580 (二级修复): the switch-back reuse path (hasReusableCachedTranscript →
// skipHistory) served the resident surface with zero fetches and zero
// reconciles, so a session that grew while the tab was away (background inbox
// turn, collab message, any other writer) kept showing stale content — the
// 2026-10-07 "切出不切回不刷新" report. The fix gates the fetch-free switch on
// a fingerprint check against a fresh branch-meta read (a sidecar file, NOT a
// history decode): match keeps the fast path fast; mismatch/unknown drops back
// to the bounded latest-page fetch.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fingerprintMatchesMeta, residentSurfaceFresh } from "../lib/hydrateHistoryApply";

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

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const controller = readFileSync(join(root, "lib/useController.ts"), "utf8");

console.log("\nswitch-back freshness gate (任务580)");

// ── 1. fingerprint comparator semantics ──────────────────────────────────────
{
  const meta = { sessionRevision: 41, sessionDigest: "digest-41" };
  ok(fingerprintMatchesMeta({ revision: 41, revisionKnown: true, digest: "digest-41" }, meta), "equal revision+digest counts as fresh");
  ok(!fingerprintMatchesMeta({ revision: 40, revisionKnown: true, digest: "digest-41" }, meta), "stale revision is stale even when the digest matches");
  ok(!fingerprintMatchesMeta({ revision: 41, revisionKnown: true, digest: "digest-40" }, meta), "digest mismatch vetoes even when the revision matches");
  ok(!fingerprintMatchesMeta({ revision: 41, revisionKnown: false, digest: "digest-41" }, meta), "revision-unknown cannot prove freshness");
  ok(fingerprintMatchesMeta({ revision: 0, revisionKnown: false, digest: "" }, { sessionRevision: 0, sessionDigest: "" }), "an identity-less backend keeps the legacy always-fresh behaviour");
}

// ── 2. resident surface gate ─────────────────────────────────────────────────
{
  const meta = { sessionRevision: 7, sessionDigest: "d7" };
  ok(residentSurfaceFresh({ historyRevision: 7, historyDigest: "d7" }, meta), "resident page at the meta fingerprint is fresh");
  ok(!residentSurfaceFresh({ historyRevision: 6, historyDigest: "d7" }, meta), "a resident page behind the meta revision is stale (the switch-back fetch case)");
  ok(!residentSurfaceFresh(undefined, meta), "no resident state cannot prove freshness");
  ok(!residentSurfaceFresh({ historyRevision: 7, historyDigest: "d7" }, undefined), "no meta cannot prove freshness (conservative refetch)");
  ok(!residentSurfaceFresh({ historyDigest: "d7" }, meta), "a fingerprint-less resident page cannot prove freshness");
}

// ── 3. source contract: the hydrate wiring consults the gate ────────────────
ok(
  /residentStale = !residentSurfaceFresh\(resident, freshMeta\)/.test(controller),
  "loadSessionDataForTab gates the reuse path on residentSurfaceFresh",
);
// tl1332: the stale branch now also consults the heavy-page gate, but a stale
// fingerprint still drops the fetch-free switch unconditionally.
ok(
  /if \(residentStale\) \{[\s\S]{0,240}tailFirstFill = residentPageHeavy\(/.test(controller),
  "a stale resident fingerprint drops the fetch-free switch (tl1332 gate rides the same branch)",
);
ok(
  /if \(residentStale\) \{[\s\S]{0,240}skipHistory = false;/.test(controller),
  "the stale branch still un-skips history for every session size",
);
ok(
  /expectedRevision: freshMeta\?\.sessionRevision \?\? sessionRevision/.test(controller),
  "the un-skipped fetch binds to the FRESH meta revision (not the caller's stale snapshot)",
);
ok(
  /let meta = skipHistory && freshMeta !== undefined \? freshMeta : await loadTimed\("meta"/.test(controller),
  "the fetch-free switch reuses the freshness meta instead of a second MetaForTab round trip",
);
// tl1332: the stale-refetch decision stays named; the degraded two-phase shape
// gets its own label so the monitor board can tell it from the plain reload.
ok(
  /: residentStale[\s\S]{0,400}\? \(tailFirstFill \? "resident-stale-tail-first" : "resident-stale"\)/.test(controller),
  "the stale-refetch decision is named in the hydrate decision log (diagnosable, not silent)",
);
ok(
  !/LoadSession\(/.test(readFileSync(join(root, "lib/hydrateHistoryApply.ts"), "utf8")),
  "the freshness gate introduces no full-session decode path",
);

assert.ok(true);
console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
