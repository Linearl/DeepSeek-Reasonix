---
description: "该骨架用于在目标工作目录（`targetDevDir`）下快速初始化 `docs` 结构，"
update_time: 2026-07-01
---

# docs 目录骨架（编号化结构参考）

该骨架用于在目标工作目录（`targetDevDir`）下快速初始化 `docs` 结构，
对应 `ll-workflow-core` 当前默认采用的编号化 docs 体系。

## 目标结构（最小）

- `docs/0.version_plan/plan/{version}`
- `docs/1.task/{A.inactive,B.todo,B.2.done,template}`
- `docs/2.tech/{architecture,api,guide,development,template}`
- `docs/3.analysis/{evidence,diagnosis,report}`
- `docs/4.misc/`
- `docs/5.git-commit/`
- `docs/6.changelog/`
- `docs/7.weekly/`
- `docs/8.test/{case,scripts,template}`

## 与工作流的关系

当 A.1 识别到目标目录缺少 docs 结构时：

1. 执行 `./tools/init-docs-structure.ps1` 初始化目录。
2. 生成并要求先补齐以下文档：
   - `docs/0.version_plan/plan/{version}/dev_{version}_plan.md`
   - `docs/1.task/template/topic-task-template.md`
3. 用户确认“版本计划 + 至少 1 个专题任务文档”后，才进入 A.2。

## 备注

- 目录骨架通过 `.gitkeep` 保持可追踪。
- `1.task/B.2.done/{version}` 与 `0.version_plan/plan/{version}` 会由初始化脚本根据版本自动补齐。
