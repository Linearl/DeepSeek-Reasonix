---
name: feedback_analysis
description: "Analyze the local Reasonix feedback inbox markdown files under the Reasonix feedback-inbox directory: classify by category, cluster repeated pain points, and write analysis-YYYYMMDD.md with a short priority list. Use when the user asks to summarize, review, or triage product feedback collected by submit_feedback."
runAs: inline
---

# Feedback analysis

Read every `feedback-*.md` under the feedback inbox (default `ReasonixHome/feedback-inbox`, env `REASONIX_FEEDBACK_INBOX`). Ignore `analysis-*.md` when counting inputs.

## How

1. List notes; parse frontmatter `category` / `tags` / `at` and the `# title` + body.
2. Tally by category (`bug` / `idea` / `praise` / `other`) and by tag frequency.
3. Cluster similar text into pain-point groups (same UI surface, same failure mode). Count each cluster; quote 1–2 short excerpts.
4. Write **one** file: `analysis-YYYYMMDD.md` in the same inbox directory with:
   - TL;DR counts
   - Pain-point clusters (sorted by count, then recency)
   - Suggested next actions (top 3–5, concrete)
5. Do not delete or rewrite the source notes.

## Stop rule

If the inbox is empty, say so and stop — do not invent analysis.
