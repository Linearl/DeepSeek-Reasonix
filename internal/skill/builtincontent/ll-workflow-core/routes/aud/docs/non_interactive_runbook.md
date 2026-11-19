---
description: "本手册用于在无人值守场景下，按一次性流程执行 `ll-audit`。"
update_time: 2026-07-01
---

# 非交互运行手册（CI / 夜间）

本手册用于在无人值守场景下，按一次性流程执行 `ll-audit`。

## 1. 前提

- `config/execution.yaml` 已存在
- `mode.non_interactive: true`
- 已确认默认策略满足当前审计目标

## 2. 推荐配置（示例）

```yaml
mode:
  non_interactive: true
  on_no_response: use_default
  wait_seconds: 60

defaults:
  audit_scope: incremental
  review_rounds: 1
  use_audit_branch: true

quality:
  merge_gate:
    enabled: true
    max_perf_regression_pct: 5
```

## 3. 执行顺序

1. 读取配置并用 `execution.schema.json` 校验
2. 输出 `effective_config.yaml`（生效配置摘要快照）
3. 阶段A：
   - A0 使用 `defaults.audit_scope`
   - A1 按规模并行扫描
   - A2 生成报告
   - A3 使用 `defaults.review_rounds`
4. 阶段B：
   - B0 使用 `defaults.use_audit_branch`
   - B1 创建工作目录
   - B2 按 `mode.on_no_response` 处理无响应路径
5. 阶段C：
   - C1 闸门校验（测试/格式/性能阈值）
   - C2 汇总归档（仅在闸门通过时允许合并）
6. 输出 `run_result.json`（机器可读结果）
7. 用 `config/run_result.schema.json` 校验 `run_result.json` 结构

## 4. 必要产物

- `effective_config.yaml`（本次运行生效配置）
- 审计报告（`docs/1.task/B.todo/{version}/YYYYMMDD_audit-fix/README.md`）
- `decision_log.md`（必须）
- `skipped/skip_log.md`（如有跳过）
- `skipped/debt_backlog.md`（如有跳过）
- `run_result.json`（必须）

## 5. 失败处理

- 若 `mode.on_no_response=abort`：出现关键决策无交互输入时立即停止。
- 若 C1 闸门失败：结论输出为 `blocked`，禁止进入“已完成/可合并”。

## 6. 追溯建议

- 在报告“基本信息”中记录：执行模式、配置文件路径、C1闸门状态
- 在 `decision_log.md` 中记录决策来源（`config_default` / `system_default`）

相关规范：

- `docs/run_result_contract.md`
- `docs/block_reason_codes.md`
- `config/run_result.schema.json`
- `templates/github_actions_non_interactive.yml`
