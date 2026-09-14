# Reasonix project memory

This file is loaded into every session's system prompt (the cache-stable prefix),
so keep it concise and durable — it is the project's standing instructions to the
agent. It is the Reasonix analog of Claude Code's CLAUDE.md.

## Conventions

- Go kernel under `internal/`; each package owns one concern. A package's long
  explanation belongs in its `doc.go`, not spread across implementation files.
- One transport-agnostic `control.Controller` sits behind every frontend (chat
  TUI, HTTP/SSE serve, Wails desktop). Add behavior to the controller, not a
  frontend, so all three inherit it.
- Layering (enforced): utility packages import nothing under `reasonix/`; only
  the frontends `cli`, `serve`, `acp`, `bot`, `botruntime`, `boot` and the hosts
  `cmd/`, `desktop/` may import `control`; nothing below a frontend may import
  one. The declared sets live in `tools/repolint/layers.go`.
- Subagent delegation keeps five concepts apart: a profile says how a worker
  thinks, `TaskSpec` what this call wants, `CapabilityGrant` what it may touch,
  `ContextRequest` what it starts from, `SchedulerPolicy` when it runs. Put a
  field in whichever member decides its value — profiles carry ceilings, never
  per-call values. `internal/agent/profile_boundary_test.go` enforces it.
- Cache-first: the system-prompt prefix (base prompt + tools + memory) must stay
  byte-stable across turns so DeepSeek's automatic prefix cache stays warm. Never
  mutate it mid-session — ride the turn tail instead (see `control.Compose`).
- Performance features land with an effect test at their final boundary
  (`internal/boot/effect_test.go` pattern): assert what actually reaches the
  provider request, frontend sink, or trajectory through the real `boot.Build`
  assembly. Component correctness is not system effectiveness.
- A mutex- or atomic-guarded struct is ratcheted on its **scalar** field count
  (`struct-state`), not its total: independent flags multiply into states no
  type records as legal. Fixing a boundary case by adding one more `bool` is
  the move this blocks — group by lifetime into a named sub-state instead
  (`agent.perTurnState` is the pattern), which costs one field and removes the
  whole product.

## Comments

Default is none — the code is the truth. Write one only when the **why** is
non-obvious: a hidden constraint, a workaround anchored to something verifiable,
an invariant the type system cannot express, or an external-protocol quirk.

- Declaration doc: ≤15 lines. Package comment: ≤8 lines, or ≤40 in a `doc.go`.
- Every other comment: ≤3 lines. Struct-field and trailing `//`: 1 line.
- Never: restatements of the code, phase/stage narrative, incident or
  conversation history, section banners, commented-out code, `@param` lists.
- `TODO(#nnn):` and `HACK(#nnn):` need the issue anchor. `FIXME` is banned.
- One responsibility per file; 800 lines is the ceiling.

`go run ./tools/repolint` enforces all of it against a ratchet baseline: recorded
debt is tolerated, anything new fails CI. Never widen the baseline to land a
change — fix the code. `-update` exists for carrying debt through a rename or an
extraction, and that diff must be justified in the PR.

## Memory

- Standing instructions are hierarchical: committed/shared `REASONIX.md`,
  `AGENTS.md`, and `CLAUDE.md`; personal `*.local.md` variants; matching files in
  ancestor directories; and user-global files under the memory state root
  (`REASONIX_STATE_HOME`, otherwise `REASONIX_HOME`, otherwise `~/.reasonix` on
  macOS/Linux or `%APPDATA%\reasonix` on Windows). All distinct supported files
  in a directory load; `AGENTS.md` is not merely a fallback.
- `@path` on its own line imports another file's contents.
- `#<note>` in chat quick-adds an always-on instruction. The `remember` tool
  instead saves a fallible background fact (frontmatter file + `MEMORY.md`
  index). Fact `type` classifies content; independent `scope` controls whether it
  is project-only (the default) or explicitly global. The index and pinned
  compatibility guidance load in a host-generated `session-context` snapshot
  before the next real user turn. Standing-doc edits still receive a temporary
  tail note and enter the system prompt after reload/new session.

## Notes

