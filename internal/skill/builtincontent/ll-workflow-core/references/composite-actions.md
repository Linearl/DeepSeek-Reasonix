---
description: "`ll-workflow-core` 作为统一入口时，推荐使用四个工作流简称："
update_time: 2026-07-01
---

# 复合动作摘要

## 统一入口简称

`ll-workflow-core` 作为统一入口时，推荐使用四个工作流简称：

- `init`：初始化 / docs bootstrap / 导入旧文档
- `req`：需求开发 / 澄清 / requirements 产物
- `dev`：功能开发 / 持续开发循环
- `aud`：项目审计 / 审计修复

## 标准复合动作

- `flow.requirement_develop`
  - 需求开发：原始诉求 → `inactive requirement` 语义目录中的需求文档
- `flow.requirement_activate`
  - 激活：`inactive requirement` 语义目录 → `todo` 语义目录中的任务包
- `flow.task_expand_package`
  - 扩充：把单需求扩成六件套任务包
- `flow.task_implement`
  - 实施：改动、验证、证据、回写
- `flow.task_archive_done`
  - 归档：验证通过后迁到 `done` 语义目录，并同步 requirements.md
- `flow.audit_fix_cycle`
  - 审计修复闭环：分析 → 修复 → 验证 → 总结
- `flow.version_closeout`
  - 版本收口：增强 run_result.json → 同步 requirements.md → changelog → 总结

## 建议用法

1. 用户心智接近“阶段”的操作，优先做成复合动作。
2. workflow 主干优先引用复合动作，保持可读性。
3. 当某个阶段需要特殊处理时，再在 workflow 内部局部展开基础动作或 `custom.described_action`。
4. 循环、条件和错误策略属于 workflow 层，不属于复合动作内部硬编码。
5. 目录路径解析属于 layout 配置层，也不属于复合动作内部硬编码。

## 复合动作与简称 workflow 的关系

建议把四个简称理解为“入口级 workflow”，而不是简单等于四个复合动作：

- `init` workflow：可能包含 `flow.docs_bootstrap_and_import`，也可能附带冲突处理与验证步骤
- `req` workflow：可能串联 `flow.requirement_develop`、`flow.task_expand_package` 以及多轮澄清循环
- `dev` workflow：可能串联 `flow.task_implement`、门控问答、版本收口
- `aud` workflow：可能串联 `flow.audit_fix_cycle`、多轮复核、跳过记录与最终验证

所以：

- 复合动作是“中层能力块”
- `init/req/dev/aud` 是“面向用户激活的入口 workflow”
