package control

// 任务920 压缩单飞门（controller 半）：压缩事务在途时，用户新提交必须 park
// 进 parkedTurns FIFO、压完才执行（验收①）；无压缩时提交零额外等待（验收②）；
// 多个 park 按 FIFO 放行、与 581 parkedTurns 语义一致（验收③）。S3 边界：压缩
// 失败也要放行不卡死；running 期间到达的 park-path interrupt 在压缩收尾后才
// fire（不打断在途摘要——912 修掉的 context-canceled 白烧不能从门侧复活）。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// gateFoldProvider blocks the summary request until released, and answers
// ordinary turn requests with a text reply. Every request carries a label so
// tests can assert FIFO order.
type gateFoldProvider struct {
	entered     chan struct{} // closed when the summary request arrives
	release     chan struct{} // closed by the test to let the summary finish
	enteredOnce sync.Once
	failSummary bool
	turnMu      sync.Mutex
	turnSeen    []string
}

func (p *gateFoldProvider) Name() string { return "gate-fold" }

func (p *gateFoldProvider) isSummary(req provider.Request) bool {
	return len(req.Messages) > 0 &&
		strings.Contains(req.Messages[len(req.Messages)-1].Content, "Compact the preceding conversation prefix")
}

// lastUserText returns the LAST user-role message — the current turn's
// submitted text. Earlier user messages are fold residue ("continue",
// constraints) that would mask the label under test.
func (p *gateFoldProvider) lastUserText(req provider.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != provider.RoleUser {
			continue
		}
		// Skip the fold's own artifacts: the summary instruction (on summary
		// requests) and the installed <compaction-summary> projection message,
		// which is a user-role message at the head of every post-fold view.
		if strings.Contains(m.Content, "Compact the preceding") ||
			strings.Contains(m.Content, "<compaction-summary>") {
			continue
		}
		return m.Content
	}
	return ""
}

func (p *gateFoldProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if p.isSummary(req) {
		p.enteredOnce.Do(func() { close(p.entered) })
		<-p.release
		if p.failSummary {
			return nil, errors.New("terminal summarizer refusal")
		}
		ch := make(chan provider.Chunk, 2)
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "durable summary"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	p.turnMu.Lock()
	p.turnSeen = append(p.turnSeen, p.lastUserText(req))
	p.turnMu.Unlock()
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "answer"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func (p *gateFoldProvider) turns() []string {
	p.turnMu.Lock()
	defer p.turnMu.Unlock()
	return append([]string(nil), p.turnSeen...)
}

// foldableOverForceSession builds a transcript whose bulk is assistant text
// (prune cannot reclaim it), over the 0.5 fold trigger of a 5000-token window.
func foldableOverForceSession(t *testing.T) *agent.Session {
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

// gateSingleFlightController wires a real controller + foldable agent whose
// summary pass blocks until the test releases it.
func gateSingleFlightController(t *testing.T, prov *gateFoldProvider) (*Controller, string) {
	t.Helper()
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	sess := foldableOverForceSession(t)
	ag := agent.New(prov, tool.NewRegistry(), sess, agent.Options{
		ContextWindow:     5000,
		CompactRatio:      0.5,
		CompactForceRatio: 0.5,
		RecentKeep:        2,
		ArchiveDir:        t.TempDir(),
	}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: event.Discard, SessionPath: sessionPath})
	return c, sessionPath
}

// startBackgroundCompaction mirrors the 549/kickProjectionRebuild shape: a
// maintenance transaction with NO running turn — the exact admission hole the
// gate closes. Returns a done channel closed when Prepare returns.
func startBackgroundCompaction(c *Controller, ag *agent.Agent) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ag.PrepareContext(context.Background())
	}()
	return done
}

func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 验收①: compaction in flight (no running turn) + user submit → parked, the
// summary is not disturbed, and the turn only executes after the pass settles.
func TestTask920SubmitParksDuringInFlightCompaction(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	c, sessionPath := gateSingleFlightController(t, prov)

	compactionDone := startBackgroundCompaction(c, c.executor)
	<-prov.entered // the summary request is in flight

	text := "920 验收①：压缩在途提交必须排队压完才执行"
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
	// The gate must hold: the turn has not started and the user text has not
	// reached the transcript — injection here is what burns the pass.
	if c.RuntimeStatus().Running {
		t.Fatal("parked submit started a turn while compaction was in flight")
	}
	if jsonlUserTextExists(t, sessionPath, text) {
		t.Fatal("user text reached the transcript while compaction was in flight")
	}

	close(prov.release) // summary finishes → install → settle → drain
	<-compactionDone
	<-submitDone
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}

