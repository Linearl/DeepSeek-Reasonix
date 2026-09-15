---
name: deep-research
description: "Run a thorough multi-source investigation that ends in ONE written conclusion: split the question into independent sub-questions, gather sources in parallel, keep only claims that carry a citation, then write a single report. Use when the user asks for deep research, a literature or comparison survey, or a cited synthesis that spans many sources."
runAs: inline
---

# Deep research

Parallel gathering, single-point writing: research is fan-out, the report is one voice.

## How

1. Split the question into 3-6 **independent** sub-questions that can be answered without each other.
2. Gather in **parallel** — dispatch sub-agents or background jobs per sub-question; never serialize what is independent. Keep the fan-out small: 3-6 beats a dozen thin ones.
3. Keep a source note beside every claim: URL (or file path) + one-line claim. A claim with no source is dropped, not softened.
4. Cross-check the two or three load-bearing claims against a second source, and say which you verified twice.
5. Write **ONE** report: TL;DR → findings grouped by theme → citations → open questions. Do not narrate the search.

## Stop rule

Stop when new sources only repeat what you already have, or when the marginal source changes no conclusion. Say plainly what is still thin instead of padding it.
