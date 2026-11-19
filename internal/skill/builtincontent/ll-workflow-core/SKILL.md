---
name: ll-workflow-core
description: 统一的 ll 工作流总入口与扩展平台。支持 init/req/dev/aud/fix/branch/merge/health-check(hchk)/harness-audit(haud)/gt/df/runner 共 12 条路由。Use when the user explicitly asks to use ll-workflow-core, wants to run routed workflows, or needs to add future workflows beyond the current twelve. Also use when designing route registries, resource bundles, reusable base actions, custom described actions, or directory-layout based YAML workflows.
license: Complete terms in LICENSE.txt
update_time: 2026-07-01
---
# ll Workflow Core

> **平台兼容性提示**：本 skill 部分脚本为 PowerShell（.ps1）格式（位于 `routes/*/tools/*.ps1` 和 `routes/doc-fresh/scripts/*.ps1`），主要面向 Windows 环境。Linux/macOS 用户请参考脚本内嵌的等价 shell 命令，或在 WSL 下运行。YAML 工作流、Node.js 脚本（`req-sync`）、Python 校验脚本在所有平台可运行。

`ll-workflow-core` 不是“仅供参考的共享样例仓库”，而是一个**可以独立工作的统一入口技能**。

> **v0.2.0 重点**：新增 `gt`（gen-test）路由；`aud` 路由新增双变体（`aud-cc` 默认 CC Workflow / `aud-trad` 传统 YAML）；动作注册表冗余合并（`docs.maintain_requirements_md` / `config.manage_project` / `task.activate_requirement` 内部化）。详见 [CHANGELOG.md](./CHANGELOG.md)。

> **v0.2.1 重点**：新增 `df`（doc-fresh）路由——文档保鲜审计。提供双变体（`df-cc` 默认 CC Workflow / `df-trad` 传统 YAML），从原独立 skill `doc-freshness-audit` 完整迁入。所有路由数达到 11 条。详见 [CHANGELOG.md](./CHANGELOG.md)。

> REQ-1001 治理增强：`req / dev / aud` 现已统一收敛到同一套协作治理规则，覆盖**状态一致性检查、统一交接工单、审计回流三处回写、质量门与异常分级、Markdown+JSON 双格式证据、最高版本优先 + 显式回退、周度经验回灌**。

它当前直接承接十一类高频任务，而**不要求**先改造或调用其他技能正文：

- `init`：初始化 / docs bootstrap / 文档导入
- `req`：需求开发 / 需求澄清 / requirements 产物生成
- `dev`：功能开发 / 持续开发循环 / 任务推进
- `aud`：项目审计 / 代码扫描 / 审计报告（纯分析）
- `fix`：审计修复 / 读取审计包 / 逐级修复 / 归档
- `branch`：并行开发 / worktree 分支创建 / 独立开发
- `merge`：合并分支 / 主线验证 / worktree 清理
- `health-check`：项目健康检查 / 五维评估 / 债务模式识别
- `harness-audit`：AI 配置审计 / 五种介质同步性检查
- `df`：文档保鲜审计 / 过期文档扫描 / 多模型并行审计 / 修正报告
- `runner`：多角色工作流执行 / agency-orchestrator YAML 编排 / 角色扮演式任务

同时，它也不应被设计成”只会这 6 个流程”的固定技能。

它的长期目标是：

- 以 **route registry** 管理入口流程
- 以 **resource bundle** 管理模板 / 脚本 / 配置 / 参考文档
- 以 **directory profile** 管理不同仓库的目录布局
- 以 **workflow template** 支持新增第 5 条、第 6 条甚至更多流程

当用户明确说：

- “使用 `ll-workflow-core` 执行 `init`”
- “使用 `ll-workflow-core` 做某个需求的开发”
- “使用 `ll-workflow-core` 执行 `dev`”
- “使用 `ll-workflow-core` 进行项目审计”
- “使用 `ll-workflow-core` 执行 `branch`”
- “使用 `ll-workflow-core` 做并行开发”
- “使用 `ll-workflow-core` 执行 `merge`”
- “使用 `ll-workflow-core` 合并分支”
- “使用 `ll-workflow-core` 执行 `hchk`”
- “使用 `ll-workflow-core` 进行 `haud`”

本技能应直接根据用户意图或简称路由到对应 workflow，而不是要求用户再切换到其他技能。

## 何时使用

