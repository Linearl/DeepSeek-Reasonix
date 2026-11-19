---
description: "六件套任务包模板 — 标准目录结构与文件模板"
update_time: 2026-07-01
---
# 六件套任务包模板

> 从 `A.inactive/` 激活需求时，复制本目录模板到 `B.todo/{version}/{task-id}/`，按结构补齐文件。
>
> 命名规范：`{version}-r{REQ 编号}-{优先级}-{功能简述}/`
> 示例：`v09-r2022-p1-upload-auto-purpose-from-endpoint/`

## 目录结构

```text
B.todo/{version}/{task-id}/
├── README.md              # ① 任务总线   ← README-template.md
├── REQ-XXXX-original.md   # ② 需求冻结副本（从工作副本复制后冻结）
├── REQ-XXXX.md            # ③ 需求工作副本 ← REQ-template.md（若 A.inactive 无原稿）
├── tasks.md               # ④ 任务拆解   ← tasks-template.md（标准）/ tasks-template-complex.md（复杂）
├── design.md              # ⑤ 方案设计   ← design-template.md
├── decision_log.md        # ⑥ 决策留痕   ← decision_log-template.md（按需创建）
└── run_result.json        # ⑦ 执行快照   ← run_result.json.template
```

## 可用模板一览

| 文件 | 模板 | 说明 |
| --- | --- | --- |
| README.md | `README-template.md` | 任务总线：概述+结构+状态+执行清单 |
| REQ-XXXX.md | `REQ-template.md` | 需求文档：元信息+背景+范围+详情+验收+风险+变更 |
| REQ-XXXX-original.md | 从工作副本复制 | 激活时从 REQ-XXXX.md 复制，此后冻结 |
| tasks.md | `tasks-template.md`（默认） | 标准版：总表+逐项展开+DoD+依赖+验证+执行记录 |
| tasks.md | `tasks-template-complex.md` | 复杂版：标准版+里程碑+风险矩阵+门禁 |
| design.md | `design-template.md` | 方案设计：概述+决策+实现+接口+数据模型+异常 |
| decision_log.md | `decision_log-template.md` | 决策留痕：D-XX 编号，含备选方案。AI 自主决策可省略，用户参与决策则必须 |
| run_result.json | `run_result.json.template` | 执行快照，供 req-sync.js 同步。schema 见 `run_result.json-schema.md` |

## tasks.md 选择决策

默认用标准版。满足以下 **2+ 项**时切到复杂版：

- 子任务 ≥ 10 个
- 涉及 ≥ 3 个模块/层级
- 有明确的多阶段里程碑
- 存在可识别的风险敞口（数据迁移、破坏性变更）

---

## 附录：命名规范

### 目录命名

```text
{version}-r{REQ编号}-{优先级}-{功能简述}
```

| 组成部分 | 格式 | 示例 |
| --- | --- | --- |
| version | `v{大版本}` | `v09` |
| REQ 编号 | 4 位数字 | `2022` |
| 优先级 | `p0`/`p1`/`p2` | `p1` |
| 功能简述 | kebab-case | `upload-auto-purpose` |

### frontmatter description 格式

| 文件 | description 格式 |
| --- | --- |
| README.md | `"REQ-XXXX {任务名称}任务总线"` |
| REQ-XXXX.md | `"REQ-XXXX {任务名称}"` |
| REQ-XXXX-original.md | `"REQ-XXXX {任务名称} — 原始需求（冻结副本）"` |
| tasks.md | `"REQ-XXXX 任务拆解"` |
| design.md | `"REQ-XXXX 方案设计"` |
| decision_log.md | `"REQ-XXXX 决策留痕"` |
| run_result.json | 无需 frontmatter |