## Pre-push CI simulation

Run these **before every commit** to catch the fastest CI failures locally:

```bash
gofmt -w .                          # catches gofmt (saves ~13s CI)
go vet ./...                        # catches vet warnings (saves ~52s CI/lint)
make lint                           # golangci-lint at CI's pin + repolint
go test ./internal/tool/builtin/ ./internal/boot/  # catches tool/boot test breaks
```

`make lint` runs both gates CI runs, at the version in `.golangci-version`;
`make lint-install` installs it. Do not skip it: a `modernize` finding never
shows up in `go vet`, and the CI round trip that catches it instead costs ten
minutes.

## Pre-existing failures

A failure that predates your change still has to be diagnosed and driven to zero.
"Pre-existing, not mine" records where it came from; it does not close it. Decide
**which side is wrong** and act on that verdict — never leave a known red test in
the suite, because on a fork branch the suite is the only guard against a later
merge quietly overwriting our behavior (see `docs/upstream-merge-checklist.md`),
and a permanently red test swallows the next real regression.

* **The test is stale** — the behavior it pins was deliberately changed and the
  new behavior is right → update the fixture or expectation, and say in the commit
  why the new behavior is correct. Never weaken an assertion just to pass.
* **The code is wrong** — the behavior regressed, or a helper went missing →
  fix the code, not the test.
* **The test is orphaned** — its subject was deleted and no script runs it →
  delete it, referencing the commit that removed the feature.

Confirm the failure is genuinely pre-existing first (`git stash` your change and
re-run); that check decides the framing, not whether the failure gets fixed.

### Current list (2026-09-14)

Five failures found by `go test ./internal/...` after the task-64 port, all confirmed
pre-existing by stashing the port and re-running. They are not caused by the port, and they are
still not allowed to stay red.

| Test | Package | Diagnosis |
|---|---|---|
| `TestBuiltinToolContractDocumentation` | `internal/tool` | **fork-only tool never registered** — `restart_and_update` (task 81) and `view_image` have no table row or read-only flag in the contract docs |
| `TestEveryBuiltinDeclaresSnipStance` | `internal/tool` | **same two tools** — neither implements `tool.SnipHinter` nor is listed in `acceptsDefaultSnip`, so they silently take a generic default (the test exists to catch exactly that) |
| `TestGoalCompletionAndRealBlockedStillTerminate` | `internal/control` | see below |
| `TestRepeatedCompleteWithOnlyProjectCheckFinishes` | `internal/control` | see below |
| `TestResolveRefsAttachmentKinds` | `internal/control` | see below |

The previous six were diagnosed to verdicts and closed (all green as of 2026-09-14); their
table is kept below for the record.
 Six tests were red on this branch; all six are green as of 2026-09-14,
each diagnosed to a verdict rather than carried:

| Test | Package | Verdict | Fixed in |
|---|---|---|---|
| `TestOfficialDeepSeekIgnoresVisionMetadata` | `provider/anthropic` | **stale** — pinned the hard block the fork removed | task 63 capability work |
| `TestOfficialRequestURLImageHardLimit` | `provider/anthropic` | **stale** — same | task 63 capability work |
| `TestOfficialDeepSeekImageMetadataMatchesTextOnlyWireBytes` | `provider/anthropic` | **stale** — same | task 63 capability work |
| `TestOfficialDeepSeekResponsesIgnoresVisionMetadata` | `provider/responses` | **stale** — same | task 63 capability work |
| `TestOfficialDeepSeekResponsesImageMetadataMatchesTextOnlyWireBytes` | `provider/responses` | **stale** — same | task 63 capability work |
| `TestMergeTreeRechecksBudgetBeforeNewRound` | `agent` | **stale** — window too small, wrong guard fired | `a560c78b8` |

**The sixth one, resolved.** The open question was whether the 256-token summary output floor was
too aggressive for a 2000-token window, or whether the test predated the floor. **The test's
window was the problem**: 2000 tokens leaves 190 of summary output, so the output floor rejected
the call before the *call* budget the test means to exercise — the message named a guard the test
does not test. A roomier window fixes it; the call budget is then set to leave exactly one round
of two, so the failure lands on the round boundary the name promises.

