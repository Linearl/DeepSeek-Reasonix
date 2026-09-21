---
name: gh-issue-triage
description: "Triage and close the loop on issues in your own GitHub repository: collect open issues, classify, fix, verify with tests/commits, and close only with evidence. Use when the user asks to manage, fix, or clean up issues on a repository they maintain."
runAs: inline
---

# gh-issue-triage

Closed-loop issue management for maintainers. Collect → classify → fix → verify → close.
**Closing requires evidence.** Never close silently.

## Preconditions

1. `gh` authenticated and the user has write access to `owner/repo`.
2. Confirm scope: all open issues, a label, a milestone, or an explicit number list.

## Steps

### 1. Collect

```bash
gh issue list --repo {owner}/{repo} --state open --limit 50 \
  --json number,title,labels,updatedAt,assignees,author
```

Group by:

| Bucket | Rule of thumb |
|---|---|
| **bug** | Reproducible defect, crash, wrong result |
| **question** | Needs a reply, not a code change |
| **stale** | No activity ≥ 90 days and no assignee |
| **duplicate** | Same root cause as another open issue |
| **wontfix / invalid** | Out of scope or unreproducible |

### 2. Classify and report

Produce a short table: number | title | bucket | next action. Ask the user which bucket to process first.

### 3. Fix (for bugs)

1. Create a branch from the default branch.
2. Reproduce first; add a failing test when possible.
3. Implement the minimal fix.
4. Run the project's test suite.

### 4. Verify

Required before any close:

- [ ] Fix commit is **pushed** to the remote.
- [ ] Tests for the fix are green locally (cite the command).
- [ ] Linked in the issue comment (`Fixes #N` in the PR or a comment with the commit SHA).

### 5. Close with evidence

```bash
gh issue comment <number> --repo {owner}/{repo} --body "Fixed in <sha>. Tests: <command> passed."
gh issue close <number> --repo {owner}/{repo} --reason completed
```

For wontfix/invalid/question:

```bash
gh issue comment <number> --repo {owner}/{repo} --body "<reason>"
gh issue close <number> --repo {owner}/{repo} --reason "not planned"
```

## Hard gates

| Action | Must have |
|---|---|
| Close as completed | Pushed fix commit + green tests + linked SHA |
| Close as not planned | Written reason comment first |
| Close duplicates | Comment pointing at the canonical issue |
| Bulk close | Explicit user confirmation of the number list |

Never bulk-close without listing every issue number to the user first.
