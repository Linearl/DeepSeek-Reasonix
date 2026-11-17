package agent

// tl1648x 压缩竞态新形态（auto-resume 注入面）：auto-resume 的 continue prompt
// 经正常 admission 提交（压缩未在飞→放行），resumed turn 自己 kick 起后台
// pressure 压缩（armRebuildEpisode/guardian/手动），随后 turn 内生的 host 引导
// （tl1650 通道 F：retry/nudge/advisory/续跑提示）经裸 conversation.Add 直落
// transcript——压缩窗口内任何 append 都让 commitSummaryProjection 的 CAS
// （transcriptVersion+前缀哈希，compact_commit.go）失配，整条 pass 被丢
// （errCompressStaleContext → summaryFailed → blocked，20261117 09:4x 现场
// user_dropped=18 形态）。首轮 tl1648 的 gate 只盖 steer 消费面，本文件钉
// 通道 F 的排队面（host_guidance_gate.go 统一收口）：
//  1. 压缩在飞 × host 引导到达 → 等待不落盘（CAS 前缀不动）；
//  2. settle 后等待中的引导落盘进上下文（Origin=host，排队闭环）；
//  3. 有界等待超时 → 照写（CAS 兜底保留，引导绝不静默丢失）；
//  4. 无压缩 → 即时落盘（现状零变化，零门成本）；
//  5. resume 形态全链路：注入排队 → 压缩 commit 三要素（version/len/前缀）
//     不失配 → settle → 注入落盘；对照半段证明 bypass 直写必然失配；
//  6. 源码断言：通道 F 写入点全部走统一收口，防新增旁路（tl1650 门禁思想）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func newGateTestAgent(t *testing.T) *Agent {
	t.Helper()
	return New(testutil.NewMock("m", testutil.Turn{Text: "done"}), tool.NewRegistry(), NewSession(""), Options{}, event.Discard)
}

// shrinkHostGuidanceWait shrinks the queue window for timeout-path tests and
// restores the production values on cleanup.
//
// tl1669 reconciliation: the 1648x-specific wait triple (hostGuidanceCompaction*
// ) was COLLAPSED onto the single shared await primitive
// (compaction_await.go compactionAwaitBound / compactionAwaitPoll) when the
// execution model became the synchronous await. The PIN keeps its shape — same
// assertion, same count — only the shrunk knobs moved.
func shrinkHostGuidanceWait(t *testing.T, bound time.Duration) {
	t.Helper()
	origBound, origPoll := compactionAwaitBound, compactionAwaitPoll
	compactionAwaitBound, compactionAwaitPoll = bound, 5*time.Millisecond
	t.Cleanup(func() {
		compactionAwaitBound, compactionAwaitPoll = origBound, origPoll
	})
}

// transcriptVersionLen is the CAS's first two comparands (compact_commit.go):
// version and message count.
func transcriptVersionLen(t *testing.T, a *Agent) (uint64, int) {
	t.Helper()
	msgs, ver := a.sess.conversation.snapshotMessagesVersion()
	return ver, len(msgs)
}

// TestTl1648xHostGuidanceWaitsForCompaction pins acceptance 1+2: guidance
// arriving while a real maintenance transaction is in flight (the resumed
// turn's own nudge racing a background pressure compaction) must not touch
// the transcript, and the same guidance lands once the pass settles.
func TestTl1648xHostGuidanceWaitsForCompaction(t *testing.T) {
	a := newGateTestAgent(t)

	// Compaction in flight (the same counter 任务920 sets past the fast path).
	a.maintaining.Add(1)
	ver0, len0 := transcriptVersionLen(t, a)

	landed := make(chan struct{})
	go func() {
		defer close(landed)
		a.addHostGuidance(HostGeneratedUserMessage("queued resume-side guidance"))
	}()
	select {
	case <-landed:
		t.Fatal("host guidance landed while compaction was in flight — the CAS would burn the pass")
	case <-time.After(300 * time.Millisecond):
		// Still queued: the transcript is untouched while the pass runs.
	}

	if ver, length := transcriptVersionLen(t, a); ver != ver0 || length != len0 {
		t.Fatalf("queued guidance mutated the transcript during compaction: version %d→%d len %d→%d", ver0, ver, len0, length)
	}

	// Pass settles (endMaintenance's last release) — the queued guidance lands.
	a.maintaining.Add(-1)
	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("queued host guidance never landed after the compaction settled")
	}
	msgs, ver := a.sess.conversation.snapshotMessagesVersion()
	if ver == ver0 || len(msgs) <= len0 {
		t.Fatalf("guidance did not grow the transcript after settle: version %d→%d len %d→%d", ver0, ver, len0, len(msgs))
	}
	last := msgs[len(msgs)-1]
	if !IsHostGeneratedUserMessage(last) || last.Content != "queued resume-side guidance" {
		t.Fatalf("landed guidance lost its host shape or content: %+v", last)
	}
}

