---
description: "docs 根目录说明与使用规范"
update_time: 2026-07-01
---
# docs 目录使用规范

> **新项目复制本模板后，必须阅读本文档。**

## 目录结构总览

```text
docs/
├── 0.version_plan/           # 版本计划与里程碑
│   └── template/             # 版本计划模板
├── 1.task/                   # 任务管理（需求生命周期）
│   ├── A.inactive/           # 未激活需求
│   ├── B.todo/               # 待执行任务
│   ├── B.1.paused/           # 暂停任务
│   ├── B.2.done/             # 已完成任务
│   ├── B.3.cancelled/        # 已取消任务
│   ├── 六件套/               # 任务包六件套示例
│   ├── audit-package/        # 审计任务包示例
│   └── template/             # 任务模板（六件套、审计包、todolist）
├── 2.tech/                   # 技术文档
│   ├── api/                  # API 接口文档
│   ├── architecture/         # 架构设计文档
│   ├── database/             # 数据库设计文档
│   ├── development/          # 开发文档
│   │   ├── standards/        # 编码规范
│   │   ├── templates/        # 开发模板
│   │   ├── tools/            # 工具使用说明
│   │   └── workflow/         # 工作流规范
│   ├── guide/                # 操作指南
│   ├── specification/        # 技术规范（含 html 规范）
│   ├── user_guide/           # 用户指南
│   │   ├── basic/            # 基础功能
│   │   ├── data/             # 数据管理
│   │   ├── deployment/       # 部署指南
│   │   ├── fiftyone/         # 数据可视化工具
│   │   ├── outbound/         # 数据导出
│   │   ├── testing/          # 测试相关
│   │   ├── upload/           # 上传功能
│   │   └── user/             # 用户管理
│   └── template/             # 技术文档模板
├── 3.analysis/               # 分析文档（三层模型）
│   ├── evidence/             # 原始证据（仅事实）
│   ├── diagnosis/            # 事前分析
│   │   ├── 01-active/        # 活跃诊断文档
│   │   └── 02-outdated/      # 已归档诊断文档
│   ├── report/               # 事后分析
│   │   └── template/         # 报告模板
│   └── template/             # 分析文档模板
├── 4.misc/                   # 杂项文档
│   └── template/             # 杂项模板
├── 5.git-commit/             # Git 提交规范与记录
│   └── template/             # 提交模板
├── 6.changelog/              # 变更日志
│   └── template/             # 变更日志模板
├── 7.weekly/                 # 周报与经验回灌
│   └── template/             # 周报模板
├── 8.test/                   # 测试资产
│   ├── case/                 # 测试用例
│   │   ├── e2e/              # E2E 测试用例
│   │   └── regression/       # 回归测试用例
│   ├── guide/                # 测试指南
│   ├── reports/              # 测试报告
│   │   ├── integration/      # 集成测试报告
│   │   └── manual/           # 手动测试报告
│   ├── scripts/              # 测试脚本
│   └── template/             # 测试模板
├── todolist/                 # 待办清单（非正式任务）
├── INDEX.md                  # 自动生成的文档索引
└── README.md                 # 本文件
```

## 核心原则

### 1. 编号优先

所有一级目录使用数字编号（0-8），确保排序稳定：
- `0.version_plan` → 版本规划（最先查看）
- `1.task` → 任务执行（核心）
- `2.tech` → 技术沉淀
- ...以此类推

### 2. 三层模型（3.analysis）

