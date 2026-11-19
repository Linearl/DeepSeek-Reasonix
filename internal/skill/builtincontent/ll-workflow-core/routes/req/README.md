---
description: "需求澄清、requirements/design/tasks 产物生成"
update_time: 2026-07-01
---

# req 路由

> **目的**:需求澄清、requirements/design/tasks 产物生成
> **来源技能**:`ll-requirements`
> **状态**:`stable`

## 别名

`req`, `requirement`, `requirements`, `requirement-development`, `需求开发`, `需求澄清`

## 目录说明

- `templates/` — 路由产物模板(需求、决策、门控、run_result 模板)
  - `REQ-template.md` — 需求文档模板
  - `README-template.md` — 任务专题 README 模板
  - `gate-questions-template.md` — 澄清门控问题模板
  - `design-template.md` — 设计文档模板
  - `decision_log-template.md` — 决策日志模板
  - `run_result.json-schema.md` — run_result 字段规范
  - `run_result.json.template` — run_result JSON 模板
- `config/` — 路由专属配置
  - `run_result.schema.json` — run_result JSON Schema

## Workflow

主 workflow 路径:`../templates/workflows/req.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `req` 行。
