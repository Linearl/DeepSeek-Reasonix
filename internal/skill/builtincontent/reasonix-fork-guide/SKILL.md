---
name: reasonix-fork-guide
description: "Explain how to use this Reasonix fork day-to-day: worktree parallel development, session collaboration modes, experimental features, skills/commands, and where to find docs. Use when the user asks how to use the fork, where a feature lives, or how to set up a parallel workflow. Also use when the agent itself is stuck in repeated tool-call failures — argument validation errors, duplicate-result rejections, or read/write-evidence deadlocks — to check correct call shapes and recovery steps before retrying. Distinct from reasonix-guide (capability self-diagnostics)."
runAs: inline
---

# Reasonix fork user guide

A **usage** guide for this fork. For capability loading / doctor diagnostics, use `reasonix-guide` instead.

## When to use which guide

| Need | Skill |
|---|---|
| How do I run parallel work / worktrees / sessions? | **this guide** |
| Why is my skill / command / MCP missing? | `reasonix-guide` |
| My tool calls keep failing (shape / dedupe / evidence errors) | **this guide → Tool call troubleshooting** |

## Tool call troubleshooting (agent self-service)

When tool calls keep failing, check this table **before retrying** — most failures are call-shape mistakes, not host bugs. Retrying an unchanged wrong call wastes turns: the host returns the identical error text, which the duplicate-result filter then silently omits.

| Signal | Usual cause | Correct action |
|---|---|---|
| `argument validation failed … expected required properties: to, message` (or similar) on a `use_capability` call | Target arguments were **double-wrapped**: `arguments` holds another `{arguments, capability_id}` envelope | **Flatten**: pass the target tool's parameters directly as the value of `arguments` — correct shape: `{"action":"call","capability_id":"tool:talk_to_session","arguments":{"to":"…","message":"…"}}` |
| `duplicate tool result omitted (identical to call_id=…)` | The previous call produced byte-identical output — usually the same wrong call returning the same error | Do not resend; change the call. To view the omitted original, page it via `session:tool_result` (argument `tool_call_id`) |
| edit rejected with `WRITE_EVIDENCE_STALE` (after a successful edit) | The file changed since your last read; a same-window re-read is deduped so no fresh evidence is produced | Re-read with a **different `offset`/`limit` window**, then retry the edit |
| `Repeated read: original text is available in call_id=…` | Read-dedupe fired; a deduped read does **not** refresh write-evidence | Widen or shift the read window (different offset) to force a fresh read |
| Cross-session reply never arrives / "thread not in sender's mailbox" | Reply used your own outbound message id as `thread_id` | `thread_id` must be the **incoming** message id you received, not one you sent |
| `talk_to_session` queued but the other side never starts | Target session was never opened (runtime not ready), or its inbox gate is closed | Ask the user to open the target session once; delivery is level-triggered on next gate-open |

**use_capability golden rule**: the outer envelope is `action` + `capability_id` + `arguments`. `arguments` must be the target tool's own parameter object — never nest `capability_id` or `arguments` keys inside it.

**Delivery checks**: `queued` ≠ delivered. The authoritative delivery signal is the recipient's `inbox.jsonl` containing your `messageId` (and `seen.json` for read state); outbound "refused" receipts can be false alarms.

## Parallel development (worktrees)

1. **Create** via agent tool `create_worktree` or `open_isolated_worktree_project`, or desktop **Create isolated worktree**.
2. Worktrees land under managed storage (`DeliveryWorktreeDir`) or `github-repo/worktrees/`.
3. **Merge back**: `prepare_worktree_merge` → show inspection (conflicts/blockers) → `merge_worktree_back`.
4. Fleet tasks may declare `worktree_root` so concurrent writers' overlapping files fail preflight before anything starts.

If writes outside the project root are blocked, either:

- add the path to `[sandbox] allow_write`, or
- enable the experiment `experimental_parallel_full_access` (config or `REASONIX_PARALLEL_FULL_ACCESS=1`) so managed worktree roots are trusted.

## Session collaboration

- **Enable it first**: Settings → 实验室 (Lab) → **跨会话通信** → turn the master switch on, then restart when prompted. Collaboration tools (`talk_to_session`, `create_collab_session`, inbox, …) register only when this is on; sessions opened before the switch stay without them until rebuilt.
- **Collaboration hop limit** lives in the same panel (3~1000, default 5) — how many round-trips a cross-session message chain may take before delivery is refused.
- Multiple desktop instances on one session surface a concurrent-writer notice; content is kept as a separate version (**View versions**).
- Autopilot approval tier: `[agent] approval_tier = guardian | parent | human`.
- The builtin skills `ll-iteration-parallel-dev` (and the feedback→plan→dev iteration loop) **require** this switch; `ll-iteration-intake` and `ll-iteration-plan` also work without it.
- `create_collab_session` takes `sessions[]` for batch creation, or top-level `title` + `purpose` for a single one; `group` files it into a sidebar group.

## Skills and commands

- Builtin skills (this file, `reasonix-guide`, `deep-research`, `gh-issue-submit`, `gh-issue-triage`, `ll-iteration-intake`, `ll-iteration-plan`, `ll-iteration-parallel-dev`, …) need no install; the `ll-iteration-*` trio is also materialized into your user skills dir for customization.
- Project skills: `<workspace>/.reasonix/skills/<name>/SKILL.md`.
- Commands: `<workspace>/.reasonix/commands/` or user home; `/name` in the composer.

Run `reasonix doctor capabilities --json` (or Settings → Diagnostics) for a full inventory.

## Experimental features

Settings → **实验室 (Lab)** — a grouped navigation of experimental switches (each **default off**; restart when prompted after changing a boot-time switch). Highlights:

- **跨会话通信 (session collaboration)**: master switch + collaboration hop limit + per-feature gates (delete-session / require-reply / steer …).
- **缓存大小调整 (cache tuning)**: resident tab count / transcript cache / render cache ceilings (restart to apply).
- **dream / distill**: session-trajectory mining for skill suggestions.
- **parallel full access**: trust managed worktree roots without per-path approvals.

## Where the docs live

| Topic | Path |
|---|---|
| User guide | `docs/GUIDE.md` |
| Config paths | `docs/CONFIG_PATHS.md` |
| Skills / tasks | `docs/TASK_CONTRACT.md`, `docs/TASK_CATALOG.md` |
| Collaboration modes | `docs/COLLABORATION_MODES.md` |
| Tool approval | `docs/TOOL_APPROVAL_MODES.md` |

Open the matching `.zh-CN.md` sibling for Chinese.

## First actions when the user is stuck

1. Identify the surface: desktop UI / CLI / agent tool / config.
2. For missing capabilities → `reasonix-guide`.
3. For "how do I do X" → stay here and point at the concrete path or setting.
4. For repeated tool-call failures → **Tool call troubleshooting** above, fix the call shape, then retry once.
5. Prefer pointing at shipped docs over rewriting them in chat.
