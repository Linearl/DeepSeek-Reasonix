---
description: "5.git-commit Git提交目录说明"
update_time: 2026-07-01
---
# 5.git-commit Git提交

> **Git 提交规范与记录管理。**

## 目录结构

```text
5.git-commit/
├── template/          # 模板目录
└── README.md          # 本文件
```

## 使用规范

### 提交规范

遵循 Conventional Commits 规范：
```text
<type>(<scope>): <subject>

<body>

<footer>
```

### Type 类型

| Type | 说明 | 示例 |
| --- | --- | --- |
| feat | 新功能 | feat(upload): 添加批量上传功能 |
| fix | Bug 修复 | fix(auth): 修复登录超时问题 |
| docs | 文档更新 | docs(readme): 更新安装说明 |
| style | 代码格式（不影响逻辑） | style: 格式化代码 |
| refactor | 重构 | refactor(core): 提取公共函数 |
| perf | 性能优化 | perf(query): 优化数据库查询 |
| test | 测试 | test(unit): 添加单元测试 |
| chore | 构建/工具变更 | chore(deps): 更新依赖 |

### 命名规范

| 类型 | 格式 | 示例 |
| --- | --- | --- |
| 月度摘要 | `{YYYY}-{MM}-commit-summary.md` | `2026-04-commit-summary.md` |

## 模板列表

- `template/01-commit-month-summary-template.md`：月度提交摘要模板（按类型、范围与风险汇总）

## 维护规则

1. **提交信息必须规范**：遵循 Conventional Commits 格式
2. **定期生成摘要**：每月生成提交摘要文档
3. **关联需求编号**：重要提交关联 REQ-XXXX

## 关联规范

- [template/](./template/)
