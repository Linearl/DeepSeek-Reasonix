---
description: "专题完成判定证据模板 文件说明"
update_time: 2026-07-01
---

# 专题完成判定证据模板

> 用途说明：用于阶段 A.3 的“完成判定并归档”证据收集，确保文档、代码、测试、断言四类证据完整可追溯。

## 最小必填字段

- `featureId`
- `version`
- `owner`
- `status`
- `evidenceRef`
- 专题名称
- 所属版本（`dev_XX`）
- 来源路径（todo 路径）
- 文档完成标记证据
- 代码证据
- 测试证据
- 断言证据（交付含否定性断言时必填：`reasonix claim-check` CLI 或 `claim_check` 工具的输出，或 `test -f`/`grep -c` 谓词原始输出；采样查询 head/tail 无效）
- 归档决策与归档目标

## 可选字段

- 审核人
- 风险备注
- 关联提交哈希
- 提交粒度（`task` / `version`）
- 提交阶段（`B.5` / `C.1`）

## 专题信息

- `featureId`：
- `version`：`dev_XX`
- `owner`：
- `status`：
- `evidenceRef`：
- 专题名称：
- 所属版本：`dev_XX`
- 来源路径：`docs/1.task/B.todo/{version}/{task-id}/...`

## 判定项（需全部满足）

### 1) 文档完成标记

- [ ] 存在明确完成状态（如 `已完成` / `status: done`）
- 证据位置：

### 2) 代码证据

- [ ] 对应实现已落地（文件/函数/接口/配置）
- 证据位置：

### 3) 测试证据

- [ ] 至少一项有效验证通过（自动/手工）
- 证据位置：

### 4) 断言证据（含否定性断言时必填）

- [ ] 每条「不存在 / 缺失 / 未实现」类结论均附结构化判定：`reasonix claim-check --claim missing --pattern X --base D1 ...`（或 agent `claim_check` 工具；或 `test -f`/`grep -c` 原始输出）
- [ ] 无采样查询（find/head/tail 截断输出）被用作存在性证据
- [ ] `uncertain: true` 的判定未被写成干净结论；verdict 射程仅为存在性，未扩大为内容/行为断言
- 证据位置：

## 归档决策

- [ ] 可归档
- 归档目标：`docs/1.task/B.2.done/{version}/{task-id}`
- 提交粒度（可选）：
  - [ ] task（阶段 B.5）
  - [ ] version（阶段 C.1）
- 关联提交哈希（可选）：
- 备注：
