# memory-search

Search and analyze session trajectory files for patterns (Reasonix jsonl flavor).

## When to use
User wants historical behavior analysis: repeated errors, tool usage patterns, execution chains across sessions.

## How
1. Locate session stores under `%APPDATA%\reasonix\` (read-only!) or workspace session copies.
2. Prefer grep / small Python scripts over SQL — Reasonix sessions are **jsonl/events**, not SQLite.
3. Query templates (adapt paths):
   - Repeated tool errors: grep `error` / `recovery_required` in events + count by tool name.
   - Execution chain: extract user → assistant text → tool calls order for a turn.
   - Frequency: group by day / project from session filenames.
4. Output a short findings table + 3–5 actionable patterns.

## Notes for Reasonix
- **Do not write** under `%APPDATA%\reasonix\`.
- For durable facts, write a report under `report/` or `issues/reports/`, not memory dirs.
