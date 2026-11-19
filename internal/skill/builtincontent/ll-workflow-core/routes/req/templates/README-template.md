---
description: "REQ-XXXX {任务名称}"
update_time: 2026-07-01
---
# REQ-XXXX {任务名称}

## 概述

{一两句话描述任务目标}

## 六件套

| 文件 | 必填 | 用途 |
| --- | --- | --- |
| [README.md](README.md) | ✅ | 任务总线（本文件） |
| [REQ-XXXX.md](REQ-XXXX.md) | ✅ | 需求文档（工作副本） |
| [REQ-XXXX-original.md](REQ-XXXX-original.md) | ✅ | 原始需求冻结副本 |
| [tasks.md](tasks.md) | ✅ | 任务拆解 |
| [design.md](design.md) | 简单可省 | 方案设计 |
| [decision_log.md](decision_log.md) | 无决策可省 | 决策留痕 |
| [run_result.json](run_result.json) | ✅ | 执行快照（供 req-sync.js 同步） |

> 简单任务（≤3 文件、无架构决策）可省略 `design.md` 和 `decision_log.md`。
> 但 `README.md`、`REQ-*.md`、`tasks.md`、`run_result.json` 始终不可省略。

## 关联

- {来源/关联文档链接，如无则写"无"}
