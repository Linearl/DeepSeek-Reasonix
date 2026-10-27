package main

// 任务 887：空闲开轮桥的第三态——「有排队 follow-up 但本进程没有任何 runtime」
// 的会话（从未打开 / 重启后不落盘），此前不在任何桥 target 集合里：
//   - sessionCollabLiveTargets = 可见 tab + detached runtime（都要求 ctrl 已发布）；
//   - drain() 的补位开流只覆盖 pendingContacts（MailStore 里**未 ack** 的邮件）。
// 一条已投递、已 ack、落进目标 durable inbox 的排队 follow-up，一旦 runtime
// 消失（进程重启、tab 关闭），两条路都不再认领它——只能等人手动打开一次。
// 569/579 解决的是「detached 找不到 owner」，本件补上「从未打开」这一态。
//
// 修法（任务书方案 A，接桥非造桥）：桥在 sweep 时做一次 catalog（名册）+
// inbox manifest 的**只读**反查（sessioninbox.ProbeSnapshot，切片1），把
// 「有 StateQueued 项、无 runtime」的会话作为额外的 idleTurnTargetView 注入
// 同一次 sweepTargets：空闲窗口、579 三振预算、in-flight 去重、570
// open_retry_exhausted 回执、709 自动暂停唤醒全部原样复用；唯一新增动作是
// run 前的「按需拉起 runtime」（复用 drain 同款 OpenTopicSession/Detached
// 入口，但永不 activate——开轮不许打断用户，边界2）。
//
// 反查纪律：
//   - 只读：ProbeSnapshot 不建目录、不取写锁、不跑 recovery——4s/15s 节奏的
//     探测绝不能改写 manifest（否则探测本身会把队列标成 recovered+paused）。
//   - 防空转（验收④）：只有 probe 真实看到 StateQueued 队首才生成 view；
//     archived / 无 contact / 已有活 runtime（Ctrl 已发布或构建在飞
//     buildCancel!=nil；ReadOnly/Spectator 接管面同判）一律不拉起；
//     Ctrl=nil 且无构建排队的 restored skeleton tab（任务 619 ②）**不算**
//     覆盖——它无人拥有，正是 1185 的盲区，必须由桥认领；UserPaused 队列
//     绝不唤醒（709：暂停是用户意志）。
//   - 有界（验收③）：拉起失败走 sweepTargets 既有的 579 三振预算——3 次后
//     封存队首、写 570 open_retry_exhausted 回执（消息保留不丢弃）。
//   - 与守卫④同门：本文件所有入口都挂在 sweepIdleInboxTurns 之下，
//     drain_inbox 模式（task 224）下整体停用，不叠加。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/secrets"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// dormantProbeInterval throttles the catalog+manifest reverse lookup: between
// two probes the cached candidates feed the sweep. The queued item itself has
// been stranded for as long as the runtime has been gone — a ≤15s discovery
// latency on top of the 45s idle window is well inside the bridge's "约1分钟"
// promise. Var so tests can drive probe rounds deterministically.
var dormantProbeInterval = 15 * time.Second

// dormantWakeCtrlTimeout bounds one wake's wait for the stood-up runtime to
// publish its controller: cold builds measured 3-5s, the worst observed
// (lease-blocked rebuild retrying every 2s) ~30s (task 738), so 60s covers it
// with margin. Timeout counts as an opening strike — bounded by the 579
// budget, never an unbounded goroutine.
var dormantWakeCtrlTimeout = 60 * time.Second

// dormantWakeCtrlPoll is the controller-publish poll cadence of one wake.
const dormantWakeCtrlPoll = 200 * time.Millisecond

// dormantQueuedTarget is one reverse-lookup hit: a roster session with a
// durable queued follow-up and no live surface in this process.
type dormantQueuedTarget struct {
	id    sessioncollab.Identity // ContactID / SessionPath / Scope / Workspace / TopicID
	head  sessioninbox.InboxItemMeta
	items []sessioninbox.InboxItemMeta
	// autoPaused mirrors manifest.Paused&&!UserPaused: an automatic pause
	// (restart recovery / reopen backlog) is cleared by the 709 resume seam
	// before the wake opens the turn; a UserPaused queue is never probed into
	// a target at all.
	autoPaused bool
}

