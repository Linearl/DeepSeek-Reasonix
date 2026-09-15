# deep-research

Parallel multi-source research with a single-point write-up.

## When to use
User asks for a thorough multi-source investigation / deep research on a topic that needs cited synthesis.

## How
1. Split the question into 3–6 independent sub-questions.
2. Dispatch sub-agents (or background jobs) to search/fetch in parallel — research is parallel, writing is single-point.
3. Collect source notes with URL + claim; discard claims without a source.
4. Write ONE report: TL;DR → findings by theme → citations → open questions.
5. Save under `issues/reports/调研报告【非诊断】/` or `report/` with `<主题>-调研-YYYYMMDD.md`.

## Notes for Reasonix
- Use `task` / fleet-style parallel work when available; otherwise sequential fetch is fine.
- Prefer WebSearch/WebFetch then local `read_file`/`grep` on cloned sources.
- Never invent citations.