分析类文档采用三层模型：
- **evidence/**：原始证据（仅事实，不放结论）
- **diagnosis/**：事前分析（调研、现状、风险）
- **report/**：事后分析（复核、收口、验收）

### 3. 任务生命周期（1.task）

任务按状态分目录：
- **A.inactive/**：未激活需求
- **B.todo/**：待执行任务
- **B.1.paused/**：暂停任务
- **B.2.done/**：已完成任务
- **B.3.cancelled/**：已取消任务

### 4. 二级/三级目录使用规范

| 目录 | 用途 | 文件类型 |
| --- | --- | --- |
| **2.tech/api/** | REST API 接口文档、请求/响应示例 | 接口定义、变更记录 |
| **2.tech/architecture/** | 系统架构设计、模块划分、技术选型 | 架构图、ADR、设计决策 |
| **2.tech/database/** | 数据库表结构、迁移脚本、ER 图 | DDL、迁移记录、数据字典 |
| **2.tech/development/standards/** | 编码规范、命名约定、代码风格 | 规范文档、检查清单 |
| **2.tech/development/templates/** | 代码模板、脚手架配置 | 模板文件、配置示例 |
| **2.tech/development/tools/** | 开发工具使用说明、调试技巧 | 工具文档、FAQ |
| **2.tech/development/workflow/** | 开发工作流、分支策略、发布流程 | 流程文档、规范说明 |
| **2.tech/guide/** | 操作指南、快速上手、常见问题 | 分步指南、FAQ |
| **2.tech/specification/** | 技术规范、协议定义、数据格式 | 规范文档、示例 |
| **2.tech/user_guide/basic/** | 基础功能使用说明 | 用户手册、操作步骤 |
| **2.tech/user_guide/data/** | 数据管理相关功能文档 | 数据导入/导出/查询指南 |
| **2.tech/user_guide/deployment/** | 部署安装、环境配置 | 部署手册、配置说明 |
| **2.tech/user_guide/fiftyone/** | 数据可视化工具使用 | 操作指南、示例 |
| **2.tech/user_guide/outbound/** | 数据导出功能文档 | 导出格式、操作步骤 |
| **2.tech/user_guide/testing/** | 测试功能相关文档 | 测试指南、用例说明 |
| **2.tech/user_guide/upload/** | 文件上传功能文档 | 上传规范、限制说明 |
| **2.tech/user_guide/user/** | 用户管理、权限控制 | 用户手册、权限说明 |
| **3.analysis/evidence/** | 原始证据数据（仅事实，不含结论） | 日志、截图、数据导出 |
| **3.analysis/diagnosis/01-active/** | 活跃诊断文档（待处理问题） | 诊断报告、分析文档 |
| **3.analysis/diagnosis/02-outdated/** | 已归档诊断文档（问题已解决） | 历史诊断、归档记录 |
| **3.analysis/report/template/** | 事后分析报告模板 | 报告模板、验收标准 |
| **4.misc/template/** | 杂项文档模板 | 通用模板 |
| **5.git-commit/template/** | Git 提交信息模板 | commitlint 配置、模板 |
| **6.changelog/template/** | 变更日志模板 | changelog 格式、示例 |
| **7.weekly/template/** | 周报模板 | 周报格式、经验回灌模板 |
| **8.test/case/e2e/** | E2E 测试用例（Playwright） | 测试脚本、测试数据 |
| **8.test/case/regression/** | 回归测试用例 | 测试脚本、检查清单 |
| **8.test/guide/** | 测试指南、环境配置 | 测试手册、环境说明 |
| **8.test/reports/integration/** | 集成测试报告 | 测试结果、覆盖率报告 |
| **8.test/reports/manual/** | 手动测试报告 | 测试记录、缺陷清单 |
| **8.test/scripts/** | 测试辅助脚本 | 脚本文件、配置 |
| **8.test/template/** | 测试文档模板 | 测试报告模板、用例模板 |

### 5. "路牌"式规则文档

每个关键目录放置规则文档，使用明确标题：
- `任务包六件套构成-编号分类规则-自动归档机制.md`
- `01-docs 目录作用与命名规范.md`
- `02-审计任务包模板说明.md`

## 目录内文件分类

每个子目录下有两类文件：

### 说明文件（长期维护，不复制后修改）

文件名使用描述性"路牌"命名，从标题即可看出内容：

| 目录 | 说明文件名 | 内容 |
| --- | --- | --- |
| 0.version_plan/ | `版本计划目录结构与模板使用说明.md` | 目录用途、模板使用方法 |
| 1.task/ | `任务管理目录结构与生命周期.md` | 目录结构、状态枚举、命名规范 |
| 1.task/ | `任务包六件套构成-编号分类规则-自动归档机制.md` | 六件套构成、编号规则、归档机制 |
| 2.tech/ | `技术文档分类与编写规范.md` | 文档分类、编写标准 |
| 3.analysis/ | `分析文档分类与三层模型.md` | 三层模型、文档分类 |
| 8.test/ | `测试文档目录结构与编写规范.md` | 测试资产组织方式 |

### 模板文件（复制后修改，存放在 template/ 子目录）

每个子目录的 `template/` 下存放可复制的模板文件：

```text
1.task/template/
├── 六件套/          # 任务包六件套模板
│   ├── README-template.md
│   ├── REQ-template.md
│   ├── tasks-template.md
│   └── run_result.json.template
├── audit-package/   # 审计任务包模板
└── todolist.md
```

## 文件命名规范

| 类型 | 格式 | 示例 |
| --- | --- | --- |
| 说明文件 | `{描述性名称}.md` | `任务包六件套构成-编号分类规则-自动归档机制.md` |
| 模板文件 | `{名称}.md` 或 `{名称}-template.md` | `template/version-plan.md` |
| 规范 | `{序号}-{名称}规范.md` | `01-docs 目录作用与命名规范.md` |
| 报告 | `{日期}-{主题}.md` | `2026-04-01-架构整改报告.md` |
| 任务包 | `v{版本}-r{编号}-p{优先级}-{简称}/` | `v11-r1001-p1-四件套治理/` |

## 维护规则

1. **新增文档前先查 INDEX.md**：了解现有结构
2. **文档必须有 frontmatter**：包含 `description` 字段
3. **使用相对路径引用**：便于迁移
4. **定期同步索引**：执行 `node scripts/generate-docs-index.js --apply`

## 关联规范

- [2.tech/development/workflow/01-docs 目录作用与命名规范.md](./2.tech/development/workflow/)