- 用户明确点名 `ll-workflow-core`。
- 用户说”执行 `init / req / dev / aud / fix / branch / merge`”。
- 用户要做：初始化、需求开发、功能开发、项目审计、审计修复、并行分支开发、合并清理。
- 用户要做：需求澄清、requirements 文档、任务清单 → 命中 `req` 路由
- 用户要做：单元测试生成、并行测试、服务层单测 → 命中 `gt` 路由
- 用户要做：文档保鲜、过期文档扫描、文档新鲜度检查、stale 文档检查 → 命中 `df` 路由
- 用户要新增未来流程，而不希望被限制在现在这 4 个入口里。
- 需要先用“自定义基础动作”试运行，再决定是否升级成正式基础动作。
- 需要在不修改其他 `ll-*` 技能的前提下，直接使用统一 workflow 入口。
- 需要给 workflow 增加循环、条件分支、重试/退出等控制语法。

## 工作流简称（激活入口）

| 简称 | 英文全称 | 对应语义 | 典型中文触发 |
| --- | --- | --- | --- |
| `init` | initialize | docs 初始化 / 历史文档导入 / 目录重构 / 索引同步 | 初始化、建 docs、导入旧文档、同步索引 |
| `req` | requirements | 需求开发 / 需求分类 / 复杂度评估 / 需求资产生成 | 需求开发、需求文档、需求澄清、做需求 |
| `dev` | development | 功能开发 / 持续开发循环 / todo 推进 | 功能开发、开发这个需求、执行 dev |
| `aud` | audit | 项目审计 / 代码扫描 / 审计报告（纯分析） | 项目审计、代码审计、代码健康诊断、执行 aud |
| `fix` | audit fix | 审计修复 / 读取审计包 / 逐级修复 / 归档 | 审计修复、修复审计问题、处理 P1 问题、执行 fix |
| `branch` | parallel development | 并行开发 / worktree 分支创建 / 独立开发 | 并行开发、创建分支、worktree 开发、执行 branch |
| `merge` | merge & cleanup | 合并分支回主线 / 链式验证 / worktree 清理 | 合并分支、合并清理、收口、执行 merge |
| `hchk` | health check | 项目健康检查 / 五维评估 / 债务模式识别 / 版本生命周期判断 | 健康检查、版本启动检查、版本复盘、战略校准 |
| `haud` | harness audit | AI 配置审计 / 五种介质同步性检查 / 反模式检测 | 配置审计、CLAUDE.md 审计、settings.json 审计、配置同步 |
| `gt` | generate test | 并行生成单元测试（覆盖服务层） | 生成测试、并行测试、测试覆盖、服务层测试、执行 gt |
| `df` | doc freshness | 文档保鲜审计 / 过期文档扫描 / 多模型并行审计 / 修正报告 | 文档保鲜、过期文档、文档审计、stale 文档检查、执行 df |

## 独立路由规则（强制）

1. **用户显式给出简称时，直接路由**：
	- `init` → 初始化工作流
	- `req` → 需求开发工作流（v0.2.2 起：4 个项目配置字段全部生效；non_interactive 模式产物与 interactive 同构；requirement_status 轴已写入）
	- `dev` → 功能开发工作流
	- `aud` → 项目审计工作流（默认 CC Workflow 版本）
	- `aud-cc` → CC Workflow 强制版本（v2 并行审计）
	- `aud-trad` → 传统 YAML 版本（兼容其他编程智能体）
	- `gt` → 并行生成测试（CC Workflow 优先）
	- `df` → 文档保鲜审计（CC Workflow 优先，可降级 YAML）
	- `df-cc` → CC Workflow 强制版本
	- `df-trad` → 传统 YAML 版本
	- `fix` → 审计修复工作流
	- `branch` → 并行开发工作流
	- `merge` → 合并清理工作流
	- `hchk` → 健康检查工作流
	- `haud` → 配置审计工作流

### Executor 选择策略（强制）

**判断顺序**（自上而下）：

1. **当前智能体是否支持 CC Workflow？**
	- 是（Claude Code）→ 使用 CC Workflow 脚本（执行更快、门控更强）
	- 否（其他编程智能体）→ 使用 YAML 工作流

2. **是否有对应的 Workflow 脚本？**
	- 是 → 优先使用 Workflow 脚本
	- 否 → 使用 YAML 工作流

