---
description: "ll-workflow-core 新增流程（route）与平台扩展指南"
update_time: 2026-07-01
---
# ll-workflow-core 扩展指南

本文档说明如何把 `ll-workflow-core` 从“当前 4 条路由”继续扩展为可持续演进的平台。

## 1. 扩展目标

新增流程时，不推荐再新建一个平行的 `ll-*` 技能；优先应在 `ll-workflow-core` 内新增一个 **route**。

推荐的扩展单元：

- 一个 route 元数据条目
- 一个 workflow 文件
- 一组 route 资源目录（如有需要）
- 一个或多个 resource bundle
- 必要时一个新的 directory profile

## 2. 最小扩展步骤

### 步骤 1：复制 workflow 模板

复制：

- `templates/workflows/route-template.workflow.yaml`

改名为：

- `templates/workflows/<short_name>.workflow.yaml`

例如：

- `ops.workflow.yaml`
- `mig.workflow.yaml`
- `sync.workflow.yaml`

### 步骤 2：注册 route

编辑：

- `config/route-registry.yaml`

至少补充：

- `id`
- `short_name`
- `workflow`
- `purpose`
- `aliases`
- `directory_profile_ref`
- `resource_bundles`

### 步骤 3：声明资源包

如果该 route 需要专属模板、脚本或配置，优先在：

- `routes/<short_name>/`

下创建资源目录，然后在：

- `config/resource-bundles.yaml`

中声明新的 bundle。

### 步骤 4：必要时补目录 profile

如果新 route 面向的仓库目录布局与默认 profile 差异较大，优先在：

- `config/directory-layout.yaml`

中新增 profile，而不是直接把新目录硬编码进 workflow。

## 3. 推荐目录模式

### 最小模式

- `templates/workflows/<short_name>.workflow.yaml`
- `config/route-registry.yaml`

适用于：

- 轻量流程
- 主要复用已有 bundle
- 不需要专属模板或脚本

### 标准模式

- `templates/workflows/<short_name>.workflow.yaml`
- `routes/<short_name>/templates/`
- `routes/<short_name>/references/` 或 `docs/` 或 `config/` 或 `scripts/`
- `config/resource-bundles.yaml` 中新增 `route.<short_name>.assets`

适用于：

- 有自己的一组模板
- 有自己的说明文档或脚本
- 未来可能继续扩展

## 4. 什么时候该新增基础动作

如果新 route 中某个 `custom.described_action` 满足以下条件，建议升级为正式动作：

- 在两个及以上 route 中重复出现
- 输入输出已经稳定
- 可以清楚描述 preconditions / side_effects / evidence_policy

否则先继续保持为 `custom.described_action`，不要过早抽象。

## 5. 什么时候该新增 shared bundle

如果某些模板 / 脚本 / 文档被多个 route 共用，建议从 `route.<short_name>.assets` 提升到：

- `shared.*`

例如：

- `shared.requirement-core`
- `shared.validation-core`
- `shared.closeout-core`

## 6. 什么时候该新增 directory profile

出现以下情况时，优先新增 profile：

- `docs` 目录仍存在，但 task / analysis / changelog 分区与当前仓库不同
- 项目是轻量仓库，没有完整 numbered docs 结构
- 不同 route 面向不同类型仓库，需要不同的目录语义映射

## 7. 一条简单判断线

如果你发现自己正准备：

- 在 workflow 里手写很多路径
- 在 SKILL.md 里硬编码第 5 条流程的规则
- 在多个 route 中复制同一段模板路径列表

那通常意味着应该改的是：

- `directory-layout.yaml`
- `route-registry.yaml`
- `resource-bundles.yaml`

而不是继续堆更多硬编码。