// 验收②: no compaction in flight → the submit admits immediately, no parking,
// no extra wait. The gate is a single atomic read on the ordinary path.
func TestTask920NoCompactionNoDelay(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	close(prov.release) // never blocks anything
	c, sessionPath := gateSingleFlightController(t, prov)

	text := "920 验收②：无压缩时提交零额外等待"
	c.SubmitDisplay(text, text)
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)

	c.mu.Lock()
	parked := len(c.parkedTurns)
	c.mu.Unlock()
	if parked != 0 {
		t.Fatalf("no-compaction submit parked %d bodies, want 0", parked)
	}
	if c.executor.CompactionInFlight() {
		t.Fatal("CompactionInFlight must be false on the ordinary path")
	}
}

// 验收③: two submits during one in-flight compaction drain in FIFO order —
// the settle hook starts the oldest, finishGuardedTurn drains the next.
func TestTask920FIFOAcrossCompaction(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	c, sessionPath := gateSingleFlightController(t, prov)

	compactionDone := startBackgroundCompaction(c, c.executor)
	<-prov.entered

	first := "920 验收③：FIFO 第一条"
	second := "920 验收③：FIFO 第二条"
	// Park sequentially so the arrival order — and therefore the expected
	// drain order — is deterministic.
	go c.SubmitDisplay(first, first)
	waitCond(t, "first submit parked", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parkedTurns) == 1
	})
	go c.SubmitDisplay(second, second)
	waitCond(t, "both submits parked", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parkedTurns) == 2
	})

	close(prov.release)
	<-compactionDone
	waitForUserTextInJsonl(t, sessionPath, first)
	waitForUserTextInJsonl(t, sessionPath, second)
	waitIdleAdmission(t, c)

	seen := prov.turns()
	var firstIdx, secondIdx = -1, -1
	for i, s := range seen {
		if strings.Contains(s, "FIFO 第一条") && firstIdx < 0 {
			firstIdx = i
		}
		if strings.Contains(s, "FIFO 第二条") && secondIdx < 0 {
			secondIdx = i
		}
	}
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("parked turns never executed: %v", seen)
	}
	if firstIdx > secondIdx {
		t.Fatalf("FIFO violated: first=%d second=%d (order %v)", firstIdx, secondIdx, seen)
	}
}

// S3: a FAILED compaction still settles the gate — the parked turn releases,
// nothing strands. The settle hook rides the deferred release, not success.
func TestTask920FailedCompactionReleasesParked(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{}), failSummary: true}
	c, sessionPath := gateSingleFlightController(t, prov)

	compactionDone := startBackgroundCompaction(c, c.executor)
	<-prov.entered

	text := "920 S3：压缩失败后排队轮必须放行"
	submitDone := make(chan struct{})
	go func() {
		defer close(submitDone)
		c.SubmitDisplay(text, text)
	}()
	waitCond(t, "submit parked behind failing compaction", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parkedTurns) == 1
	})

	close(prov.release)
	<-compactionDone
	<-submitDone
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}

// S3: a park-path interrupt that lands during an in-flight compaction must
// NOT cancel the running turn mid-summary (that burns the pass — the exact
// shape 912 repaired); it fires when the pass settles, then the turn unwinds
// and the parked submit drains.
func TestTask920InterruptDeferredUntilCompactionSettles(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	c, sessionPath := gateSingleFlightController(t, prov)

	started := make(chan struct{})
	unwound := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(unwound)
		return ctx.Err()
	})
	<-started

	compactionDone := startBackgroundCompaction(c, c.executor)
	<-prov.entered

	text := "920 S3：interrupt 延迟到压缩收尾"
	submitDone := make(chan struct{})
	go func() {
		defer close(submitDone)
		c.SubmitDisplay(text, text)
	}()
	waitCond(t, "submit parked behind running turn + compaction", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.parkedTurns) == 1
	})

	// The interrupt must NOT have fired: the running turn is still alive and
	// the summary is undisturbed.
	select {
	case <-unwound:
		t.Fatal("running turn was cancelled while the compaction pass was in flight")
	case <-time.After(200 * time.Millisecond):
	}

	close(prov.release)
	<-compactionDone
	// Settle fires the deferred interrupt → the turn unwinds → the parked
	// body drains and lands.
	select {
	case <-unwound:
	case <-time.After(15 * time.Second):
		t.Fatal("deferred interrupt never fired after the compaction settled")
	}
	<-submitDone
	waitForUserTextInJsonl(t, sessionPath, text)
	waitIdleAdmission(t, c)
}

// settle 钩子的空队列合同：fast-path 之外，钩子还可能在没有任何 park 的场合
// fire（普通轮结束、手动 compact 收尾）。空队列 settle 必须是无副作用 no-op。
func TestTask920CompactionSettledWithEmptyQueueIsNoop(t *testing.T) {
	prov := &gateFoldProvider{entered: make(chan struct{}), release: make(chan struct{})}
	close(prov.release)
	c, _ := gateSingleFlightController(t, prov)

	c.onCompactionSettled()
	c.onCompactionSettled()
	c.mu.Lock()
	parked := len(c.parkedTurns)
	running := c.running
	c.mu.Unlock()
	if parked != 0 || running {
		t.Fatalf("settle with empty queue mutated state (parked=%d running=%v)", parked, running)
	}
}
