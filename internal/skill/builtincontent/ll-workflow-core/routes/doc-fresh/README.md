---
description: "文档保鲜审计——扫描过期技术文档、多模型子代理并行审计、生成修正报告。默认走传统 YAML 工作流（依赖 Python 扫描器 check-doc-freshness.py），CC Workflow 变体走 df-cc。"
update_time: 2026-07-01
---

# df 路由

> **目的**:文档保鲜审计——扫描过期技术文档、多模型子代理并行审计、生成修正报告。默认走传统 YAML 工作流（依赖 Python 扫描器 check-doc-freshness.py），CC Workflow 变体走 df-cc。
> **来源技能**:`doc-freshness-audit`
> **状态**:`stable`

## 变体

| 变体 | short_name | executor | workflow |
|------|-----------|----------|----------|
| df-cc | `df-cc` | `cc_workflow` | `../templates/cc-workflows/doc-fresh-parallel.js` |
| df-trad | `df-trad` | `traditional_yaml` | `../templates/workflows/doc-fresh.workflow.yaml` |

## 别名

`df`, `doc-fresh`, `doc-freshness`, `doc-freshness-audit`, `文档保鲜`, `过期文档`, `文档审计`, `文档新鲜度`, `检查文档新鲜度`

## 目录说明

- `config/` — 路由专属配置
- `docs/` — 路由文档(扩展点说明、调用示例)
- `scripts/` — 路由附带的辅助脚本
- `templates/` — 路由产物模板

## Workflow

主 workflow 路径:`../templates/workflows/doc-fresh.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `df` 行。
