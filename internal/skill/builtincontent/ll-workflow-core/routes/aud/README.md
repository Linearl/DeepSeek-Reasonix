---
description: "项目审计（纯分析）。扫描 → 报告 → 复核 → 输出诊断报告。不承担修复职责，修复由 fix 工作流承接。支持 12 维全栈诊断清单。"
update_time: 2026-07-01
---

# aud 路由

> **目的**:项目审计（纯分析）。扫描 → 报告 → 复核 → 输出诊断报告。不承担修复职责，修复由 fix 工作流承接。支持 12 维全栈诊断清单。
> **来源技能**:`ll-audit`
> **状态**:`stable`

## 变体

| 变体 | short_name | executor | workflow |
|------|-----------|----------|----------|
| aud-cc | `aud-cc` | `cc_workflow` | `../templates/cc-workflows/audit-parallel-v2.js` |
| aud-trad | `aud-trad` | `traditional_yaml` | `../templates/workflows/aud.workflow.yaml` |

## 别名

`aud`, `audit`, `project-audit`, `code-audit`, `项目审计`, `代码审计`, `代码健康诊断`

## 目录说明

- `config/` — 路由专属配置
- `docs/` — 路由文档(扩展点说明、调用示例)
- `scripts/` — 路由附带的辅助脚本
- `templates/` — 路由产物模板

## Workflow

主 workflow 路径:`../templates/workflows/aud.workflow.yaml`

## 触发词

见 SKILL.md 「工作流简称(激活入口)」段的 `aud` 行。
