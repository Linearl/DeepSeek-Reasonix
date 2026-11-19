---
description: "1. 每个决策点只追加，不覆盖历史记录。"
update_time: 2026-07-01
---

# 决策日志（decision_log.md）

> 记录 A0 / A3 / B0 / B2 的关键决策，确保审计过程可追溯。

- 配置文件：`config/execution.yaml`
- 优先级：用户显式指令 > execution.yaml > 技能默认值

| 时间 | 阶段 | 决策点 | 用户选择 | 默认是否触发 | 决策来源 | 备注 |
|------|------|--------|----------|--------------|----------|------|
| {YYYY-MM-DD HH:mm} | A0 | 审计范围 | 全量审计 | 否 | user_input | 首次审计 |
| {YYYY-MM-DD HH:mm} | A3 | 复核轮数 | 2轮（标准） | 否 | user_input | 时间与质量平衡 |
| {YYYY-MM-DD HH:mm} | B0 | 审计分支 | 启用 audit/2026-03-12 | 是/否 | user_input/config_default | 若无人值守可为 config_default |
| {YYYY-MM-DD HH:mm} | B2 | P2 级别处理 | 跳过整个级别 | 否 | user_input | 需硬件环境验证 |

## 填写规则

1. 每个决策点只追加，不覆盖历史记录。
2. 若选择“跳过”，必须在备注中附原因或引用 `skip_log` 条目。
3. 若由默认策略触发（非交互），`默认是否触发` 必须填“是”。
4. `决策来源` 仅允许：`user_input`、`config_default`、`system_default`。
5. 多选（multiSelect）场景下，`用户选择` 建议写为 JSON 数组（如 `['P1','P2']`）或逗号分隔字符串（如 `P1,P2`）。
