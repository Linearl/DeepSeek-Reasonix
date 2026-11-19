---
description: "ll-workflow-core 独立仓库总览，说明定位、目录结构、安装方式与发布入口"
update_time: 2026-07-01
---
# ll-workflow-core

`ll-workflow-core` 是一个面向 GitHub Copilot / agent 的统一工作流技能。

它把原本分散在多个 `ll-*` 技能里的高频入口，收敛为一套可扩展的
workflow 平台，当前内置 **11 条路由**：

- `init`：初始化 docs 骨架、导入历史材料、整理目录布局
- `req`：需求澄清、requirements/design/tasks 产物生成
- `dev`：开发循环、任务推进、版本收口
- `aud`：审计、分级修复、验证与收口（双变体：`aud-cc` / `aud-trad`）
- `fix`：审计修复、读取审计包、逐级修复、归档
- `branch`：并行开发、worktree 分支创建、独立开发
- `merge`：合并分支、链式验证、worktree 清理
- `hchk`（health-check）：项目健康检查、五维评估、债务模式识别
- `haud`（harness-audit）：AI 配置审计、五种介质同步性检查
- `gt`（gen-test）：并行生成后端服务层单元测试（CC Workflow 优先）
- `df`（doc-fresh）：文档保鲜审计、过期文档扫描、多模型并行审计（CC Workflow 优先）

这个仓库既可以直接作为 skill 安装目录使用，也适合作为独立 GitHub 仓库维护。

## 核心价值

- **统一入口**：用户只需要记住 `ll-workflow-core` 与 11 条路由的 `short_name`。
- **统一治理**：`req / dev / aud` 共用一套状态一致性、交接工单、审计回流、
  质量门、双格式证据与经验回灌规则，不再在旧技能入口层分叉。
- **可持续扩展**：通过 route registry、resource bundle、directory profile 和
  workflow template 扩展第 12 条、第 13 条流程，而不是不断平行复制新技能。
- **目录解耦**：workflow 不直接硬编码具体仓库路径，而是通过
  `config/directory-layout.yaml` 里的语义槽位解析目标目录。
- **资源可打包**：模板、脚本、配置和参考文档都能按 bundle 管理，便于复用和发布。
- **Executor 自适应**：每条路由支持声明多个变体（如 `aud-cc` / `aud-trad`），
  执行时根据当前智能体是否支持 CC Workflow 自动选择。

## v0.2.1 重点变更

详见 [CHANGELOG.md](./CHANGELOG.md)。核心三条：

1. **新增 `df` 路由** —— 文档保鲜审计，从原独立 skill `doc-freshness-audit` 完整迁入；CC Workflow 优先（`df-cc` 默认），可降级到 YAML 版（`df-trad`）。
2. **4 代理并行审计** —— `opus-deep` / `sonnet-structural` / `haiku-quick` / `sonnet-devil` 互补覆盖，逐段比对文档与代码 + 反面论证拦截过度报告。
3. **跨项目分发** —— `templates/cc-workflows/doc-fresh-parallel.js` 与 `routes/doc-fresh/` 资源一同随 release 分发，其他项目引入 ll-workflow-core 即可使用文档保鲜能力。

## v0.2.0 重点变更

详见 [CHANGELOG.md](./CHANGELOG.md)。核心三条：

1. **新增 `gt` 路由** —— 并行生成后端服务层单元测试，CC Workflow 优先。
2. **`aud` 路由双变体** —— `aud-cc`（CC Workflow 并行审计，默认推荐） / `aud-trad`（YAML 传统版，兼容其他编程智能体）。
3. **动作注册表冗余合并**：
   - `docs.upgrade_run_results` + `docs.sync_requirements_md` → `docs.maintain_requirements_md`（`mode` 切换）
   - `config.load_project_config` + `config.write_project_config` → `config.manage_project`（`mode` 切换）
   - `task.activate_requirement` 内部调用 `task.resolve_identity`，composite 不再并列声明
   - `docs.sync_governance_entries` 范围收窄到 `docs/README.md` + `docs/INDEX.md`，不再触达 `requirements.md`

## 仓库结构

| 路径 | 作用 |
| --- | --- |
| `SKILL.md` | 技能入口说明与自动触发元数据 |
| `CHANGELOG.md` | 版本变更记录，便于独立仓库发布与回溯 |
| `CONTRIBUTING.md` | 贡献指南，说明 route / bundle / profile 的维护方式 |
| `.gitignore` | 忽略本地验证与未来脚本副产物 |
| `LICENSE.txt` | 许可文件 |
| `config/` | route registry、directory layout、resource bundles |
| `docs/` | 人类可读说明文档与发布指南 |
| `references/` | 轻量速查材料 |
| `routes/` | 每条 route 的专属模板、脚本、配置、参考文件 |
| `templates/` | 平台级通用模板与 workflow/schema 定义 |
| `templates/cc-workflows/` | CC Workflow 脚本副本（`aud-cc` / `gt` / `df-cc` 路由用，release 随包分发） |
| `templates/workflows/` | 传统 YAML 工作流（`aud-trad` / `df-trad` 等路由用） |

## 当前内置 routes

