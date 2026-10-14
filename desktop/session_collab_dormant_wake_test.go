package main

// 任务 887 验收映射（dormant 反查 + 按需拉起）：
// ① 新建/从未打开的会话收到跨会话消息后自动开轮——单测模拟该形态：
//    durable manifest 里有排队 follow-up、本进程无任何 runtime（covered 空、
//    无 tab）→ 反查命中 → 桥拉起（wake seam 被调用）→ RunInboxTurn 真执行
//    （runner 收到排队原文）。真实 Store 写 manifest、真实 ProbeSnapshot 读、
//    真实 Controller 消费，只有 App 壳不在场（与 579 e2e 同一取舍）。
// ② 既有 569/579 链路零回归：本文件不改 sweepTargets 合同，回归由既有
//    idle_turn/548/585/709 族测试钉住（包级跑）。
// ③ 拉起失败有界重试+回执：wake 失败走 579 三振预算，3 次后封存队首、
//    exhausted 钩子带 570 回执坐标只触发一次，之后不再调用 wake。
// ④ 无人值守滥用面=只有真实排队 follow-up 才拉起：covered / 有 tab 表面 /
//    无 manifest / 无队首（uncertain 人审架）/ UserPaused / archived /
//    无 contact 全部不生成候选；ErrProbeBusy 静默，其余 probe 错误上报。

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/sessioninbox"
)

// dormantEnqueue writes one queued follow-up through the real Store path and
// closes it — the durable state left behind when a runtime is gone.
func dormantEnqueue(t *testing.T, sessionPath string, collabMsgID string) sessioninbox.InboxReceipt {
	t.Helper()
	st, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	rec, err := st.Enqueue(sessioninbox.EnqueueRequest{
		Intent: sessioninbox.IntentFollowup,
		Envelope: sessioninbox.PromptEnvelope{
			DisplayText:  "dormant queued instruction",
			RawText:      "dormant queued instruction",
			SubmitText:   "dormant queued instruction",
			CollabMsgID:  collabMsgID,
			CollabMailTo: "contact-peer",
		},
		Source: "collab:contact-peer",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return rec
}

// dormantRoster builds one roster entry per (contact, path).
func dormantRoster(entries ...string) map[string]sessioncollab.Identity {
	out := map[string]sessioncollab.Identity{}
	for i := 0; i+1 < len(entries); i += 2 {
		out[entries[i]] = sessioncollab.Identity{ContactID: entries[i], SessionPath: entries[i+1], Scope: "global"}
	}
	return out
}

// 验收①：无 runtime（covered 空、无 tab）+ durable manifest 有排队 follow-up
// → 反查命中 → 桥拉起（wake seam 恰好一次）→ RunInboxTurn 真执行，runner
// 收到排队原文。空闲窗口照旧：未满 N 不拉起。
func TestDormantWakeOpensQueuedTurnWithoutRuntime(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	dormantEnqueue(t, sessionPath, "msg_dormant_1")

	// 反查核心：真实只读 probe（manifest 由真实 Store 写出，无 runtime 在场）。
	cands := collectDormant(
		dormantRoster("contact-self", sessionPath),
		nil,                                // covered: no live runtime this pass
		func(string) bool { return false }, // no tab surface
		sessioninbox.ProbeSnapshot,
		func(_, _ string, err error) { t.Fatalf("probe must not fail: %v", err) },
	)
	if len(cands) != 1 {
		t.Fatalf("want exactly 1 dormant candidate, got %d", len(cands))
	}
	if cands[0].head.CollabMsgID != "msg_dormant_1" || cands[0].head.CollabMailTo != "contact-peer" {
		t.Fatalf("receipt coords lost in reverse lookup: %+v", cands[0].head)
	}

	log := &runLog{}
	var wakeCalls int32
	var wokenMu sync.Mutex
	var woken []*control.Controller
	t.Cleanup(func() {
		wokenMu.Lock()
		defer wokenMu.Unlock()
		for _, c := range woken {
			c.Close()
		}
	})
	view := dormantTurnView(cands[0], dormantWakeDeps{
		// 拉起 = stand up a real controller on the same session path — the
		// seam boundary between "App opens the surface" and "bridge runs the turn".
		wake: func(id sessioncollab.Identity) (control.SessionAPI, error) {
			atomic.AddInt32(&wakeCalls, 1)
			ctrl := control.New(control.Options{
				Runner:      idleTurnRunner(func(input string) { log.add(input) }),
				Sink:        event.FuncSink(func(event.Event) {}),
				SessionDir:  dir,
				SessionPath: sessionPath,
			})
			wokenMu.Lock()
			woken = append(woken, ctrl)
			wokenMu.Unlock()
			return ctrl, nil
		},
		isActive: func(string) bool { return false },
		exhausted: func(contactID, itemID, collabMsgID, collabMailTo, source string, attempts int, lastErr error) {
			t.Errorf("no failure expected, got exhausted(contact=%s item=%s attempts=%d err=%v)", contactID, itemID, attempts, lastErr)
		},
	})

	b := newIdleTurnBridge()
	now := time.Now()
	// 窗口未满：不拉起。
	b.sweepTargets([]idleTurnTargetView{view}, now)
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay-time.Second))
	if got := atomic.LoadInt32(&wakeCalls); got != 0 {
		t.Fatalf("wake must wait out the idle window, called %d times", got)
	}
	// 满 N：拉起 + RunInboxTurn 执行。
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	got := waitRunLog(t, log, 1)
	if len(got) != 1 || got[0] != "dormant queued instruction" {
		t.Fatalf("RunInboxTurn must consume the queued instruction, got %v", got)
	}
	if atomic.LoadInt32(&wakeCalls) != 1 {
		t.Fatalf("wake must be called exactly once (memoized across resume/run), got %d", wakeCalls)
	}
}