3. **默认规则**：
	- 有 Workflow 脚本支持的路由（`aud`、`gt`）→ 默认 CC Workflow
	- 无 Workflow 脚本支持的路由（`init`、`req`、`dev`、`fix` 等）→ 默认 YAML 工作流

### aud 工作流执行规则

`aud` 路由有两个变体，按 Executor 选择策略自动适配：

#### 变体 A：`aud-cc`（CC Workflow 版本，默认推荐）

使用 Claude Code Workflow 工具执行 9 维度并行审计：

```javascript
Workflow({
  scriptPath: '.claude/workflows/audit-parallel-v2.js',
  args: {
    scope: 'full' | 'incremental' | 'quick',
    targetDir: '02-dev/dev_12',  // 默认当前版本目录
    date: '2026-06-22',  // 审计日期
  }
})
```

**审计模式选择**：
- `full`：9 维度全部执行（默认）
- `incremental`：核心维度（security, stability, quality, performance）
- `quick`：快速审计（security, quality）

**特性**：门控检查、并发 3、超时 5 分钟、降级策略。

**产物落位**（自动保存）：
```
docs/3.analysis/diagnosis/01-active/YYYY-MM-DD-代码审计-{scope}/
├── README.md                      # 主报告
├── audit-findings.json            # 全量发现数据
├── run-result.json                # 执行元数据
├── scan-findings.json             # 合并扫描结果
├── 00-preprocessing/              # 预处理子报告
├── 01-scan-batch1/ ~ 03-scan-batch3/   # 扫描批次子报告
├── 04-review/                     # 复核子报告
└── 05-report/                     # 报告草稿
```

#### 变体 B：`aud-trad`（传统 YAML 版本）

使用 YAML 定义的工作流（`templates/workflows/aud.workflow.yaml`），兼容其他编程智能体（Cursor、Continue.dev、Aider 等）：

```yaml
# 传统 YAML 审计工作流（简化示例）
phases:
  - id: scope_selection
  - id: dimension_scan
  - id: review
  - id: report_generation
```

**何时使用**：
- 当前智能体不支持 CC Workflow
- 需要在其他 AI 编程工具中触发
- 需要审计工作流可移植

### gt 工作流执行规则

`gt`（gen-test）路由：并行生成后端服务层单元测试。

#### 变体 A：`gt`（CC Workflow 版本，默认）

```javascript
Workflow({
  scriptPath: '.claude/workflows/test-generation-parallel.js',
  args: {
    targetDir: '02-dev/dev_12/backend/src/services',
    coverageTarget: 0.7,
  }
})
```

**特性**：
- 3 组并行生成（核心业务 / 数据处理 / 基础设施）
- 每组自动 Mock 外部依赖
- AAA 模式 + 边界用例 + 异常路径

**产物落位**：
```
02-dev/dev_12/backend/src/services/{module}/__tests__/*.test.ts
```

### df 工作流执行规则

`df`（doc-fresh）路由：文档保鲜审计。扫描过期技术文档，按 4 代理并行审计，生成修正报告。

#### 变体 A：`df-cc`（CC Workflow 版本，默认推荐）

```javascript
Workflow({
  scriptPath: '.claude/skills/ll-workflow-core/templates/cc-workflows/doc-fresh-parallel.js',
  args: {
    repoRoot: '<repo-root>',
    scanPath: '2.tech/',           // 默认 2.tech/
    workDir: '04-auto_audit',      // 默认 04-auto_audit/
    forceFull: false,              // true = 忽略 SHA 去重
    maxFilesPerRun: 12,            // 单次审计文档上限
    maxBatchSize: 4,               // 每 batch ≤4 篇
  }
})
```

**6 阶段流水线**：扫描（Python）→ 按前缀分批 → 4 代理并行审计 → reviewer 复核 → 汇总 → 标记已审计。

**4 代理配置**（每 batch 派出 4 份报告）：

| 代理 | 模型 | 角色 | 输出 |
| --- | --- | --- | --- |
| A 深度分析 | opus | 逐段比对文档与代码 | `{batch}/opus-deep.md` |
| B 结构检查 | sonnet | 验证路径、路由、目录结构 | `{batch}/sonnet-structural.md` |
| C 快速扫描 | haiku | 日期/版本/端口/硬编码数字 | `{batch}/haiku-quick.md` |
| D 反面论证 | sonnet | 寻找文档仍然有效的证据 | `{batch}/sonnet-devil.md` |

