---
description: "1. 是否认可当前任务已经完成"
update_time: 2026-07-01
---

# 自动开发循环执行单（Loop Worksheet）

> 说明：不要求每轮从 A.1 开始。
>
> - 若上下文完整，可直接进入 B.1~B.5 核心循环。
> - B.4 为强制门控问询（三问题）；B.5 决定回流 B.2 或任务级提交。
> - 当任务全集完成后，执行 C.1/C.2/C.3。

## 基本信息

- 日期：
- 当前版本（自动识别）：`dev_XX`
- 工作流开始时间（`workflowStartAt`，ISO 8601）：
- 工作流结束时间（`workflowEndAt`，ISO 8601，C.3 收口填写）：
- 执行轮次：

---

## 阶段A：预处理（A.1-A.3，按需执行）

## A.1 - 版本识别

- 目标版本目录扫描结果：
- 匹配到 `dev_(\d+)`：
- 最大版本：
- 用户指定目录（如有）：
- 最终 `targetDevDir`：
- 最终 `version`：

## A.1.2 - docs 结构检查与初始化门控

- docs 路径：`{targetDevDir}/docs`
- 是否存在最小结构：
- 初始化脚本执行记录（如执行）：
  - 脚本：`./tools/init-docs-structure.ps1`
  - 参数：
  - 结果：

- A2 前置门控：
  - [ ] 版本计划文档已完成（`docs/09-plan/{version}/dev_{version}_plan.md`）
  - [ ] 已至少拆分 1 个专题任务文档到 `docs/1.task/B.todo/{version}`
  - 用户确认时间：

## A.2 - todo 专题筛选

- todo 路径：`{targetDevDir}/docs/1.task/B.todo/{version}`
- 版本号前缀（两位）：
- 命中专题（`^前缀[-_].+`）：
- 排除专题及原因：

## A.3 - 完成判定与归档

|专题|文档完成标记|代码证据|测试证据|是否归档|归档路径|
|---|---|---|---|---|---|
|示例|✅|✅|✅|✅|`{targetDevDir}/docs/1.task/B.2.done/{version}/{task-id}`|

- 需求文档双副本核对：
  - [ ] 工作副本（原名）位于专题目录
  - [ ] 原始副本（`-original`）位于专题目录
  - [ ] A.inactive 对应需求正文已移出

---

## 阶段B：循环执行（B.1-B.5）

## B.1 - ask_questions 建议

- 候选专题：
- 推荐顺序：
- 提问批次文件：

## B.2 - 开发与测试

- 用户选择：
- 实施内容：
- 测试项与结果：
- 阻塞问题（如有）：

## B.3 - 文档更新 + 归档

- 更新文档：
- 归档动作：

- 标准字段（来自过程资产）：
  - `featureId`：
  - `version`：
  - `owner`：
  - `status`：
  - `evidenceRef`：

## B.4 - 门控输出 + ask_questions 三问题

- 当前任务完成摘要（功能/测试/文档）：
- 已完成任务数 / 总任务数：
- 未完成专题清单：
- 下一轮候选建议：

- ask_questions 固定问题：
  1. 是否认可当前任务已经完成
  - EN: Do you acknowledge that the current task is complete?
  - 推荐 / Recommended: 认可 / Acknowledge completion
  1. 后续此环节是否需要 ask_questions 暂停供查检，还是自动化连续工作
  - EN: Should ask_questions pause for inspection, or continue in automated mode?
  - 推荐 / Recommended: 自动化连续工作 / Automated continuous mode
  1. 是否需要创建新的专题任务并保存到 `todo`
  - EN: Do you want to create a new topic task and save it to `todo`?
  - 推荐 / Recommended: 暂不创建 / Do not create now

- 用户回答：
  - 任务完成认可：
  - 查检模式：
    - [ ] ask_questions 暂停查检
    - [ ] 自动化连续工作
  - 新专题创建：
    - [ ] 需要创建
    - [ ] 暂不创建
  - 新专题路径（如创建）：
  - 用户反馈意见：

## B.5 - 分支执行（回流或任务级提交）

- 分支结果：
  - [ ] 用户不认可且有反馈 → 回到 B.2
  - [ ] 用户认可且创建新专题 → 先创建专题文档，再提交
  - [ ] 用户认可（或无反馈）→ 执行任务级原子提交

- 若回流 B.2：
  - 反馈修正点：
  - 回流时间：

- 若执行任务级提交：
  - 提交范围（本任务）：
  - git 提交信息：
  - 提交哈希：

- 提交后版本状态：
  - [ ] 当前版本仍有未完成任务 → 回到 B.1
  - [ ] 当前版本全部完成 → 进入 C.1

---

## 阶段C：后处理

## C.1 - 版本文档系统校验与版本级提交（宏观）📝

> EN mirror: Must update `docs/06-update_log` and complete a version-level commit.

- 校验范围：
- 更新文档：
- `06-update_log` 更新日志路径：
- `06-update_log` 更新摘要：
- 构建/测试复核：
- 版本级 git 提交信息：
- 版本级提交哈希：
- 与 B.5 任务级提交关系说明：

## C.2 - LESSONS 沉淀 🧠

- 记录文件：`../LESSONS.md`
- 本轮完成项：
- 关键决策：
- 阻塞项与解除方式：
- 下一轮改进建议：
- 表达检查（清晰简洁）：
  - EN key: clear and concise
  - [ ] 结论优先
  - [ ] 依据简明
  - [ ] 下一步可执行

## C.3 - 提交导出与进展总结（workflowStartAt ~ workflowEndAt）

> EN mirror: Export commit range by `workflowStartAt ~ workflowEndAt`, then gate whether to save summary.

- 导出脚本（相对路径）：`./tools/export-workflow-git-log.ps1`
- 导出时间范围：`workflowStartAt` ~ `workflowEndAt`
- C.3 收口记录：
  - workflowEndAt：
- 提交导出结果摘要：
  - 提交总数：
  - 关键提交：
  - 涉及模块：

- 进展总结：
  - 已完成：
  - 未完成：
  - 风险与下一步：

- ask_questions（强制）：是否需要将本次 C.3 总结保存到 md 文件？
  - [ ] 保存
  - [ ] 不保存
- 若保存：
  - 目标路径：
  - 输出文件：
