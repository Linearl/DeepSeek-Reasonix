# 922 handoff（886 两项机制修复）

分支：wt-922，基线 main-v2-stable @ f8a09feec（已含 886 交付 fde5956d3 哨兵+机制定性）。
日期：2026-10-15。

## 一、实施输入（886 机制定性，已核对源码）

### ① inFlight 残留（超限会话在飞消息不丢）

- `tabEventSink.turn.inFlight` 三处写：`tryBeginTurn`（turn_admission.go:162）、`Emit` 收到 TurnStarted（tabs.go:1497-1501）；两处清：TurnDone fan-out（tabs.go:1594-1598）、`cancelTurnStart`（tabs.go:1655，仅 `admission.finish` 在 `started==false` 时调，turn_admission.go:50-53）。
- 泄漏链（886 定性）：rebind 窗口内 `admission.finish` 见 `RuntimeStatus().Running==true` 跳过 cancelTurnStart（指望 TurnDone 清），但 epoch/rebind 窗口内 TurnDone 未回到该 sink ⇒ inFlight 残留 true。此后该 tab 上任何 Submit / `beforeInboxDispatch`→`beginRuntimeTurn` 撞 `tryBeginTurn` false → ErrTurnRunning → `dispatchInboxOnce` 按设计静默 idle（inbox_dispatch.go:168）→ 队首永不认领。
- 修复落点：
  1. **rebind 窗口补 cancelTurnStart**：`commitSessionRebind`（!detachSource 收尾，oldSink 被 fence 时）+ `reattachDetachedSessionRuntimeForReattach`（oldSink 被 fence 时）对被 fence 的 oldSink 调 `cancelTurnStart()`——fence 后该 sink 不再属于任何活 runtime，TurnDone 不会再回来清。
  2. **接续协议（残留自愈）**：`reattachDetachedSessionRuntimeForRebind` 的 applyRuntimeTab 之后 + `attachExistingRuntime` 路径：若挂上的 ctrl `RuntimeStatus().Running==false`（不 running 不 finishing）而 sink.inFlight==true，说明 TurnDone 已永久丢失（超限/异常轮），清残留（cancelTurnStart）+ WARN 留痕。
  3. 附带：TurnStarted 的 `s.turn.inFlight = true`（tabs.go:1497）改为带时间戳（turnSubmissionState.reservedAt），供残留判定「旧预留」。

### ② 桥盲区（579 桥 target 不含可见 tab）

- `sessionCollabTargets()`（session_collab.go:1952）构造 target **不带 ctrl 字段**；`sweepIdleInboxTurns`（session_collab_idle_turn.go:554）对 `t.ctrl == nil` **静默 continue** ⇒ 空闲桥对全部可见 tab 全盲（佐证：生产日志 `decision=skip:active_tab` 0 条，该分支不可达）。886 现场：reattach 后会话是可见 tab，桥完全看不见，queued_followup 4 次 drain 空转无人接续。
- 修复落点：`sweepIdleInboxTurns` 对 `t.ctrl == nil && t.tabID != ""` 走 `tabByID(t.tabID).Ctrl` 兜底解析（session_work_detail.go:32-35 同款模式）；仍解析不到才 continue，且补一行 debug 留痕（不再零日志静默）。
- 连带效果：可见非 active tab 恢复 569/579 设计内的桥可见性（activeTab 门在 sweepTargets 内，边界 2 不变）；796 finishing 自愈（heal seam）对可见 tab 恢复可达。

## 二、验收对照

1. inFlight 残留场景单测：rebind fence 后 oldSink/挂载 sink 的残留被清，后续 tryBeginTurn 可认领（正常路径 TurnStarted→TurnDone 清理不回归）。
2. 桥盲区单测：ctrl==nil 的可见 tab target 被桥纳入（view.run 可用、activeTab 仍被边界 2 拦截）。
3. 反向用例：579/569/796 族测试全绿（包级 go test）。
4. 与 886 哨兵联动观测点：哨兵只报体量（sessionTranscriptMb/sessionEventsFileMb），本件补的是体量超标后的「消息不丢、可恢复投递」；WARN 留痕关键字面写在报告残留节。

## 三、实施结果（2026-10-15 终验）

切片 commit：
1. `d75a7e708` 922 handoff（本文件）
2. `ba2548eef` 922 ②桥盲区（sweepIdleInboxTurns 拆出 idleTurnTargetViews + tabByID.Ctrl 兜底 + 不可解析留痕；附单测）
3. `1917ab20d` 922 ①inFlight（rebind fence 补 cancelTurnStart ×2 + attach 接续协议 clearStaleTurnReservation ×3 落点；附单测）

验收：
- ①超限会话在飞消息不丢：`TestEmitTurnDoneClearsDetachedSinkReservation`（rebind fence 后 TurnDone 仍回 sink 清预留）+ `TestClearStaleTurnReservationOnIdleAttach`（残留清掉后队首可认领=降级后可恢复投递）。
- ②桥盲区：`TestIdleTurnSweepResolvesVisibleTabCtrlByID`（ctrl==nil 可见 tab 经兜底进入视图并真开轮消费队首）。
- ③反向用例：`TestClearStaleTurnReservationKeepsLiveTurn`（在飞预留不误清）、`TestIdleTurnSweepKeepsActiveTabMarker`（activeTab 不丢）、`TestIdleTurnSweepDropsUnresolvableCtrlTarget`（竞态丢弃不 panic）、`TestClearStaleTurnReservationNilSafe`；族回归 `TestIdleTurn|TestDormant|TestTabEventSink|TestTurnSubmission|TestHeal|TestFinishing|TestSweep` 全绿（19.4s）。
- 包级：`go build ./... && go vet ./...` 干净；全量 `go test -count=1 -timeout 40m .`（2124s）10 条失败**全部在基线 f8a09feec 同条件下复现**（9 确定性 + 1 双侧 flaky：SetActiveRemoteTab），wt-922 失败集 ⊆ 基线失败集，零新增红。预存红清单：WindowsDesktopCommandsUseProcConstructors / EnsureBlankTabConcurrentModelSwitch / SetActiveRemoteTab（flaky）/ PumpDerivedHopExhaustion（双侧同文案）/ RebindWithTakeoverMirror（3s 超时载荷敏感，双侧同文案）/ SettingsExposesEffectiveSandboxWriteRoots / CatalogTopicTitle+CatalogManualDefaultTitle / BuildTabControllerBlocksWhenSessionLeaseHeld / StartTopicActivationFailureDetachesPreviousAndReattaches。
- ④与886哨兵联动：哨兵（sessionTranscriptMb≥64 / sessionEventsFileMb≥100 WARN）报体量；本件补「体量超标会话的消息不丢+可恢复投递」。观测点：`cleared stale sink turn reservation after runtime attach`（886 机制①自愈触发）与 `[idle-turn-sweep] ... skip:no_ctrl_resolvable`（target 解析失败留痕）——哨兵 WARN 同窗若见 queued_followup 搁浅，先 grep 这两行。

## 四、环境备忘

- grep 用 `git grep`；提交显式路径、中文 message「922 」开头；不 add -A。
- 主仓勿写；wt-922 内 `git rev-parse --show-toplevel` 应以 wt-922 结尾。