**特性**：

- 门控驱动：每个 batch 必须 4/4 报告齐备才进入复核
- SHA + TTL 去重：`audited_sha` 14 天内同 SHA 跳过
- 跨项目分发：副本在 `templates/cc-workflows/`，其他项目引入即用

**产物落位**：

```
{runDirRoot}/
├── scan-result.json
├── batch-NN-{slug}/
│   ├── opus-deep.md
│   ├── sonnet-structural.md
│   ├── haiku-quick.md
│   └── sonnet-devil.md
├── review-NN.md
└── summary.md
```

#### 变体 B：`df-trad`（传统 YAML 版本，兼容其他编程智能体）

使用 YAML 工作流（`templates/workflows/doc-fresh.workflow.yaml`）+ Python 扫描器 + Agent 提示词模板。兼容 Cursor / Continue.dev / Aider 等。Phase 3 派出 4 代理的提示词字段格式见 `routes/doc-fresh/prompts/opus-deep.md` 等模板。

**何时使用**：

- 当前智能体不支持 CC Workflow
- 需要在其他 AI 编程工具中触发
- 需要审计工作流可移植
2. **用户未给简称时，根据意图推断**：
	- 提到初始化 / bootstrap / 导入旧文档 → `init`
	- 提到需求拆解 / 需求文档 / 澄清 → `req`
	- 提到开发某需求 / 推进 todo / 持续开发 → `dev`
	- 提到审计 / 代码质量 / 安全检查 / 扫描 → `aud`
	- 提到修复审计问题 / 审计修复 / 处理 P1 问题 → `fix`
	- 提到并行开发 / 创建独立分支 / worktree → `branch`
	- 提到合并分支 / 合并清理 / 收口清理 → `merge`
	- 提到健康检查 / 版本启动检查 / 版本复盘 / 战略校准 → `hchk`
	- 提到配置审计 / CLAUDE.md / settings.json / agent config / 配置同步 → `haud`
	- 提到文档保鲜 / 过期文档 / 文档审计 / 文档新鲜度 / stale 文档检查 → `df`
3. **用户只说”使用 ll-workflow-core”但意图仍模糊时**：必须先做工作流选择，不得盲目进入某个默认流程。
4. **除非用户明确要求比较旧技能与新技能，否则不需要再读取其他技能正文才能执行本技能**。

## 目录结构抽象规则（强制）

本技能中的 workflow、动作和复合动作**不得默认把目录结构硬编码在正文里**。

统一原则：

1. 先读取项目级配置：项目根目录下的 `.ll-workflow.yaml`（如存在）
2. 再读取目录结构配置：[`config/directory-layout.yaml`](./config/directory-layout.yaml)
3. workflow 中只使用”逻辑槽位（slot）”或”语义目录名（semantic name）”
4. 如：
	- `todo` 不等于固定字符串，而是 `task_todo_version_root`
	- `inactive requirement` 不等于固定路径，而是 `task_inactive_root`
	- `done` 不等于固定路径，而是 `task_done_version_root`
5. 若仓库目录结构调整，优先改项目配置 `.ll-workflow.yaml` 或 `directory-layout.yaml`，而不是批量改 workflow 正文。

## 项目级配置（.ll-workflow.yaml）

项目可以在根目录创建 `.ll-workflow.yaml` 来自定义工作流行为，而不需要修改 skill 内部配置。

### 配置文件结构

```yaml
version: “1.0”
base_profile: default  # 可选：default / generic_docs_v1 / minimal_workspace

project:
  name: “项目名称”
  type: fullstack  # fullstack / backend / frontend / library / mobile
  docs_language: zh  # zh / en

slots:
  # 覆盖 base_profile 中的目录槽位
  docs_root: “${targetDevDir}/docs”
  task_todo_root: “${targetDevDir}/docs/tasks/todo”

workflows:
  init:
    import_source: “docs/legacy”  # 历史文档导入目录
    import_strategy: copy  # move / copy / link
  branch:
    max_parallel_slots: 3  # worktree 并行最大数量（1-10）

aliases:
  - name: “建文档”
    maps_to: init
```

### 配置优先级

1. workflow 显式传入的参数（最高优先级）
2. 项目根目录 `.ll-workflow.yaml`
3. route-registry.yaml 中绑定的 profile
4. directory-layout.yaml 中的默认 profile（最低优先级）

