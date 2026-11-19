---
description: "ll-workflow-core 文档索引：入口说明、目录结构、workflow、动作与 schema 说明"
update_time: 2026-07-01
---
# ll-workflow-core 文档索引

## 推荐阅读顺序

1. [`../README.md`](../README.md)
   - 先看仓库级总览，理解它为什么可以作为独立 skill / 独立仓库存在。
1. [`structure.md`](./structure.md)
   - 先看目录结构，理解 `config / templates / routes / docs / references` 分工。
2. [`workflows.md`](./workflows.md)
   - 再看 route workflow，理解 `init / req / dev / aud` 分别负责什么。
3. [`action-registry.md`](./action-registry.md)
   - 需要补动作或编排 workflow 时看基础动作清单。
4. [`composite-actions.md`](./composite-actions.md)
   - 需要减少 workflow 重复步骤时看复合动作。
5. [`workflow-schema.md`](./workflow-schema.md)
   - 需要新增步骤类型、顶层字段或 route 元数据时看 schema。
6. [`EXTENDING.md`](./EXTENDING.md)
   - 需要新增第 5 条、第 6 条流程时看扩展方法。
7. [`PUBLISHING.md`](./PUBLISHING.md)
   - 需要把本技能抽成独立 GitHub 仓库时看发布步骤与检查清单。

## 当前平台能力

- 统一入口：`init / req / dev / aud`
- 统一目录抽象：`config/directory-layout.yaml`
- 统一 route 注册：`config/route-registry.yaml`
- 统一资源包注册：`config/resource-bundles.yaml`
- 通用 docs 模板池：`templates/docs/`
- 新 route 起手模板：`templates/workflows/route-template.workflow.yaml`

## 文档分工

| 文档 | 主要回答的问题 |
| --- | --- |
| `structure.md` | 每个目录和子目录分别做什么？ |
| `workflows.md` | 各个 workflow 的阶段结构、输入输出和适用场景是什么？ |
| `action-registry.md` | 有哪些基础动作可复用？输入输出是什么？ |
| `composite-actions.md` | 有哪些多步复合流程已经稳定沉淀？ |
| `workflow-schema.md` | workflow YAML 该怎么写？支持哪些 step type？ |
| `EXTENDING.md` | 如何新增 route / bundle / profile / workflow？ |
| `PUBLISHING.md` | 如何把当前目录直接发布成独立 GitHub 仓库？ |

## 维护建议

- 改目录布局时优先修改 `directory-layout.yaml`
- 新增流程时优先注册 route，而不是再建一个平行 `ll-*` 技能
- 需要复用的模板或脚本优先提升为 shared bundle
- 复杂 workflow 优先拆成复合动作，不要一直把步骤堆在单个 YAML 中
