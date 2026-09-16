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

- Multiple desktop instances on one session surface a concurrent-writer notice; content is kept as a separate version (**View versions**).
- Autopilot approval tier: `[agent] approval_tier = guardian | parent | human`.

## Skills and commands

- Builtin skills (this file, `reasonix-guide`, `deep-research`, …) need no install.
- Project skills: `<workspace>/.reasonix/skills/<name>/SKILL.md`.
- Commands: `<workspace>/.reasonix/commands/` or user home; `/name` in the composer.

Run `reasonix doctor capabilities --json` (or Settings → Diagnostics) for a full inventory.

## Experimental features

Settings → Experimental. Each switch is **default off**. After changing a boot-time switch, restart when prompted.

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