### 配置 Schema

完整 schema 参见：[`templates/project-config.schema.yaml`](./templates/project-config.schema.yaml)

## 可用资源

### 配置文件
- 项目级配置：项目根目录 `.ll-workflow.yaml`（如存在）
- 目录结构配置：[`config/directory-layout.yaml`](./config/directory-layout.yaml)
- 路由注册表：[`config/route-registry.yaml`](./config/route-registry.yaml)
- 资源包注册表：[`config/resource-bundles.yaml`](./config/resource-bundles.yaml)
- 治理配置：[`config/workflow-governance.yaml`](./config/workflow-governance.yaml)

### Schema 与注册表
- 项目配置 schema：[`templates/project-config.schema.yaml`](./templates/project-config.schema.yaml)
- workflow schema：[`templates/workflow.schema.yaml`](./templates/workflow.schema.yaml)
- 动作注册表：[`templates/action-registry.yaml`](./templates/action-registry.yaml)
- 复合动作注册表：[`templates/composite-actions.yaml`](./templates/composite-actions.yaml)

### 模板与参考
- 基础动作参考：[`references/action-glossary.md`](./references/action-glossary.md)
- 复合动作参考：[`references/composite-actions.md`](./references/composite-actions.md)
- 统一交接工单模板：[`templates/handoff-ticket-template.md`](./templates/handoff-ticket-template.md)
- 统一证据模板：[`templates/evidence-bundle-template.md`](./templates/evidence-bundle-template.md)、[`templates/evidence-bundle-template.json`](./templates/evidence-bundle-template.json)

### 入口 workflow
- [`templates/workflows/init.workflow.yaml`](./templates/workflows/init.workflow.yaml)
- [`templates/workflows/req.workflow.yaml`](./templates/workflows/req.workflow.yaml)
- [`templates/workflows/dev.workflow.yaml`](./templates/workflows/dev.workflow.yaml)
- [`templates/workflows/aud.workflow.yaml`](./templates/workflows/aud.workflow.yaml)
- [`templates/workflows/fix.workflow.yaml`](./templates/workflows/fix.workflow.yaml)
- [`templates/workflows/branch.workflow.yaml`](./templates/workflows/branch.workflow.yaml)
- [`templates/workflows/merge.workflow.yaml`](./templates/workflows/merge.workflow.yaml)
- [`templates/workflows/health-check.workflow.yaml`](./templates/workflows/health-check.workflow.yaml)
- [`templates/workflows/harness-audit.workflow.yaml`](./templates/workflows/harness-audit.workflow.yaml)

### 路由资源目录
- `routes/init/`：由 `ll-bootstrap` 迁入的模板与导入规则
- `routes/req/`：由 `ll-requirements` 迁入的需求模板与 schema
- `routes/dev/`：由 `ll-develop` 迁入的开发模板、tools、参考文件
- `routes/aud/`：由 `ll-audit` 迁入的模板、docs、scripts、config
- `routes/fix/`：审计修复路由资源（复用 aud 模板）
- `routes/branch/`：由 ll-workflow-core 直接管理的并行开发路由资源
- `routes/merge/`：由 ll-workflow-core 直接管理的合并清理路由资源
- `routes/health-check/`：项目健康检查工作流（原 ll-health-check）
- `routes/harness-audit/`：配置审计工作流（原 harness-audit）

### 文档模板（基于项目实际结构，已同步）

skill 的 `templates/docs/` 目录已与项目 `docs/*/template/` 同步，以 docs 中的最新版本为准：

- `templates/docs/0.version_plan/`：版本计划模板
  - `01-version-plan-template.md`
  - `02-milestone-checklist-template.md`
  - `README.md`
- `templates/docs/1.task/`：任务包模板（完整六件套 + 审计包）
  - `01-任务专题文档标准模板.md`
  - `02-审计任务包模板说明.md`
  - `六件套/`：README-template、REQ-template、design-template、tasks-template 等
  - `audit-package/`：审计包完整模板（含 P1-P4 示例）
  - `todolist-template.md`
- `templates/docs/2.tech/`：技术文档模板（skill 独有）
  - `01-doc-frontmatter-description-template.md`
  - `02-analysis-report-template.md`
  - `03-guide-doc-template.md`
  - `04-api-doc-template.md`
