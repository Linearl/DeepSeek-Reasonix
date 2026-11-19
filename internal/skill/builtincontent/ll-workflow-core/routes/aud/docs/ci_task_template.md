---
description: "本模板用于将 `ll-audit` 纳入 CI 或夜间任务，默认走非交互模式。"
update_time: 2026-07-01
---

# CI / 夜间任务模板（非交互执行）

本模板用于将 `ll-audit` 纳入 CI 或夜间任务，默认走非交互模式。

## 1) 预置条件

- `config/execution.yaml` 中设置：`mode.non_interactive: true`
- 已存在 `config/execution.schema.json`
- 目标仓库可写入审计产物目录

## 2) 任务步骤（逻辑模板）

1. 读取并校验 `execution.yaml`
  - 建议调用：`python .github/skills/ll-audit/scripts/validate_execution_config.py --config .github/skills/ll-audit/config/execution.yaml --schema .github/skills/ll-audit/config/execution.schema.json`
2. 生成 `effective_config.yaml`
3. 执行审计/修复流程
4. 产出 `run_result.json`
5. 若 `status=blocked`，任务标记失败（同时输出 `blocking_reasons[*].code`）

## 3) VS Code tasks 参考（可选）

```json
{
  "label": "Run Code Audit Fix (Non-Interactive)",
  "type": "shell",
  "command": "python anc_host.py --auto_flow_file file/config/auto_flows/audit_flow.yaml --auto_verbose",
  "group": "test"
}
```

> 注：上例为参考样式，实际命令请替换为项目内可执行入口。

## 4) GitHub Actions 样例（推荐）

- 模板文件：`templates/github_actions_non_interactive.yml`
- 使用方式：复制到仓库 `.github/workflows/` 后按项目入口命令替换 `Run non-interactive audit flow` 步骤

## 5) CI 判定建议

- `run_result.json.status == \"pass\"` → 任务通过
- `run_result.json.status == \"blocked\"` → 任务失败并提示 `blocking_reasons`
