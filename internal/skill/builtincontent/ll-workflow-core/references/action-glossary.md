---
description: "本文件是 `templates/action-registry.yaml` 的阅读版摘要，便于先看语义再看字段。"
update_time: 2026-07-01
---

# 基础动作摘要

本文件是 `templates/action-registry.yaml` 的阅读版摘要，便于先看语义再看字段。

## 统一前提：先加载目录结构配置

`ll-workflow-core` 中凡是涉及目录、文件落位、治理入口同步的动作，默认都应先读取：

- `config/directory-layout.yaml`

换句话说：

- `todo` 是哪个目录
- `inactive requirement` 是哪个目录
- `done` 是哪个目录
- `docs index / changelog / guide` 落在哪个目录

这些都应通过 layout 配置解析，而不是在动作里写死。

## 基础动作分层

### 布局类

- `layout.load_directory_profile`：加载目录结构配置并解析逻辑槽位
- `layout.resolve_slot`：按槽位名取具体路径

### 上下文类

- `context.resolve_target_version`：识别用户指定目录或当前最高 `dev_xx`
- `context.load_governance_context`：读取 docs 规范、任务规则、技能约束
- `context.scan_todo_topics`：扫描当前版本待办专题并输出候选任务

### 交互与决策类

- `interaction.ask_questions_gate`：按门控模板向用户发起问答并记录答案
- `decision.record`：记录关键决策、默认策略或回退理由
- `state.update_task_status`：回写任务状态、阶段状态或执行结果

### 需求/任务包类

- `requirement.analyze_request`
- `requirement.create_inactive_doc`
- `task.resolve_identity`
- `task.activate_requirement`
- `task.freeze_original_copy`
- `task.ensure_package_scaffold`

### 实施/验证类

- `implementation.apply_changes`
- `validation.execute`
- `evidence.collect`
- `docs.sync_governance_entries`（仅同步 INDEX/README，不再处理 requirements.md）
- `docs.maintain_requirements_md`（合并原 upgrade_run_results + sync_requirements_md，按 mode 选择）
- `archive.move_task_package`

### 审计 / docs 导入类

- `audit.scan_codebase`
- `audit.cross_review_findings`
- `docs.bootstrap_structure`
- `docs.import_documents`

### 实验类

- `custom.described_action`
  - 用于“先试运行、后标准化”的动作占位。
  - 必须提供足够具体的 `description`，AI 按描述执行。
  - 一旦重复出现，应提炼成正式基础动作并替换掉它。

## 目录抽象后的理解方式

更新后建议这样理解动作：

- 动作负责“做什么”
- layout 配置负责“做在什么目录里”

例如：

- `context.scan_todo_topics` 不再默认等于“扫某个写死的 docs 子目录”
- 它现在等于“扫 layout 中 `todo` 语义映射对应的目录”

这样一来，仓库目录结构变化时，只需要改 `directory-layout.yaml`，而不是把每个 workflow 改一遍。
