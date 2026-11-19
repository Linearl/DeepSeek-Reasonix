---
description: "本文档说明 `config/execution.yaml` 的字段含义、默认值与生效顺序。"
update_time: 2026-07-01
---

# execution.yaml 配置说明

本文档说明 `config/execution.yaml` 的字段含义、默认值与生效顺序。

## 1) 生效顺序

决策优先级（从高到低）：

1. 用户显式指令
2. `config/execution.yaml`
3. 技能内置默认值

若某字段缺失，自动回退到内置默认值，并建议在 `decision_log.md` 记录来源为 `system_default`。

---

## 2) 默认行为

- 默认 `mode.non_interactive: false`（交互式）
- 默认开启 `ask_questions`（`interactive.questions_enabled: true`）
- 默认无响应策略 `use_default`

### 非交互判定规则（重要）

- 不使用“夜间时间段”自动推断。
- 仅依据 `mode.non_interactive` 判定执行模式。
- `mode.non_interactive=true` 时，所有 A0/A3/B0/B2 决策均走配置默认路径，并写入 `decision_log.md`。

> 注意：`ask_questions` 本身无 timeout 参数。`mode.wait_seconds` 仅供外层编排器实现“超时降级”。

---

## 3) 字段说明

### mode

- `non_interactive`：是否非交互执行。
- `on_no_response`：无响应策略。
  - `use_default`：用 defaults 继续。
  - `abort`：中止流程并标记 blocked。
- `wait_seconds`：外层等待秒数。

### defaults

- `audit_scope`：`full` / `incremental`
- `review_rounds`：1~3
- `use_audit_branch`：是否默认建审计分支

### quality.merge_gate

- `enabled`：是否启用合并闸门
- `max_perf_regression_pct`：性能回归阈值（%）

### logging.decision_log

- `enabled`：是否记录关键决策
- `path`：决策日志路径

### skip.debt_backlog

- `enabled`：是否生成技术债台账
- `sla`：P1~P4 建议处理窗口

### branch

- `name_pattern`：分支命名模板（支持 `{date}`）

### interactive

- `questions_enabled`：交互模式下是否启用问答

### commit

- `enforce_template`：是否强制提交信息模板
- `template`：模板字符串

---

## 4) 推荐配置示例

### 交互式（推荐）

```yaml
mode:
  non_interactive: false
  on_no_response: use_default
  wait_seconds: 90
```

### 非交互（CI/夜间）

```yaml
mode:
  non_interactive: true
  on_no_response: use_default
  wait_seconds: 60

defaults:
  audit_scope: incremental
  review_rounds: 1
  use_audit_branch: true
```

---

## 5) 落地建议

1. 在流程开始时先读取配置，输出“生效配置摘要”。
2. 每个关键决策（A0/A3/B0/B2）写入 `decision_log.md`。
3. 若 C1 闸门不通过，C2 只能输出“未完成（待处理）”。
4. 建议先用 `execution.schema.json` 校验配置合法性。
5. 每次运行落盘 `effective_config.yaml` 与 `run_result.json`。

可选工具：
- 校验脚本：`scripts/validate_execution_config.py`
- 校验脚本单测：`scripts/test_validate_execution_config.py`
- CI模板：`docs/ci_task_template.md`
- GitHub Actions 模板：`templates/github_actions_non_interactive.yml`
- 结果契约：`docs/run_result_contract.md`
- 结果 Schema：`config/run_result.schema.json`

---

## 6) 非交互一次性执行（推荐顺序）

1. 读取 `execution.yaml` 并用 `execution.schema.json` 校验
2. 输出 `effective_config.yaml`（生效配置快照）
3. A0 直接采用 `defaults.audit_scope`
4. A3 直接采用 `defaults.review_rounds`
5. B0 直接采用 `defaults.use_audit_branch`
6. B2 根据 `mode.on_no_response` 决定继续或中止
7. 执行 C1 闸门校验，`blocked` 时停止 C2 合并动作
8. 输出报告 + `decision_log.md` + `debt_backlog.md`（如有跳过）
9. 输出 `run_result.json`（机器可读执行结果）
