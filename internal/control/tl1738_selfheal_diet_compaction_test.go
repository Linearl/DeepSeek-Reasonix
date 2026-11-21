package control

// tl1738 复现钉：压缩在飞 × 自愈 diet rewrite / park 放行。
//
// 现场（20261120 15:52-15:55，desktop.log.4）：15:52:31 压力压缩 pass 启动
// （snapshot 取自 927 消息），15:52:35 resume 自愈的后台 diet rewrite 落地
// （RewriteAndSaveCompact → Session.Rewrite 改写 canonical，绕过 tl1669 await
// 漏斗），15:55:09 pass commit CAS 三重失配 → "conversation changed while
// compression was running"，pass 整条作废（user_dropped=6，本代重试阻断）。
//
// 钉 A1（主钉，15:55 形态）：压缩在飞时 SelfHealOpenSession 的 diet 必须
// 让路（defer），pass 必须 COMMIT；diet rewrite 落进 pass 窗口即红。
// 钉 A2（能力保留）：无压缩在飞时 diet 照常落地——让路不得吃掉自愈本身。
// 钉 B1（放行防线回归钉，18:30/20:34 形态的 control 侧防线）：压缩在飞时
// onCompactionSettled 的 dequeue 重检必须持队不放行；pass settle 后放行、
// parked 文本落盘、pass COMMIT。
//
// 工程纪律：构造失败一律 t.Fatal，禁止 SKIP。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1738OverWindowSession builds the incident-shaped transcript: bulk
// assistant text (prune cannot reclaim it) over a small health window, so
// InspectSessionHealth flags projection_overflow and the diet has real work
// (fold) to do.
func tl1738OverWindowSession(t *testing.T) *agent.Session {
	t.Helper()
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "standing constraint: never change the public API"})
	big := strings.Repeat("word ", 400)
	for i := 0; i < 6; i++ {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: big})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	}
	return sess
}

// tl1738SelfHealController wires a real controller + foldable agent over a
// session that is ALSO persisted to disk (the health inspector reads the
// file, the diet rewrites the live canonical — both faces must exist).
func tl1738SelfHealController(t *testing.T, prov *gateFoldProvider) (*Controller, *agent.Agent, string) {
	t.Helper()
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	sess := tl1738OverWindowSession(t)
	if err := sess.Save(sessionPath); err != nil {
		t.Fatalf("persist the session transcript: %v", err)
	}
	ag := agent.New(prov, tool.NewRegistry(), sess, agent.Options{
		ContextWindow: 5000,
		CompactRatio:  0.5,
		RecentKeep:    2,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: event.Discard, SessionPath: sessionPath})
	return c, ag, sessionPath
}

// setSessionSelfHealGateForTest turns the self-heal switch on for the test
// and restores the nil (disabled) default on cleanup.
func setSessionSelfHealGateForTest(t *testing.T) {
	t.Helper()
	SetSessionSelfHealGate(func() bool { return true })
	t.Cleanup(func() { SetSessionSelfHealGate(nil) })
}

// startGatedCompaction mirrors startBackgroundCompaction but keeps the pass's
// error — the burned-pass verdict (errCompressStaleContext) is the red face.
func startGatedCompaction(ag *agent.Agent) <-chan error {
	errc := make(chan error, 1)
	go func() { errc <- ag.PrepareContext(context.Background()) }()
	return errc
}

// TestTl1738SelfHealDietDefersToInFlightCompaction is the A1 pin (the
// 20261120 15:52-15:55 incident shape): with the summary request in flight,
// the open-session heal must NOT land its diet rewrite — a Session.Rewrite
// inside the pass's snapshot→commit window moves the transcript under the
// commit CAS (compact_commit.go) and burns the whole pass. The heal defers;
// the pass commits.
func TestTl1738SelfHealDietDefersToInFlightCompaction(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	c, ag, _ := tl1738SelfHealController(t, prov)
	setSessionSelfHealGateForTest(t)

	compactionErr := startGatedCompaction(ag)
	<-prov.entered // the summary request is in flight (maintaining > 0)

	ver0, len0 := ag.Session().TranscriptVersion(), ag.Session().Len()

	out := c.SelfHealOpenSession(300) // small window: overflow flags, diet wants to act

	if out.Dieted {
		t.Fatal("the diet rewrite landed while the compaction pass was in flight — it moves the canonical under the pass's commit CAS (the 15:55:09 burn)")
	}
	if !strings.Contains(out.Detail, "diet_deferred_compaction_in_flight") {
		t.Fatalf("deferred outcome not reported: detail=%q actions=%v", out.Detail, out.Actions)
	}
	if ver, length := ag.Session().TranscriptVersion(), ag.Session().Len(); ver != ver0 || length != len0 {
		t.Fatalf("the canonical mutated inside the pass window: version %d→%d len %d→%d", ver0, ver, len0, length)
	}

	close(prov.release) // the summarizer answers → the pass must commit
	select {
	case err := <-compactionErr:
		if err != nil {
			t.Fatalf("the compaction pass failed instead of committing: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the compaction pass never resolved")
	}
}

// TestTl1738SelfHealDietStillAppliesWithoutCompaction is the A2 pin: the
// defer path must not eat the heal itself. With no compaction in flight the
// same overflow session gets its diet (strip+fold) landed on the canonical.
func TestTl1738SelfHealDietStillAppliesWithoutCompaction(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	close(prov.release) // nothing ever blocks
	c, ag, _ := tl1738SelfHealController(t, prov)
	setSessionSelfHealGateForTest(t)

	before := ag.Session().Len()
	out := c.SelfHealOpenSession(300)
	if !out.Dieted {
		t.Fatalf("the diet never applied with no compaction in flight (detail=%q) — the defer path ate the heal", out.Detail)
	}
	if after := ag.Session().Len(); after >= before {
		t.Fatalf("the diet did not fold the canonical: len %d→%d", before, after)
	}
}

// TestTl1738ParkedDrainHoldsWhileCompactionInFlight is the B1 pin (the
// control-side defense line for the 20261117 18:30/20:34 shapes): the settle
// hook's dequeue must re-check the in-flight gate — a settle firing (or being
// replayed) while a pass is STILL in flight must not spawn the parked body
// into the pass's window. After the pass settles the queue drains, the parked
// text lands in the transcript, and the pass COMMITS.
func TestTl1738ParkedDrainHoldsWhileCompactionInFlight(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	c, ag, sessionPath := tl1738SelfHealController(t, prov)

	compactionErr := startGatedCompaction(ag)
	<-prov.entered

	text := "tl1738 B1：压缩在飞 park，settle 前不放行，settle 后放行落盘"
	submitDone := make(chan struct{})
	go func() {
		defer close(submitDone)
		c.SubmitDisplay(text, text)
	}()
	waitCond(t, "submit parked behind in-flight compaction", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parkedTurns) == 1
	})

	// A settle that fires while the pass is still in flight (hook replay /
	// double-fire / post-failure settle racing a manual pass) must hold the
	// queue: the dequeue re-check reads CompactionInFlight first.
	c.onCompactionSettled()
	c.mu.Lock()
	parked, running := len(c.parkedTurns), c.running
	c.mu.Unlock()
	if parked != 1 || running {
		t.Fatalf("a mid-pass settle drained the queue (parked=%d running=%v)", parked, running)
	}

	close(prov.release)
	select {
	case err := <-compactionErr:
		if err != nil {
			t.Fatalf("the compaction pass failed instead of committing: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the compaction pass never resolved")
	}

	// The settle after the pass drains the FIFO: the parked turn runs and the
	// user text lands as a real transcript row.
	<-submitDone
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}
