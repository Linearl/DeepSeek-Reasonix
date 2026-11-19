---
description: "```json"
update_time: 2026-07-01
---

# run_result.json Schema

> run_result.json 是 req-sync.js（templates/scripts/req-sync/）的数据源，标准字段如下。
>
> **v0.2.2 扩充**：新增 `requirementStatus / reqType / categoryCode / complexity / skipFlags / gateRounds / decisionPoints` 字段。
> **v0.2.3 同步**：`run_result.schema.json` 已补齐上述字段到 JSON Schema（`properties` 中新增 7 个字段定义）。

## 核心字段

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `reqId` | string | 是 | 需求编号，格式 REQ-#### |
| `title` | string | 是 | 需求中文标题（名词性短语，不加动词） |
| `priority` | string | 是 | P0/P1/P2/P3 |
| `version` | string | 是 | 目标版本号，如 dev_11 |
| `status` | string | 是 | pending/in_progress/done/paused/cancelled |
| `createdAt` | string | 是 | YYYY-MM-DD |
| `completedAt` | string/null | 否 | 完成日期，null 表示未完成 |
| `source` | string | 否 | 需求来源说明 |
| `note` | string | 否 | 备注，允许空字符串 |

## v0.2.2 扩充字段

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `requirementStatus` | string | 否 | 需求轴状态，枚举 draft/review/approved/active/done（与 workflow-governance.yaml.allowed_pairs 对齐） |
| `reqType` | string | 否 | 需求类型（feature/bugfix/refactor/ops/docs/test） |
| `categoryCode` | number | 否 | 编号段首位数字（1-9） |
| `complexity` | string | 否 | 复杂度（simple/medium/complex） |
| `skipFlags` | object | 否 | `{clarification: bool, design: bool}` |
| `gateRounds` | number | 否 | 澄清门控已使用的轮数 |
| `decisionPoints` | array | 否 | 关键决策点列表，每项含 `{decisionPoint, choice, reason}` |

## 示例

```json
{
  "reqId": "REQ-2000",
  "title": "{需求标题示例}",
  "priority": "P1",
  "version": "dev_11",
  "status": "done",
  "createdAt": "2026-04-07",
  "completedAt": "2026-04-08",
  "source": "v11 需求规格",
  "note": "",
  "requirementStatus": "approved",
  "reqType": "feature",
  "categoryCode": 2,
  "complexity": "medium",
  "skipFlags": { "clarification": false, "design": false },
  "gateRounds": 2,
  "decisionPoints": [
    {
      "decisionPoint": "requirement_mode",
      "choice": "interactive",
      "reason": "用户选择交互模式"
    }
  ]
}
```
