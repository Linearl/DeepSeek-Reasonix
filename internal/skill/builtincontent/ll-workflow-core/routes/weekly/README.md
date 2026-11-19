---
name: route.weekly
description: 项目周报生成路由 — 收集 git log + docs 数据源，生成或追加周报到 docs/7.weekly/
route: wk
aliases: [wk, weekly, weekly-report, project-weekly, 周报, 写周报, 项目周报]
---

# wk 路由 — 项目周报生成

## 何时使用

当用户提出以下诉求时触发：

- 写周报 / 生成周报 / 项目周报
- weekly report / project weekly report
- wk / weekly

## 能力边界

1. 确定周报周期（当前周 or 上周，由 mode 控制）。
2. 从 git log 提取本周提交记录。
3. 从 docs/ 各数据源自动提炼本周完成内容。
4. 生成或追加周报到 `docs/7.weekly/YYYY-W{ISO周号}-weekly.md`。
5. 生成"本周迭代对用户的影响"段落。
6. 生成"经验回灌"段落。

## 配置

`config/report-config.json`:

- `mode`: `"current_week"`（默认，当周实时追加）或 `"last_week"`（上周回顾，创建新文件）

## AI 执行流程

### 步骤 1：确定周报周期

```
mode = "current_week":
  periodStart = 本周一
  periodEnd   = 本周日
  如果 docs/7.weekly/YYYY-W{ISO周号}-weekly.md 已存在 → 追加模式
  否则 → 创建新文件

mode = "last_week":
  periodStart = 上周一
  periodEnd   = 上周日
  始终创建新文件
```

### 步骤 2：收集数据源

| 数据源 | 路径 | 提取内容 |
|--------|------|----------|
| Git 提交 | `git log --since="{periodStart}" --until="{periodEnd}" --oneline` | 本周提交摘要 |
| 已完成任务包 | `docs/1.task/B.2.done/{version}/` | 本周新增归档目录的 REQ/README |
| 待办状态变更 | `docs/1.task/B.todo/{version}/` | run_result.json status 变化 |
| 发布说明 | `docs/6.changelog/releases/release-notes.md` | 最新条目 |
| 版本计划 | `docs/0.version_plan/v0.2-plan.md` | 进度更新 |
| 架构知识库 | `docs/1.task/B.todo/{version}/v0.2-architecture-knowledge.md` | 新增决策 |

### 步骤 3：生成周报内容

模板包含以下章节（详见 `templates/weekly-report-template.md`）：

1. **本周完成** — 按功能域分组 + 项目符号列表
2. **问题与风险** — 问题/影响/处理
3. **KPI 指标** — 目标 vs 实际
4. **经验回灌** — 从 decision_log / tasks 提取教训
5. **下周展望** — 后续里程碑
6. **本周迭代对用户的影响** — 面向产品用户的自然语言描述

### 步骤 4：生成"用户影响"段落

- 面向用户，不面向开发者
- 从 REQ 文档的"目标与范围"和"验收标准"提取用户价值
- 参考 release-notes 的行文风格：一段总述 + 项目符号列表
- 覆盖所有用户接触点

### 步骤 5：生成"经验回灌"段落

从 decision_log.md 和 tasks.md 中提取：
- 关键决策理由
- 踩过的坑和解决方式
- 隐藏约束
- 每条一句话，`- **标题**：说明` 格式

### 步骤 6：落盘

- 文件命名：`docs/7.weekly/YYYY-W{ISO周号}-weekly.md`
- 追加模式：在文件末尾追加，不覆盖已有内容
- 更新 front matter 的 `updated` 日期

## 注意事项

- mode 默认 `current_week`。用户说"写上周周报"时切换 `last_week`。
- 周报面向用户和项目管理者，中文优先。
- 不在正文中出现实现细节术语（除经验回灌章节）。
- 如果本周无新进展，标注"本周无新进展"。
