#!/usr/bin/env node
// check-bridge-json-tags: prove the Wails boundary has a matching contract on both
// sides.
//
// The boundary is JSON, and neither language checks it for us: Go emits a struct's
// Go field names when a field has no tag, and a TypeScript `interface` is an
// assertion over whatever arrives, not a verification of it. So a renamed or
// untagged field compiles, type-checks, and silently yields undefined at runtime.
//
// That is not hypothetical. ConsolidationReport crossed the bridge without tags, so
// the frontend read `report.blockedByDivergence` as undefined for as long as the
// code existed - which turned a merge the engine had *refused* into one the panel
// reported as succeeded, and meant the confirmation offering to let a copy win never
// appeared. Nothing errored anywhere.
//
// This check treats `desktop/frontend/src/lib/bridge.ts` as the contract: every
// interface declared there names a value the frontend expects, and the Go struct of
// the same name must carry exactly the matching tags.
//
// Usage: node scripts/check-bridge-json-tags.mjs [repoRoot]
// Exits non-zero on a mismatch, so it can gate a build.

import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

const repoRoot = resolve(process.argv[2] ?? ".");
const bridgePath = join(repoRoot, "desktop/frontend/src/lib/bridge.ts");
// Bridge values are not all declared in desktop/: the agent package owns the reports
// the desktop package returns verbatim, so both trees are scanned. Scanning only
// desktop/ is what let ConsolidationReport - the struct this check exists for - go
// unnoticed.
const goRoots = [join(repoRoot, "desktop"), join(repoRoot, "internal")];

/** Interfaces declared in bridge.ts that are frontend-only conveniences rather than
 *  values returned across the bridge. Keep this empty unless a real mismatch makes
 *  an entry necessary - every name added here stops being checked. */
const FRONTEND_ONLY = new Set([]);

/** Cross-name mirrors: bridge.ts interface -> the Go struct whose JSON shape it
 *  mirrors, when the two names differ (tl1842, from the tl1831-d3 scan). Same-name
 *  matches are found automatically; these are the ones name matching cannot see.
 *  Qualifier is "desktop" for desktop/ (package main) or the Go package name for
 *  internal/<pkg>/. Bare struct names collide across packages (ten packages declare
 *  `Entry`, six declare `Snapshot`), so the qualifier is mandatory.
 *
 *  Entries are checked exactly like same-name mirrors: both directions, field names.
 *  A mapping whose interface or struct disappears fails the gate, and so does a
 *  mapping made redundant by a later same-name match - the registry must stay true,
 *  never silently stale. */
const CROSS_NAME_MIRRORS = {
  CollabSentDispatchRow: "collabinbox.SentDispatchRow",
  CollabSentDispatchSnapshot: "collabinbox.SentDispatchSnapshot",
  PreRepairCleanupFailureView: "desktop.PreRepairCleanupFailure",
  SelfHealActionView: "agent.SelfHealAction",
  HeadSynthesisItemView: "desktop.HeadSynthesisItem",
  CollabMailEntryView: "collabinbox.Entry",
  CollabMailSnapshotView: "collabinbox.Snapshot",
  CollabMailDetailView: "collabinbox.MailDetail",
  CollabMailChainView: "collabinbox.Chain",
  CollabMailChainsView: "collabinbox.ChainSnapshot",
  CollabInboxCleanNowView: "desktop.CollabInboxCleanResult",
  CollabViewCardView: "desktop.CollabViewCard",
  CollabViewExchangeView: "desktop.CollabViewExchange",
  CollabViewSessionView: "desktop.CollabViewSession",
  CollabViewOverviewView: "desktop.CollabViewOverview",
  RunJournalRunSummaryView: "desktop.RunJournalRunSummary",
  RunJournalTimelineEntryView: "desktop.RunJournalTimelineEntry",
  CollabChannelView: "collabchannel.Channel",
  CollabChannelMemberView: "collabchannel.Member",
  CollabChannelFanoutCellView: "collabchannel.FanoutRow",
  CollabDirectChainPairView: "collabinbox.DirectChainPair",
  CollabDirectChainSnapshotView: "collabinbox.DirectChainSnapshot",
  CollabDirectChainReadView: "collabinbox.DirectChainReadView",
  CollabChannelCancelResultView: "collabchannel.CancelResult",
  CollabChannelClearResultView: "collabchannel.ClearResult",
  DagMigrationSessionView: "desktop.dagMigrationSessionView",
  DagMigrationScanView: "desktop.dagMigrationScanView",
  DagMigrationRunView: "desktop.dagMigrationRunView",
  DagBackupCleanupPlanView: "desktop.dagBackupCleanupPlanView",
  DagBackupCleanupRunView: "desktop.dagBackupCleanupRunView",
  DagBackupCleanupRunRow: "desktop.dagBackupCleanupRunRow",
  VersionEntry: "desktop.InstalledVersion",
};

