---
description: "ll-workflow-core 目录结构说明：各目录与子目录的职责边界"
update_time: 2026-07-01
---
# ll-workflow-core 目录结构说明

本文档说明 `.github/skills/ll-workflow-core` 下各个目录、子目录与关键文件分别负责什么。

## 1. 顶层目录总览

| 路径 | 作用 | 何时修改 |
| --- | --- | --- |
| `SKILL.md` | 技能入口说明，定义何时触发、如何路由、有哪些核心资源 | 调整技能定位、路由入口、主文档索引时 |
| `config/` | 平台级配置：目录 profile、route 注册表、resource bundle 注册表 | 扩展流程、切换目录布局、整理资源依赖时 |
| `docs/` | 人类可读说明文档：目录职责、workflow 说明、动作说明、schema 说明、扩展指南 | 结构变化或新增能力后 |
| `references/` | 轻量级参考资料，偏摘要型、速查型，不承担完整手册职责 | 需要保留简短速查材料时 |
| `routes/` | 每条 route 的专属资源区，存放模板、脚本、配置、参考文档 | 某条 route 需要专属资源时 |
| `templates/` | 平台可复用模板与 YAML 定义，包括动作注册表、复合动作、workflow schema、workflow 文件、docs 模板 | 定义或调整平台模板与 YAML 协议时 |

## 2. `config/` 目录

| 文件 | 作用 | 说明 |
| --- | --- | --- |
| `directory-layout.yaml` | 定义目录 profile、slot、semantic、sync target | 解决“workflow 不应硬编码目录结构”的问题 |
| `route-registry.yaml` | 定义 route 元数据、别名、workflow 路径、profile 绑定、resource bundle 依赖 | 解决“未来新增第 5 条流程如何接入”的问题 |
| `resource-bundles.yaml` | 定义共享资源包与 route 专属资源包 | 解决“workflow 正文不应散写模板路径”的问题 |

### 2.1 `directory-layout.yaml`

它是目录抽象的单一事实来源（SSOT），负责回答：

- docs 根目录在哪里
- todo / inactive / done 分别映射到哪里
- requirements / README / INDEX 等治理入口怎么同步
- 不同仓库布局该选哪个 profile

### 2.2 `route-registry.yaml`

它是流程入口的单一注册处，负责回答：

- 当前有哪些 route
- 用户说了某个关键词时该进哪条 workflow
- 每条 workflow 依赖什么 profile 和 bundle
- 哪条 route 处于 `stable` 或 `experimental`

### 2.3 `resource-bundles.yaml`

它把模板、脚本、配置、参考文档聚合成 bundle。workflow 只声明 bundle ID，而不应在正文里罗列大量相对路径。

## 3. `docs/` 目录

这是 `ll-workflow-core` 的正式手册区，建议优先从这里阅读：

| 文件 | 作用 |
| --- | --- |
| `README.md` | 文档索引，告诉使用者先看哪份说明 |
| `structure.md` | 说明整个技能目录结构 |
| `workflows.md` | 说明 `templates/workflows/` 下的 workflow |
| `action-registry.md` | 说明基础动作注册表 |
| `composite-actions.md` | 说明复合动作注册表 |
| `workflow-schema.md` | 说明 workflow schema |
| `EXTENDING.md` | 说明如何新增 route / bundle / profile / workflow |

## 4. `references/` 目录

该目录保留轻量级参考材料，适合：

- 快速浏览动作名称
- 快速浏览复合动作名称
- 为正文文档提供简短摘要入口

如果内容已经需要系统性解释，应优先放到 `docs/`，而不是继续堆到 `references/`。

## 5. `routes/` 目录

`routes/` 用于放每条 route 的专属资源，而不是公共模板。

### 当前已有 route 目录

| 路径 | 来源 | 主要内容 |
| --- | --- | --- |
| `routes/init/` | `ll-bootstrap` | docs bootstrap 模板、导入参考 |
| `routes/req/` | `ll-requirements` | requirements/design/tasks 模板、schema |
| `routes/dev/` | `ll-develop` | 开发循环模板、tools、reference |
| `routes/aud/` | `ll-audit` | 审计模板、执行说明、脚本、配置 |

### route 目录的设计原则

- **专属资源放 route 下**：只服务单条流程的模板、脚本、配置应放到 `routes/<short_name>/`
- **共享资源提升为 bundle**：两个及以上 route 会用到的资源，应提升到 shared bundle
- **route 目录不负责注册**：真正的入口注册仍应在 `config/route-registry.yaml`

## 6. `templates/` 目录

`templates/` 是平台层模板区，不等于“某个 route 的私有模板区”。

### 6.1 平台级 YAML

| 文件 | 作用 |
| --- | --- |
| `action-registry.yaml` | 基础动作注册表 |
| `composite-actions.yaml` | 复合动作注册表 |
| `workflow.schema.yaml` | workflow 的结构约定与语法说明 |

### 6.2 `templates/workflows/`

该目录存放每条 route 的 workflow 定义文件。

当前包括：

- `init.workflow.yaml`
- `req.workflow.yaml`
- `dev.workflow.yaml`
- `aud.workflow.yaml`
- `route-template.workflow.yaml`

其中 `route-template.workflow.yaml` 不是运行中的正式 route，而是新增流程时的模板起点。

### 6.3 `templates/docs/`

该目录存放通用 docs 模板镜像。它最初来自一套经过验证的编号化
docs 结构样本，但现在已经被抽象成可跨流程、跨仓库复用的模板池。

当前分区包括：

- `0.version_plan/`
- `2.tech/`
- `3.analysis/report/`
- `4.misc/`
- `5.git-commit/`
- `6.changelog/`
- `7.weekly/`
- `8.test/`

## 7. 修改时的优先级建议

如果你要做的是：

- **新增流程入口** → 先改 `config/route-registry.yaml`
- **新增共享模板/脚本集合** → 先改 `config/resource-bundles.yaml`
- **适配不同仓库目录结构** → 先改 `config/directory-layout.yaml`
- **补平台说明** → 改 `docs/`
- **补 route 私有资源** → 改 `routes/<short_name>/`
- **新增或修改 workflow 定义** → 改 `templates/workflows/`

## 8. 一句总结

`ll-workflow-core` 的目录设计目标是：

- `config/` 负责注册与抽象
- `templates/` 负责平台模板
- `routes/` 负责流程私有资源
- `docs/` 负责可读说明
- `SKILL.md` 负责入口行为
