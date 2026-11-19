---
description: "持久化 worktree 池分配 slot → 开发任务包 → 提交。3 slot 全满时回退 ephemeral。并行开发入口。"
update_time: 2026-07-01
---

# branch 路由

> **目的**:持久化 worktree 池分配 slot → 开发任务包 → 提交。3 slot 全满时回退 ephemeral。并行开发入口。
> **来源技能**:`ll-workflow-core`
> **状态**:`experimental`

## 别名

`branch`, `parallel-dev`, `worktree`, `并行开发`, `分支开发`, `创建分支`, `worktree 开发`

## 目录说明

- `templates/` — 路由产物模板(当前仅含 README 占位)
  - `README.md` — 模板目录说明,具体模板待补

## Workflow

主 workflow 路径:`../templates/workflows/branch.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `branch` 行。
