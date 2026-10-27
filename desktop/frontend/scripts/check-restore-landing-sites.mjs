#!/usr/bin/env node

/**
 * Restore-landing site contract (tl 1204) for the transcript scroll SSOT.
 *
 * "Should this surface be at the bottom, and where is its anchor" is decided
 * in exactly one place: TranscriptKernel (anchor authority). Positioning a
 * freshly restored surface is allowed through exactly two chains:
 *   a) synchronous landing  — kernel.restoreLanding() (the replaceSurface
 *      effect and the hydrate-complete frame are its only call sites);
 *   b) async fallback       — kernel.scheduleTailSync()'s single rAF pass,
 *      which emits tail-sync-fallback-exhausted when it cannot land.
 * Every other "make sure we end up at the bottom" call site (the old
 * #9567 setScrollMode("tail-follow") + scrollToBottom() remount chain, inline
 * begin("tail-sync") + correctAnchor combos) is a forbidden re-grown patch.
 *
 * The bottom verdict formula is also single-source: readers ask
 * kernel.isViewportAtBottom / TRANSCRIPT_BOTTOM_THRESHOLD_PX, never a copy.
 *
 * Exit 0 = contract holds, 1 = violations found (each printed).
 * The audit function is exported so the test harness can (1) assert the real
 * sources and (2) prove the guard itself trips on injected violations.
 */

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const SOURCE_ROOT = fileURLToPath(new URL("../src", import.meta.url));

const KERNEL = "lib/transcriptKernel.ts";
const HOOK = "lib/useTranscriptKernel.ts";
const TRANSCRIPT = "components/Transcript.tsx";
const DIAG_PANEL = "components/ScrollDiagnosticPanel.tsx";
const DIAG_LIB = "lib/frontendDiagnostics.ts";

function countMatches(content, re) {
  const matches = content.match(re);
  return matches ? matches.length : 0;
}

/** Strip comments so prose that MENTIONS a call (e.g. a comment documenting
 * the deleted #9567 chain) is never counted as a call site. */