- `templates/docs/3.analysis/report/`：分析报告模板
- `templates/docs/4.misc/`：杂项文档模板

### 专用报告模板
- `templates/health-check-report.md`：项目健康检查报告模板（五维评估 + 债务模式）
- `templates/audit-report.md`：代码审计报告模板（六维度评分 + 优先级路线图）
- `templates/audit-package/`：审计任务包完整模板（含 P1-P4 示例目录、decision_log、effective_config、skipped）
- `templates/debt-backlog.md`：技术债台账模板
- `templates/decision-log.md`：决策日志模板
- `templates/implementation-summary.md`：修复实施总结模板
- `templates/solution.md`：解决方案模板
- `templates/issue-description.md`：问题描述模板
- `templates/docs/5.git-commit/`：Git 提交模板
- `templates/docs/6.changelog/`：变更日志模板
- `templates/docs/7.weekly/`：周报模板（含 `03-high-value-learning-backflow-template.md`）
- `templates/docs/8.test/`：测试文档模板

### 经验教训
- `lessons/`：工作流执行过程中沉淀的历史经验教训，遇到疑难问题时查阅

### 脚本模板
- [`templates/scripts/generate-docs-index.js`](./templates/scripts/generate-docs-index.js)：生成 docs/INDEX.md 索引
- [`templates/scripts/req-sync.js`](./templates/scripts/req-sync.js)：同步 requirements.md（含归档功能）

### 说明文档
- [`docs/README.md`](./docs/README.md)：说明文档总览
- [`README.md`](./README.md)：仓库总览
- [`docs/PUBLISHING.md`](./docs/PUBLISHING.md)：独立发布指南
- [`docs/EXTENDING.md`](./docs/EXTENDING.md)：扩展指南
- [`docs/structure.md`](./docs/structure.md)：目录结构说明
- [`docs/workflows.md`](./docs/workflows.md)：workflow 说明
- [`docs/action-registry.md`](./docs/action-registry.md)：基础动作说明
- [`docs/composite-actions.md`](./docs/composite-actions.md)：复合动作说明
- [`docs/workflow-schema.md`](./docs/workflow-schema.md)：schema 说明

## 执行顺序（统一入口）

1. 读取 [`config/route-registry.yaml`](./config/route-registry.yaml)。
2. 识别用户是否显式给出 route `short_name`。
3. 若未显式给出，则根据 route registry 的别名与意图提示进行路由。
4. 读取 [`config/directory-layout.yaml`](./config/directory-layout.yaml)。
5. 读取 [`config/resource-bundles.yaml`](./config/resource-bundles.yaml)。
6. 读取动作注册表与复合动作注册表。
7. 加载目标 workflow YAML。
8. 用目录 profile 的逻辑槽位解析所有工作目录。
9. 按 workflow 的 phases / branch / loop 执行。

## 使用建议

1. 先在 `action-registry.yaml` 中寻找已有基础动作。
2. 若找不到合适动作，优先使用 `custom.described_action` 试运行。
3. 当同类自定义动作在多个 workflow 中重复出现时，再沉淀为正式基础动作。
4. workflow 优先引用复合动作；仅在需要细化时再直接展开基础动作。
5. 所有路径优先走目录配置的逻辑槽位，不直接写死 `docs/...` 子目录。
6. 循环与条件分支统一使用 `type: while`、`type: for_each`、`type: branch`、`type: switch`、`type: repeat_until`。

## 当前样例覆盖

- `init`：结构创建 + 导入映射 + 分类循环 + 项目配置加载 + 索引同步（INDEX.md / requirements.md / task README）
- `req`：项目配置加载 + 需求分类 + 复杂度评估 + 可跳过澄清 + 可跳过 design + 按复杂度调整门控轮数
- `dev`：循环推进 + 用户门控分支
- `aud`：范围选择 + 多轮复核 + 分级修复
- `branch`：worktree 创建 + 依赖复制 + 开发提交
- `merge`：状态检查 + 链式验证 + 清理收口

## req 工作流特性

### 需求分类（自动）

基于项目配置的 `category_mapping` 关键词，自动分类需求类型和编号段：

