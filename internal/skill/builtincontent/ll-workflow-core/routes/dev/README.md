---
description: "功能开发、循环推进 todo、版本收口"
update_time: 2026-07-01
---

# dev 路由

> **目的**:功能开发、循环推进 todo、版本收口
> **来源技能**:`ll-develop`
> **状态**:`stable`

## 别名

`dev`, `development`, `feature-development`, `功能开发`, `持续开发`, `开发这个需求`

## 目录说明

- `templates/` — 路由产物模板(开发循环、循环工作表、证据、JSON 模板)
  - `loop-worksheet.md` — 开发循环工作表(每轮迭代模板)
  - `topic-task-template.md` — 任务包专题模板
  - `ask-questions-batch.md` — 批量提问模板
  - `completion-evidence.md` — 完成证据模板
  - `run_result.json.template` — run_result JSON 模板
  - `docs-skeleton/` — 文档骨架模板
  - `feature/` — 功能模板
- `references/` — 路由相关参考文档
  - `numbered-docs-structure.md` — 编号化 docs 结构说明
  - `run_result_schema.md` — run_result 字段规范
  - `task-package-naming.md` — 任务包命名规则
- `tools/` — 路由附带的辅助脚本
  - `init-docs-structure.ps1` — 初始化 docs 目录结构
  - `export-workflow-git-log.ps1` — 导出工作流 git 日志
  - `upgrade-run-result.mjs` — 升级 run_result 格式

## Workflow

主 workflow 路径:`../templates/workflows/dev.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `dev` 行。
