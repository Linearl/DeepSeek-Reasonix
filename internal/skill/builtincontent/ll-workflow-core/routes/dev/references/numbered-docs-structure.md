---
description: "dev route 使用的编号化 docs 结构参考，用于初始化脚本与目录门禁说明"
update_time: 2026-07-01
---
# 编号化 docs 结构说明（dev route 参考）

本文档定义 dev route 当前默认采用的编号化 docs 结构，供
`routes/dev/tools/init-docs-structure.ps1` 与相关门禁模板参考。

## 目标顶层结构

- `docs/0.version_plan/`
- `docs/1.task/`
- `docs/2.tech/`
- `docs/3.analysis/`
- `docs/4.misc/`
- `docs/5.git-commit/`
- `docs/6.changelog/`
- `docs/7.weekly/`
- `docs/8.test/`

## 关键子结构（最小必需）

### 0.version_plan

- `0.version_plan/plan/{version}/`
- `0.version_plan/template/`

### 1.task

- `1.task/A.inactive/`
- `1.task/B.todo/{version}/`
- `1.task/B.2.done/{version}/`
- `1.task/template/`
- `1.task/requirements.md`

### 2.tech

- `2.tech/architecture/`
- `2.tech/api/`
- `2.tech/guide/`
- `2.tech/development/`
- `2.tech/template/`

### 3.analysis

- `3.analysis/evidence/`
- `3.analysis/diagnosis/`
- `3.analysis/report/`

### 4.misc

- `4.misc/references/`

## 与 dev route 阶段映射

- A.1.2：检查并初始化上述目录结构。
- A.2：从 `docs/1.task/B.todo/{version}` 筛选当前版本任务。
- A.3：将完成任务归档到 `docs/1.task/B.2.done/{version}/{task-id}`。
- C.1：版本收口时同步 `docs/6.changelog/`。
- C.3：总结建议或阶段结论可沉淀到 `docs/3.analysis/report/` 或
  `docs/3.analysis/evidence/`。

## 初始化脚本

使用 `./tools/init-docs-structure.ps1`：

- 作用：创建目录骨架、补齐版本目录、落地版本计划模板与专题模板。
- 关键产物（示例）：
  - `docs/0.version_plan/plan/{version}/dev_{version}_plan.md`
  - `docs/1.task/template/topic-task-template.md`

如需覆盖已存在模板，请使用 `-Force`。

## requirements.md 同步工具

`1.task/requirements.md` 是需求全量清单的 SSOT，由以下工具自动维护：

### req-sync.js（templates/scripts/req-sync.js）

通过扫描 `1.task/B.2.done`、`B.todo`、`B.1.paused`、`B.3.cancelled`、`A.inactive` 下所有任务包的 `run_result.json` 和 `README.md`，自动同步 `requirements.md` 的第 3 章（全量清单）和第 4 章（按状态看板）。

```bash
# 预览同步结果（dry-run 默认开启）
pnpm llwf:req-sync

# 实际写入
pnpm llwf:req-sync -- --apply

# 查询可用编号
node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js next --category 7

# 查询编号详情
node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js query <编号>

# 为缺失 run_result.json 的任务包批量生成
node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js backfill --apply
```

### upgrade-run-result.mjs

增强已有 `run_result.json` 的元数据质量（中文标题、来源层、备注、日期）。

```bash
# 预览
node scripts/upgrade-run-result.mjs --dry-run

# 全量应用
node scripts/upgrade-run-result.mjs --apply

# 指定版本
node scripts/upgrade-run-result.mjs --apply --dirs v09
```

### 工作流集成

在 dev route 的阶段 B.3（文档归档）和 C.1（版本收口）中，建议执行：

1. `upgrade-run-result.mjs --apply` 提升 run_result.json 质量
2. `pnpm llwf:req-sync -- --apply` 同步 requirements.md
3. 验证 sync 结果无异常差异
