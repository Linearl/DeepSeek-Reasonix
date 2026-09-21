---
name: reasonix-fork-guide
description: "Explain how to use this Reasonix fork day-to-day: worktree parallel development, session collaboration modes, experimental features, skills/commands, and where to find docs. Use when the user asks how to use the fork, where a feature lives, or how to set up a parallel workflow. Distinct from reasonix-guide (capability self-diagnostics)."
runAs: inline
---

# Reasonix fork user guide

A **usage** guide for this fork. For capability loading / doctor diagnostics, use `reasonix-guide` instead.

## When to use which guide

| Need | Skill |
|---|---|
| How do I run parallel work / worktrees / sessions? | **this guide** |
| Why is my skill / command / MCP missing? | `reasonix-guide` |

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
4. Prefer pointing at shipped docs over rewriting them in chat.