// collectDormant is the pure core of the 887 reverse lookup (seams injected so
// the sweep logic is unit-testable without a desktop App — same pattern as
// idleTurnTargetView). covered = contacts with a live runtime this pass;
// hasLiveRuntime = the session path has a tab that owns a runtime (published
// controller or in-flight boot — 任务 1185 narrowed this from "any tab
// surface": a restored skeleton owns nothing and must be claimed); probe is
// the read-only manifest peek.
func collectDormant(
	roster map[string]sessioncollab.Identity,
	covered map[string]bool,
	hasTab func(sessionPath string) bool,
	probe func(sessionPath string) (sessioninbox.InboxSnapshot, bool, error),
	onProbeErr func(contact, sessionPath string, err error),
) []dormantQueuedTarget {
	var out []dormantQueuedTarget
	for contact, id := range roster {
		// ContactID emptiness is checked on the identity, not just the map
		// key: the bridge keys all per-target state by contact, an
		// unaddressable session must never wake.
		if contact == "" || id.ContactID == "" || id.Archived || id.SessionPath == "" {
			continue
		}
		if covered[contact] || hasTab(id.SessionPath) {
			continue
		}
		snap, found, err := probe(id.SessionPath)
		if err != nil {
			// ErrProbeBusy is ordinary writer contention — the next probe
			// round retries; anything else (corrupt manifest) is worth a log.
			if !errors.Is(err, sessioninbox.ErrProbeBusy) && onProbeErr != nil {
				onProbeErr(contact, id.SessionPath, err)
			}
			continue
		}
		if !found {
			continue
		}
		if snap.Paused && snap.UserPaused {
			// 709 absolute quiet: a user-held pause is the user's will — the
			// wake never crosses it, not even to stand up a runtime.
			continue
		}
		var head sessioninbox.InboxItemMeta
		hasQueued := false
		for _, item := range snap.Items {
			if item.State == sessioninbox.StateQueued {
				head = item
				hasQueued = true
				break
			}
		}
		if !hasQueued {
			// 验收④：uncertain/blocked 队列是人审架（796 H4），不拉起。
			continue
		}
		out = append(out, dormantQueuedTarget{
			id:         id,
			head:       head,
			items:      snap.Items,
			autoPaused: snap.Paused && !snap.UserPaused,
		})
	}
	return out
}

// dormantWakeDeps are the App-bound seams of one dormant view, injected so the
// view logic is unit-testable without a desktop App.
type dormantWakeDeps struct {
	wake     func(id sessioncollab.Identity) (control.SessionAPI, error)
	isActive func(sessionPath string) bool
	// exhausted mirrors the live wiring: 570 receipt, plus the 731 autopilot
	// escalation only when a tab exists (dormant views have none — a failed
	// wake leaves no surface to escalate from, the receipt is the truth).
	exhausted func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error)
}

// dormantTurnView builds the sweep view for one dormant candidate. The wake is
// memoized per view instance so sweepTargets' resume→run sequence (709
// auto-pause) stands the runtime up exactly once; a fresh view each sweep pass
// means a failed wake is genuinely retried on the next pass (bounded by the
// 579 budget, which is keyed by (contact, head item) and survives view rebuilds).
func dormantTurnView(c dormantQueuedTarget, deps dormantWakeDeps) idleTurnTargetView {
	var (
		once    sync.Once
		ctrl    control.SessionAPI
		wakeErr error
	)
	ensure := func() (control.SessionAPI, error) {
		once.Do(func() { ctrl, wakeErr = deps.wake(c.id) })
		return ctrl, wakeErr
	}
	view := idleTurnTargetView{
		contactID: c.id.ContactID,
		// tabID stays empty: no tab exists until the wake stands one up, so
		// the 731 tab escalation is unavailable for this view (receipt only).
		running:            false, // by definition: no runtime in this process
		activeTab:          false, // no tab at all
		autoPaused:         c.autoPaused,
		itemsSummary:       summarizeInboxItems(c.items),
		queuedID:           c.head.ID,
		queuedCollabMsgID:  c.head.CollabMsgID,
		queuedCollabMailTo: c.head.CollabMailTo,
		queuedSource:       c.head.Source,
		// resume: sweepTargets calls this before run for autoPaused views
		// (709 order). It stands the runtime up too — the memoized ensure
		// shares that cost with run below.
		resume: func() error {
			ctrl, err := ensure()
			if err != nil {
				return err
			}
			_, err = ctrl.ResumeInboxAutoPause()
			return err
		},
		run: func(ctx context.Context, itemID string) error {
			ctrl, err := ensure()
			if err != nil {
				return err // wake failure → strike → 579 budget → 570 receipt
			}
			// 边界2 re-check at fire time: if the user opened this session
			// while the wake stood it up, the live path owns it now — yield
			// (race-classified: no budget strike, no receipt).
			if deps.isActive != nil && deps.isActive(c.id.SessionPath) {
				return control.ErrTurnRunning
			}
			// Defensive pause re-check against the FRESH controller snapshot:
			// the probe cache is ≤dormantProbeInterval stale, and the store's
			// cross-process recovery (RunID change) pauses the queue on first
			// open even when the probe saw it unpaused (709 auto path).
			if snap := ctrl.InboxSnapshot(); snap.Paused {
				if snap.UserPaused {
					return sessioninbox.ErrPaused // race-classified: user took the wheel
				}
				if _, rerr := ctrl.ResumeInboxAutoPause(); rerr != nil {
					return rerr
				}
			}
			return ctrl.RunInboxTurn(ctx, itemID)
		},
		exhausted: deps.exhausted,
	}
	return view
}

