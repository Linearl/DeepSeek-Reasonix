---
description: "ll-workflow-core 版本变更记录，面向独立仓库发布与后续维护"
update_time: 2026-07-01
---
<!-- markdownlint-disable MD024 -->

# Changelog

本项目遵循

## [Unreleased]

### Added
- **`runner` 路由（2026-08-12）**:从原独立 skill `workflow-runner` 完整迁入——多角色工作流执行器,解析 agency-orchestrator 格式 YAML 工作流,用当前会话 LLM 依次扮演每个角色执行（无需 API key）。路由数 11 → 12。
- **git 版本管理**:仓库纳入 git（2026-07-01 初始化）,`.gitignore` 覆盖 `node_modules/` / `html/` / `_*.py` / `*.bak` / `AUDIT-REPORT-draft*.md`。
- **契约测试 `tests/workflow-inputs-format.test.js`** — 校验所有 workflow `inputs/outputs` 每项都是 `{name, description}` 对象,与 `workflow.schema.yaml` 的 `item.required_fields` 双重保险。
- **`shared.cc-workflows` 资源包**:`resource-bundles.yaml` 新增 `shared.cc-workflows`,被 `aud-cc` / `gt-cc` / `df-cc` 三个变体共享,统一管理 `templates/cc-workflows/` 副本。

### Changed
- **`aud` 路由清理**:删除残留 `templates/workflows/aud.workflow.yaml.bak`(13969 字节),`*.bak` 已加入 `.gitignore`。
- **`hchk` / `haud` 路由补全**:`route-registry.yaml` 中 `route.health-check` / `route.harness-audit` 补 `route_root: ../routes/<short_name>`,与其他 9 个路由风格一致。
- **`directory-layout.yaml` 精简**:删除 `route_profile_bindings` 整段(单一数据源,避免与 `route-registry.yaml` 的 `directory_profile_ref` 重复)。
- **`workflow.schema.yaml` 加固**:`context_contract.inputs/outputs.item.required_fields: [name, description]` 固化"必须对象格式"决策。
- **`utils.js` SSOT 注释恢复**:`LIFECYCLE_STATUS_MAP` 前面 12 行 JSDoc 注释重新加回,显式声明其为整个 ll-workflow-core 的 SINGLE SOURCE OF TRUTH。
- **`doc-fresh.workflow.yaml` inputs/outputs 补全**:7 个 `name` 缺 `description` 的字段补齐。
- **状态字面值 SSOT 化**:抽出 `LIFECYCLE_STATUS_MAP` 为单一事实源(`templates/scripts/req-sync/utils.js` L53),同步对齐 8 个 markdown 模板。文档侧字面值统一为 `🆕 待激活`/`📋 待办`/`🚧 进行中`/`⏸️ 暂停`/`✅ 已完成`/`🛑 已终止`。
- **status emoji 改名**:`⏳ 进行中` → `🚧 进行中`、`✅ 完成` → `✅ 已完成`、`🆕 激活` → `🆕 待激活`(与 SSOT 对齐;旧值在历史快照/grep 命令中保留,无破坏性)。
- **frontmatter 规范化**:180 个 `.md` 文件全部具备 `description` + `update_time` 双字段。
- **`gt` 路由双变体**:新增 `gt-cc`(cc_workflow,推荐)与 `gt-trad`(traditional_yaml 占位),注册于 `route.gen-test`。
- **SKILL.md 路由数 10 → 11**:`df`(doc-fresh)路由已并入。
- **SKILL.md frontmatter 触发词扩充**:补充 `req`/`gt`/`df` 中文触发句式。
- **六件套 README 双副本说明**:`template/六件套/`(模板源)与 `六件套/`(展示副本)各自加用途说明块。

### Added
- **契约测试**:`tests/route-frontmatter.test.js`(8 项)— 校验 SKILL.md frontmatter ↔ route-registry.yaml 一致性、路由必填字段、variants executor 合法性、id/short_name 唯一性。
- **`routes/gt/README.md`**:`gt` 路由目录说明,涵盖变体表、与 dev/req/fix 的关系、触发词。

### Notes
- **req-sync.mjs → req-sync.js**:`req-sync.mjs` 已迁至 `req-sync.js`(CommonJS 形式)。`req-sync.mjs` 路径仅作为历史引用保留在 CHANGELOG,代码侧全部走 `.js` 路径。

”Keep a Changelog”的记录思路，并使用语义化版本管理变更。

## [0.2.2] - 2026-06-23