/** Verified cross-name mirrors with a documented wire divergence (tl1842): the
 *  interface really mirrors the struct, but individual fields do not line up, so a
 *  strict both-way check would stay red on known debt. Each entry lists the exact
 *  fields allowed to differ; anything outside the lists still fails the gate, and a
 *  listed field that stops existing (e.g. the Go snake_case tag gets fixed to
 *  camelCase) also fails, forcing the list to shrink with the fix instead of
 *  entrenching it. The casing rows are pre-existing wire bugs - the frontend reads
 *  those fields as undefined today; fixing them is a behavior change and belongs to
 *  a follow-up task, not a silent checker exception. */
const CROSS_NAME_MIRROR_DEVIATIONS = {
  SessionHeadView: {
    go: "agent.SessionHead",
    tsOnly: ["parentHead", "forkFrom", "createdAt", "lastActivity", "messageCount"],
    goOnly: ["parent_head", "fork_from", "leaf", "created_at", "last_activity", "message_count"],
  },
  SessionHealthReportView: {
    go: "agent.SessionHealthReport",
    tsOnly: [
      "heads", "suggestedHeadId", "selectedHeadId", "projectionTokens", "windowTokens",
      "materializedMsgs", "eventChainMsgs", "logSchema", "headCount", "loadTimeout", "loadMillis",
    ],
    goOnly: ["projection_tokens", "window_tokens", "token_source", "compact_fail_streak"],
  },
  SelfHealOverflowWarningView: {
    go: "agent.SelfHealOverflowWarning",
    tsOnly: ["suggestedAction", "projectionTokens", "windowTokens", "dietActionNotRun", "hydrateActionDone"],
    goOnly: ["suggested_action", "projection_tokens", "window_tokens", "diet_action_not_run", "hydrate_action_done"],
  },
  SelfHealReportView: {
    go: "agent.SelfHealReport",
    tsOnly: ["backupDir", "headFrom", "headTo"],
    goOnly: ["backup_dir", "head_from", "head_to"],
  },
  SessionRepairRecordView: {
    go: "agent.RepairRecord",
    tsOnly: ["backupDir"],
    goOnly: ["backup_dir", "warning"],
  },
};

/** bridge.ts interfaces that cross the boundary without a named Go struct: their
 *  payloads are emitted inline over the wails event bus, so there is nothing to
 *  field-check against. Every name here stops being checked - keep the reason
 *  honest and remove the entry if the payload ever grows a named struct. */
const UNMAPPABLE_INTERFACES = {
  TerminalOutputEvent: "terminal output event payload; emitted inline over the event bus, no named Go struct",
  TerminalExitEvent: "terminal exit event payload; emitted inline over the event bus, no named Go struct",
  SessionConsolidatedEvent: "session:consolidated event payload ({mainPath?}); emitted inline, no named Go struct",
};

/** Parse `export interface Name { field: Type; ... }` from bridge.ts. */
function parseTypeScriptInterfaces(source) {
  const interfaces = new Map();
  const re = /export interface (\w+)\s*\{/g;
  let match;
  while ((match = re.exec(source)) !== null) {
    const name = match[1];
    // Walk braces so a nested object literal cannot end the body early.
    let depth = 1;
    let i = re.lastIndex;
    while (i < source.length && depth > 0) {
      if (source[i] === "{") depth += 1;
      else if (source[i] === "}") depth -= 1;
      i += 1;
    }
    // Comments first, so a `// a: b` line cannot register a field.
    const body = source
      .slice(re.lastIndex, i - 1)
      .replace(/\/\/[^\n]*/g, "")
      .replace(/\/\*[\s\S]*?\*\//g, "");
    const fields = new Map();
    const fieldRe = /^\s*(\w+)(\?)?\s*:/gm;
    let fieldMatch;
    while ((fieldMatch = fieldRe.exec(body)) !== null) {
      fields.set(fieldMatch[1], Boolean(fieldMatch[2]));
    }
    interfaces.set(name, fields);
  }
  return interfaces;
}

/** Every non-test .go file under the given roots, recursively. */
function goFilesUnder(roots) {
  const out = [];
  const visit = (dir) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === "node_modules" || entry.name === "testdata") continue;
        visit(full);
      } else if (entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) {
        out.push(full);
      }
    }
  };
  for (const root of roots) visit(root);
  return out;
}

