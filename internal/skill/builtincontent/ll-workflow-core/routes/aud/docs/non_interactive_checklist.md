---
description: "用于验证 `mode.non_interactive: true` 时流程是否按预期执行。"
update_time: 2026-07-01
---

# 非交互模式验收清单

用于验证 `mode.non_interactive: true` 时流程是否按预期执行。

## A. 初始化

- [ ] 成功读取 `config/execution.yaml`
- [ ] 通过 `config/execution.schema.json` 校验
- [ ] 生成 `effective_config.yaml`
- [ ] 生效优先级符合：用户指令 > 配置文件 > 系统默认

## B. 阶段行为映射

- [ ] A0 未发起问答，使用 `defaults.audit_scope`
- [ ] A3 未发起问答，使用 `defaults.review_rounds`
- [ ] B0 未发起问答，使用 `defaults.use_audit_branch`
- [ ] B2 依据 `mode.on_no_response` 执行（`use_default` 或 `abort`）

## C. 留痕与产物

- [ ] `decision_log.md` 存在且含 `决策来源`
- [ ] 默认触发项 `默认是否触发=是`
- [ ] 跳过项存在时生成 `skipped/debt_backlog.md`
- [ ] 生成 `run_result.json`

## D. 闸门与结论

- [ ] C1 闸门通过时状态为 `pass`
- [ ] C1 闸门失败时状态为 `blocked`
- [ ] `blocked` 时未执行主分支合并

## E. 机器可读校验

- [ ] `run_result.json` 字段完整（`status/summary/quality/artifacts`）
- [ ] `perf_regression_pct` 与阈值比较正确
- [ ] `blocking_reasons` 在 blocked 场景非空
