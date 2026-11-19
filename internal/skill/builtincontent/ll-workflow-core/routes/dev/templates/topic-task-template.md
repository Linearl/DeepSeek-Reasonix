---
description: "__TASK_ID__-__TOPIC_NAME__ __STATUS_ICON____STATUS_TEXT__ 文件说明"
update_time: 2026-07-01
---

> **状态字面值 SSOT**：本文档出现的任务状态 emoji + 文案（🆕 待激活 / 📋 待办 / 🚧 进行中 / ⏸️ 暂停 / ✅ 已完成 / 🛑 已终止）均来自 `templates/scripts/req-sync/utils.js` 的 `LIFECYCLE_STATUS_MAP`（L53）。修改字面值请改那里。

# __TASK_ID__-__TOPIC_NAME__ __STATUS_ICON____STATUS_TEXT__

## 📋 任务元信息

- 所属版本：`dev___VERSION__`
- 任务编号：`__TASK_ID__`
- 专题目录：`docs/1.task/B.todo/__VERSION__/__TASK_ID__-__TOPIC_SLUG__/`
- 创建日期：`__DATE__`
- 最后更新：`__DATE__`
- 负责人：`__OWNER__`
- 状态：`🆕 待激活 / 📋 待办 / 🚧 进行中 / ⏸️ 暂停 / ✅ 已完成 / 🛑 已终止`
- 审计触发：`required / suggested / not_required`（必填）

---

## 🎯 任务概要

### 核心问题

- 待补充

### 当前状态

- 待补充

### 预期结果（可验证）

- 待补充

---

## 🔍 技术分析

### 现状与约束

- 待补充

### 范围定义

#### In Scope

- 待补充

#### Out of Scope

- 待补充

---

## 🧩 分阶段执行计划

### Phase 1：__PHASE_1_NAME__

- [ ] 子任务 1.1
- [ ] 子任务 1.2

### Phase 2：__PHASE_2_NAME__

- [ ] 子任务 2.1
- [ ] 子任务 2.2

### Phase 3：__PHASE_3_NAME__

- [ ] 子任务 3.1
- [ ] 子任务 3.2

> 约束：每完成一个 Phase，必须立即回写本主文档（状态、完成项、证据链接、下一步）。

---

## 📊 关键文件与证据

### 代码/脚本

- `__FILE_PATH_1__`
- `__FILE_PATH_2__`

### 文档/测试

- `__DOC_OR_TEST_PATH_1__`
- `__DOC_OR_TEST_PATH_2__`

---

## 🚦 质量门与审计触发

- 质量门等级：`low / medium / high / critical`
- 质量门动作：`continue / pause / block`
- 是否触发审计：`required / suggested / not_required`
- 统一交接工单：`待填写`

---

## ✅ 验收标准（DoD）

- [ ] 功能符合预期
- [ ] 测试通过（含关键场景）
- [ ] 文档已更新（含主文档与必要索引）
- [ ] 风险与后续建议已记录

---

## 🧾 执行日志（持续追加）

- `__DATE__` | `Init` | 初始化任务主文档 | `✅` | 待补充

---

## 📦 阶段归档索引（完成后维护）

- 归档根目录：`docs/1.task/B.2.done/__VERSION__/__TASK_ID__-__TOPIC_SLUG__/`
- 阶段目录：`docs/1.task/B.2.done/__VERSION__/__TASK_ID__-__TOPIC_SLUG__/phase-<phase-id>-<yyyymmdd>/`
- 已归档阶段：
  - `phase-a-__YYYYMMDD__`（示例）

## 📄 需求文档双副本（激活时创建）

- 工作副本：保留原需求文件名（示例：`REQ-2001-{需求名称}.md`），可随任务推进更新。
- 原始副本：同目录追加 `-original` 后缀（示例：`REQ-2001-{需求名称}-original.md`），创建后冻结不改。

---

## 🔗 关联任务

- `__RELATED_TASK_ID_1__`
- `__RELATED_TASK_ID_2__`
