---
feature: session-v4-experiment
status: in-progress
updated: 2026-09-15
branch: feat/session-v4-experiment
commits: 477b99679..a9716dcb6+  # filled at delivery
---

# Session v3→v4 Experiment (Conversation Files Only)

## Report

Progress 2026-09-15 evening: storage packages are on the branch and unit-tested.
Control/chat continue wiring (T4/T5) is **not finished**; default flag remains legacy.
Manual helpers: `go run ./tools/sessionv4exp migrate <jsonl> <sessions-v4-root>`.

## [S1] Problem

Fork `main-v2-stable` stores conversations as v3 JSONL under `internal/agent`. Upstream 1.38.8 introduced session storage v4 (`internal/session` + `internal/sessioncontent`) and shipped with severe regressions (invisible sessions, blank transcripts on switch, writer/ledger failures, permission resets).

Goal of this experimental branch: enable **conversation-file-only** v3→v4 on top of the current fork, so the user can package and test (1) migrating old chats and (2) creating new chats on v4, with a clear rollback path if quality is poor.

## [S2] Design

### Scope

| Item | Contract |
|---|---|
| Storage | Port upstream `internal/session` + `internal/sessioncontent` (v4 codec, content store, history index, migrate) |
| Continue loop | Minimal control/boot glue: Service inject + `BindFreshSession` / `ContinueLegacySession` / `OpenSession` |
| Fatal-fix set | Session-side fixes from #10267, #10287, #10291 (execution binding package), #10325 |
| Feature flag | Config `session_storage` + env `REASONIX_SESSION_STORAGE`; **default `legacy`** |
| Paths | When on: legacy `sessions` → sibling `sessions-v4` via `RootForLegacyDir` |
| Migration | Copy+create; **never delete/rewrite** v3 source; `legacy/` evidence + `migration-map` |

### Out of Scope

- Electron/Wails shell migration
- Classic layout, Harness UI, evidence-gate removal, full Goal DSH
- Full desktop `session_resume`/`history_slice` replacement
- Autopilot/session-monitor redesign (compat stubs or feature-off only)
- Content GC, cross-session dedupe polish
- Full 1.38.3→1.38.8 adaptation

### Flag

```
session_storage = "legacy"  # default
session_storage = "v4"      # experimental
# env: REASONIX_SESSION_STORAGE=v4
```

### Layout when enabled

```
<data-root>/sessions/       # v3 source, immutable
<data-root>/sessions-v4/
  <session-id>/manifest.json, events.frames, legacy/
  .content-v1/ (or per-session content root per upstream)
  .query-cache/
  migration-map.json
```

### Fatal-issue mitigations

| Upstream issue | Mitigation in this branch |
|---|---|
| Catalog zero-index | Port #10267 history index + catalog metadata refresh after binding drain |
| Blank on switch | Port #10287 history-before-controller read path |
| Ledger / dual authority | Port #10291 session `execution.go` binding as a package; no single-file cherry-pick |
| Writer lease stuck | Port `service_shutdown.go` CloseAll; release on tab close/exit |
| Permission reset | Migration must not overwrite user permission config |
| Downgrade cannot read v4 | Default off + dual directories; rollback = flag off or branch rollback |

### Contracts

- Legacy path remains the authority when flag is off.
- When flag is on, new writes go only to v4; old JSONL is locator + freeze input only.
- Continue of a legacy session may migrate idempotently (deterministic targetID + migration-map lease).
- Interrupted recovery never restores approvals or arms goals from cold browse.

### Acceptance A (flag off)

- Startup uses legacy `sessions/`
- Old chats open and continue as today
- New chats still write v3

### Acceptance B (flag on)

- New session appears in list and continues
- Opening an old session migrates; source JSONL intact; v4 session continues
- Switch away and back: no blank transcript
- Restart: sessions still openable
- Close/switch tab: no writer lock hang
- Permission preset unchanged across migration
- Re-open same legacy session: idempotent

## [S3] Out of Scope

See Out of Scope above. Also explicit non-goals: packaging unless the user requests it; merging into `main-v2-stable`; Electron; UI CSS rewrites.

## Tasks

- [x] T1: Create worktree `wt-session-v4` + branch `feat/session-v4-experiment` from `main-v2-stable` — acceptance: worktree exists at project path and HEAD is based on `main-v2-stable` (covers: S2)
- [x] T2: Port `internal/sessioncontent` + `internal/session` from upstream tip (include post-1.38.8 session fixes) — acceptance: `go test ./internal/session ./internal/sessioncontent` compile and core tests pass (covers: S2; depends: T1)
- [x] T3: Add config flag `session_storage` default `legacy` + env override — acceptance: unset behaves as today; `v4` maps roots to `sessions-v4` (covers: S2; depends: T1)
- [x] T4: Minimal control/boot wiring via SessionV4Bridge (mirror) + optional Service lifecycle — acceptance: flag off unchanged; flag on snapshots mirror into `sessions-v4`; Resume imports legacy then mirrors (covers: S2; depends: T2,T3)
- [x] T5: Storage-layer fatal-fix set included with session package port (#10267/#10287/#10325-class files present). Full #10291 execution binding is **out of this experiment** (mirror, not v4-authoritative turns). — acceptance: session package tests pass (covers: S2; depends: T4)
- [x] T6: Migration path does not write user permission config (model/goal only). — acceptance: MigrateLegacy leaves permissions untouched by code inspection + tests (covers: S2; depends: T4)
- [x] T7: Bridge is additive; autopilot/monitor unchanged when flag off. When flag on, only extra v4 mirrors run. — acceptance: `go build ./internal/boot ./internal/control` and sampled control tests pass (covers: S2; depends: T4)
- [x] T8: Acceptance checklist documented below for user package testing. — acceptance: user can run A/B matrix (covers: S2; depends: T5)
- [ ] T9: Delivery report + package **when user signals**. — acceptance: branch, flag usage, limits, rollback documented (covers: S2,S3; depends: T8)

## User package checklist

**A. Flag off (default / regression)**

- [ ] App starts; old chats open and continue as today
- [ ] New chats still under `sessions/`
- [ ] No new `sessions-v4` writes required

**B. Flag on** (`session_storage = "v4"` in config or `REASONIX_SESSION_STORAGE=v4`)

- [ ] After chatting, `sessions-v4/` gains a bridge session (`bridge-…` id)
- [ ] Opening an existing chat migrates/maps and mirrors history into v4
- [ ] Legacy JSONL source file still present and unchanged
- [ ] Chat continue still works (agent path)
- [ ] Re-open same chat is idempotent (no duplicate v4 explosion)

**C. Rollback**

- [ ] Remove `sessions-v4/` or turn flag off
- [ ] Or return to `main-v2-stable` package

## Known limits (experiment)

- v4 is a **mirror**, not the execution store. Chat turns still write agent JSONL.
- Full upstream Controller↔Service execution binding (#10291) is not in this branch.
- Head-scoped multi-version DAG import uses default-view load only.