## Import cycle rule

Before importing a new internal package from a non-test file, verify the target package's **test files** aren't already importing back to you:

```
# BAD: agent(_test.go) → tool/builtin(sessions.go) → agent  → setup failed
```

Use `go test ./path/to/target/` to detect cycles **before** pushing. A `[setup failed]` message means a cycle exists.

## PR hygiene

- **One force-push per round of review feedback.** Multiple force-pushes destroy review history and confuse reviewers.
- **Keep the PR diff minimal.** Only the files relevant to the PR's purpose — no stray changes from other branches.
- **Amend, don't add commits, for review feedback** — keeps the commit history clean.
- **Add files by explicit path.** `git add <file1> <file2>` (or `git add -u` for tracked files only). Never `git add -A` / `git add .` — those stage other branches' or sessions' uncommitted work. Always run `git status --short` before committing and confirm the staged set matches what this PR is about.
- **Fork-only feature lands → register its anchor the same session.** Every feature/perf change that diverges from upstream gets a row in `scripts/check-fork-integrity.mjs` `CHECKS` in the same commit that lands it (see `docs/upstream-merge-checklist.md` §1.1). Anchors are semantic strings (assignment/call shape like `skipHistory: hasLocalItems`), not comments or bare symbol names — `33b6c32ec` was silently reverted by the 1.38.3 merge for a month because its function survived while its logic did not.

## Git discipline for parallel agents

Multiple subagents and Reasonix sessions may write to the same worktree at once. These rules keep that safe.

### Committing
- Commit only files you changed in this session.
- Always include `fixes #<n>` / `closes #<n>` in the commit message when an issue or PR exists.
- Stage files by explicit path (`git add internal/x/y.go internal/x/y_test.go`). Never `git add -A` / `git add .` — those sweep up other agents' uncommitted work.
- Run `git status --short` before committing and verify the staged set is only your files.

### Forbidden Git operations
- `git reset --hard` — destroys uncommitted changes
- `git checkout .` — destroys uncommitted changes
- `git clean -fd` — deletes untracked files
- `git stash` — stashes all changes including other agents' work
- `git add -A` / `git add .` — stages other agents' uncommitted work
- `git commit --no-verify` — bypasses required checks; never allowed

### Safe workflow
```bash
git status --short                          # 1. see what is yours
git add <file1> <file2> ...                  # 2. stage explicit paths only
git commit -m "fix(scope): description"      # 3. commit
git pull --rebase && git push                # 4. push (never reset/checkout)
```

### On rebase conflict
- Resolve conflicts only in files you modified.
- If the conflict is in a file you did not modify, abort and ask the user.
- Never force push.

### User override
If user instructions conflict with these rules, ask for confirmation before executing.

## PR metadata gates

Two CI guards read the PR body. The scripts are the source of truth and both
run locally: `scripts/check-cache-impact.sh`, `scripts/check-docs-impact.sh`.
Separators must be an ASCII `-` or `:` — an em dash fails the docs guard.

Cache-sensitive diffs (`internal/tool/`, `internal/provider/`,
`internal/boot/`, `internal/agent/agent.go`, and the rest of the list in the
script) require:

```
Cache-impact: <none|low|medium|high> - <reason>
Cache-guard: <focused guard test/command or existing guard rationale>
```

`none` is a legitimate impact when the provider-visible prefix stays
byte-identical; only an empty value, `todo`, or `tbd` is rejected. If the diff
also touches `internal/config/`, `internal/memory/`, `internal/outputstyle/`,
`internal/skill/`, or `internal/boot/`, add `System-prompt-review: <note>` —
that field additionally rejects `none` and `n/a`, so it must name a reviewer.

User-visible diffs (`cmd/reasonix/`, `desktop/`, `npm/`, and most of
`internal/`; tests and lockfiles are exempt) require one of these, chosen by
whether the same PR edited `docs/*.md`:

```
Documentation-impact: updated - <what changed>            # docs/*.md edited
Documentation-impact: none - <why the docs stay correct>  # not edited
```
