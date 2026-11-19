---
description: "templates/workflows 说明：各 route workflow 与新增流程模板的职责、输入输出与阶段结构"
update_time: 2026-07-01
---
# workflows 说明

本文档对应：

- `.github/skills/ll-workflow-core/templates/workflows/`

目标是说明每个 workflow 文件负责什么、适合什么场景、主要阶段如何组织。

## 1. workflow 文件总览

| 文件 | short_name | 来源 | 主要用途 | 资源包 |
| --- | --- | --- | --- | --- |
| `init.workflow.yaml` | `init` | `ll-bootstrap` | 初始化 docs 骨架、导入历史文档、做导入校验与总结 | `shared.core`、`route.init.assets`、`shared.docs.common` |
| `req.workflow.yaml` | `req` | `ll-requirements` | 从原始需求到 requirements/design/tasks/run_result 的完整需求开发流程 | `shared.core`、`route.req.assets`、`shared.docs.common` |
| `dev.workflow.yaml` | `dev` | `ll-develop` | 扫描 todo、循环推进单个任务、做版本收口 | `shared.core`、`route.dev.assets`、`shared.docs.common` |
| `aud.workflow.yaml` | `aud` | `ll-audit` | 选择审计范围、扫描、复核、分级修复与收口 | `shared.core`、`route.aud.assets`、`shared.docs.common` |
| `route-template.workflow.yaml` | `example` | 模板文件 | 用于新增第 N 条 route 的起手模板，不是正式生产 route | `shared.core`、`shared.docs.common` |

## 2. 共同结构

所有正式 workflow 当前都遵循同一套共识：

- 引用 `action-registry.yaml`
- 引用 `composite-actions.yaml`
- 引用 `workflow.schema.yaml`
- 引用 `directory-layout.yaml`
- 引用 `workflow-governance.yaml`
- 引用 `route-registry.yaml`
- 引用 `resource-bundles.yaml`
- 显式声明：
  - `short_name`
  - `directory_profile_ref`
  - `resource_bundles`
  - `source_skill`

这意味着它们都是“可注册、可发现、可扩展”的 route 单元。

补充：`req / dev / aud` 还共享一套治理约束，用于统一状态一致性、交接工单、审计回流、质量门、双格式证据与经验回灌。

## 3. 各 workflow 分阶段说明

### 3.1 `init.workflow.yaml`

| 阶段 | 作用 | 核心动作 |
| --- | --- | --- |
| `a_prepare_context` | 解析版本并加载目录配置 | `context.resolve_target_version`、`layout.load_directory_profile` |
| `a_bootstrap` | 初始化 docs 结构 | `docs.bootstrap_structure`、`decision.record` |
| `b_import_mapping` | 分类来源文档、处理无法自动归类的文档、执行导入 | `custom.described_action`、`interaction.ask_questions_gate`、`docs.import_documents` |
| `c_validation` | 导入后校验并生成总结 | `validation.execute`、`custom.described_action` |

适用场景：

- 新建 docs 体系
- 历史文档导入
- 目录重构后的重新收纳

### 3.2 `req.workflow.yaml`

| 阶段 | 作用 | 核心动作 |
| --- | --- | --- |
| `p0_input_intake` | 建立版本、目录与治理上下文，生成初始需求草稿 | `context.resolve_target_version`、`context.load_governance_context`、`requirement.analyze_request` |
| `p1_clarify_and_resolve_conflicts` | 通过门控问答循环消解未决项 | `interaction.ask_questions_gate`、`custom.described_action`、`decision.record` |
| `p2_write_requirements` | 建立任务身份与任务包骨架，写入 requirements | `task.resolve_identity`、`flow.task_expand_package`、`custom.described_action` |
| `p3_write_design` | 写入 design | `custom.described_action` |
| `p4_write_tasks` | 生成 tasks | `custom.described_action` |
| `p5_finalize` | 同步治理入口、写状态、校验状态一致性、输出 `run_result` 与证据 | `docs.sync_governance_entries`、`state.update_task_status`、`governance.validate_state_sync`、`evidence.emit_dual_format`、`custom.described_action` |

适用场景：

- 需求澄清
- 需求资产生成
- 下游 `dev` 前的结构化准备

### 3.3 `dev.workflow.yaml`

| 阶段 | 作用 | 核心动作 |
| --- | --- | --- |
| `a_preprocess` | 建立版本、目录与 todo 快照，必要时补 docs 骨架 | `context.scan_todo_topics`、`docs.bootstrap_structure` |
| `b_execution_loop` | 反复选择任务、执行任务、门控确认、刷新待办清单 | `custom.described_action`、`interaction.ask_questions_gate`、`governance.validate_state_sync`、`flow.task_implement` |
| `c_closeout` | 做版本级收口与总结 | `flow.version_closeout`、`custom.described_action` |

适用场景：

- 持续开发循环
- todo 推进
- 版本收口前的连续执行

### 3.4 `aud.workflow.yaml`

| 阶段 | 作用 | 核心动作 |
| --- | --- | --- |
| `a_prepare_context` | 解析版本、目录与治理上下文 | `context.resolve_target_version`、`layout.load_directory_profile` |
| `a0_scope_selection` | 确认全量/增量审计策略 | `switch`、`interaction.ask_questions_gate` |
| `a1_scan_and_review` | 扫描代码并做多轮复核 | `audit.scan_codebase`、`audit.cross_review_findings`、`repeat_until` |
| `b_fix_by_priority` | 按 P1→P4 批次修复或跳过 | `custom.described_action`、`for_each`、`flow.audit_fix_cycle` |
| `c_closeout` | 最终验证、同步入口、执行回流三处回写校验、输出审计结果 | `validation.execute`、`docs.sync_governance_entries`、`audit.verify_backflow`、`evidence.emit_dual_format`、`custom.described_action` |

适用场景：

- 全量或增量审计
- 审计问题修复闭环
- 发布前风险检查

### 3.5 `route-template.workflow.yaml`

该文件是新增 route 的标准起点，已经预置了：

- route 解析
- version 解析
- directory profile 加载
- resource bundle 装载
- 一个占位业务阶段
- 一个最小收口阶段

它适合用于：

- 未来新增 `ops` / `mig` / `sync` / `report` 等新 route
- 先快速起一个试点流程，再逐步抽象动作

## 4. 如何选择 route-template 而不是复制旧 workflow

优先用 `route-template.workflow.yaml` 的场景：

- 新流程语义明显不同于 `init/req/dev/aud`
- 不想继承旧流程里过多历史包袱
- 希望 route 一开始就按 route registry / resource bundle / directory profile 体系接入

优先复制现有 workflow 的场景：

- 只是做 `req` 或 `aud` 的变体
- 阶段结构与现有流程高度相似
- 主要变化只是多一个门控或多一个批处理阶段

## 5. 维护建议

- 若 workflow 新增了通用阶段，优先考虑提升为复合动作，而不是让多个 workflow 手抄相同步骤。
- 若 workflow 中开始散写很多模板路径，通常意味着应该补 `resource-bundles.yaml`。
- 若 workflow 中开始散写很多 docs 目录路径，通常意味着应该补 `directory-layout.yaml`。
