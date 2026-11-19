---
description: "action-registry 说明：基础动作分类、输入输出、失败策略与使用建议"
update_time: 2026-07-01
---
# action-registry 说明

本文档对应：

- `.github/skills/ll-workflow-core/templates/action-registry.yaml`

目标是把基础动作按类别讲清楚，便于 route 设计者快速判断“该复用哪个动作”。

## 1. 读取方法

建议按下面的顺序看基础动作：

1. **先看类别**：这能快速判断动作属于路由、目录、任务、验证还是审计
2. **再看输入/输出**：决定它能不能直接串进当前 workflow
3. **最后看默认失败策略**：决定是否适合放在关键路径上

## 2. 基础动作一览（表格）

### 2.1 路由与资源装载

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `route.resolve_workflow` | 根据 route registry 解析当前用户请求对应的 workflow | `routeRegistryRef`、`userRequest`、`explicitRoute` | `route`、`workflowPath`、`directoryProfileRef`、`resourceBundles` | `needs_user_confirmation` |
| `resource.load_bundle` | 根据 resource bundle registry 装载模板、脚本、配置与参考文档路径 | `resourceBundlesRef`、`bundleIds` | `loadedBundles`、`includedPaths` | `block` |

### 2.2 目录抽象与路径解析

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `layout.load_directory_profile` | 读取 directory profile，解析 slot 与 semantic | `directoryProfileRef`、`workspaceRoot`、`targetDevDir`、`version` | `layout`、`slots`、`semantics` | `block` |
| `layout.resolve_slot` | 把逻辑槽位解析成具体路径 | `layout`、`slot` | `resolvedPath` | `block` |
| `layout.resolve_semantic` | 把语义目录名解析成对应路径与槽位 | `layout`、`semanticName` | `resolvedPath`、`slotName` | `block` |

### 2.3 上下文与治理加载

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `context.resolve_target_version` | 解析用户显式目录或自动识别最高 `dev_xx` | `userTarget`、`workspaceRoot` | `targetDevDir`、`version` | `needs_user_confirmation` |
| `context.load_governance_context` | 读取 docs 规范、任务规则与仓库治理约束 | `targetDevDir`、`layout` | `docRules`、`repoRules` | `block` |
| `context.scan_todo_topics` | 扫描 todo 语义槽位下的待办专题并给出摘要 | `layout`、`semanticName` | `todoTopics`、`recommendedTopics`、`progressSummary` | `block` |

### 2.4 交互与门控

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `interaction.ask_questions_gate` | 发起结构化门控问答，并把结果写入上下文 | `questionSet`、`mode`、`recommendedOptions` | `answers`、`gateResult` | `use_default_or_block` |

### 2.5 需求与任务身份

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `task.resolve_identity` | 生成或确认 `taskId`、任务目录名与任务包路径 | `reqId`、`title`、`version`、`layout` | `taskId`、`taskFolder`、`taskPath` | `block` |
| `requirement.analyze_request` | 把自然语言诉求转为结构化需求要点与未决问题 | `userRequest`、`context` | `requirementDraft`、`openQuestions` | `block` |
| `requirement.create_inactive_doc` | 在 inactive requirement 语义目录创建需求文档 | `requirementDraft`、`versionHint`、`layout` | `requirementPath` | `block` |
| `task.activate_requirement` | 把需求从 inactive 激活到 todo 并生成任务包路径 | `requirementPath`、`taskFolder`、`layout` | `taskPath`、`workingReqPath` | `block` |
| `task.freeze_original_copy` | 创建 `-original` 冻结副本 | `workingReqPath` | `originalReqPath` | `block` |
| `task.ensure_package_scaffold` | 补齐 README / REQ / tasks / design / decision_log 等任务包文件 | `taskPath`、`layout`、`templateRefs` | `packageFiles` | `block` |
| `state.update_task_status` | 写任务状态、阶段状态和原因 | `taskPath`、`newStatus`、`reason`、`layout` | `statusRecord` | `block` |
| `decision.record` | 记录关键决策、默认选择或跳过原因 | `decisionPoint`、`choice`、`reason` | `decisionEntry` | `continue_with_warning` |

### 2.6 实施、验证与证据

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `implementation.apply_changes` | 在受控范围内完成代码或文档修改 | `taskPath`、`scope`、`constraints`、`layout` | `changedFiles`、`implementationNotes` | `block` |
| `validation.execute` | 执行测试、规则检查或人工验证 | `validationPlan`、`changedFiles` | `validationResult`、`evidence` | `block` |
| `evidence.collect` | 汇总日志、截图、链接与校验产物 | `validationResult`、`artifacts` | `evidenceBundle` | `continue_with_warning` |

### 2.7 文档与归档

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `docs.sync_governance_entries` | 同步 requirements / README / INDEX / changelog 等治理入口 | `layout`、`syncProfile`、`taskPath`、`statusRecord` | `syncedFiles` | `block` |
| `archive.move_task_package` | 把任务包迁到 done 或其他生命周期目录 | `taskPath`、`layout`、`archiveSlot` | `archivePath` | `block` |
| `docs.bootstrap_structure` | 创建 docs 骨架与模板目录 | `layout`、`targetSlot`、`bootstrapMode` | `bootstrapResult` | `block` |
| `docs.import_documents` | 把历史文档导入目标 docs，并输出导入报告 | `sourceDir`、`layout`、`targetSlot`、`importStrategy` | `importReport`、`unresolvedMappings` | `block` |

### 2.8 审计专用

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `audit.scan_codebase` | 根据范围执行代码审计扫描 | `auditScope`、`fileSet` | `findings`、`codebaseMetrics` | `block` |
| `audit.cross_review_findings` | 多轮交叉复核 findings，减少误报与行号偏差 | `findings`、`reviewRounds` | `reviewedFindings`、`reviewSummary` | `block` |

### 2.9 通用占位动作

| Action ID | 作用 | 核心输入 | 主要输出 | 默认失败策略 |
| --- | --- | --- | --- | --- |
| `custom.described_action` | 当暂时没有合适标准动作时，用描述驱动方式先试运行 | `description`、`expected_outputs`、`success_criteria`、`constraints`、`layout` | `result`、`evidence` | `block` |

## 3. 使用建议

### 3.1 优先使用正式基础动作

如果一个动作已经在注册表中存在，优先直接复用，不要重复写 `custom.described_action`。

### 3.2 什么时候用 `custom.described_action`

适用场景：

- 新流程刚起步，还没形成稳定动作抽象
- 动作逻辑只在一个 workflow 出现一次
- 还在探索阶段，不适合立刻沉淀为正式动作

### 3.3 什么时候把自定义动作提升为正式动作

满足以下任一条件时，建议升级：

- 两个及以上 workflow 出现类似描述
- 输入/输出已经稳定
- 已经能说清楚 preconditions / side_effects / evidence_policy

## 4. 一句总结

`action-registry.yaml` 负责定义“平台能稳定复用的最小动作单元”，它是 workflow 编排的基础积木库。
