---
description: "harness-audit 路由资源 — AI 配置审计工作流"
update_time: 2026-07-01
---
# Harness-Audit 路由

本目录包含 harness-audit 子工作流的资源文件，原为独立技能 `harness-audit`，现已整合到 ll-workflow-core 中。

## 工作流概述

**用途**：审计 AI 辅助配置的正确性、完备性、同步性。

**五种配置介质**：
1. CLAUDE.md — AI 行为约束
2. settings.json — 工具与权限
3. Linter — 代码风格守护
4. CI — 自动化守卫
5. Git Hooks — 提交门槛

**简称**：`haud`

**简称**：`haud`

**简称**：`haud`

**触发条件**：
- 用户提到配置审计、CLAUDE.md、settings.json、agent config、配置同步

## 目录结构

```
routes/harness-audit/
├── README.md                              # 本文件
└── reference/
    ├── README.md                          # 参考资源说明
    ├── capability-gate.mjs                # 能力门控脚本
    ├── diff-gate.mjs                      # 差异门控脚本
    ├── scan-duplicate-definitions.mjs     # 重复定义扫描脚本
    ├── validate-capability-map.mjs        # 能力地图验证脚本
    ├── 参考文章-五种介质方法论.md           # 五种介质方法论详解
    └── config/
        └── diff-gate-config.json          # 差异门控配置
```

## 工作流阶段

| 阶段 | 名称 | 说明 |
|------|------|------|
| A | 配置文件发现 | 扫描项目中的 AI 配置文件 |
| B | 配置质量评估 | 评估每个配置介质的质量 |
| C | 配置同步性检查 | 检查不同配置介质之间的一致性 |
| D | 反模式检测 | 检测配置中常见的反模式和安全风险 |
| E | 健康评分 | 基于评估结果计算健康评分 |
| F | 改进建议生成 | 生成具体的配置改进建议和行动项 |

## 配置介质权重

| 介质 | 权重 | 说明 |
|------|------|------|
| CLAUDE.md | 40% | AI 行为约束，最关键 |
| settings.json | 20% | 工具与权限配置 |
| Linter | 15% | 代码风格守护 |
| CI | 15% | 自动化守卫 |
| Git Hooks | 10% | 提交门槛 |

**注意**：安全问题一票否决。

## 参考资源

### 五种介质方法论

详细方法论参见：[`reference/参考文章-五种介质方法论.md`](./reference/参考文章-五种介质方法论.md)

### 脚本工具

- `capability-gate.mjs`：能力门控，检查配置是否满足最低要求
- `diff-gate.mjs`：差异门控，检测配置文件之间的不一致
- `scan-duplicate-definitions.mjs`：扫描重复的配置定义
- `validate-capability-map.mjs`：验证能力地图的完整性

## 模板文件

审计报告模板位于 `templates/audit-report.md`，包含：
- 健康评分摘要
- 配置介质评估详情
- 同步性检查结果
- 反模式检测结果
- 改进建议（快速 + 战略）

## 与其他路由的关系

- **与 aud 路由**：harness-audit 专注于 AI 配置审计，aud 路由专注于代码审计
- **与 health-check 路由**：harness-audit 可作为 health-check 的一个评估维度
- **独立使用**：可直接通过 `harness-audit` 或 `配置审计` 触发

## 迁移说明

本路由从独立技能 `harness-audit` 迁移而来，保留了：
- 完整的六阶段工作流
- 五种配置介质评估方法论
- 参考脚本和配置文件
- 方法论参考文章

原始技能已归档至 `.claude/skills/_archived/harness-audit/`。