function stripComments(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/^[^"'\n]*\/\/.*$/gm, (line) => line.slice(0, line.indexOf("//")));
}

/** Audit a { relativePath: content } map against the restore-landing
 * contract. Returns an array of human-readable violation strings. */
export function auditRestoreLandingSites(sources) {
  const violations = [];
  const get = (path) => sources[path];
  const has = (path) => typeof get(path) === "string";

  // Kernel: exactly one authority definition; the recovery protocol's only
  // direct write executors are correctAnchor (shared landing), scrollToTail
  // (explicit user button), and the scheduleTailSync rAF (the fallback).
  if (has(KERNEL)) {
    const k = stripComments(get(KERNEL));
    const defs = countMatches(k, /restoreLanding\s*\(/g);
    if (defs !== 1) violations.push(`${KERNEL}: kernel.restoreLanding definition count = ${defs}, expected exactly 1 (the single authority).`);
    // correctAnchor has two exits (tail floor / block offset) sharing one
    // private executor, plus the explicit scrollToTail button path, plus the
    // single scheduleTailSync rAF fallback = 4.
    const executors = countMatches(k, /this\.writeAndFinish\s*\(/g);
    if (executors !== 4) violations.push(`${KERNEL}: direct writeAndFinish call sites = ${executors}, expected exactly 4 (correctAnchor tail exit + correctAnchor block exit + explicit scrollToTail + scheduleTailSync rAF fallback). A new recovery write site re-grew a patch chain.`);
    const authority = countMatches(k, /isViewportAtBottom\s*\(/g);
    if (authority < 2) violations.push(`${KERNEL}: isViewportAtBottom missing or unused inside the kernel itself (expected the definition plus its use in observeNativeScroll).`);
  } else violations.push(`${KERNEL}: missing from sources.`);

  // Hook: the synchronous chain has exactly two wires into the authority —
  // the replaceSurface effect and the hydrate-complete wrapper. No inline
  // tail-sync recovery combos outside settleGeometry's streaming follow-up.
  if (has(HOOK)) {
    const h = stripComments(get(HOOK));
    const wires = countMatches(h, /kernel\.restoreLanding\s*\(/g);
    if (wires !== 2) violations.push(`${HOOK}: kernel.restoreLanding call sites = ${wires}, expected exactly 2 (replaceSurface effect + hydrate-complete wrapper).`);
    const inlineTailSync = countMatches(h, /kernel\.begin\(\s*"tail-sync"/g);
    if (inlineTailSync !== 1) violations.push(`${HOOK}: inline kernel.begin("tail-sync") sites = ${inlineTailSync}, expected exactly 1 (settleGeometry's streaming geometry commit). An inline recovery combo re-grew here.`);
    const formulaCopy = countMatches(h, /scrollHeight\s*-\s*geometry\.clientHeight\s*-\s*geometry\.scrollTop\s*<=/g);
    if (formulaCopy !== 0) violations.push(`${HOOK}: a render-layer copy of the atBottom formula re-appeared; ask kernel.isViewportAtBottom instead.`);
    const asksAuthority = countMatches(h, /kernel\.isViewportAtBottom\s*\(/g);
    if (asksAuthority !== 1) violations.push(`${HOOK}: render-time isAtBottom must ask kernel.isViewportAtBottom exactly once.`);
  } else violations.push(`${HOOK}: missing from sources.`);

  // Transcript: the unconditional remount landing chain stays dead; the only
  // remaining tail-follow forcing is the blank-sentinel recovery (a blank
  // viewport has no reader to override) and the explicit user button.
  if (has(TRANSCRIPT)) {
    const t = stripComments(get(TRANSCRIPT));
    const forced = countMatches(t, /setScrollMode\(\s*"tail-follow"\s*\)/g);
    if (forced !== 1) violations.push(`${TRANSCRIPT}: setScrollMode("tail-follow") sites = ${forced}, expected exactly 1 (blank-sentinel recovery). The #9567 remount chain must stay deleted.`);
    const button = countMatches(t, /scrollToBottom\(\)/g);
    if (button !== 2) violations.push(`${TRANSCRIPT}: scrollToBottom() call sites = ${button}, expected exactly 2 (jump-to-bottom button + blank-sentinel recovery).`);
  } else violations.push(`${TRANSCRIPT}: missing from sources.`);

  // Diagnostics read the shared threshold constant, not a second "4".
  for (const path of [DIAG_PANEL, DIAG_LIB]) {
    if (!has(path)) { violations.push(`${path}: missing from sources.`); continue; }
    const d = get(path);
    if (countMatches(d, /atBottom:\s*bottomDistance\s*<=\s*TRANSCRIPT_BOTTOM_THRESHOLD_PX\b/g) !== 1) {
      violations.push(`${path}: the atBottom diagnostic verdict must use TRANSCRIPT_BOTTOM_THRESHOLD_PX exactly once.`);
    }
  }
  return violations;
}

function loadRealSources() {
  const sources = {};
  const visit = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== "__tests__") visit(path);
      } else if (/\.(?:ts|tsx)$/.test(entry.name) && !/\.test\.(?:ts|tsx)$/.test(entry.name)) {
        const relative = path.slice(SOURCE_ROOT.length + 1).replaceAll("\\", "/");
        sources[relative] = readFileSync(path, "utf8");
      }
    }
  };
  visit(SOURCE_ROOT);
  return sources;
}

const isMain = process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];
if (isMain) {
  const violations = auditRestoreLandingSites(loadRealSources());
  if (violations.length > 0) {
    for (const violation of violations) console.error(`check-restore-landing-sites: ${violation}`);
    console.error(`check-restore-landing-sites: ${violations.length} violation(s) found.`);
    process.exit(1);
  }
  console.log("check-restore-landing-sites: OK (recovery protocol = sync restoreLanding + rAF scheduleTailSync, single authority, no re-grown patch chains)");
}
