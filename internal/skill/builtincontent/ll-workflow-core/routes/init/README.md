---
description: "初始化 worktree pool 追踪 + 初始化 docs 骨架、导入历史文档、重建目录结构"
update_time: 2026-07-01
---

# init 路由

> **目的**:初始化 worktree pool 追踪 + 初始化 docs 骨架、导入历史文档、重建目录结构
> **来源技能**:`ll-workflow-core`
> **状态**:`stable`

## 别名

`init`, `initialize`, `bootstrap`, `docs-init`, `初始化`, `导入旧文档`, `worktree-init`, `初始化 worktree`, `初始化 worktree 池`

## 目录说明

- `templates/` — 路由产物模板(目录骨架、导入映射、docs-tree)
  - `docs-tree.md` — 初始化时生成的 docs/ 目录树模板
  - `files/` — 模板文件库(各类型文档的初始内容)
- `references/` — 路由相关参考文档
  - `import-decision-gates.md` — 旧文档导入决策门控

## Workflow

主 workflow 路径:`../templates/workflows/init.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `init` 行。
