---
description: "用于统一 `run_result.json.blocking_reasons[*].code`，便于统计与自动化处理。"
update_time: 2026-07-01
---

# blocked 原因编码规范

用于统一 `run_result.json.blocking_reasons[*].code`，便于统计与自动化处理。

## 标准编码

- `SCHEMA_INVALID`：配置未通过 schema 校验
- `CONFIG_MISSING`：关键配置文件缺失
- `QUESTION_ABORTED`：`on_no_response=abort` 导致流程中止
- `TEST_FAIL`：测试未通过
- `FORMAT_FAIL`：格式或静态检查未通过
- `PERF_REGRESSION`：性能回归超过阈值
- `MERGE_GATE_FAIL`：C1 合并闸门整体失败
- `UNKNOWN_ERROR`：未分类异常

## 处理建议映射

| 编码 | 建议动作 | 是否可自动重试 |
|------|----------|----------------|
| `SCHEMA_INVALID` | 修复 `execution.yaml` 字段后重跑 | 否 |
| `CONFIG_MISSING` | 补齐缺失配置文件并重跑 | 否 |
| `QUESTION_ABORTED` | 调整 `mode.on_no_response` 或改回交互模式 | 否 |
| `TEST_FAIL` | 修复失败用例后重跑 C1 | 视情况 |
| `FORMAT_FAIL` | 先格式化/静态修复，再重跑 | 是 |
| `PERF_REGRESSION` | 调整实现或放宽阈值（需评审） | 否 |
| `MERGE_GATE_FAIL` | 按子项修复后重跑闸门 | 否 |
| `UNKNOWN_ERROR` | 落日志定位根因后分类归档 | 否 |

## 使用建议

1. 同一轮执行可记录多个原因编码。
2. 第一条原因建议写“主因”。
3. 保持编码稳定，新增编码需更新本文件与 `run_result_contract.md`。