> **注意**：本版本含部分破坏性变更（custom → action、删除 route.req.extension_points 字段），按用户约定仍记为 0.2.x 增量。详见 Migration Notes。

> **注意**：本版本含部分破坏性变更（custom → action、删除 route.req.extension_points 字段），按用户约定仍记为 0.2.x 增量。详见 Migration Notes。

> **注意**：本版本含部分破坏性变更（custom → action、删除 route.req.extension_points 字段），按用户约定仍记为 0.2.x 增量。详见 Migration Notes。

### Added

- **新 action `requirement.compute_overrides`**（base）：替代 v0.2.1 的 `custom.described_action apply_complexity_overrides`。读 4 个项目配置字段（category_mapping / complexity_rules / skip_clarification_keywords / skip_design_complexity），输出 4 个控制开关（skipClarification / skipDesign / effectiveMaxGateRounds / unresolvedCount）。
- **新 action `requirement.refine_ears`**（base）：替代 v0.2.1 的 `custom.described_action refine_acceptance_by_description`。输入 requirementDraft + openQuestions + latestAnswers，输出 refinedRequirementDraft + 压缩后的 openQuestions + unresolvedCount + earsChecklist。关键约束：openQuestions 真实压缩，不允许静默丢未决项。
- **新 action `task.compute_path_hint`**（base）：b_input_intake 阶段预计算 taskPathHint（不创建目录），供中间 phase 的 evidence/governance 引用。
- **新 composite `flow.req_apply_overrides`**：包装 `requirement.compute_overrides`，req 路由专用。

### Changed

- **`templates/workflows/req.workflow.yaml`**：
  - `apply_complexity_overrides` step 从 `custom.described_action` 改为 `composite (flow.req_apply_overrides)`
  - `refine_acceptance_by_description` step 从 `custom.described_action` 改为 `requirement.refine_ears`
  - `b_input_intake` 末尾新增 `preflight_taskPath_hint` step
  - `c_clarify_and_resolve_conflicts` 的 `clarify_round_loop` while 条件改为 `unresolved_count > 0`，与 gate.pass_when 双指标一致
  - `non_interactive` 分支补 3 步默认决策链（load_default_decision_bundle / apply_default_decisions / refine_with_defaults）
  - `g_finalize` 补 `sync_requirement_status` step 写入 requirement_status=approved；补 `collect_req_evidence` step 满足 evidence.collect → evidence.emit_dual_format 强顺序约束
  - `interaction.ask_questions_gate` 显式传 `gateTemplatePath`
- **`templates/action-registry.yaml`**：
  - `state.update_task_status` 新增可选 input `statusAxis: task | requirement`，默认 `task`（向后兼容）
  - `interaction.ask_questions_gate` 新增可选 input `gateTemplatePath`
- **`config/route-registry.yaml`**：`route.req` 删除未支撑的 `extension_points` 字段，改用 `template_files` 字段显式注册 3 个模板
- **`routes/req/templates/run_result.json-schema.md`**：扩充 6 个 v0.2.2 字段（requirementStatus / reqType / categoryCode / complexity / skipFlags / gateRounds / decisionPoints）

### Fixed

- **requirement_status 轴未写入**：req 路由只写 task_status=requirements_ready，dev 路由入站时 governance.validate_state_sync 双轴不一致。新增 `sync_requirement_status` 步骤写入 requirement_status=approved
- **`evidence.collect → evidence.emit_dual_format` 强顺序约束被破坏**：req 路由直接调 emit_dual_format 传 statusRecord，违反 v0.2.0 强顺序约束。新增 `collect_req_evidence` 步骤先 collect 后 emit
- **`non_interactive` 模式产物不一致**：跳过澄清后 `refinement_result` 为 null，导致下游 `write_requirements_doc` 拿不到 refinement 输入。补 3 步默认决策链保证 non_interactive 与 interactive 产物同构
- **`.ll-workflow.yaml` 项目配置 `skip_clarification_keywords` / `skip_design_complexity` 字段从未生效**：v0.2.1 的 `apply_complexity_overrides` 是 custom，AI 解读时忽略这两个字段。v0.2.2 通过结构化 `requirement.compute_overrides` 全部生效

### Tests

- **`tests/action-registry.test.js`**：新增 5 个 describe 块（19 用例），覆盖 4 个新 action + state.update_task_status 扩展 + interaction.ask_questions_gate 扩展
- **`tests/composite-actions.test.js`**：新增 2 个 describe 块（4 用例），覆盖 flow.req_apply_overrides + 与 flow.requirement_activate 互斥约束
- **总用例数**：从 30 提升至 **50**（+20）

