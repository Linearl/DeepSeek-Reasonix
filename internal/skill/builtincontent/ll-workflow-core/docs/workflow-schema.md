---
description: "workflow-schema 说明：顶层字段、路由契约、步骤类型、失败策略与风格约束"
update_time: 2026-07-01
---
# workflow-schema 说明

本文档对应：

- `.github/skills/ll-workflow-core/templates/workflow.schema.yaml`

目标是说明 `ll-workflow-core` 的 workflow YAML 应该长什么样、支持哪些步骤类型、有哪些风格约束。

## 1. schema 的定位

这个 schema 目前更偏“结构约定 + 可读规范”，不只是给机器验证，也给人类和 AI 提供统一的编排语言。

它解决的问题是：

- workflow 顶层应该有哪些字段
- route 相关信息该写在哪
- `branch / while / for_each / repeat_until` 应该怎么写
- workflow 里哪些实践是推荐的，哪些是反模式

## 2. 顶层字段

| 顶层字段 | 是否必需 | 作用 |
| --- | --- | --- |
| `workflow` | 是 | workflow 的主体定义 |
| `imports` | 否 | 引用动作注册表、复合动作注册表、目录配置、route registry、resource bundles |

### `workflow` 下的核心字段

| 字段 | 作用 |
| --- | --- |
| `id` | workflow 唯一标识 |
| `short_name` | route 简称，例如 `init` / `req` / `dev` / `aud` |
| `name` | 面向人类的名称 |
| `description` | 对流程主线的简要说明 |
| `owner_skill` | 当前 workflow 归属的技能 |
| `activation_aliases` | 用户可能直接说出的别名 |
| `activation_hint` | 用户未显式给简称时，用于意图路由 |
| `directory_profile_ref` | 指向 directory profile 的引用 |
| `mode_support` | 该 workflow 支持的运行模式 |
| `defaults` | 默认失败策略、轮次限制、状态字段等 |
| `context` | 输入、输出、变量、layout、loadedBundles |
| `phases` | 分阶段的流程主体 |

## 3. imports 支持什么

当前允许的 imports 字段包括：

- `action_registry`
- `composite_registry`
- `schema`
- `directory_layout`
- `route_registry`
- `resource_bundles`

推荐：

- 正式 route workflow 都同时引入 `directory_layout`、`route_registry`、`resource_bundles`

## 4. routing_contract（路由契约）

| 字段 | 作用 |
| --- | --- |
| `short_name` | route 的标准简称 |
| `activation_aliases` | 显式关键词或别名 |
| `activation_hint` | 意图提示说明 |
| `directory_profile_ref` | 默认使用哪个目录 profile |
| `resource_bundles` | workflow 依赖哪些 bundle |
| `source_skill` | 若来自历史技能迁移，用于追溯来源 |

## 5. context_contract（上下文契约）

| 字段 | 作用 |
| --- | --- |
| `inputs` | workflow 需要的外部输入 |
| `outputs` | workflow 最终产出 |
| `variables` | 运行时中间变量 |
| `layout` | 由 directory profile 解析出的 slot / semantic 映射 |
| `loadedBundles` | 由 resource bundle 装载出的资源清单 |

## 6. 支持的步骤类型

### 6.1 `action`

最常见的单步动作。

必需字段：

- `id`
- `title`
- `action`

常见可选字段：

- `description`
- `with`
- `save_as`
- `when`
- `on_fail`
- `evidence_policy`

### 6.2 `composite`

用于调用复合动作。

必需字段：

- `id`
- `title`
- `uses`

可选字段：

- `with`
- `save_as`
- `when`
- `on_fail`
- `notes`（v0.2.0 起在某些场景下强制，详见下节）

#### 6.2.1 `notes_required_when` 条件必填（v0.2.0 起）

当 `composite.uses` 列表中的某个 action 在 `action-registry.yaml` 中存在
`usage_boundary` 字段，且 boundary 文本匹配以下任一关键词时，
**composite 必须填写非空 `notes` 字段**：

