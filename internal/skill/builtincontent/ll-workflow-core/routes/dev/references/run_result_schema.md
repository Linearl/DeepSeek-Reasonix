---
description: "`req-sync.js` 兼容以下历史 ID 字段名："
update_time: 2026-07-01
---

# run_result.json 字段参考

> 状态字面值见 `templates/scripts/req-sync/utils.js` 的 `LIFECYCLE_STATUS_MAP`（SSOT）。

> 任务包 `run_result.json` 的标准字段定义，供 `req-sync.js`（templates/scripts/req-sync/）和 `upgrade-run-result.mjs` 解析。

## 必填字段

| 字段 | 类型 | 格式 | 说明 | 示例 |
|------|------|------|------|------|
| `reqId` | string | `REQ-XXXX` | 需求编号 | `"REQ-7001"` |
| `title` | string | 中文自由文本 | 任务标题 | `"requirements.md 自动化管理工具"` |
| `priority` | string | `P0`/`P1`/`P2`/`P3` | 优先级 | `"P1"` |
| `version` | string | `dev_XX` | 目标版本 | `"dev_09"` |
| `status` | string | 见枚举表 | 当前状态 | `"in_progress"` |
| `createdAt` | string | `YYYY-MM-DD` | 创建日期 | `"2026-04-28"` |

## 可选字段

| 字段 | 类型 | 格式 | 说明 | 示例 |
|------|------|------|------|------|
| `completedAt` | string \| null | `YYYY-MM-DD` | 完成日期 | `null` |
| `source` | string | 自由文本 | 来源层（诊断/审计/需求） | `"安全审计 H1"` |
| `note` | string | 自由文本 | 备注/说明 | `"合并 REQ-2003（{需求描述}）"` |
| `tasks` | object | `{total, completed, pending}` | 子任务统计 | `{"total":5,"completed":3,"pending":2}` |

## Status 枚举

| 值 | 含义 | requirements.md 映射 |
|----|------|---------------------|
| `completed` / `done` / `pass` / `implemented` | 已完成 | ✅ 已完成 |
| `in_progress` / `in-progress` | 进行中 | 🚧 进行中 |
| `pending` / `todo` | 待办 | 📋 待办 |
| `paused` | 暂停 | ⏸️ 暂停 |
| `cancelled` / `terminated` | 已终止 | 🛑 已终止 |

## 兼容性说明

`req-sync.js` 兼容以下历史 ID 字段名：

| 历史字段 | 处理方式 |
|---------|---------|
| `reqId` | 直接使用 |
| `requirementId` | 去掉 `REQ-` 前缀后使用 |
| `taskId` | 去掉 `REQ-` 前缀后使用 |

日期字段按优先级查找：`createdAt` > `date` > `started_at`；`completedAt` > `ended_at` > `updatedAt`。

## 完整示例

```json
{
  "reqId": "REQ-7077",
  "title": "requirements.md 自动化管理工具",
  "priority": "P1",
  "version": "dev_09",
  "status": "completed",
  "createdAt": "2026-04-28",
  "completedAt": "2026-04-28",
  "source": "执行总线",
  "note": "req-sync.js 开发完成，支持 5 个子命令",
  "tasks": { "total": 9, "completed": 9, "pending": 0 }
}
```

## 历史别名条目

当旧编号需求被"内化"到新需求时，可创建历史别名任务包：

```json
{
  "reqId": "REQ-2002",
  "title": "筛选清单导出",
  "priority": "P1",
  "version": "dev_08",
  "status": "completed",
  "createdAt": null,
  "completedAt": null,
  "source": "",
  "note": "历史别名，已内化至 REQ-2006（{需求描述}）"
}
```

关键约定：
- `status` 设为 `completed`，确保在 requirements.md 中显示为 ✅ 已完成
- `note` 标注"历史别名，已内化至 REQ-XXXX"，指明去向
- createdAt/completedAt 可为 null（历史数据不追溯日期）