// 验收④：只有真实排队 follow-up 才生成候选，逐条排除面。
func TestDormantDiscoveryOnlyRealQueuedFollowup(t *testing.T) {
	dir := t.TempDir()
	queuedPath := filepath.Join(dir, "queued.jsonl")
	dormantEnqueue(t, queuedPath, "msg_queued")

	// live: contact 已被本轮 live runtime 覆盖 → 排除（用 queuedPath 同款 manifest 证明排除来自 covered 而非探测）。
	tabbedPath := filepath.Join(dir, "tabbed.jsonl")
	dormantEnqueue(t, tabbedPath, "msg_tabbed")

	uncertainPath := filepath.Join(dir, "uncertain.jsonl")
	uncertainRec := dormantEnqueue(t, uncertainPath, "msg_uncertain")
	ust, err := sessioninbox.Open(uncertainPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open uncertain store: %v", err)
	}
	// 唯一队首移出 queued → 796 H4 人审架，不拉起。
	if err := ust.SetState(uncertainRec.ItemID, sessioninbox.StateUncertain, "review"); err != nil {
		t.Fatalf("set uncertain: %v", err)
	}
	ust.Close()

	pausedPath := filepath.Join(dir, "userpaused.jsonl")
	dormantEnqueue(t, pausedPath, "msg_paused")
	pst, err := sessioninbox.Open(pausedPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open paused store: %v", err)
	}
	if err := pst.SetPaused(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := pst.SetUserPaused(true); err != nil {
		t.Fatalf("user pause: %v", err)
	}
	pst.Close()

	roster := dormantRoster(
		"live", queuedPath,
		"tabbed", tabbedPath,
		"queued", queuedPath,
		"uncertain", uncertainPath,
		"userpause", pausedPath,
		"nomanifest", filepath.Join(dir, "never-used.jsonl"),
		"archived", queuedPath,
		"nocontact", queuedPath,
	)
	roster["archived"] = sessioncollab.Identity{ContactID: "archived", SessionPath: queuedPath, Scope: "global", Archived: true}
	roster["nocontact"] = sessioncollab.Identity{SessionPath: queuedPath, Scope: "global"} // ContactID == ""

	covered := map[string]bool{"live": true}
	var probeErrs []string
	cands := collectDormant(roster, covered,
		func(path string) bool { return path == tabbedPath },
		sessioninbox.ProbeSnapshot,
		func(contact, _ string, err error) { probeErrs = append(probeErrs, contact+":"+err.Error()) },
	)
	if len(cands) != 1 || cands[0].id.ContactID != "queued" {
		names := make([]string, 0, len(cands))
		for _, c := range cands {
			names = append(names, c.id.ContactID)
		}
		t.Fatalf("only the real queued follow-up (no runtime, no tab) must be woken, got %v", names)
	}
	if len(probeErrs) != 0 {
		t.Fatalf("probe must not error for these fixtures: %v", probeErrs)
	}
}

// 验收④ + 反查容错：ErrProbeBusy 是写者持锁的常态竞争——静默跳过不上报
// （下一轮重试）；其余 probe 错误（损坏 manifest 等）必须上报给 pump 日志。
func TestDormantDiscoveryProbeErrorsClassified(t *testing.T) {
	roster := dormantRoster(
		"busy", "/x/busy.jsonl",
		"corrupt", "/x/corrupt.jsonl",
	)
	var reported []string
	cands := collectDormant(roster, nil, func(string) bool { return false },
		func(path string) (sessioninbox.InboxSnapshot, bool, error) {
			if filepath.Base(path) == "busy.jsonl" {
				return sessioninbox.InboxSnapshot{}, false, sessioninbox.ErrProbeBusy
			}
			return sessioninbox.InboxSnapshot{}, false, errors.New("decode manifest: boom")
		},
		func(contact, _ string, _ error) { reported = append(reported, contact) },
	)
	if len(cands) != 0 {
		t.Fatalf("probe failures must not produce candidates, got %d", len(cands))
	}
	if len(reported) != 1 || reported[0] != "corrupt" {
		t.Fatalf("ErrProbeBusy must be silent, other errors must be reported; got %v", reported)
	}
}

// 验收③：拉起失败走 579 三振预算——3 次 wake 失败后 exhausted 钩子（570 回执
// 坐标）恰好触发一次，且之后的 sweep 不再调用 wake（预算封存，队首保留）。
func TestDormantWakeFailureBoundedWithReceipt(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	dormantEnqueue(t, sessionPath, "msg_dormant_1")

	cands := collectDormant(dormantRoster("contact-self", sessionPath), nil,
		func(string) bool { return false }, sessioninbox.ProbeSnapshot, nil)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}

	var wakeCalls int32
	type exhaustedCall struct {
		itemID, msgID, mailTo, source string
		attempts                      int
	}
	var gotMu sync.Mutex
	var got []exhaustedCall
	exhaustedCount := func() int {
		gotMu.Lock()
		defer gotMu.Unlock()
		return len(got)
	}
	// Production shape: sweepIdleInboxTurns rebuilds the view from the probe
	// cache on EVERY pass, so each pass gets a fresh wake memo and the failed
	// wake is genuinely retried (the budget — not the memo — bounds it).
	newView := func() idleTurnTargetView {
		return dormantTurnView(cands[0], dormantWakeDeps{
			wake: func(sessioncollab.Identity) (control.SessionAPI, error) {
				atomic.AddInt32(&wakeCalls, 1)
				return nil, errors.New("lease held by another process")
			},
			isActive: func(string) bool { return false },
			exhausted: func(_, itemID, msgID, mailTo, source string, attempts int, _ error) {
				gotMu.Lock()
				defer gotMu.Unlock()
				got = append(got, exhaustedCall{itemID, msgID, mailTo, source, attempts})
			},
		})
	}

	b := newIdleTurnBridge()
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{newView()}, now) // start the idle clock
	fire := now.Add(sessionCollabIdleTurnDelay + time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && exhaustedCount() == 0 {
		// run() is async (safego.Go); re-sweep until the budget is spent.
		b.sweepTargets([]idleTurnTargetView{newView()}, fire)
		time.Sleep(20 * time.Millisecond)
	}
	gotMu.Lock()
	calls := append([]exhaustedCall(nil), got...)
	gotMu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("3 strikes must fire exhausted exactly once, got %d calls", len(calls))
	}
	if calls[0].attempts != idleTurnMaxAttempts {
		t.Fatalf("exhausted attempts = %d, want %d", calls[0].attempts, idleTurnMaxAttempts)
	}
	if calls[0].msgID != "msg_dormant_1" || calls[0].mailTo != "contact-peer" || calls[0].source != "collab:contact-peer" {
		t.Fatalf("570 receipt coords lost: %+v", calls[0])
	}
	if n := atomic.LoadInt32(&wakeCalls); n != idleTurnMaxAttempts {
		t.Fatalf("wake must be bounded at %d attempts, got %d", idleTurnMaxAttempts, n)
	}
	// 预算封存：后续 sweep 不再拉起、回执不重发。
	for i := 0; i < 3; i++ {
		b.sweepTargets([]idleTurnTargetView{newView()}, fire)
	}
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&wakeCalls); n != idleTurnMaxAttempts {
		t.Fatalf("budget must suppress further wakes, got %d calls", n)
	}
	if exhaustedCount() != 1 {
		t.Fatalf("exhausted must stay single-shot, got %d", exhaustedCount())
	}
}