/** Parse `type Name struct { ... }` from every non-test .go file under the roots,
 *  keyed both by bare name (same-name mirror lookup, historic behavior) and by
 *  qualifier-qualified name (`pkg.Struct` / `desktop.Struct` for package main).
 *  Bare names collide across packages - ten packages declare `Entry`, six declare
 *  `Snapshot` - so every registry lookup goes through the qualified key. */
function parseGoStructs(roots, repoRootPath) {
  const structs = new Map();
  for (const file of goFilesUnder(roots)) {
    const source = readFileSync(file, "utf8");
    const pkg = /^package (\w+)/m.exec(source)?.[1] ?? "";
    // desktop/ is package main; the registries qualify it as "desktop" (it is the
    // desktop app package, and "main" would say nothing).
    const qualifier = pkg === "main" && file.startsWith(join(repoRootPath, "desktop")) ? "desktop" : pkg;
    const re = /^type (\w+) struct \{/gm;
    let match;
    while ((match = re.exec(source)) !== null) {
      const name = match[1];
      let depth = 1;
      let i = re.lastIndex;
      while (i < source.length && depth > 0) {
        if (source[i] === "{") depth += 1;
        else if (source[i] === "}") depth -= 1;
        i += 1;
      }
      const body = source.slice(re.lastIndex, i - 1).replace(/\/\/[^\n]*/g, "");
      const tagged = new Map();
      const untagged = [];
      const seen = new Set();
      const taggedRe = /^\s*([A-Z]\w*)\s+([^\n`]+?)\s+`([^`]*)`/gm;
      let fieldMatch;
      while ((fieldMatch = taggedRe.exec(body)) !== null) {
        seen.add(fieldMatch[1]);
        const tag = /json:"([^",]*)/.exec(fieldMatch[3]);
        if (tag && tag[1] && tag[1] !== "-") tagged.set(fieldMatch[1], tag[1]);
      }
      const plainRe = /^\s*([A-Z]\w*)\s+([^\n`]+?)\s*$/gm;
      while ((fieldMatch = plainRe.exec(body)) !== null) {
        if (seen.has(fieldMatch[1])) continue;
        if (/^json:/.test(fieldMatch[2])) continue;
        untagged.push(fieldMatch[1]);
      }
      const info = { tagged, untagged, file, qualifier };
      structs.set(name, info); // bare name; last writer wins (same-name lookup only)
      structs.set(`${qualifier}.${name}`, info); // qualified name; collision-free
    }
  }
  return structs;
}

const bridge = readFileSync(bridgePath, "utf8");
const interfaces = parseTypeScriptInterfaces(bridge);
const structs = parseGoStructs(goRoots, repoRoot);

/** Field-level contract check between one bridge.ts interface and one Go struct.
 *  `allowedTsOnly` / `allowedGoOnly` are the documented divergence lists from
 *  CROSS_NAME_MIRROR_DEVIATIONS (empty for strict mirrors). */
function checkMirror(name, go, tsFields, allowedTsOnly, allowedGoOnly, problems) {
  const tsNames = new Set(tsFields.keys());
  const goNames = new Set(go.tagged.values());

  for (const field of tsNames) {
    if (goNames.has(field)) continue;
    const asGoName = field.charAt(0).toUpperCase() + field.slice(1);
    if (go.untagged.includes(asGoName)) {
      problems.push(
        `${name}.${field}: Go field ${asGoName} carries no json tag, so the frontend reads undefined`,
      );
    } else if (allowedTsOnly.includes(field)) {
      // documented divergence; a listed field is still expected to exist below
    } else {
      problems.push(`${name}.${field}: declared in bridge.ts but absent from ${go.file}`);
    }
  }
  for (const [field, tag] of go.tagged) {
    if (tsNames.has(tag)) continue;
    if (allowedGoOnly.includes(tag)) continue;
    problems.push(`${name}.${field}: Go tag "${tag}" is not declared in bridge.ts`);
  }
  // Deviation lists must track reality: a listed field that no longer exists means
  // the divergence was fixed (or renamed) - shrink the list instead of keeping a
  // stale exception around (upper-bound + new-is-red discipline).
  for (const field of allowedTsOnly) {
    if (!tsNames.has(field)) {
      problems.push(`${name}: deviation tsOnly lists "${field}" but bridge.ts no longer declares it; update the registry`);
    }
  }
  for (const tag of allowedGoOnly) {
    if (![...go.tagged.values()].includes(tag)) {
      problems.push(`${name}: deviation goOnly lists "${tag}" but ${go.file} no longer emits it; update the registry`);
    }
  }
}

const problems = [];
let checked = 0;
let mapped = 0;
let deviated = 0;
let unmappable = 0;
for (const [name, tsFields] of interfaces) {
  if (FRONTEND_ONLY.has(name)) continue;
  const sameGo = structs.has(name) ? structs.get(name) : null;
  const mappedGo = CROSS_NAME_MIRRORS[name]
    ? structs.get(CROSS_NAME_MIRRORS[name])
    : undefined;
  const deviation = CROSS_NAME_MIRROR_DEVIATIONS[name];
  const deviationGo = deviation ? structs.get(deviation.go) : undefined;

  // Registry hygiene: every registered name must still resolve, and a mapping that
  // a same-name struct has made redundant must be removed rather than silently kept.
  if (CROSS_NAME_MIRRORS[name] && mappedGo === undefined) {
    problems.push(`${name}: CROSS_NAME_MIRRORS maps to "${CROSS_NAME_MIRRORS[name]}" but that struct no longer exists`);
  }
  if (CROSS_NAME_MIRRORS[name] && sameGo) {
    problems.push(`${name}: CROSS_NAME_MIRRORS entry is redundant - a same-name Go struct exists now; remove the mapping`);
  }
  if (deviation && deviationGo === undefined) {
    problems.push(`${name}: CROSS_NAME_MIRROR_DEVIATIONS maps to "${deviation.go}" but that struct no longer exists`);
  }
  if ((CROSS_NAME_MIRRORS[name] || deviation) && UNMAPPABLE_INTERFACES[name]) {
    problems.push(`${name}: registered in two registries at once; keep exactly one`);
  }

  if (sameGo) {
    // Same-name mirror (the historic path): names match on both sides.
    checkMirror(name, sameGo, tsFields, [], [], problems);
    checked += 1;
  } else if (mappedGo) {
    checkMirror(name, mappedGo, tsFields, [], [], problems);
    checked += 1;
    mapped += 1;
  } else if (deviationGo) {
    checkMirror(name, deviationGo, tsFields, deviation.tsOnly, deviation.goOnly, problems);
    checked += 1;
    deviated += 1;
  } else if (UNMAPPABLE_INTERFACES[name]) {
    unmappable += 1;
  } else {
    // The silent-skip replacement (tl1842 / G4): an interface no same-name struct
    // covers and no registry entry explains is an unverified segment of the JSON
    // boundary - exactly the state that let cross-name structs drift unnoticed.
    problems.push(
      `${name}: no same-name Go struct and no registry entry (CROSS_NAME_MIRRORS / CROSS_NAME_MIRROR_DEVIATIONS / UNMAPPABLE_INTERFACES); its json tags go unchecked`,
    );
  }
}

if (problems.length > 0) {
  console.error("check-bridge-json-tags: FAIL");
  for (const problem of problems) console.error(`  - ${problem}`);
  console.error(
    `\n${problems.length} mismatch(es) across ${checked} mirrored interface(s)\n` +
      `(${mapped} cross-name mapped, ${deviated} mapped with documented deviations, ${unmappable} unmappable event payloads).\n` +
      "A missing tag fails silently on both sides: Go emits the Go field name, and the\n" +
      "TypeScript declaration asserts rather than verifies, so the value arrives as\n" +
      "undefined with nothing raising. Add the tag, or correct whichever side is wrong.",
  );
  process.exit(1);
}
console.log(
  `check-bridge-json-tags: OK (${checked} mirrored interface(s) agree: ` +
    `${checked - mapped - deviated} same-name, ${mapped} cross-name mapped, ` +
    `${deviated} with documented deviations; ${unmappable} unmappable event payload(s) registered)`,
);
