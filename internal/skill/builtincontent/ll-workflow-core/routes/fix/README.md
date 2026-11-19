---
description: "fix 审计修复子工作流路由说明"
update_time: 2026-07-01
---
# fix — 审计修复

> 简称：`fix` | 状态：experimental | 来源：ll-audit

## 概述

读取审计任务包（`{YYYYMMDD}_audit-fix/`），按 P1→P4 严重性优先级逐级修复审计发现的问题。

## 与 aud 的关系

| 工作流 | 职责 | 产出 |
|--------|------|------|
| `aud` | 扫描 → 报告 → 复核（纯分析） | 诊断报告（`3.analysis/diagnosis/`）+ 审计包（`1.task/B.todo/`） |
| `fix` | 读取审计包 → 逐级修复 → 验证 → 归档 | 修复后的审计包（`1.task/B.2.done/`）+ run_result |

## 输入

- 审计包路径：`docs/1.task/B.todo/{version}/{YYYYMMDD}_audit-fix/`
- 若未指定，自动查找当前版本下最新的审计包

## 阶段

```
A0. 读取审计包 ── 定位审计包，提取问题清单
A1. 分支策略   ── 确认是否启用修复分支
B0. 逐级修复   ── P1→P4 逐级处理（修复/跳过）
C0. 结果验证   ── 测试 + 合并闸门
C1. 汇总归档   ── 更新 README + 技术债台账 + 归档
```

## 审计包目录结构

```text
{YYYYMMDD}_audit-fix/
├── README.md           ← 主报告（含修复状态）
├── decision_log.md     ← 决策留痕
├── effective_config.yaml
├── P1/                 ← 每项一个子目录
│   └── P1-001-xxx/
│       ├── 问题描述.md
│       ├── 解决方案.md
│       └── 实施总结.md
├── P2/
├── P3/
├── P4/
└── skipped/
    ├── skip_log.md
    └── debt_backlog.md
```

## 模板

修复阶段复用 `routes/aud/templates/` 下的模板：

- `问题描述_template.md`
- `解决方案_template.md`
- `实施总结_template.md`
- `decision_log_template.md`
- `debt_backlog_template.md`

## 提交规范

```text
fix(P{优先级}-{序号}): {简短描述}
```

示例：`fix(P1-001): remove backend/.env from git, add .env.example`

## 归档条件

- 所有 P1 问题已修复或明确跳过
- C0 测试通过
- run_result.json 已生成

满足后从 `B.todo/{version}/` 移动到 `B.2.done/{version}/`。