// 709 自动暂停唤醒：autoPaused 候选开轮前先清自动暂停（turn 能 Claim 即为
// 证明），且 resume→run 共享同一次拉起（wake 恰好一次，memoized）。
func TestDormantWakeResumesAutoPauseBeforeRunning(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	dormantEnqueue(t, sessionPath, "msg_dormant_1")
	// Automatic pause only (UserPaused=false) — the restart-recovery shape.
	st, err := sessioninbox.Open(sessionPath, sessioninbox.Limits{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.SetPaused(true); err != nil {
		t.Fatalf("auto pause: %v", err)
	}
	st.Close()

	cands := collectDormant(dormantRoster("contact-self", sessionPath), nil,
		func(string) bool { return false }, sessioninbox.ProbeSnapshot, nil)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if !cands[0].autoPaused {
		t.Fatal("auto pause (Paused && !UserPaused) must be carried into the candidate")
	}

	log := &runLog{}
	var wakeCalls int32
	var wokenMu sync.Mutex
	var woken []*control.Controller
	t.Cleanup(func() {
		wokenMu.Lock()
		defer wokenMu.Unlock()
		for _, c := range woken {
			c.Close()
		}
	})
	view := dormantTurnView(cands[0], dormantWakeDeps{
		wake: func(sessioncollab.Identity) (control.SessionAPI, error) {
			atomic.AddInt32(&wakeCalls, 1)
			ctrl := control.New(control.Options{
				Runner:      idleTurnRunner(func(input string) { log.add(input) }),
				Sink:        event.FuncSink(func(event.Event) {}),
				SessionDir:  dir,
				SessionPath: sessionPath,
			})
			wokenMu.Lock()
			woken = append(woken, ctrl)
			wokenMu.Unlock()
			return ctrl, nil
		},
		isActive: func(string) bool { return false },
		exhausted: func(_, _, _, _, _ string, attempts int, err error) {
			t.Errorf("unexpected exhausted: attempts=%d err=%v", attempts, err)
		},
	})

	b := newIdleTurnBridge()
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	got := waitRunLog(t, log, 1)
	if len(got) != 1 {
		t.Fatalf("turn must run after the auto-pause is cleared, got %v", got)
	}
	if n := atomic.LoadInt32(&wakeCalls); n != 1 {
		t.Fatalf("resume→run must share one wake (memoized), got %d", n)
	}
}

// 边界2 竞态重检：拉起期间用户恰好打开了该会话（isActive）→ run 让路
// （ErrTurnRunning 被 idleTurnRaceError 分类为竞态：不烧预算、不写回执）。
func TestDormantWakeYieldsWhenSessionBecomesActive(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	dormantEnqueue(t, sessionPath, "msg_dormant_1")

	cands := collectDormant(dormantRoster("contact-self", sessionPath), nil,
		func(string) bool { return false }, sessioninbox.ProbeSnapshot, nil)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}

	var wakeCalls int32
	var exhausted atomic.Int32
	var wokenMu sync.Mutex
	var woken []*control.Controller
	t.Cleanup(func() {
		wokenMu.Lock()
		defer wokenMu.Unlock()
		for _, c := range woken {
			c.Close()
		}
	})
	view := dormantTurnView(cands[0], dormantWakeDeps{
		wake: func(sessioncollab.Identity) (control.SessionAPI, error) {
			atomic.AddInt32(&wakeCalls, 1)
			ctrl := control.New(control.Options{
				Runner:      idleTurnRunner(func(string) {}),
				Sink:        event.FuncSink(func(event.Event) {}),
				SessionDir:  dir,
				SessionPath: sessionPath,
			})
			wokenMu.Lock()
			woken = append(woken, ctrl)
			wokenMu.Unlock()
			return ctrl, nil
		},
		isActive: func(string) bool { return true },
		exhausted: func(_, _, _, _, _ string, _ int, _ error) {
			exhausted.Add(1)
		},
	})

	b := newIdleTurnBridge()
	now := time.Now()
	b.sweepTargets([]idleTurnTargetView{view}, now)
	b.sweepTargets([]idleTurnTargetView{view}, now.Add(sessionCollabIdleTurnDelay+time.Second))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&wakeCalls) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt32(&wakeCalls); n != 1 {
		t.Fatalf("wake must have been attempted once, got %d", n)
	}
	if exhausted.Load() != 0 {
		t.Fatal("race-classified yield must not fire the exhaustion receipt")
	}
	b.mu.Lock()
	strikes := 0
	for _, n := range b.failCounts {
		strikes += n
	}
	b.mu.Unlock()
	if strikes != 0 {
		t.Fatalf("race yield must not burn budget, strikes=%d", strikes)
	}
}
