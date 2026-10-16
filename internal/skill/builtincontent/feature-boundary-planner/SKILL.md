---
name: feature-boundary-planner
description: Map a Reasonix behavior change to current UI surfaces, state owners, protocol commands, persistence, and validation. Use for impact analysis, product-boundary planning, or an implementation handoff grounded in the checked-out fork source (desktop app, Go host, React frontend).
runas: inline
---

# Feature Boundary Planner

Trace the requested behavior through the current checkout. A feature belongs in the result only when current source or an explicit new requirement supports it. Verify referenced paths and symbols before using them; do not reconstruct removed functionality from historical catalogs.

## Choose The Scope

- **impact-only:** inspect and report without changing code or product specs.
- **planning:** establish behavior and acceptance cases before implementation.
- **implementation-handoff:** turn confirmed decisions into a bounded implementation and validation plan.

Use the mode implied by the request. Clarify only unknown decisions that materially change the scope; do not ask the user to reconfirm facts already established in this task.

## Find Current Evidence

1. Search aliases and node IDs in [reasonix-feature-graph.yaml](references/reasonix-feature-graph.yaml) for the user's terms. Read only matched nodes and their one-hop relationships, then verify the declared files, symbols and semantics against the current checkout. The graph is a curated seed index, not a complete feature inventory. Use [source-discovery.md](references/source-discovery.md) to fill gaps or start when there is no match. Read fork context in `FORK.md` / `REASONIX.md`; for desktop frontend work also read `desktop/AGENTS.md`.
2. Locate the entrypoint with `glob`/`git ls-files` and focused `grep` searches (builtin `grep` tool inside the repo, `git grep` from bash). Trace direct callers with the builtin `lsp_references` tool when a language server is attached; otherwise fall back to `git grep -n "<symbol>"`. The builtin `code_index` tool gives file outlines and symbol definition candidates — verify its hits with `read_file` or `grep` before relying on them.
3. Classify the change: presentation, option source, draft/default, validation, commit effect, persistence, or recovery.
4. Trace each user surface separately through validation and the command that commits the change. Shared UI components do not establish shared state or side effects.
5. Identify the authoritative owner, derived views, protocol boundary (wails bindings / wire events), persistence and failure behavior. Name the semantic reason for each upstream or downstream dependency; imports alone do not prove a product relationship.
6. Inspect one meaningful semantic hop first, expanding only when an unresolved owner or caller requires it. Rank relationships as must-inspect, should-inspect, conditional, invariant-only, or evidence-only.

For stateful or asynchronous behavior, show a concise diagram:

```text
user action → surface draft → validation → owner command → event / persistence
                                                └── derived UI projection
```

## Maintain The Seed Graph

Keep existing node IDs for unchanged semantic boundaries and add aliases for new terminology. Report missing seeds or changed relationships as `graph-drift-candidate`; static reachability alone does not establish a product dependency.

In `impact-only` mode, report proposed graph changes without editing files. In planning or implementation, update only verified entries within the task's scope. The graph contract, inlined here:

- the YAML must parse and every node `id` must be unique;
- every relationship endpoint must name an existing node and carry a valid rank (must-inspect / should-inspect / conditional / invariant-only / evidence-only);
- every `codeSeeds` file must exist in the checkout and every declared symbol must still be present, or the seed must be dropped;
- a graph entry is a retrieval seed, never proof of behavior or test coverage;
- do not restore missing historical docs from memory of them.

## Plan And Prune

In planning modes, write or update a feature spec before implementation. Create the spec directory if needed; do not assume an existing case catalog, test runner, or coverage workflow.

Select only dimensions that can change behavior in this fork: state, event, target surface/tab, agent mode (yolo / auto / autopilot, each crossed with ask gates), runtime availability (local desktop tab vs remote serve tab), persistence source, and locale. Prefer representative and high-risk combinations over a global Cartesian product.

Classify cases as accepted, undefined, pruned, ignored, or bug-candidate. Give every pruned case an invariant or guard; leave undefined behavior as a concrete decision. Record accepted cases with setup, action, assertions and required evidence using [case-planning-template.md](references/case-planning-template.md).

Before handing off validation, check which tests, fixtures and commands actually exist in the target package (`desktop/*_test.go`, `desktop/frontend/bench/*.mjs`, `go test` packages). Distinguish planned tests, executed tests and admitted regression coverage. A missing test path is a gap, not coverage.

## Output

Use [impact-brief-template.md](references/impact-brief-template.md) for the relevant parts of the result:

- behavior summary and scope;
- UI surface matrix, shared implementation and divergent behavior;
- ranked dependencies with current file/symbol evidence;
- state owners, validation points, commit commands and persistence;
- invariants and representative validation cases;
- unresolved decisions, unavailable evidence and relevant graph drift or updates.

## Boundaries

- User-dir and project skills with the same name override builtins (`internal/skill/builtins.go`); a fix shipped only in the builtin copy is shadowed wherever an override exists.
- Turn admission is host-owned (`desktop/turn_admission.go`); the frontend's optimistic overlay is a display draft, never an accepted runtime fact.
- Keep local desktop tabs (`desktop/tabs.go`, `desktop/app_tabs.go`) separate from remote serve tabs (`desktop/remote_tab_*.go`) until their synchronization is proven.
- Treat runtime state, snapshots, indexes, settings and caches as distinct until their synchronization is proven (e.g. `topic_state_store` vs display caches vs `runtime_state.go`).
- Do not infer current functionality from a directory left behind by build artifacts, an old document, or a historical branch.
- Do not silently change product semantics to match an implementation discrepancy; report the evidence and the decision needed.
