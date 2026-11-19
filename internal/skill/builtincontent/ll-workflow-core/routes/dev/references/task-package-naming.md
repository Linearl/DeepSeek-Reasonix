---
description: "格式：`v{version}-r{req_id}-p{priority}-{slug}`"
update_time: 2026-07-01
---

# 项目任务包命名规范与六件套体系

> 适用范围：所有通过 ll-workflow-core dev/branch 路由创建的任务包

---

## 1. 目录命名

格式：`v{version}-r{req_id}-p{priority}-{slug}`

| 段 | 说明 | 示例 |
|---|------|------|
| `v{version}` | 版本号 | `v11` |
| `r{req_id}` | 需求编号（4 位） | `r7195` |
| `p{priority}` | 优先级 | `p1` / `p2` / `p3` |
| `{slug}` | 短横线分隔的英文描述 | `merge-flow-comprehensive-fix` |

完整示例：`v11-r7195-p1-merge-flow-comprehensive-fix`

## 2. 六件套文件清单（标准任务包）

| 文件 | 必填 | 说明 |
|------|------|------|
| `README.md` | ✅ | 任务总线，含概述、结构、状态、执行清单 |
| `REQ-{id}-{slug}.md` | ✅ | 需求工作副本（持续更新） |
| `REQ-{id}-{slug}-original.md` | ✅ | 需求原始冻结副本 |
| `tasks.md` | ✅ | 任务拆解（含逐项展开、DoD、验证步骤） |
| `design.md` | 简单可省 | 方案设计（含接口、数据模型、异常处理） |
| `decision_log.md` | 无决策可省 | 决策留痕 |
| `run_result.json` | ✅ | 执行快照（供 req-sync.js 同步） |

**简单任务**（≤3 文件、无架构决策）可省略 `design.md` 和 `decision_log.md`。
但 `REQ-*.md`、`tasks.md`、`run_result.json` 始终不可省略。

## 3. 文件命名规范

- `README.md` — 固定名为 README.md
- `REQ-{req_id}-{slug}.md` — 需求工作副本，slug 与目录名描述段一致
- `REQ-{req_id}-{slug}-original.md` — 原始冻结副本，后缀 `-original`
- `tasks.md` — 固定名
- `design.md` — 固定名
- `decision_log.md` — 固定名
- `run_result.json` — 固定名

## 4. run_result.json 必填字段

```json
{
  "reqId": "REQ-7195",
  "title": "中文标题",
  "priority": "P1",
  "version": "dev_XX",
  "status": "done",
  "createdAt": "2026-05-27",
  "completedAt": null,
  "source": "审计报告 / 需求文档",
  "note": ""
}
```

## 5. 模板位置

六件套模板位于项目根目录 `docs/1.task/template/`。
技能镜像位于 `routes/req/templates/` 和 `routes/dev/templates/`。

> **注意**：docs 已从版本目录 `02-dev/dev_XX/docs/` 迁移至仓库根目录 `docs/`。

## 6. 编号分类规则（4 位）

任务编号首位数字表示类别，遵循 `requirements.md` §2.1：

| 编号段 | 类别 |
|---|------|
| 1000-1999 | 架构和基础设施任务 |
| 2000-2999 | 核心功能开发任务 |
| 3000-3999 | 用户界面和体验任务 |
| 4000-4999 | 测试和质量保证任务 |
| 5000-5999 | 文档和规范任务 |
| 6000-6999 | 运维和部署任务 |
| 7000-7999 | 技术债务和重构任务 |
| 8000-8999 | 研究和调研任务 |
| 9000-9999 | 未分类和临时任务 |

**禁止**将文档修复任务放入 7xxx 段（应归 5xxx）。
**禁止**将部署/运维任务放入 7xxx 段（应归 6xxx）。
**禁止**将测试任务放入 7xxx 段（应归 4xxx）。

查询下一个可用编号：`node .claude/skills/ll-workflow-core/templates/scripts/req-sync.js next --category <N>`