### Migration Notes

- **custom → action**：原来调用 `apply_complexity_overrides` / `refine_acceptance_by_description` 的下游代码需改为 `flow.req_apply_overrides` / `requirement.refine_ears`
- **`extension_points` 字段迁移**：`route.req` 删 `extension_points`，模板路径已迁移到 `template_files`
- **`run_result.json` 字段**：`req-sync.mjs` 现在可消费 `requirementStatus / reqType / complexity / skipFlags / gateRounds / decisionPoints` 6 个新字段（向后兼容）

## [0.2.1] - 2026-06-23

### Added

- **新路由 `df`（doc-fresh）**：从原独立 skill `doc-freshness-audit` 完整迁入，作为第 11 条路由接入 ll-workflow-core。6 阶段流水线（扫描→分批→4 代理并行审计→复核→汇总→标记）。触发关键词：`df` / `doc-fresh` / `doc-freshness` / `文档保鲜` / `过期文档` / `文档审计` / `文档新鲜度`。
- **`df` 路由双变体**：
  - `df-cc`（默认）：CC Workflow 版本，执行 `templates/cc-workflows/doc-fresh-parallel.js`，内置 Python 扫描器调用、4 代理并行（opus-deep / sonnet-structural / haiku-quick / sonnet-devil）、批次完整性门控、reviewer 复核。
  - `df-trad`：传统 YAML 版本（`templates/workflows/doc-fresh.workflow.yaml`），兼容 Cursor / Continue.dev / Aider 等其他编程智能体。
- **`routes/doc-fresh/` 资源目录**：完整迁入 `scripts/`（Python 扫描器 + 定时任务脚本）、`prompts/`（5 个 Agent 提示词模板）、`templates/`（4 个阶段模板 + document-freshness.yaml 配置）、`config/`（SETUP.md）、`examples/`（4 个脱敏示例）、`LESSONS.md`（踩坑经验）。
- **CC Workflow 脚本副本**：`templates/cc-workflows/doc-fresh-parallel.js`，与 `audit-parallel-v2.js` / `test-generation-parallel.js` 同级存放，支持 release 打包与跨项目分发。
- **资源包 `route.doc-fresh.assets`**：在 `config/resource-bundles.yaml` 中注册，整合 `routes/doc-fresh/` 下所有资源（scripts / prompts / templates / config / examples / LESSONS）。

### Changed

- **`config/route-registry.yaml`**：新增 `route.doc-fresh`（stable），绑定 `route.doc-fresh.assets` bundle 与 2 个 variants（`df-cc` / `df-trad`）；`source_skill` 登记为 `doc-freshness-audit` 以保留迁移溯源。
- **`SKILL.md`**：工作流简称表追加 `df` 行；`activation_aliases` 列表追加 `df-cc` / `df-trad`；新增”df 工作流执行规则”小节，含 4 代理配置表与产物落位说明；CC Workflow 脚本副本表格追加 `doc-fresh-parallel.js`。
- **路由总数**：从 10 条提升至 **11 条**（init / req / dev / aud / fix / branch / merge / hchk / haud / gt / df）。

### Migration Notes

- **触发方式**：`/ll-workflow-core df` 或 `/df` 直接调用；老 skill `doc-freshness-audit` 仍可独立调用（保留向后兼容），其 `SKILL.md` 顶部已添加迁移说明。
- **Python 扫描器路径变更**：原 `04-auto_audit/scripts/check-doc-freshness.py` → 现位于 `.claude/skills/ll-workflow-core/routes/doc-fresh/scripts/check-doc-freshness.py`。独立调用时改用新路径即可。
- **`prompts/` 路径变更**：原 `.claude/skills/doc-freshness-audit/prompts/` → 现位于 `.claude/skills/ll-workflow-core/routes/doc-fresh/prompts/`。子代理 prompt 模板无修改。

## [0.2.0] - 2026-06-23

### Added

- **新路由 `gt`（gen-test）**：通过 `templates/cc-workflows/test-generation-parallel.js` 并行生成后端服务层单元测试（3 组并行：核心业务 / 数据处理 / 基础设施）。触发关键词：`gt` / `gen-test` / `生成测试` / `并行测试生成` / `测试生成`。
- **`aud` 路由双变体**：
  - `aud-cc`（默认）：CC Workflow 版本，执行 9 维度并行审计，3 批 × 3 维度、内置门控检查、复核并发 3、单批超时 5 分钟、降级策略。
  - `aud-trad`：传统 YAML 版本（`templates/workflows/aud.workflow.yaml`），兼容 Cursor / Continue.dev / Aider 等其他编程智能体。
