---
description: "ask_questions 标准片段库 文件说明"
update_time: 2026-07-01
---

# ask_questions 标准片段库

> 用途：统一 A0 / A3 / B0 / B2 的提问结构，减少执行歧义。

---

## A0 审计范围选择

- **header**: `审计范围`
- **question**: `本次审计采用哪种范围？全量适合首轮或发布前，增量适合日常迭代。`
- **multiSelect**: `false`
- **options**:
  - `全量审计（推荐首轮）` ✅ recommended
  - `增量审计（基于Git变更）`

---

## A3 复核轮数选择

- **header**: `复核轮数`
- **question**: `请选择交叉复核轮数。轮数越高越稳妥，但耗时更长。`
- **multiSelect**: `false`
- **options**:
  - `1轮（快速）`
  - `2轮（标准）` ✅ recommended
  - `3轮（严格）`

---

## B0 审计分支选择

- **header**: `分支策略`
- **question**: `修复阶段是否启用审计分支？建议启用以隔离风险并集中验收。`
- **multiSelect**: `false`
- **options**:
  - `启用审计分支（audit/YYYY-MM-DD）` ✅ recommended
  - `不启用，直接在当前分支修复`

---

## B2 优先级处理策略

- **header**: `级别处理`
- **question**: `当前优先级问题如何处理？`
- **multiSelect**: `false`
- **options**:
  - `继续修复该级别所有问题` ✅ recommended
  - `选择性修复（逐个确认）`
  - `跳过整个级别`
  - `结束修复`

---

## B2 批量级别选择（可选，多选）

- **header**: `批量级别`
- **question**: `请选择本轮要处理的优先级（可多选）。`
- **multiSelect**: `true`
- **options**:
  - `P1`
  - `P2`
  - `P3`
  - `P4`

> 说明：该片段用于“可叠加决策”，结果应按 `P1→P2→P3→P4` 顺序串行处理。

---

## C1 验证项选择（可选，多选）

- **header**: `验证项`
- **question**: `请选择本次需要执行的验证项（可多选）。`
- **multiSelect**: `true`
- **options**:
  - `测试（tests）` ✅ recommended
  - `格式检查（format）` ✅ recommended
  - `性能回归对比（performance）`
  - `结果Schema校验（run_result schema）` ✅ recommended

---

## B2 跳过原因采集

- **header**: `跳过原因`
- **question**: `请填写跳过原因（将写入 skip_log 和 decision_log 供后续追踪）。`
- **allowFreeformInput**: `true`

---

## 记录约束

- 每次关键选择后必须追加到 `decision_log.md`
- 记录字段：时间、阶段、决策点、选择、默认是否触发、决策来源、备注
- 如用户无响应且进入非交互模式，必须记录“使用默认策略”
- 多选结果建议格式：`["P1","P2"]` 或 `P1,P2`

---

## 与 execution.yaml 的协同规则

- 配置文件：`config/execution.yaml`
- 优先级：用户显式指令 > execution.yaml > 技能内置默认值
- `mode.non_interactive: true` 时，跳过提问并使用 `defaults.*`
- `interactive.questions_enabled: false` 时，按非交互路径执行
- `mode.on_no_response`：
  - `use_default`：使用默认值继续
  - `abort`：终止并标记 blocked
- 使用默认值时，`decision_log.md` 中 `默认是否触发=是`、`决策来源=config_default`
