---
description: "composite-actions 说明：复合动作的职责、依赖链与适用场景"
update_time: 2026-07-01
---
# composite-actions 说明

本文档对应：

- `.github/skills/ll-workflow-core/templates/composite-actions.yaml`

目标是说明复合动作把哪些基础动作组合在一起，以及它们适合放在什么场景下复用。

## 1. 复合动作的定位

如果说基础动作是“积木”，那么复合动作就是“常用拼装模块”。

它的价值在于：

- 减少 workflow 重复写步骤
- 固化仓库里已经稳定的多步流程
- 让新增 workflow 更快落地

## 2. 复合动作总览

| 复合动作 ID | 作用 | 典型场景 | 主要输入 | 主要输出 |
| --- | --- | --- | --- | --- |
| `flow.requirement_develop` | 创建 inactive requirement 文档 | 用户刚提出原始需求，尚未进入 todo | `userRequest`、`workspaceRoot`、`directoryProfileRef` | `requirementPath`、`openQuestions` |
| `flow.requirement_activate` | 把需求从 inactive 激活到 todo，并创建 original 副本 | 需求确认后正式进入任务包流程 | `requirementPath`、`title`、`workspaceRoot`、`directoryProfileRef` | `taskPath`、`workingReqPath`、`originalReqPath` |
| `flow.task_expand_package` | 把单文档扩充成六件套任务包 | todo 阶段需要补齐实施资产 | `taskPath`、`directoryProfileRef` | `packageFiles` |
| `flow.task_implement` | 实现、验证、收集证据并回写状态 | `dev` 或问题修复流程 | `taskPath`、`scope`、`validationPlan`、`directoryProfileRef` | `changedFiles`、`validationResult`、`evidenceBundle` |
| `flow.task_archive_done` | 验证通过后归档到 done | 任务完成归档 | `taskPath`、`archiveSlot`、`directoryProfileRef` | `archivePath`、`evidenceBundle` |
| `flow.audit_fix_cycle` | 审计发现问题后的分析→修复→验证闭环 | `aud` 的分级修复批次 | `auditScope`、`directoryProfileRef` | `reviewedFindings`、`validationResult` |
| `flow.docs_bootstrap_and_import` | 创建 docs 骨架并导入历史文档 | `init` 流程 | `targetDevDir`、`sourceDir`、`directoryProfileRef` | `bootstrapResult`、`importReport` |
| `flow.version_closeout` | 完成版本级文档同步与收口 | `dev` 后处理 / 版本收尾 | `workspaceRoot`、`validationPlan`、`directoryProfileRef` | `validationResult`、`syncedFiles` |

## 3. 每个复合动作依赖了哪些基础动作

### 3.1 `flow.requirement_develop`

依赖：

- `layout.load_directory_profile`
- `context.resolve_target_version`
- `context.load_governance_context`
- `requirement.analyze_request`
- `requirement.create_inactive_doc`
- `docs.sync_governance_entries`

适合：

- 把一段模糊自然语言诉求快速转成 inactive requirement 文档

### 3.2 `flow.requirement_activate`

依赖：

- `layout.load_directory_profile`
- `context.resolve_target_version`
- `task.resolve_identity`
- `task.activate_requirement`
- `task.freeze_original_copy`
- `state.update_task_status`
- `docs.sync_governance_entries`

适合：

- 从“需求池”进入“todo 任务包”时使用

### 3.3 `flow.task_expand_package`

依赖：

- `layout.load_directory_profile`
- `context.load_governance_context`
- `task.ensure_package_scaffold`
- `state.update_task_status`
- `docs.sync_governance_entries`

适合：

- 任务包骨架缺件时补齐六件套

### 3.4 `flow.task_implement`

依赖：

- `layout.load_directory_profile`
- `context.load_governance_context`
- `implementation.apply_changes`
- `validation.execute`
- `evidence.collect`
- `decision.record`
- `state.update_task_status`
- `docs.sync_governance_entries`

适合：

- 几乎所有“实施 + 验证 + 证据收集”类流程

### 3.5 `flow.task_archive_done`

依赖：

- `layout.load_directory_profile`
- `validation.execute`
- `evidence.collect`
- `archive.move_task_package`
- `state.update_task_status`
- `docs.sync_governance_entries`

适合：

- 从 todo 归档到 done