- **CC Workflow 脚本副本**：`templates/cc-workflows/` 目录下复制 `audit-parallel-v2.js` 与 `test-generation-parallel.js`，附带 `README.md` 说明同步策略（项目级 → skill 副本单向同步，ll-workflow-core release 前必须同步）。

### Changed

- **`templates/action-registry.yaml` 动作冗余合并**（v0.2.0 重点）：
  - `docs.upgrade_run_results` + `docs.sync_requirements_md` → **`docs.maintain_requirements_md`**（`mode: upgrade | sync | upgrade_then_sync`）
  - `config.load_project_config` + `config.write_project_config` → **`config.manage_project`**（`mode: read | write | read_write`）
  - `task.activate_requirement` 描述重写：内部自动调用 `task.resolve_identity` 计算 taskId/taskPath，composite 不再并列声明两者
  - `docs.sync_governance_entries` 范围收窄：仅同步 `docs/README.md` + `docs/INDEX.md`，**不再触达 `requirements.md`**（与 `docs.maintain_requirements_md` 严格互斥）
  - 明确 `layout.resolve_slot` 与 `layout.resolve_semantic` 互斥边界（业务语义层 vs 底层 slot 层）
  - 明确 `evidence.collect` → `evidence.emit_dual_format` 顺序约束（collect 只汇总不落盘，emit_dual_format 只序列化不收集）
- **`templates/composite-actions.yaml` 同步**：
  - `flow.requirement_activate` 移除 `task.resolve_identity`（已在 `task.activate_requirement` 内部调用）
  - `flow.task_archive_done` 与 `flow.version_closeout` 用 `docs.maintain_requirements_md` 替代 `docs.upgrade_run_results` + `docs.sync_requirements_md` 双调用
- **`config/route-registry.yaml`**：
  - 新增 `gt` route（experimental），绑定 CC Workflow executor
  - `aud` route 新增 `variants` 字段（`aud-cc` / `aud-trad`）
  - workflow 路径从项目级迁移到 `templates/cc-workflows/` 与 `templates/workflows/`
- **`templates/workflows/aud.workflow.yaml` 优化**：
  - 显式声明 9 维度配置（security / stability / architecture / quality / performance / testability / observability / maintainability / compatibility）
  - 3 维度为一批次并行执行（避免超出 API 并发 4 上限）
  - 加入 strict / soft 两种门控策略、单批超时、降级策略
  - 复核阶段支持全量复核（不再只复核 P1 / P2，问题较多时也全量）
- **`SKILL.md`**：增加 `gt` 路由、Executor 选择策略、aud 双变体说明、CC Workflow 副本目录引用。
- **`README.md`**：扩充内置 routes 表格（4 → 10）、补充 v0.2.0 重点变更说明、补充 CC Workflow 副本目录。
- **`templates/workflow.schema.yaml`**：在 `composite` 步骤类型下新增 **`conditional_required.notes_required_when`** 规则 —— 当 `composite.uses` 中的 action 在 `action-registry.yaml` 中存在 `usage_boundary` 且包含"内部调用/取代/合并/替代/互斥/顺序约束"关键词时，composite 必须填写非空 `notes` 字段。
- **`tests/` 契约测试**（v0.2.0 起）：
  - `tests/action-registry.test.js`（20 个用例）：覆盖 `docs.maintain_requirements_md`（mode: upgrade | sync | upgrade_then_sync）与 `config.manage_project`（mode: read | write）的 mode 切换契约、产物区分、副作用分流、互斥边界声明；同时新增"被合并的旧 action 必须消失"回归保护。
  - `tests/composite-actions.test.js`（10 个用例）：覆盖 `notes_required_when` 规则的端到端验证、`uses` 引用完整性、互斥边界（`docs.maintain_requirements_md` vs `docs.sync_governance_entries`、`layout.resolve_slot` vs `layout.resolve_semantic`），以及 v0.2.0 三项关键修复的回归保护。
  - 测试框架：Node.js v18+ 内置 `node:test`，无第三方依赖；`npm test` / `npm run test:verbose` 即可运行。