| 关键词 | 含义 | 必须说明的内容 |
| --- | --- | --- |
| `内部调用` | 该 action 内部链式调用其他动作 | 说明被调用的内部动作，避免下游重复声明 |
| `取代` / `合并` / `替代` | 该 action 收敛了多个旧动作 | 列出被收敛的旧动作清单 |
| `互斥` | 该 action 与另一个 action 互斥 | 说明互斥对象与边界 |
| `顺序约束` | 该 action 必须在另一个 action 之前/之后执行 | 说明执行顺序 |

**为什么需要这个规则**？

- 防止 composite 重复声明已经在某个 action 内部完成的工作（例如 `task.activate_requirement` 已经内部调用 `task.resolve_identity`，composite 不应再并列声明两者）
- 防止 composite 误用互斥动作（例如同时调用 `docs.maintain_requirements_md` 与 `docs.sync_governance_entries`，二者职责严格分离）
- 防止 composite 违反顺序约束（例如先 `evidence.emit_dual_format` 再 `evidence.collect` 是反序）

**验证来源**：通过 `tests/composite-actions.test.js` 自动验证，规则触发即失败。

**示例**：

```yaml
- id: flow.task_archive_done
  uses:
    - docs.maintain_requirements_md  # 触发条件：usage_boundary 含"严格互斥"
    - docs.sync_governance_entries
  notes:
    # docs.maintain_requirements_md 默认 mode=upgrade_then_sync 即可一次性完成元数据升级 + 清单同步
```

### 6.3 `branch`

条件分支。

必需字段：

- `id`
- `type: branch`
- `if`
- `then`

可选字段：

- `else`

### 6.4 `switch`

多分支选择。

必需字段：

- `id`
- `type: switch`
- `expression`
- `cases`

可选字段：

- `default`

### 6.5 `for_each`

遍历数组类上下文。

必需字段：

- `id`
- `type: for_each`
- `items`
- `item_as`
- `steps`

可选字段：

- `index_as`
- `when`
- `on_fail`

### 6.6 `while`

条件循环。

必需字段：

- `id`
- `type: while`
- `condition`
- `steps`

可选字段：

- `max_iterations`
- `on_fail`

### 6.7 `repeat_until`

先执行、后判断是否结束的循环。

必需字段：

- `id`
- `type: repeat_until`
- `until`
- `steps`

可选字段：

- `max_iterations`
- `on_fail`

## 7. 失败策略（on_fail_policy）

允许值包括：

| 策略 | 含义 |
| --- | --- |
| `block` | 阻断流程 |
| `continue_with_warning` | 带警告继续 |
| `use_default` | 使用默认策略继续 |
| `retry_once` | 重试一次 |
| `needs_user_confirmation` | 需要用户确认 |
| `stop_current_branch` | 仅终止当前分支 |

## 8. style_guide（风格约束）

当前 schema 中已经把一些平台约束写成了 style guide，重点包括：

- workflow 应优先通过 route registry 被发现，而不是只靠人工记忆文件名
- workflow 应尽量声明 resource bundles
- workflow 主干优先引用复合动作，局部细节再下钻基础动作
- 新增但不稳定的动作优先用 `custom.described_action`
- workflow 在首次使用目录前必须先加载 directory profile
- `todo / inactive / done / changelog` 等目录都应通过 `layout` 引用
- 复杂 workflow 必须显式写出分支与循环，不要把控制逻辑藏进大段描述里

## 9. 什么时候改 schema

适合改 schema 的场景：

- 新增了一种稳定可复用的步骤类型
- 新增了 route 级别的元数据字段
- 新增了 context 中必须被 workflow 理解的契约字段
- 平台想强制一种新的编排约束

不适合改 schema 的场景：

- 只是某个 workflow 私有的小需求
- 只是某个 route 临时试点
- 只是某一条动作的个别输入字段变化

## 10. 一句总结

`workflow.schema.yaml` 负责定义 `ll-workflow-core` 的“流程语言”，它约束的不是某一条 route，而是整个 workflow 平台应该如何表达自己。
