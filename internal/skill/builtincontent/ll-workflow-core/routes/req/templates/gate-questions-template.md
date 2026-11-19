---
description: "<!-- markdownlint-disable MD060 -->"
update_time: 2026-07-01
---

# gate-questions.md 模板

<!-- markdownlint-disable MD060 -->

## 1. 门控会话元信息

- 会话ID:
- 阶段: P0 / P1 / P2 / P3 / P4 / P5
- Gate: G0 / G1 / G2 / G3 / G4
- 轮次: {n}
- 模式: interactive / non_interactive / hybrid
- 执行人:
- 时间:

## 2. Gate Round {n}（每轮最多4问）

### Q1 目标确认（Goal Confirmation）

- 当前理解：
- A. 正确（推荐）
- B. 需修正：
- 用户选择：
- 影响范围：

### Q2 范围边界（Scope Boundary）

- A. 仅包含：{范围A}（推荐）
- B. 包含扩展：{范围B}
- 用户选择：
- 影响范围：

### Q3 验收标准（Acceptance Criteria）

- A. 采用 EARS + 可测指标（推荐）
- B. 仅文本描述（不推荐）
- 用户选择：
- 影响范围：

### Q4 风险偏好（Risk Preference）

- A. 保守（推荐）
- B. 激进
- 用户选择：
- 影响范围：

## 3. 用户回答与审计记录

- answer_summary:
- resolved_items:
- unresolved_items:
- conflict_items:
- assumptions_added:
- decision_refs: [DEC-xxx]
- evidence_refs: [EV-xxx]

## 4. 门控判定（本轮）

- gate_result: pass / continue / blocked / failed
- 判定理由:
- 是否进入下一轮: yes / no
- 下一动作:

## 5. 轮次汇总（阶段级）

| 轮次 | 已解决项数 | 未决项数 | 冲突项数 | 高风险冲突数 | 结果 |
|---|---|---|---|---|---|
| 1 | 0 | 0 | 0 | 0 | pass / continue / blocked / failed |

## 6. 超限与退出规则检查

- max_gate_rounds:
- max_total_rounds:
- max_conflict_retries:
- 当前是否超限: yes / no
- 若超限，退出状态建议: blocked / failed

## 7. 与 run_result 对齐字段

- phase_id:
- gate_id:
- rounds_used:
- phase_status: pass / blocked / failed / cancelled
- blocking_reasons:
- errors:

<!-- markdownlint-enable MD060 -->