- **`package.json`**：新增 skill 级 `package.json`（name=`ll-workflow-core-tests`），声明 `test` / `test:registry` / `test:composite` / `test:verbose` 四个脚本，仅依赖 `js-yaml`。
- **测试接入 CI**（v0.2.0 后置建议落地）：
  - 新增 `.github/workflows/_workflow-core-check.yml`（可复用 workflow）：依次运行 `npm test` + 三副本 MD5 一致性校验 + `notes_required_when` 触发清单摘要。
  - 新增 `.github/workflows/workflow-core-ci.yml`（调用方）：监听 `.claude/skills/ll-workflow-core/**` 与 `skills/ll-workflow-core/**` 路径变更，对两套副本各触发一次可复用 workflow。
  - 新增根级辅助脚本：`scripts/test-workflow-core.mjs`（三副本并行跑测试 + MD5 一致性 + 触发清单摘要）、`scripts/test-workflow-core-copies.mjs`（独立 MD5 一致性检查器）。
  - 根 `package.json` 暴露 5 个 `test:workflow-core*` 脚本，便于本地一键验证。
- **`tests/composite-actions.test.js` 快照输出优化**：`识别所有触发 notes_required_when 的 composite（说明性快照）` 用例改为渲染对齐 ASCII 表格（`composite / 触发动作 / hasNotes` 三列），PR review 时一眼可见触发数量与缺失情况；缺失 `notes` 时打印 `⚠️` 警告并指向下方强制 test。
- **composite 自检清单文档化**：
  - `docs/workflow-schema.md` 新增 §6.2.1 `notes_required_when` 节，说明触发关键词表与示例 composite。
  - `docs/composite-actions.md` 新增 §5 composite 自检清单 + §6 触发清单表，§7 自检指引更新为指向新清单。
  - `CONTRIBUTING.md` 新增"针对 composite 扩展的最低要求（v0.2.0 起）"与"触发契约测试"两节，附 5 条 `npm run test:workflow-core*` 命令与 2 个新增 commit message 示例（`chore` / `test` 类型）。

### Removed

- `docs.upgrade_run_results` 基础动作（合并到 `docs.maintain_requirements_md`）
- `docs.sync_requirements_md` 基础动作（合并到 `docs.maintain_requirements_md`）
- `config.load_project_config` 基础动作（合并到 `config.manage_project`）
- `config.write_project_config` 基础动作（合并到 `config.manage_project`）
- 项目级 `audit-parallel.js` / `audit-continue.js`（v1 版本，已被 `audit-parallel-v2.js` 替代）

### Fixed

- `requirement.classify_type` outputs 字段的 YAML 缩进错误（曾导致 YAML 解析失败）
- `flow.requirement_activate` 重复声明 `task.resolve_identity` 与 `task.activate_requirement` 的冗余问题
- `config.manage_project` 的 `usage_boundary` 缺少与 `config.interactive_setup` / `config.merge_profiles` 的互斥声明（由 `composite-actions.test.js` 捕获后修复）
- `flow.requirement_develop` / `flow.task_expand_package` / `flow.task_implement` / `flow.audit_fix_cycle` 缺少 `notes` 字段（由 `notes_required_when` 规则捕获后补全，每个补充说明具体边界）

## [0.1.0] - 2026-04-13

### Added

- 新增仓库级 `README.md`，用于独立仓库场景下的总览、安装与使用入口。
- 新增 `LICENSE.txt`，补齐独立发布所需的许可文件。
- 新增 `docs/PUBLISHING.md`，说明从 monorepo 子目录提取并发布为独立 GitHub 仓库的步骤与检查清单。
- 新增 `CONTRIBUTING.md` 与 `.gitignore`，补齐基础仓库维护元信息。

### Changed

- `SKILL.md` 补充 license 元数据，并把仓库总览与发布指南纳入资源入口。
- `docs/README.md`、`docs/structure.md`、`config/resource-bundles.yaml`、`config/directory-layout.yaml` 的表述已去除明显母仓库来源依赖，提升独立发布可读性。
- `routes/req/templates/run-result-template.json` 中的仓库名改为占位符 `__REPO_NAME__`。
- `routes/init/templates/docs-tree.md` 与 `routes/dev/templates/docs-skeleton/README.md` 已统一到当前编号化 docs 结构。
- `routes/dev/references/dev06-docs-structure.md` 已替换为更中性的 `numbered-docs-structure.md`。

### Fixed

- `routes/dev/tools/init-docs-structure.ps1` 现在会生成与当前 skill 文档一致的编号化 docs 骨架，而不是旧版 `req/tech/proc` 结构。
- 初始化脚本生成的版本计划、专题模板、`docs/README.md` 与 `docs/INDEX.md` 路径说明已与新结构对齐。
