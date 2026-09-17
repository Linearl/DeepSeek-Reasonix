---
name: collect_issues
description: "Collect the local Reasonix feedback inbox (AI意见箱) into the task list: read every unprocessed feedback-*.md, analyze and classify each note, create or extend tasks in docs/tasklist (with a fresh task id), then rename each processed file with a 已转入tasklist suffix. Use when the user asks to 收意见箱 / collect issues / 盘点反馈 / 把意见转入任务清单."
runAs: inline
---

# Collect issues (意见箱 → tasklist)

Turn the local feedback inbox into tracked tasks. One pass = read → analyze → create tasks → mark files processed. The feedback inbox lives at `<ReasonixHome>/feedback-inbox` (usually `%APPDATA%\reasonix\feedback-inbox`); the task list lives at `docs/tasklist/` inside the current workspace (inner git repo — commit there).

## Step 1 — Read

1. List `feedback-*.md` in the inbox directory. **Skip** any file whose name already ends with `-已转入tasklist.md` (already processed) and any `analysis-*.md` (analysis output, not input).
2. Read each remaining note in full: frontmatter (`at` / `category` / `tags`) plus the `# title` and body.

## Step 2 — Analyze

For each note decide:

- **Domain**: which tasklist domain file fits (`02-待办-UI与交互` / `03-待办-上游跟进与适配` / `04-待办-会话协作与远程` / `05-待办-恢复与副本` / `06-待办-执行引擎与上下文`). When a note spans domains, pick the one carrying the primary fix.
- **Novelty**: is this a NEW gap, or already covered by an existing task / recent commit? Check the tasklist and, when the note cites a package version, whether a later package already fixed it. Mark each finding 已修待实测 vs 新缺口 — never re-file something already fixed without saying so.
- **Severity**: 致命阻塞 / 高频摩擦 / 体验改进 / 观察项.

## Step 3 — Create tasks

1. Get the next task id: `python docs/tasklist/scripts/next-task-id.py` (run from the workspace root).
2. **One task per theme** (not per note): cluster notes about the same subsystem into one task with lettered sub-items (157.A/B/C…). Cite the source inbox filename in every sub-item.
3. Append to the chosen domain file: title line `## 任务 <N>（🟡 立项 <YYYY-MM-DD>，来源=AI意见箱）<主题>`, then for each finding: 现象（实测证据）/ 根因（已核实 or 待查）/ 修复方向 / 验收 checkbox.
4. Update the notes' source attribution: 已修的部分标注 commit 与包版本，避免后续重复立项。
5. Commit the tasklist inner repo: `git add <domain files> && git commit -m "tl: 任务 <N> 立项（来源=AI意见箱 <slug>…）"`.

## Step 4 — Mark files processed

1. Rename each processed note in place: `feedback-YYYYMMDD-<slug>.md` → `feedback-YYYYMMDD-<slug>-已转入tasklist.md`.
2. Keep the rename strictly one-to-one (never merge or delete notes).

## Step 5 — Report

Report per note: task id(s) created or extended, domain file, one-line disposition (已修待实测 / 新缺口 / 并入既有任务). If the inbox is empty or everything is already processed, say so and stop.

## Rules

- Never delete feedback notes; renaming is the only mutation.
- One feedback note may map to several sub-items, but every finding cites its source file.
- If a finding contradicts an existing task's 已修 claim, flag it in the task body as 待复核 instead of silently overwriting.
