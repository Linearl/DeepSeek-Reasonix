# 活体巡检与重启更新（详版）· B4 外移

> `SKILL.md` §4b 保留「规则 / 判据三步 / 处置按形态」；本文件放**重启更新判据**与实证。

---

## restart_update execute 被挡的判据（2026-09-25 用户定）

`execute` 报 **「a turn is running or background jobs are active」** 时：

- **发起方自身 turn 不计入**（没有鸡生蛋问题）。
- 挡道的是**另一个活跃对话**（协作子会话正在跑 turn）或**未清的 background job**。

**处置**：

1. `wait` 清 job；
2. `get_session_status` 找活跃对话（等待其 idle，或唤醒 / 停止）；
3. **不要**试图结束自己的 turn 来绕——**绕不过**。

**注意 build 与 running 的错位**：build 自动铺 staging 后，`active version` 可能先变，而
`running version` 还是旧的——只差一次 relaunch。target 状态在 turn 边界会被清，
所以 **execute 前需重新 `set_target staging`**。

---

## 实证（2026-09-24）

- 301 / 300 派活撞上 1624 出包 relaunch → `interrupted_turn pending` → 唤醒消息续跑成功。
- 另案：开发-1「编辑被拒转用户决定」**4.3 小时零交付**（任务 225 同族）。

**结论**：出包 relaunch 窗口**避开派活**；relaunch 之后**必唤醒**。

---

## 关联

- [[session-recovery-cleanup]] Phase 0（判据三步的完整流程）
- 任务 225（「转用户决定」决策框级联）
