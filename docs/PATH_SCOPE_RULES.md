# Path scope rules (experimental)

> **Status**: experimental. Disabled by default. Enable with
> `experimental_path_rules = true` in `[desktop]` (Settings → Experimental → Path rules)
> or leave off for production-identical confinement.

These rules describe how Reasonix scopes **read** and **write** access to paths
when path-scope enforcement is on. They do **not** replace `allow_write` /
write-access approval; they add a structured vocabulary for declaring which
directories a session or sub-agent may touch.

## Goals

1. Make path boundaries explicit and reviewable.
2. Let fleet / sub-agent `write_paths` stay inside declared scopes.
3. Keep escape attempts fail-closed even when the experiment is on.

## Scopes

| Scope | Meaning | Typical roots |
|---|---|---|
| **workspace** | The session project root | desktop tab cwd, CLI cwd |
| **session-private** | Temp / caches private to the logical session | session temp dir |
| **managed-worktree** | Product-managed isolated worktrees | `DeliveryWorktreeDir()`, sibling `worktrees/` |
| **user-approved** | Roots the user added | `[sandbox] allow_write`, `--add-dir`, write-access approval |
| **forbidden** | Never readable/writable by tools | Reasonix session stores, secrets, deny rules |

## Evaluation order

For a write target path `P`:

1. If `P` is under a **forbidden** root → deny (no approval can override).
2. Else if `P` is under **workspace**, **session-private**, **user-approved**, or (when parallel full-access is on) **managed-worktree** → allow.
3. Else → write-access approval (once / session / project), same as today.

For a read target:

1. Forbidden roots are denied.
2. `forbid_read` config roots are denied.
3. Otherwise allow (tools may still page/limit large files).

## Sub-agent `write_paths`

- Must resolve inside the parent's allowed scopes.
- With path-scope rules on, a claim may also name a path under a **managed-worktree** root when `experimental_parallel_full_access` is on.
- Escapes outside every scope fail preflight before the sub-agent starts.

## Fleet `worktree_root`

- Used for merge-conflict preflight (who conflicts with whom).
- When parallel full-access is on, the declared root is also added as a write claim for that task.

## Interaction with existing switches

| Switch | Role |
|---|---|
| `experimental_path_rules` | Enables this document's evaluation order as the product policy |
| `experimental_parallel_full_access` | Trusts managed worktree roots as write surfaces |
| `[sandbox] allow_write` | Persistent user-approved roots |
| write-access approval | Interactive once/session/project grants |

## Non-goals

- Not a second permission DSL.
- Not a sandbox off-switch.
- Does not grant network, credentials, or process rights.
