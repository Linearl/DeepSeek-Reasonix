---
name: gh-issue-submit
description: "Submit a high-quality GitHub issue: discover the repository's issue templates, search open AND closed duplicates, research the source to locate a root cause (file:line) with tiered fix suggestions, fill the template, and verify the submission. Use when the user wants to file a bug report or feature request against a GitHub repository."
runAs: inline
---

# gh-issue-submit

Pipeline for filing a GitHub issue the maintainers can act on. Prefer evidence over speculation.

## Preconditions

1. Confirm `gh` is authenticated (`gh auth status`). If not, ask the user to run `gh auth login` — do not invent tokens.
2. Confirm the target repository (`owner/repo`). If the user only names a product, resolve it from the current git remote or ask.

## Steps

### 1. Discover templates

```bash
gh api repos/{owner}/{repo}/contents/.github/ISSUE_TEMPLATE --jq '.[].name' 2>/dev/null || true
```

- If templates exist, pick the one matching the request (bug vs feature) and follow its sections.
- If none exist, use the standard sections below.

### 2. Search duplicates (open AND closed)

```bash
gh search issues --repo {owner}/{repo} --limit 20 --json number,title,state,url -- \
  "<keywords from the problem>"
gh search issues --repo {owner}/{repo} --state closed --limit 10 --json number,title,state,url -- \
  "<keywords>"
```

- Read closed issues too: maintainers often leave a workaround or a "will fix in X" comment.
- If a duplicate exists, **do not file a new issue**. Summarize the existing one and ask the user whether to comment instead.

### 3. Source research (when the repo is open source)

If the user wants root-cause depth:

1. Clone or use an existing local checkout.
2. Grep for the failing symbol / error string.
3. Record **file:line** evidence.
4. Propose 1–3 fix tiers: quick workaround / targeted patch / architectural.

Never invent line numbers. If you cannot find the code, say so and skip this section.

### 4. Draft the body

Standard sections when no template exists:

```
## Summary
One or two sentences.

## Steps to reproduce
1. …

## Expected
…

## Actual
…

## Environment
- OS / version
- Product version
- Relevant config (redact secrets)

## Root cause (if researched)
- file:line + explanation
- Suggested fix tiers
```

### 5. Submit and verify

```bash
gh issue create --repo {owner}/{repo} --title "<title>" --body-file /tmp/issue.md
gh issue view <number> --repo {owner}/{repo} --json url,title,state
```

Report the issue URL back to the user.

## Hard rules

- Never submit without the user seeing the draft title + body first (show, then ask to confirm).
- Never include secrets, tokens, or absolute home paths that identify the user's machine beyond necessity.
- Closed-issue duplicates are still duplicates.