// dormantIdleTurnTargets is the pump adapter: refresh the cached reverse-lookup
// at most every dormantProbeInterval, then build one sweep view per still-
// dormant candidate. The hasTab/covered guards are re-checked at build time so
// a session opened between probes is dropped immediately (its live view, if
// any, owns it).
func (p *sessionCollabPump) dormantIdleTurnTargets(now time.Time, covered map[string]bool) []idleTurnTargetView {
	if p == nil || p.app == nil {
		return nil
	}
	app := p.app
	p.dormantMu.Lock()
	if p.dormantProbeAt.IsZero() || now.Sub(p.dormantProbeAt) >= dormantProbeInterval {
		p.dormant = collectDormant(
			app.collabRosterIndex(),
			covered,
			app.sessionHasLiveRuntimeForPath,
			sessioninbox.ProbeSnapshot,
			func(contact, sessionPath string, err error) {
				// Rate-limited by the probe interval itself (≤1 log / 15s per
				// broken session); ErrProbeBusy never reaches here.
				log.Printf("[session-collab] dormant probe failed for contact %s (%s): %v", contact, sessionPath, err)
			},
		)
		p.dormantProbeAt = now
	}
	candidates := append([]dormantQueuedTarget(nil), p.dormant...)
	p.dormantMu.Unlock()

	views := make([]idleTurnTargetView, 0, len(candidates))
	for _, c := range candidates {
		if covered[c.contactID()] || app.sessionHasLiveRuntimeForPath(c.id.SessionPath) {
			continue
		}
		views = append(views, dormantTurnView(c, dormantWakeDeps{
			wake:      p.wakeDormantRuntime,
			isActive:  app.isActiveSessionPath,
			exhausted: p.recordIdleTurnExhausted,
		}))
	}
	return views
}

// contactID is a tiny accessor keeping the build-time guard readable.
func (c dormantQueuedTarget) contactID() string { return c.id.ContactID }