| short name | 语义 | 典型触发 | executor |
| --- | --- | --- | --- |
| `init` | 初始化与导入 | 初始化 docs、导入旧文档、目录重构 | traditional_yaml |
| `req` | 需求开发 | 需求澄清、需求文档、产物落位 | traditional_yaml |
| `dev` | 开发循环 | 推进 todo、持续开发、版本收口 | traditional_yaml |
| `aud` | 项目审计 | 代码审计、安全检查、审计修复 | cc_workflow（`aud-cc` 默认） / traditional_yaml（`aud-trad`） |
| `fix` | 审计修复 | 处理 P1/P2/P3 问题、修复审计包 | traditional_yaml |
| `branch` | 并行开发 | 创建 worktree、独立分支开发 | traditional_yaml |
| `merge` | 合并清理 | 合并分支回主线、清理 worktree | traditional_yaml |
| `hchk` | 健康检查 | 五维评估、债务模式、版本复盘 | traditional_yaml |
| `haud` | 配置审计 | CLAUDE.md / settings.json / CI / Git Hooks 审计 | traditional_yaml |
| `gt` | 测试生成 | 并行生成服务层单元测试、覆盖度提升 | cc_workflow |
| `df` | 文档保鲜 | 过期文档扫描、4 代理并行审计、修正报告 | cc_workflow（`df-cc` 默认） / traditional_yaml（`df-trad`） |

> **Executor 选择策略**：当前智能体支持 CC Workflow → 用脚本版（`aud` / `gt` / `df` 默认走 `cc_workflow`）；不支持（如 Cursor、Continue.dev）→ 自动降级到 `traditional_yaml` 变体（`aud-trad` / `df-trad`）。

## 统一治理资源（REQ-1001）

以下资源已成为 `ll-workflow-core` 的治理 SSOT：

- `config/workflow-governance.yaml`：统一治理配置
- `templates/handoff-ticket-template.md`：统一交接工单模板
- `templates/evidence-bundle-template.{md,json}`：统一证据模板
- `templates/docs/7.weekly/03-high-value-learning-backlog-template.md`：周度经验回灌清单模板

说明：旧 `ll-bootstrap / ll-requirements / ll-develop / ll-audit` 仍保留兼容入口，但新的协作治理口径以本仓库为准。

## 快速开始

### 作为 skill 安装

把本仓库内容放到以下任一目录即可：

- 仓库级：`.github/skills/ll-workflow-core/`
- 用户级：`~/.github/skills/ll-workflow-core/`

安装后，可直接使用如下表达触发：

- “使用 `ll-workflow-core` 执行 `init`”
- “使用 `ll-workflow-core` 做需求开发”
- “使用 `ll-workflow-core` 执行 `dev`”
- “使用 `ll-workflow-core` 做项目审计”

### 作为独立仓库维护

如果你计划把它单独发布到 GitHub，建议把当前目录内容直接作为仓库根目录。

发布前至少检查：

1. `SKILL.md` frontmatter 是否完整
2. `README.md` / `docs/PUBLISHING.md` 是否与当前结构一致
3. `config/*.yaml` 是否仍可自洽解析
4. `routes/` 与 `templates/` 中是否残留组织内专属名称或绝对路径

详细步骤见 [`docs/PUBLISHING.md`](./docs/PUBLISHING.md)。

### 版本管理（git）

本仓库 v0.2.2 起纳入 git 版本管理（2026-07-01 初始化）。`.gitignore` 已覆盖：

- `node_modules/` / `package-lock.json`（测试依赖按需安装）
- `html/`（演示 HTML 由 `html/merge-workflows.mjs` 现生成）
- `_*.py` / `*.bak` / `*.tmp`（一次性脚本与临时文件）
- `AUDIT-REPORT-draft*.md`（审计草稿不入库，正式报告 `AUDIT-REPORT-{N}.md` 入库）

提交规范参考 [`templates/docs/5.git-commit/`](./templates/docs/5.git-commit/)。

## Profile 策略

当前内置三类目录 profile：

- `default`：适用于采用编号化 docs 体系的仓库
- `generic_docs_v1`：适用于通用 docs 布局但不完全遵循当前编号体系的仓库
- `minimal_workspace`：适用于轻量仓库或早期试点项目

如果你的仓库结构不同，优先修改 `config/directory-layout.yaml`，而不是直接改 workflow。

## 推荐阅读顺序

1. [`SKILL.md`](./SKILL.md)
2. [`docs/README.md`](./docs/README.md)
3. [`CONTRIBUTING.md`](./CONTRIBUTING.md)
4. [`docs/structure.md`](./docs/structure.md)
5. [`docs/workflows.md`](./docs/workflows.md)
6. [`docs/PUBLISHING.md`](./docs/PUBLISHING.md)

## 许可

见 [`LICENSE.txt`](./LICENSE.txt)。

## 多项目同步（副本拉取方式）

本仓库是 ll-workflow-core 的**权威版本**（私有仓库）。各项目内的副本
（如 `.claude/skills/ll-workflow-core`、`.agents/skills/ll-workflow-core`）
通过独立克隆保持同步：

```bash
# 首次安装（在项目根目录执行，需 GitHub PAT 认证）
git clone https://github.com/Linearl/ll-workflow-core.git .claude/skills/ll-workflow-core

# 日常更新
cd .claude/skills/ll-workflow-core && git pull
```

注意：
- 在宿主仓库（如 data_platform）中，副本目录需加入宿主 `.gitignore`（如 `ll-workflow-core/`），避免作为未跟踪目录混入宿主提交。
- 不使用 git submodule：技能属于"引用外部资产"，独立 clone + git pull 足够，避免嵌套 .git 带来的打包/扫描问题。
- 私有仓库克隆需要 fine-grained PAT（Contents: Read）或 gh 登录态；不要在命令行明文粘贴 token（用凭据管理器或 `gh auth login`）。
