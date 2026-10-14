# 任务 887 交接（wt-887）

日期：2026-10-14/15　分支：`wt-887`（基于 `main-v2-stable` 73ff458bd）
工作区：`github-repo/worktrees/wt-887`（独立 worktree，未碰主仓）

## 任务

P1 backend：新建会话无 runtime 时无法自动开轮——空闲开轮桥 target 只含 tabs+detached
runtime（`sessionCollabLiveTargets`），「从未打开/重启后无 runtime」是未覆盖第三态。
用户口径：收信对话应可后台跑，不应需要手动打开一次。

修法=任务书方案 A（接桥非造桥）：桥反查「durable inbox 有排队 follow-up、本进程无
runtime」的会话，注入同一趟 sweepTargets；唯一新增动作是 run 前按需拉起 runtime。
既有 569（接桥）/579（三振+回执）/709（pause 来源）/796（观测）机制全部复用。

## 已完成（按 commit 顺序）

1. **切片1 `f7c8d6477`：`internal/sessioninbox/probe.go` + `probe_test.go`**
   - `ProbeSnapshot(sessionPath) (InboxSnapshot, found bool, err error)`：只读探测
     manifest——不存在即零副作用（不建目录）；`filelock.TryAcquire` 拿不到写锁 →
     `ErrProbeBusy` 静默让路（Windows rename-over-open 双向保护）；损坏 manifest
     报错但**不 quarantine**（清道夫是 Store.Open 的职责）。
   - 5 测试全绿（无 manifest 零副作用 / 排队项+回执坐标原样读且字节不变 /
     pause 来源透传 / 写者持锁让路 / 损坏只读）。

2. **切片2（待提交）：`desktop/session_collab_dormant_wake.go` + 测试 + 接线**
   - `collectDormant`（纯核心，seam 注入）：排除 covered/有 tab 表面/archived/
     无 contact/UserPaused（709 绝对静默）/无队首（uncertain 人审架，796 H4）/
     probe 错误（Busy 静默、其余上报）。
   - `dormantTurnView`：生成标准 `idleTurnTargetView`——579 三振、in-flight 去重、
     570 回执坐标、709 autoPaused 唤醒全复用；wake 用 sync.Once memo（resume→run
     共享一次拉起）；run 里 fire-time 边界2 重检（isActive → ErrTurnRunning 竞态
     让路，不烧预算）+ 新 controller 快照的防御性 pause 重查（probe 缓存 ≤15s 旧，
     且跨进程 recovery 会在首开时 Paused）。
   - `wakeDormantRuntime`：Attach 已有 runtime 优先；否则 `collabOpenDetached()`
     分流 OpenTopicSessionDetached / **openTopicSessionInactive**（永不 activate，
     边界2 不打断用户）→ 轮询等 ctrl 发布（60s 有界，StartupErr 快败）→ 失败
     走 579 三振 → 570 open_retry_exhausted 回执；错误经 secrets.RedactError
     （回执 detail 原样带 err，不能漏 provider env 名）。
   - App 侧只读查询：`sessionHasTabForPath`（含 Ctrl=nil 构建中 tab、ReadOnly/
     Spectator 接管面 → 不双开）、`dormantWakeCtrlForSessionPath`（扫 tabs+
     detached，controllerForSessionPath 只扫 a.tabs 会把 park 后误判未发布）、
     `isActiveSessionPath`。
   - 接线：`session_collab.go` pump 加 dormant 缓存字段（每 `dormantProbeInterval`
     =15s 反查一次，视图每 tick 重建）；`sweepIdleInboxTurns` 在 live 视图后
     append dormant 视图（covered 集合由 live 半场传入，防双开）。
   - 6 个测试：验收①全链（真实 Store 写 manifest + 真实 ProbeSnapshot +
     真实 Controller 消费，runner 收到原文）/ 验收④ 逐条排除面 /
     ErrProbeBusy vs 损坏分类 / 验收③ 三振+回执坐标+封存单发 /
     709 autoPaused 先 resume 后 run 且 wake 恰一次 / 边界2 竞态让路不烧预算。
   - **测试形状教训**：`sweepTargets` 的 run 在 goroutine 里跑、生产每 pass 重建
     view；单测若复用同一 view，sync.Once 会把 wake 记忆成"只试一次"——三振测试
     必须每 pass newView（已修）。

## 测试与基线

- 预存红（与 887 无关，已在**主仓 main-v2-stable 原样复现**确认）：
  1. `internal/control`: `TestCascadeWaitLedgerMarksWaitingUserDuringDelegateWait`
     （包耗时 443s）
  2. `internal/agent`: `TestInterruptedRecoveryKeepsRedactedFailureSummary`
     （task340 MockProvider 脚本问题）
- 基线命令：
  - `go test ./internal/sessioninbox/ ./internal/control/`（根模块）
  - `cd desktop && go test . -run 'TestIdleTurn|TestSessionCollab|TestCollab'`（独立
    module `reasonix/desktop`，**不是** `go test ./desktop/`）
- 改后已跑绿：
  - `internal/sessioninbox` 全包（含新 probe 5 测试）
  - `internal/sessioncollab` 全包
  - desktop：新 `TestDormant*` 6 个 + collab/idle-turn 族回归全绿
  - `go vet ./desktop` 通过
- desktop 全包测试：后台跑（见最终汇报）

## 残留/未做

- `escalateStuckInboxForTab`（731 S2）挂在 live 视图的 tabID 上；dormant 视图无
  tab（拉起前不存在），失败时只有 570 回执、无 autopilot 升级——dormant 会话
  本来就没有 autopilot tab，语义自洽，不补。
- 拉起开的是 tab（background off）或 detached runtime（on）：创建/构建耗时
  3-30s 已计入 60s 等待；跨进程 lease 冲突会三振后写回执（消息保留）。
- dormant 反查成本：每 15s 一次 roster 扫描 + 每候选一次 manifest 只读探测
  （stat 优先，无 manifest 即止）；未做 profile，量级与既有 drain 的
  collabRosterIndex 每 tick 扫描同级。

## 验收对照

| 验收 | 状态 | 证据 |
|---|---|---|
| ① 无 runtime+排队 follow-up→桥拉起→RunInboxTurn 执行 | ✅ | `TestDormantWakeOpensQueuedTurnWithoutRuntime` |
| ② 569/579 零回归 | ✅ | collab/idle-turn 族回归全绿（同基线过滤器） |
| ③ 拉起失败有界重试+回执（3次有界+570） | ✅ | `TestDormantWakeFailureBoundedWithReceipt` |
| ④ 只有真实排队 follow-up 才拉起 | ✅ | `TestDormantDiscoveryOnlyRealQueuedFollowup` + `TestDormantDiscoveryProbeErrorsClassified` |