// wakeDormantRuntime stands up (or attaches to) the runtime of one dormant
// session and returns its published controller. Never activates the opened
// tab: the wake runs a turn in a session the user is not looking at — stealing
// focus would violate 边界2. With session_collab_background on it parks
// detached (task 264), off it opens a visible-but-inactive tab.
func (p *sessionCollabPump) wakeDormantRuntime(id sessioncollab.Identity) (control.SessionAPI, error) {
	if p == nil || p.app == nil {
		return nil, errors.New("dormant wake: pump has no app")
	}
	// Already live (user opened it, or drain's pending-mail stand-up won the
	// race): attach instead of opening a second surface.
	if ctrl, _ := p.app.dormantWakeCtrlForSessionPath(id.SessionPath); ctrl != nil {
		return ctrl, nil
	}
	// 任务1206（幂等闸）：开流前向仲裁层认领。认领失败 = 另一触发源（①drain
	// 补位 / ③恢复链）正在拉起同一会话——按竞态让位（ErrTurnRunning 同门：
	// 不计 strike、不写 570 回执），队列消息由认领方拉起的 runtime 消化。
	release, ok := p.app.claimWake(id.SessionPath, wakeClaimantBridge)
	if !ok {
		p.app.recordWakeOutcome(wakeLedgerEntry{
			SessionPath: id.SessionPath,
			ContactID:   id.ContactID,
			Claimant:    string(wakeClaimantBridge),
			Outcome:     wakeSkipped,
			Reason:      wakeStandUpInFlightReason,
		})
		return nil, control.ErrTurnRunning
	}
	defer release()
	log.Printf("[session-collab] dormant wake: standing up runtime for contact %s (%s)", id.ContactID, id.SessionPath)
	var err error
	if collabOpenDetached() {
		_, err = p.app.OpenTopicSessionDetached(id.Scope, id.Workspace, id.TopicID, id.SessionPath)
	} else {
		// openTopicSessionInactive (not OpenTopicSession): never activate —
		// 边界2. The turn fires in a session the user is not focused on.
		_, err = p.app.openTopicSessionInactive(id.Scope, id.Workspace, id.TopicID, id.SessionPath)
	}
	if err != nil {
		// Redact before the error can reach the strike log AND the 570
		// receipt detail (recordIdleTurnExhausted formats lastErr verbatim);
		// open errors may echo provider env-var names (same class drain
		// already redacts).
		p.app.recordWakeOutcome(wakeLedgerEntry{
			SessionPath: id.SessionPath,
			ContactID:   id.ContactID,
			Claimant:    string(wakeClaimantBridge),
			Outcome:     wakeFailed,
			Reason:      "wake open: " + secrets.RedactError(err),
		})
		return nil, errors.New("wake open: " + secrets.RedactError(err))
	}
	deadline := time.Now().Add(dormantWakeCtrlTimeout)
	for {
		ctrl, startupErr := p.app.dormantWakeCtrlForSessionPath(id.SessionPath)
		if ctrl != nil {
			p.app.recordWakeOutcome(wakeLedgerEntry{
				SessionPath: id.SessionPath,
				ContactID:   id.ContactID,
				Claimant:    string(wakeClaimantBridge),
				Outcome:     wakeOpened,
			})
			return ctrl, nil
		}
		if startupErr != "" {
			// Build already failed (lease held elsewhere, bad config): fail
			// fast instead of burning the whole timeout on each strike.
			p.app.recordWakeOutcome(wakeLedgerEntry{
				SessionPath: id.SessionPath,
				ContactID:   id.ContactID,
				Claimant:    string(wakeClaimantBridge),
				Outcome:     wakeFailed,
				Reason:      "wake build failed: " + secrets.RedactError(errors.New(startupErr)),
			})
			return nil, errors.New("wake build failed: " + secrets.RedactError(errors.New(startupErr)))
		}
		if time.Now().After(deadline) {
			p.app.recordWakeOutcome(wakeLedgerEntry{
				SessionPath: id.SessionPath,
				ContactID:   id.ContactID,
				Claimant:    string(wakeClaimantBridge),
				Outcome:     wakeFailed,
				Reason:      fmt.Sprintf("runtime for %s did not publish within %s", id.ContactID, dormantWakeCtrlTimeout),
			})
			return nil, fmt.Errorf("runtime for %s did not publish within %s", id.ContactID, dormantWakeCtrlTimeout)
		}
		time.Sleep(dormantWakeCtrlPoll)
	}
}

// dormantWakeCtrlForSessionPath 与 sessionHasLiveRuntimeForPath 于任务 1206
// 搬入仲裁层（session_wake_arbiter.go）：dormant 反查不再自带 tab/runtime
// 判定，与另外三个触发源共用 canWakeSession 单点。本文件保留两者的调用点
// （seam 注入 + build-time 复查 + wake attach 探测），判定本体在仲裁层。

// isActiveSessionPath is the fire-time 边界2 guard: true when the session's
// tab is currently the focused surface.
func (a *App) isActiveSessionPath(sessionPath string) bool {
	want := sessionRuntimeKey(sessionPath)
	if want == "" || a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	active := a.activeTabLocked()
	if active == nil {
		return false
	}
	return tabMatchesSessionKey(active, want)
}

// tabMatchesSessionKey matches one tab against a folded session-runtime key
// via its persisted SessionPath, falling back to the controller's live path
// (a booting tab may have only one of the two).
func tabMatchesSessionKey(tab *WorkspaceTab, want string) bool {
	if tab == nil || want == "" {
		return false
	}
	if tp := tab.SessionPath; tp != "" && sessionRuntimeKey(tp) == want {
		return true
	}
	if tab.Ctrl != nil {
		if sp := tab.Ctrl.SessionPath(); sp != "" && sessionRuntimeKey(sp) == want {
			return true
		}
	}
	return false
}
