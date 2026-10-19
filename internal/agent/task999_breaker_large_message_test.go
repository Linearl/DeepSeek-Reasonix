package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务999 风险联动 (issue #59 / 任务929): raising the inbox single-item gate
// to 16 MiB means a turn can now carry a ~16 MiB prompt into the provider —
// exactly the shape that triggered the incident's HTTP/2 RST storm. This test
// pins the compatibility claim: the deterministic-reject breaker is keyed on
// failure reason and attempt DURATIONS only — both independent of request
// size — so a 16 MiB request must still stop the loop after the streak and
// emit the actionable notice instead of replaying another full prefill.
func TestTask999DeterministicBreakerSurvives16MiBRequest(t *testing.T) {
	shrinkDeterministicBreakerForTest(t, 10*time.Second) // production jitter window; streak stays 3
	prov := &breakerScriptProvider{err: http2RSTError()}
	sink := &recordingSink{}
	sess := NewSession("")
	// 16 MiB user turn — the new ceiling's worst case (任务999 raised
	// DefaultMaxItemBytes to exactly this).
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: strings.Repeat("x", 16<<20)}})
	// Window sized above the payload (~4M tokens for 16 MiB of text): an
	// undersized window drags compaction maintenance into the retry loop and
	// the provider call count stops measuring the breaker alone.
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 5_000_000, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25,
	}, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail on the deterministic rejection")
	}
	if calls := prov.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 — a 16MiB request must not slip past the deterministic breaker", calls)
	}
	found := false
	for _, c := range noticeCodes(sink) {
		if c == event.NoticeCodeProviderDeterministicReject {
			found = true
		}
	}
	if !found {
		t.Fatalf("notice codes = %v, want %s on the 16MiB path", noticeCodes(sink), event.NoticeCodeProviderDeterministicReject)
	}
}

// The converse direction: a LARGE jittered failure set on a 16 MiB request
// keeps the historical retry loop — size must not leak into the jitter
// judgement either.
func TestTask999LargeRequestKeepsJitterSemantics(t *testing.T) {
	shrinkDeterministicBreakerForTest(t, time.Millisecond) // spread below this = deterministic
	prov := &breakerScriptProvider{
		err:    http2RSTError(),
		delays: []time.Duration{0, 5 * time.Millisecond, 10 * time.Millisecond, 0},
	}
	sink := &recordingSink{}
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: strings.Repeat("y", 16<<20)}})
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 5_000_000, CompactRatio: 2, MaxOutputTokens: 0, Temperature: 0.25,
	}, sink)

	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected the turn to fail at the attempt cap")
	}
	if calls := prov.callCount(); calls != 4 {
		t.Fatalf("provider calls = %d, want 4 — jittered failures on a large request keep retrying", calls)
	}
	for _, c := range noticeCodes(sink) {
		if c == event.NoticeCodeProviderDeterministicReject {
			t.Fatalf("jittered 16MiB failures emitted the deterministic-reject notice (code %s)", c)
		}
	}
}
