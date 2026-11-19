---
description: "run_result.json 标准 Schema 定义（REQ-7077）"
update_time: 2026-07-01
---
# run_result.json 标准 Schema

> 本文件定义任务包 `run_result.json` 的标准字段格式，供 `req-sync.js`（templates/scripts/req-sync/）脚本解析。

## 字段定义

### 必填字段

| 字段 | 类型 | 格式 | 说明 | 示例 |
| --- | --- | --- | --- | --- |
| `reqId` | string | `REQ-XXXX` | 需求编号 | `"REQ-7077"` |
| `title` | string | 自由文本 | 任务标题 | `"requirements.md 自动化管理工具"` |
| `priority` | string | `P0`/`P1`/`P2`/`P3` | 优先级 | `"P1"` |
| `version` | string | `dev_XX` | 目标版本 | `"dev_09"` |
| `status` | string | `pending`/`in_progress`/`done` | 当前状态。已完成任务**推荐用 `done`**（`completed` 为旧版值，效果等同） | `"done"` |
| `createdAt` | string | `YYYY-MM-DD` | 创建日期 | `"2026-04-28"` |

### 可选字段

| 字段 | 类型 | 格式 | 说明 | 示例 |
| --- | --- | --- | --- | --- |
| `completedAt` | string \| null | `YYYY-MM-DD` 或 null | 完成日期 | `null` |
| `source` | string | 自由文本 | 来源层 | `"安全审计 H1"` |
| `note` | string | 自由文本 | 备注 | `"7 条路由 Zod 校验"` |
| `e2e_scope` | string[] | 模块名数组（引用 `tests/e2e/modules.json`） | 选择性 E2E 模块范围。`["all"]` 表示全量；`["smoke"]` 仅冒烟；`[]` 或缺失跳过 | `["upload"]` |
| `e2e_note` | string | 自由文本 | e2e_scope 选择理由 | `"仅涉及上传模块的命名校验逻辑"` |

## Status 枚举

标准值（3 种）：

| 值 | 含义 | 对应 requirements.md 状态 | 说明 |
| --- | --- | --- | --- |
| `pending` | 待办 | 📋 待办 | 新任务默认值 |
| `in_progress` | 进行中 | 🚧 进行中 | |
| `done` | 已完成 | ✅ 已完成 | **推荐**，v11 起统一使用，可触发归档 |

旧版兼容值（效果等同 `done`，保留仅用于兼容历史数据）：

| 值 | 说明 |
| --- | --- |
| `completed` | 旧版标准值，v10 及之前广泛使用，仍可触发归档 |
| `pass` | v06 早期遗留，STATUS_MAP 映射为 ✅ 已完成 |
| `implemented` | v07 早期遗留，STATUS_MAP 映射为 ✅ 已完成 |

其他兼容值：

| 值 | 等价标准值 | 说明 |
| --- | --- | --- |
| `in-progress` | `in_progress` | 历史遗留，统一用下划线 |
| `todo` | `pending` | 历史遗留，新任务包禁止使用 |
| `paused`, `cancelled`, `terminated` | 保留在 STATUS_MAP 中 | 仅暂停/取消的任务使用 |

> `req-sync archive` 判断逻辑：`["done", "completed"].includes(rr.status)`

## 日期字段说明

- `createdAt`：任务包创建日期，对应 requirements.md 的"开始日期"
- `completedAt`：任务完成日期，对应 requirements.md 的"完成日期"；未完成时为 null

## 完整示例

```json
{
  "reqId": "REQ-7077",
  "title": "requirements.md 自动化管理工具",
  "priority": "P1",
  "version": "dev_09",
  "status": "done",
  "createdAt": "2026-04-28",
  "completedAt": "2026-04-28",
  "source": "执行总线",
  "note": "req-sync.js 开发完成"
}
```
