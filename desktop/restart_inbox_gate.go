package main

// 任务 868 — 无人值守会话的恢复确认门自动继续（恢复结算家族第四症，
// 前三症：543 引导结算 / 840 名册消费 / 866 重入第二路径绝根）。
//
// 现场（用户 1508 反馈+截图）：守护对话重启后弹「已恢复 15 条待处理指令」
// 三选项门（InboxRecoveryBanner），不选择则后续消息无法注入——守护/心跳类
// 无人值守会话被门卡住等人工 = 死锁，违背无人值守设计。触发背景：主对话
// 高频回执 + 守护空闲轮交错使 follow-up 积压 15 条，跨进程重启时 store 的
// loadOrInit 按 RunID 变更把整队标成 recovered 并 Paused（loadOrInitLocked：
// recovered>0 || len(items)>0 即 Paused+Recovered），ClaimItem 从此拒绝派发。
//
// 既有机制为何不够（本件只补缺口，不动它们的合同）：
//   - 579 空闲开轮桥的 709 唤醒只在「协作投递 tick + 非活动 tab + 存在可认领
//     队首」时顺带清自动暂停；活动 tab / 非协作目标 / pump 停用的守护会话
//     不在覆盖面，门依旧等人；
//   - 254/49-A2 恢复链只覆盖名册会话与 goal 会话，且 461-P2 要求更新重启
//     标记——普通重启（崩溃、日常退出重开）什么都不续；
//   - 731 自动续轮看门狗把 sessioninbox.ErrPaused 划为「有人在持门」
//     （709 绝对静默）刻意不越——前提是门后有人；无人值守时前提不成立。
//
// 设计（任务书方向①，会话级策略；方向②按条目类型分诊被否：恢复门的暂停是
// 信箱级标志，按条目分诊只能靠自动消费条目——与 641/589b 「Queued 行是真实
// 待办、静默结算=删消息」的家族裁决正面冲突）：恢复点对无人值守 tab 确定性
// 清门，把「继续执行」这个选择交给会话自身的无人值守姿态。
//
// 验收④（标记来源受控）逐点：
//   - tab.autopilot 的写入口全部经 task-325 yolo 门（SetCollaborationModeForTab
//     / closeAutopilotForTier：非 yolo 即关）；
//   - 持久化恢复经 task-465 X4 断点 C 再门控（restorePersistedTabs →
//     gateRestoredAutopilotDefaults）：陈旧 true 在 ask/auto 下不可能复活；
//   - 清门用 Controller.ResumeInboxAutoPause（任务709 原语）：store 层原子判定
//     只清非人为暂停，用户显式持有的暂停（UserPaused，含「保持暂停」按钮）
//     不可越——自动化与用户意志的竞态由 manifest 锁内的判定收口；
//   - 每次进程重启至多触发一次（恢复点单次调用），清完门即退出：用户事后
//     手动暂停不会被反复解开（709 绝对静默不受本件影响）。
//
// 其余合同：
//   - 需存在可认领的排队条目才清：纯 Uncertain 检视架（RestartOrphan /
//     SteerUnapplied 等 park 行）维持人审面——709 桥同款对齐（不打扰纯待
//     检视的架子），门与横幅留给回来的人；
//   - 461-P2 兜底②照抄：会话末步是中断工具调用时不清不派（「中断的工具
//     需要核实」评审面板是本形的人审面）；
//   - 任务840 顺序照抄：guidance settle 先于清门；
//   - 清门后踢一次派发（NotifyInboxRuntimeReady 同款 kick，与启动时 runtime
//     发布的 kick 同路径）：积压队列按 FIFO 正常开轮消化；
//   - dial（autonomous_update_resume，task 254 用户裁决）尊重：off = 用户要求
//     一切会话手动控制，门保留。但不要求 461-P2 更新重启标记——868 的现场
//     就是普通重启，要标记等于没修；
//   - 通知留痕：清门动作发一条带稳定码的通知（transcript 记录自动决策供
//     审计），不弹任何需要选择的门。

import (
	"log/slog"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// unattendedGateAutoContinueText is the bilingual Go-side fallback; frontends
// localize by NoticeCodeUnattendedGateAutoContinue.
const unattendedGateAutoContinueText = "Unattended auto-continue: the restart recovery gate was cleared automatically; queued work keeps flowing (无人值守自动继续：重启恢复门已自动解除，队列照常消化)."

// inboxRestartGateUp reports the restart-gate shape worth auto-continuing:
// the inbox is paused with the recovery mark, no human is holding the pause,
// and at least one queued item is claimable. A pure review shelf (Uncertain
// parks, no queued head) keeps its gate — that face waits for a person by
// design, and "continue" would have nothing to continue.
func inboxRestartGateUp(snap sessioninbox.InboxSnapshot) bool {
	if !snap.Paused || !snap.Recovered || snap.UserPaused {
		return false
	}
	for _, item := range snap.Items {
		if item.State == sessioninbox.StateQueued {
			return true
		}
	}
	return false
}

// autoContinueInboxGateForTab clears the restart recovery gate for one
// unattended tab at the restore point (task 868). Called from the same
// publication path as the 254/49-A2 resume family, AFTER them: a roster
// resume that already cleared the pause makes the gate-shape probe below a
// no-op, so the two chains compose without double settle or double notice.
func (a *App) autoContinueInboxGateForTab(tab *WorkspaceTab) {
	if a == nil || tab == nil || tab.Ctrl == nil {
		return
	}
	a.mu.RLock()
	eligible := tab.autopilot && tab.Ready && !tab.removed
	id := tab.ID
	a.mu.RUnlock()
	if !eligible {
		// 有人值守（或未就绪）：门照旧弹，行为与现状逐字节一致（验收②）。
		return
	}
	// dial（task 254 用户裁决）在函数内判：off = 用户要求一切会话手动控制，
	// 门保留；调用方无须各自记得这层语义。
	if a.autonomousUpdateResumeMode() == "off" {
		return
	}
	snap := tab.Ctrl.InboxSnapshot()
	if !inboxRestartGateUp(snap) {
		return
	}
	// 461-P2 兜底②：中断工具不重放，评审面板是本形的人审面，门保持。
	if tabHasInterruptedToolCall(tab) {
		slog.Warn("desktop: unattended recovery gate kept — the session's last step was an interrupted tool call ("+restartInterruptedToolMarker+"; review the pending tools manually)", "tab", id)
		return
	}
	// 任务840 顺序：已消费引导的停靠行先结算下架，再清门。
	a.settlePlannedRestartGuidanceForTab(tab)
	cleared, err := tab.Ctrl.ResumeInboxAutoPause()
	if err != nil {
		slog.Warn("desktop: unattended recovery gate auto-continue failed", "tab", id, "err", err)
		return
	}
	if !cleared {
		// 用户在快照与清门之间恰好暂停（709 竞态口），或门已被同恢复点
		// 的名册链清掉——两种都不留痕、不踢派发。
		return
	}
	slog.Info("desktop: unattended recovery gate auto-continued (868)", "tab", id, "recovered", snap.RecoveredN)
	a.noticeCodeForTab(id, event.LevelInfo, event.NoticeCodeUnattendedGateAutoContinue, unattendedGateAutoContinueText)
	// 踢一次派发：ClaimItem 的暂停闸已开，积压队列由常规调度器按 FIFO
	// 开轮消化（与启动时 runtime 发布后的 kick 同路径；忙时自动拒绝，
	// 回合完成自动再踢）。
	if kicker, ok := tab.Ctrl.(interface{ NotifyInboxRuntimeReady() }); ok {
		kicker.NotifyInboxRuntimeReady()
	}
}
