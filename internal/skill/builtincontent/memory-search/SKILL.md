---
name: memory-search
description: "Analyze session trajectories for patterns — repeated errors, tool usage, execution chains, prompt habits across many sessions. Use when the user asks what they have been working on, why a failure keeps recurring, which tools a workflow uses, or anything that needs reading Reasonix session history (jsonl/event stores) instead of a database."
runAs: inline
---

# Session memory search

Reasonix session stores are **jsonl / event logs**, not SQL: read them with grep and short scripts.

## Where

- Desktop: `%APPDATA%\reasonix\` (session store + `logs\desktop\`) — **read-only**; never write there.
- Workspace and remote copies: `<workspace>/.reasonix/sessions/`.
- One session file is one JSON object per line, and sidecars use different suffixes (`.jsonl`, `.events.jsonl`, `-recovery-*.jsonl`). Know which one you are reading before you count anything.

## How

1. Frame the question as something countable: "turns per session", "which tool errors repeat", "how long between turns".
2. Prefer `grep` plus a small Python pass over SQL or a hand-rolled parser — single sessions can be 100 MB+.
3. Templates worth adapting: count events per tool name; group failures by their summary; follow a turn ID through its tool calls; measure gaps between turns.
4. Report with the file path and the exact command you ran, and state how many sessions the conclusion covers.

## Cautions

- Trajectories contain the user's personal content: quote only what the analysis needs.
- A pattern from one session is an anecdote. Say the sample size, and check a second session before calling it a habit.