### 3.6 `flow.audit_fix_cycle`

依赖：

- `layout.load_directory_profile`
- `context.load_governance_context`
- `audit.scan_codebase`
- `audit.cross_review_findings`
- `implementation.apply_changes`
- `validation.execute`
- `decision.record`
- `docs.sync_governance_entries`

适合：

- 审计修复批次
- 问题排查后的小闭环修复

### 3.7 `flow.docs_bootstrap_and_import`

依赖：

- `layout.load_directory_profile`
- `docs.bootstrap_structure`
- `docs.import_documents`
- `validation.execute`
- `decision.record`

适合：

- docs 初始化与历史文档导入一体化场景

### 3.8 `flow.version_closeout`

依赖：

- `layout.load_directory_profile`
- `context.resolve_target_version`
- `validation.execute`
- `docs.sync_governance_entries`
- `decision.record`

适合：

- 版本级文档收尾
- changelog / docs 入口同步

## 4. 什么时候该新增复合动作

如果你发现多个 workflow 都在重复做：

- 相同的前置加载
- 相同的实现 + 验证 + 回写步骤
- 相同的归档或收口链路

那么通常意味着应该新增复合动作，而不是继续复制一整段 steps。

## 5. composite 自检清单（v0.2.0 起强制）

新增或修改 `templates/composite-actions.yaml` 中的任一 composite 时，必须逐项确认：

- [ ] **基本结构**：`id` 唯一、`uses` 非空、`entry_conditions` 与 `exit_conditions` 都已声明
- [ ] **action 引用解析**：`uses` 中每个 action id 都在 `templates/action-registry.yaml` 中存在
- [ ] **失败策略**：`default_on_fail` 取值合法（`block` / `continue_with_warning` / `use_default` / `use_default_or_block` / `needs_user_confirmation`）
- [ ] **互斥动作检查**：不在同一 composite 内同时调用互斥的 action 对（例如 `layout.resolve_slot` + `layout.resolve_semantic`、`docs.maintain_requirements_md` + `docs.sync_governance_entries` 等由 `usage_boundary` 声明互斥的组合）
- [ ] **`notes_required_when` 规则**：若 `uses` 列表中存在带 `usage_boundary` 且包含"内部调用/取代/合并/替代/互斥/顺序约束"关键词的 action，必须填写 `notes` 字段说明边界

**如何快速判断 `notes` 是否必须**：

```bash
cd skills/ll-workflow-core && npm run test:composite
```

如果 CI 失败，错误信息会明确指出哪个 composite 违反规则、触发了哪个动作。

## 6. 当前已触发 `notes_required_when` 的 composite 清单

| composite | 触发动作 | 必填 notes 内容 |
| --- | --- | --- |
| `flow.requirement_develop` | `docs.sync_governance_entries` | 说明该动作仅同步 README/INDEX，不动 requirements.md |
| `flow.requirement_activate` | `task.activate_requirement`、`docs.sync_governance_entries` | 说明 `task.activate_requirement` 内部调用 `task.resolve_identity`；`docs.sync_governance_entries` 范围 |
| `flow.task_expand_package` | `docs.sync_governance_entries` | 说明 requirements.md 同步留给归档阶段 |
| `flow.task_implement` | `evidence.collect`、`evidence.emit_dual_format`、`docs.sync_governance_entries` | 说明 evidence 收集→落盘顺序；`docs.sync_governance_entries` 范围 |
| `flow.task_archive_done` | `docs.maintain_requirements_md`、`docs.sync_governance_entries` | 说明 `docs.maintain_requirements_md` 默认 mode；二者互斥边界 |
| `flow.audit_fix_cycle` | `evidence.collect`、`evidence.emit_dual_format`、`docs.sync_governance_entries` | 同 task_implement |
| `flow.version_closeout` | `docs.maintain_requirements_md`、`docs.sync_governance_entries` | 说明 `docs.maintain_requirements_md` 取代原 upgrade + sync 双调用 |

> 清单由 `tests/composite-actions.test.js` 自动生成；新增触发型 composite 后请同步更新本表。

## 7. 一句总结

`composite-actions.yaml` 负责沉淀“已经稳定的多步流程模块”，它是从基础动作到完整 workflow 之间的中间层。
新增或修改 composite 时，请对照**第 5 节自检清单**逐项确认，避免 CI 失败。
