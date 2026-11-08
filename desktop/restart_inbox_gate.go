package main

// 任务 868 + tl1357 — 恢复确认门的自动继续（恢复结算家族第四症，
// 前三症：543 引导结算 / 840 名册消费 / 866 重入第二路径绝根）。
//
// 现场（用户 1508 反馈+截图）：守护对话重启后弹「已恢复 15 条待处理指令」
// 三选项门（InboxRecoveryBanner），不选择则后续消息无法注入——守护/心跳类
// 无人值守会话被门卡住等人工 = 死锁，违背无人值守设计。触发背景：主对话
// 高频回执 + 守护空闲轮交错使 follow-up 积压 15 条，跨进程重启时 store 的
// loadOrInit 按 RunID 变更把整队标成 recovered 并 Paused（loadOrInitLocked：
// recovered>0 || len(items)>0 即 Paused+Recovered），ClaimItem 从此拒绝派发。
//
// tl1357 扩展（形态③，有人值守面）：原 868 只清无人值守（autopilot）tab，
// 有人值守的门保留（原验收②）。20261102 实测（致命问题清单 §1 形态C）：
// 普通交互会话重启后引导消息停在自动暂停门后，会话侧永远收不到（用户
// 11:39 报告），修复=清门不再区分姿态。用户重启前发出的消息理应重放，
// 而不是等一次永远不来的「继续执行」点击；自动暂停是机器写入的标志
// （user:false/auto:true），不是用户意志。原 868 验收②（有人值守门逐字节
// 保留）由本件正式修订——保留闸全部不变，见下。
//
// 既有机制为何不够（868 只补缺口，不动它们的合同）：
//   - 579 空闲开轮桥的 709 唤醒只在「协作投递 tick + 非活动 tab + 存在可认领
//     队首」时顺带清自动暂停；活动 tab / 非协作目标 / pump 停用的守护会话
//     不在覆盖面，门依旧等人；
//   - 254/49-A2 恢复链只覆盖名册会话与 goal 会话，且 461-P2 要求更新重启
//     标记——普通重启（崩溃、日常退出重开）什么都不续；
//   - 731 自动续轮看门狗把 sessioninbox.ErrPaused 划为「有人在持门」
//     （709 绝对静默）刻意不越——前提是门后有人；重启恢复现场前提不成立。
//
// 设计（868 任务书方向①的会话级策略 + tl1357 姿态扩展；按条目类型分诊被否：
// 恢复门的暂停是信箱级标志，按条目分诊只能靠自动消费条目——与 641/589b
// 「Queued 行是真实待办、静默结算=删消息」的家族裁决正面冲突）：恢复点对
// 每个 tab 确定性清门，把「继续执行」这个选择交还给队列本身。
//
// 验收④（标记来源受控）逐点（868 保留项）：
//   - 清门用 Controller.ResumeInboxAutoPause（任务709 原语）：store 层原子判定
//     只清非人为暂停，用户显式持有的暂停（UserPaused，含「保持暂停」按钮）
//     不可越——自动化与用户意志的竞态由 manifest 锁内的判定收口；
//   - 每次进程重启至多触发一次（恢复点单次调用），清完门即退出：用户事后
//     手动暂停不会被反复解开（709 绝对静默不受本件影响）。
//
// 其余合同（全部保留，姿态无关）：
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
//     审计），不弹任何需要选择的门。tl1357 起改用 NoticeCodeRestartGateAutoContinue
//     （中性文案；旧 unattended_gate_auto_continue 码按 wire-stable 约定保留
//     定义，不再发射）。

