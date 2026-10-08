package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// destCountProvider counts the Stream calls that reach the wrapped destination,
// so tests can assert exactly how many summary requests each model served.
type destCountProvider struct {
	provider.Provider
	calls atomic.Int32
}

func (p *destCountProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.calls.Add(1)
	return p.Provider.Stream(ctx, req)
}

// switchingProvider runs one side effect on its first Stream call — the test
// stand-in for "the model switch landed while the summary was in flight".
type switchingProvider struct {
	fakeProvider
	onStream func()
	armed    bool
}

func (p *switchingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if p.onStream != nil && !p.armed {
		p.armed = true
		p.onStream()
	}
	return p.fakeProvider.Stream(ctx, req)
}

// 任务635 P0 验收②：摘要请求带会话当前模型名。The summary wire model is
// construction-bound on the destination adapter, so "the current model" means
// providerForRequest: an armed session override must receive the summary, not
// the construction provider the projection was built with.
func TestSummaryRequestFollowsModelOverride(t *testing.T) {
	home := &destCountProvider{Provider: &fakeProvider{reply: "home digest"}}
	alt := &destCountProvider{Provider: &namedFakeProvider{name: "other", fakeProvider: fakeProvider{reply: "alt digest"}}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"other/b": alt,
	}}
	sess := foldableSessionOverForce(3)
	a := New(home, nil, sess, Options{ModelResolver: resolver, ContextWindow: 5000, ArchiveDir: t.TempDir()}, event.Discard)
	if !a.SetSessionModelOverride("other/b", ModelOverrideExtras{}) {
		t.Fatal("override declined; the resolver seam is not wired")
	}

	summary, _, err := a.summarize(context.Background(), sess.Messages[1:], "")
	if err != nil {
		t.Fatalf("summarize = %v", err)
	}
	if !strings.Contains(summary, "alt digest") {
		t.Fatalf("summary = %q, want the override destination's reply", summary)
	}
	if got := alt.calls.Load(); got != 1 {
		t.Fatalf("override destination served %d summary requests, want 1", got)
	}
	if got := home.calls.Load(); got != 0 {
		t.Fatalf("construction destination served %d summary requests, want 0", got)
	}
}

// 任务635 P0 验收③：400 重试一次有替代出口。The incident shape: the summary
// in flight on the projection-era destination draws a status-400 while the tab
// switches to the current model; the lane retries once and the CURRENT
// destination completes the fold instead of the input hash parking deadlocked.
func TestSummaryRetriesOnceOnCurrentModelAfter400Switch(t *testing.T) {
	alt := &destCountProvider{Provider: &namedFakeProvider{name: "current", fakeProvider: fakeProvider{reply: "current digest"}}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"current/c": alt,
	}}
	sess := foldableSessionOverForce(3)
	// stale serves attempt 1 and arms the override mid-request — the switch
	// landing while the doomed request is on the wire.
	stale := &destCountProvider{}
	var a *Agent
	stale.Provider = &switchingProvider{
		fakeProvider: fakeProvider{
			streamErr: &provider.APIError{Status: 400, Provider: "opencode-go", Body: `{"model":"gpt-5.6-luna"}`},
		},
		onStream: func() {
			if a != nil {
				a.SetSessionModelOverride("current/c", ModelOverrideExtras{})
			}
		},
	}
	a = New(stale, nil, sess, Options{ModelResolver: resolver, ContextWindow: 5000, ArchiveDir: t.TempDir()}, event.Discard)

	res, _, err := a.summarizeFold(context.Background(), CompactionTriggerManual,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err != nil {
		t.Fatalf("summarizeFold = %v, want the retry on the current model to succeed", err)
	}
	if !strings.Contains(res.Text, "current digest") {
		t.Fatalf("summary = %q, want the current destination's reply", res.Text)
	}
	if got := stale.calls.Load(); got != 1 {
		t.Fatalf("stale destination served %d requests, want exactly 1 (one 400, no blind redial)", got)
	}
	if got := alt.calls.Load(); got != 1 {
		t.Fatalf("current destination served %d requests, want exactly 1 (retry once)", got)
	}
}

// 任务635 P0 反向钉子：destination 未变时同一个 400 是确定性拒绝，重试只会
// 重复花费——必须保持既有停试语义，恰好一次 provider 调用。
func TestSummary400SameDestinationNoRetry(t *testing.T) {
	solo := &destCountProvider{Provider: &fakeProvider{
		streamErr: errors.New("OpenCode Go: Responses: status 400: {\"model\":\"gpt-5.6-luna\"}"),
	}}
	sess := foldableSessionOverForce(3)
	a := New(solo, nil, sess, Options{ContextWindow: 5000, ArchiveDir: t.TempDir()}, event.Discard)

	_, _, err := a.summarizeFold(context.Background(), CompactionTriggerManual,
		sess.Messages, "", 100, SummaryInputSlim, foldRequest{})
	if err == nil {
		t.Fatal("summarizeFold = nil, want the 400 to surface")
	}
	if got := solo.calls.Load(); got != 1 {
		t.Fatalf("destination served %d requests, want exactly 1 — an unchanged destination makes the 400 deterministic", got)
	}
}

// 任务635 验收（复现场景三步之三的端到端面）：手动压缩跨一次在飞模型切换，
// 请求落到当前模型并成功落地投影，而不是带着旧模型名失败后停摆。
func TestManualCompactAcrossMidFlightModelSwitchLandsOnCurrentModel(t *testing.T) {
	alt := &destCountProvider{Provider: &namedFakeProvider{name: "current", fakeProvider: fakeProvider{reply: "switched digest"}}}
	resolver := &fakeModelResolver{providers: map[string]provider.Provider{
		"current/c": alt,
	}}
	sess := foldableSessionOverForce(6)
	var a *Agent
	stale := &destCountProvider{Provider: &switchingProvider{
		fakeProvider: fakeProvider{
			streamErr: &provider.APIError{Status: 400, Provider: "opencode-go", Body: `{"model":"gpt-5.6-luna"}`},
		},
		onStream: func() {
			if a != nil {
				a.SetSessionModelOverride("current/c", ModelOverrideExtras{})
			}
		},
	}}
	a = New(stale, nil, sess, Options{
		ModelResolver: resolver, ContextWindow: 5000,
		CompactRatio: 0.5, CompactForceRatio: 0.5, RecentKeep: 2,
		ArchiveDir: t.TempDir(),
	}, event.Discard)

	if err := prepareContext(context.Background(), a, CompactionTriggerManual); err != nil {
		t.Fatalf("manual compact across the switch = %v, want the fold to land on the current model", err)
	}
	if got := alt.calls.Load(); got != 1 {
		t.Fatalf("current destination served %d summary requests, want 1", got)
	}
	if after := projectionTokens(a); after == 0 {
		t.Fatalf("no projection landed; receipt = %+v", a.sess.compactionState.LastReceipt)
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied compaction", r)
	}
}