| 编号段 | 分类 | 关键词示例 |
| --- | --- | --- |
| 1xxx | 架构和基础设施 | 架构、基础设施、工具链、搭建 |
| 2xxx | 核心功能开发 | 功能、开发、API、业务、实现 |
| 3xxx | 用户界面和体验 | UI、UX、界面、交互、体验 |
| 4xxx | 测试和质量保证 | 测试、E2E、QA、覆盖、用例 |
| 5xxx | 文档和规范 | 文档、规范、说明、保鲜 |
| 6xxx | 运维和部署 | 部署、运维、环境、Docker |
| 7xxx | 技术债务和重构 | 重构、债务、去重、清理 |
| 8xxx | 研究和调研 | 调研、研究、评估、选型 |
| 9xxx | 未分类和临时任务 | 临时、杂项、其他 |

### 复杂度评估（自动）

基于需求内容自动评估复杂度，影响后续流程：

| 复杂度 | 澄清轮数 | 跳过 design | 典型场景 |
| --- | --- | --- | --- |
| simple | 1 | 是 | fix、修复、hotfix |
| medium | 2 | 否 | 重构、优化、cleanup |
| complex | 4 | 否 | 新增、添加、实现、开发 |

### 澄清循环控制

- **默认行为**：根据复杂度自动决定是否跳过澄清
- **用户覆盖**：传入 `skipClarificationOverride=true` 强制跳过
- **关键词跳过**：包含 fix/修复/hotfix/紧急/urgent 时自动跳过

### design 阶段控制

- **默认行为**：simple 复杂度跳过 design
- **项目配置**：通过 `skip_design_complexity` 自定义

## 面向未来新增流程的约束

新增流程时，优先走以下路径：

1. 在 `config/route-registry.yaml` 中注册新 route
2. 在 `templates/workflows/` 下创建 `<short_name>.workflow.yaml`
3. 若需要专属资源，在 `routes/<short_name>/` 下建目录
4. 若需要新模板集合，在 `config/resource-bundles.yaml` 中声明 bundle
5. 若目录布局有差异，新增或覆盖 `directory-layout` profile

因此，`ll-workflow-core` 未来的扩展单元不是“再做一个平行技能”，而是：

- 一个新 route
- 一个新 workflow
- 一组新 bundle
- 必要时一个新 directory profile

## 激活示例

- “使用 `ll-workflow-core` 执行 `init`”
- “使用 `ll-workflow-core` 进行某个需求的开发”
- “使用 `ll-workflow-core` 执行 `dev`”
- “使用 `ll-workflow-core` 执行 `aud`”
- “使用 `ll-workflow-core` 执行 `branch` — 创建 worktree 并行开发任务包 #42”
- “使用 `ll-workflow-core` 执行 `merge` — 合并最新 worktree 分支”
- “使用 `ll-workflow-core` 执行 `gt` — 并行生成服务层单元测试”
- “使用 `ll-workflow-core` 执行 `df` — 文档保鲜审计”
- “使用 `ll-workflow-core` 执行 `df-cc` — CC Workflow 强制版本”

## CC Workflow 脚本副本（v0.2.0 起，v0.2.x 核实更新于 2026-07-01）

`templates/cc-workflows/` 目录存放 CC Workflow 脚本副本，与项目级 `.claude/workflows/` 保持单向同步：

| 副本 | 来源 | 路由引用 | 字节数 |
| --- | --- | --- | --- |
| `templates/cc-workflows/audit-parallel-v2.js` | `.claude/workflows/audit-parallel-v2.js` | `aud-cc` | 29436 |
| `templates/cc-workflows/test-generation-parallel.js` | `.claude/workflows/test-generation-parallel.js` | `gt-cc`（默认） | 5862 |
| `templates/cc-workflows/doc-fresh-parallel.js` | `routes/doc-fresh/scripts/check-doc-freshness.py` + `routes/doc-fresh/prompts/*.md`（组合） | `df-cc` | 21673 |
| `templates/cc-workflows/README.md` | 副本目录说明 | 同步策略 | 1845 |

**核实记录**：2026-07-01 第 8 轮读文件复核时通过 `ls -la templates/cc-workflows/` 确认实际文件 4 个（3 个 .js 副本 + 1 个 README.md），与上表完全对齐。后续新增副本时，请同步更新本表。

**同步策略**：

- **方向**：项目级 `.claude/workflows/` → skill 副本（单向）
- **时机**：ll-workflow-core release 前必须同步；日常脚本优化仅更新项目级；紧急修复同步到副本
- **引用方式**：`route-registry.y