import (
	"log/slog"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// restartGateAutoContinueText is the bilingual Go-side fallback; frontends
// localize by NoticeCodeRestartGateAutoContinue (tl1357 形态③：有人值守 tab
// 同样自动清门，文案不再限定「无人值守」).
const restartGateAutoContinueText = "Restart gate auto-continue: the restart recovery gate was cleared automatically; queued work keeps flowing (重启恢复门已自动解除，队列照常消化)."

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
// 任务1206（开轮仲裁 SSOT）：清门前问仲裁层——runtime 活性由 per-tab 臂
// wakeTabRuntimeLive 单点判定（tab.Ready/tab.Ctrl 直读已废，源码守卫钉住）；
// 构建未发布时不清不踢，构建完成的 runtime:rebuilt kick 是那条时间线上
// 的负责人。时机未变：仍是恢复点同位、dial 内判、709 原语清门。
func (a *App) autoContinueInboxGateForTab(tab *WorkspaceTab) {
	if a == nil || tab == nil {
		return
	}
	a.mu.RLock()
	sessionPath := tab.SessionPath
	id := tab.ID
	a.mu.RUnlock()
	// tl1357 形态③：清门不再区分值守姿态。20261102 实测（致命问题清单 §1
	// 形态C）：有人值守的会话重启后，引导消息停在自动暂停的门后等人点
	// 「继续执行」，会话侧永远收不到——用户重启前发的消息理应在重启后重放，
	// 而不是等一次永远不来的点击。709 原语的原子判定（只清非人为暂停）
	// 与下方四道保留闸（dial=off / 纯检视架 / 中断工具 / UserPaused 竞态口）
	// 对两种姿态一体生效；dial=off 仍是「一切会话手动控制」的总开关。
	// dial（task 254 用户裁决）在函数内判：off = 用户要求一切会话手动控制，
	// 门保留；调用方无须各自记得这层语义。
	if a.autonomousUpdateResumeMode() == "off" {
		return
	}
	// 任务1206：问仲裁层（per-tab 臂）——「可开轮?谁负责」。runtime 未活
	// （skeleton/构建在飞）时不清不踢：此处的 kick 只会踢空，责任在那条
	// 时间线上的构建完成 kick（notifyTabRuntimeRebuiltAtEpoch）。
	ctrl := wakeTabLiveCtrl(tab)
	if ctrl == nil {
		return
	}
	snap := ctrl.InboxSnapshot()
	if !inboxRestartGateUp(snap) {
		return
	}
	// 461-P2 兜底②：中断工具不重放，评审面板是本形的人审面，门保持。
	if tabHasInterruptedToolCall(tab) {
		slog.Warn("desktop: restart recovery gate kept — the session's last step was an interrupted tool call ("+restartInterruptedToolMarker+"; review the pending tools manually)", "tab", id)
		return
	}
	// 任务840 顺序：已消费引导的停靠行先结算下架，再清门。
	a.settlePlannedRestartGuidanceForTab(tab)
	cleared, err := ctrl.ResumeInboxAutoPause()
	if err != nil {
		a.recordWakeOutcome(wakeLedgerEntry{
			SessionPath: sessionPath,
			Claimant:    string(wakeClaimantRestore),
			Outcome:     wakeFailed,
			Reason:      "resume_inbox_auto_pause: " + err.Error(),
		})
		slog.Warn("desktop: restart recovery gate auto-continue failed", "tab", id, "err", err)
		return
	}
	if !cleared {
		// 用户在快照与清门之间恰好暂停（709 竞态口），或门已被同恢复点
		// 的名册链清掉——两种都不留痕、不踢派发。
		a.recordWakeOutcome(wakeLedgerEntry{
			SessionPath: sessionPath,
			Claimant:    string(wakeClaimantRestore),
			Outcome:     wakeSkipped,
			Reason:      "gate_race_user_pause_or_already_cleared",
		})
		return
	}
	a.recordWakeOutcome(wakeLedgerEntry{
		SessionPath: sessionPath,
		Claimant:    string(wakeClaimantRestore),
		Outcome:     wakeResumed,
		Reason:      "restart_gate_cleared+dispatch_kick",
	})
	slog.Info("desktop: restart recovery gate auto-continued (868+tl1357)", "tab", id, "recovered", snap.RecoveredN)
	a.noticeCodeForTab(id, event.LevelInfo, event.NoticeCodeRestartGateAutoContinue, restartGateAutoContinueText)
	// 踢一次派发：ClaimItem 的暂停闸已开，积压队列由常规调度器按 FIFO
	// 开轮消化（与启动时 runtime 发布后的 kick 同路径；忙时自动拒绝，
	// 回合完成自动再踢）。
	if kicker, ok := ctrl.(interface{ NotifyInboxRuntimeReady() }); ok {
		kicker.NotifyInboxRuntimeReady()
	}
}
