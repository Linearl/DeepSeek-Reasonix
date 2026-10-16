package agent

// 任务920 压缩单飞门（agent 半）：真实的 prune/fold/rescue 维护事务必须把
// CompactionInFlight 顶起、在每个出口（含失败、含 panic 的 defer 路径）归零
// 并触发宿主 settle 钩子；快路径（低于触发线）一个纳秒都不顶——验收②「无
// 压缩时零额外等待」的门半。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// gateRecordingProvider replies with a normal text turn; compaction never
// reaches it because the session sits below the fold trigger.
type gateRecordingProvider struct{}

func (p *gateRecordingProvider) Name() string { return "gate-recording" }

func (p *gateRecordingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// 验收②: a below-trigger Prepare is a fast path — it must never raise the
// gate nor fire the settle hook. That is what keeps ordinary sampling rounds
// at zero extra admission cost.
func TestTask920FastPathNeverRaisesGate(t *testing.T) {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "small ask"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "small answer"})
	a := New(&gateRecordingProvider{}, tool.NewRegistry(), sess, Options{
		ContextWindow: 100_000,
		CompactRatio:  0.8,
		RecentKeep:    2,
		ArchiveDir:    t.TempDir(),
	}, event.Discard)

	var settles atomic.Int32
	a.SetCompactionSettledHook(func() { settles.Add(1) })

	if a.CompactionInFlight() {
		t.Fatal("CompactionInFlight must start false")
	}
	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("fast-path Prepare: %v", err)
	}
	if a.CompactionInFlight() {
		t.Fatal("below-trigger fast path must not raise the compaction gate")
	}
	if got := settles.Load(); got != 0 {
		t.Fatalf("fast path fired settle hook %d times, want 0", got)
	}
}

// 验收①的信号半: a real fold transaction raises the gate for the whole
// maintenance window and the settle hook fires exactly once on release. The
// counter is checked through the summary stream, i.e. the exact window during
// which an injected turn would burn the pass.
func TestTask920RealFoldRaisesGateAndSettlesOnce(t *testing.T) {
	probe := &gateFoldProbe{reply: "summary"}
	sess := foldableSessionOverForce(6)
	a := New(probe, tool.NewRegistry(), sess, Options{
		ContextWindow:     5_000,
		CompactRatio:      0.5,
		CompactForceRatio: 0.5,
		RecentKeep:        2,
		ArchiveDir:        t.TempDir(),
	}, event.Discard)
	probe.a = a

	var settles atomic.Int32
	a.SetCompactionSettledHook(func() { settles.Add(1) })

	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatalf("fold Prepare: %v", err)
	}
	if probe.sawUp.Load() == 0 {
		t.Fatal("summary request never observed the gate up — the pass would be injectable")
	}
	if probe.sawDown.Load() != 0 {
		t.Fatalf("summary request observed the gate DOWN %d times — injection would burn the pass", probe.sawDown.Load())
	}
	if a.CompactionInFlight() {
		t.Fatal("gate must be down after the transaction returned")
	}
	if got := settles.Load(); got != 1 {
		t.Fatalf("settle hook fired %d times, want exactly 1", got)
	}
}

// gateFoldProbe records the gate state at the summarizer request — the only
// vantage point that matters: injection between snapshot and install is
// exactly what burns a pass (errCompressStaleContext).
type gateFoldProbe struct {
	reply   string
	a       *Agent
	sawUp   atomic.Int32
	sawDown atomic.Int32
}

func (p *gateFoldProbe) Name() string { return "gate-fold-probe" }

func (p *gateFoldProbe) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if isSummaryRequest(req) && p.a != nil {
		if p.a.CompactionInFlight() {
			p.sawUp.Add(1)
		} else {
			p.sawDown.Add(1)
		}
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: p.reply}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// 验收②/S3 的门半: a failed summary still releases the gate and fires the
// settle hook exactly once — a parked turn can never outlive a failed pass.
func TestTask920FailedFoldStillSettles(t *testing.T) {
	probe := &gateFailProbe{}
	sess := foldableSessionOverForce(6)
	a := New(probe, tool.NewRegistry(), sess, Options{
		ContextWindow:     5_000,
		CompactRatio:      0.5,
		CompactForceRatio: 0.5,
		RecentKeep:        2,
		ArchiveDir:        t.TempDir(),
	}, event.Discard)

	var settles atomic.Int32
	a.SetCompactionSettledHook(func() { settles.Add(1) })

	// A terminal summary failure: the transaction returns an error (or a
	// degraded view) but in both shapes the deferred release must run.
	_, _ = a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure})

	if a.CompactionInFlight() {
		t.Fatal("failed pass must release the gate (defer path)")
	}
	if got := settles.Load(); got != 1 {
		t.Fatalf("settle hook fired %d times after failure, want exactly 1", got)
	}
}

type gateFailProbe struct{}

func (p *gateFailProbe) Name() string { return "gate-fail-probe" }

func (p *gateFailProbe) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	return nil, errors.New("terminal summarizer refusal")
}