// TestTl1648xHostGuidanceTimeoutWritesThrough pins acceptance 3: the queue
// wait is bounded; past the bound the guidance writes through so the CAS
// backstop (and the failure card visibility it feeds) stays the last resort —
// guidance is never silently dropped.
func TestTl1648xHostGuidanceTimeoutWritesThrough(t *testing.T) {
	a := newGateTestAgent(t)
	shrinkHostGuidanceWait(t, 80*time.Millisecond)

	a.maintaining.Add(1)
	start := time.Now()
	a.addHostGuidance(HostGeneratedUserMessage("writes through past the bound"))
	elapsed := time.Since(start)
	a.maintaining.Add(-1)

	if elapsed > 5*time.Second {
		t.Fatalf("bounded wait did not bound: %s", elapsed)
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	if len(msgs) == 0 || msgs[len(msgs)-1].Content != "writes through past the bound" {
		t.Fatal("timed-out guidance was dropped instead of writing through")
	}
}

// TestTl1648xHostGuidanceIdleImmediate pins acceptance 4: with no maintenance
// in flight the funnel is a pass-through (one atomic load) and the pre-tl1648x
// immediate-injection behavior is byte-identical.
func TestTl1648xHostGuidanceIdleImmediate(t *testing.T) {
	a := newGateTestAgent(t)
	if a.maintaining.Load() != 0 {
		t.Fatalf("test setup leaked an in-flight count: %d", a.maintaining.Load())
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.addHostGuidance(HostGeneratedUserMessage("immediate"))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle-path guidance did not land immediately — the idle face must stay zero-cost")
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	if len(msgs) != 1 || !IsHostGeneratedUserMessage(msgs[0]) {
		t.Fatalf("idle guidance landed wrong: %+v", msgs)
	}
}

// TestTl1648xResumeShapedInjectionDoesNotBreakCompactionCommit is the
// incident-shape pin (装机重启场景压缩不被 resume 注入打断): with a compaction
// in flight, a resume-shaped injection queues; the commit's CAS comparands
// (transcriptVersion, len, covered prefix) stay frozen so the pass commits;
// after settle the injection lands. The contrast half proves the bypass shape
// (bare Add, the pre-tl1648x behavior) invalidates the same comparands — the
// exact 09:4x user_dropped=18 mechanism.
func TestTl1648xResumeShapedInjectionDoesNotBreakCompactionCommit(t *testing.T) {
	a := newGateTestAgent(t)

	// A pass opens its window: freeze the CAS comparands the way
	// commitSummaryProjection snapshots them.
	a.maintaining.Add(1)
	ver0, len0 := transcriptVersionLen(t, a)

	landed := make(chan struct{})
	go func() {
		defer close(landed)
		a.addHostGuidance(HostGeneratedUserMessage("The app updated and restarted itself while this autopilot session was working."))
	}()
	time.Sleep(150 * time.Millisecond) // the injection arrives mid-window and queues

	// CAS freeze check: the queued injection cannot have moved version/len or
	// the covered prefix — commitSummaryProjection would install cleanly here.
	if ver, length := transcriptVersionLen(t, a); ver != ver0 || length != len0 {
		t.Fatalf("queued resume injection broke the CAS freeze: version %d→%d len %d→%d", ver0, ver, len0, length)
	}

	// Contrast: a bypassing writer (any append inside the window — steer was
	// gated in round one, channel F is the face this fix closes) moves the
	// version, which is exactly the errCompressStaleContext shape.
	a.sess.conversation.Add(provider.Message{Role: provider.RoleUser, Content: "bypasses the gate"})
	if ver, _ := transcriptVersionLen(t, a); ver == ver0 {
		t.Fatal("contrast setup failed: a bare Add must move the transcript version (the burn mechanism)")
	}
	a.sess.conversation = NewSession("") // reset for the settle half
	a.maintaining.Add(-1)

	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("queued resume injection never landed after settle")
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	if len(msgs) != 1 || !IsHostGeneratedUserMessage(msgs[0]) {
		t.Fatalf("resume-shaped injection landed wrong: %+v", msgs)
	}
}

// TestTl1648xHostGuidanceWriteSitesUseGate is the source-level assertion
// (tl1650 gate mindset): every channel-F write site in this package must go
// through the addHostGuidance funnel — a bare conversation.Add of a
// HostGeneratedUserMessage reintroduces the burn-the-pass bypass. File-level
// granularity, same honest limitation tl1650 documented: a second write site
// inside an already-funneled file using the exact bare call shape still trips
// this pin.
func TestTl1648xHostGuidanceWriteSitesUseGate(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "conversation.Add(HostGeneratedUserMessage(") {
			t.Errorf("%s writes channel-F guidance via bare conversation.Add — route it through addHostGuidance (tl1648x compaction gate)", name)
		}
	}
}
