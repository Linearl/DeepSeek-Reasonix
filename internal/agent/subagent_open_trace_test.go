package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1293: the open-window trace is a pure timing instrument. These tests pin
// the accounting (segment-close marks, first-wins event classification,
// single emission, CheckedSink passthrough) so the "subagent: open segments"
// line keeps an honest shape when the surrounding code evolves.

func newTestOpenTrace() *subagentOpenTrace {
	return newSubagentOpenTrace("fg")
}

func TestSubagentOpenSegmentsMarkAccounting(t *testing.T) {
	segs := newSubagentOpenSegments()
	base := segs.last
	segs.markAt("registry", 2*time.Millisecond)
	segs.markAt("transcript", 3*time.Millisecond)
	if got := segs.last.Sub(base); got != 5*time.Millisecond {
		t.Fatalf("last = %v, want start+5ms", got)
	}
	summary := segs.summary()
	for _, want := range []string{"registry=2ms", "transcript=3ms", "total="} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
}

// comparableSink is a comparable concrete Sink for identity assertions.
type comparableSink struct{ events []event.Event }

func (s *comparableSink) Emit(e event.Event) { s.events = append(s.events, e) }

func TestSubagentOpenNilReceiverSafe(t *testing.T) {
	var tr *subagentOpenTrace
	tr.mark("registry")
	tr.describe("name", "ref")
	tr.observe(event.Event{Kind: event.Text})
	tr.finish()
	inner := &comparableSink{}
	if got := tr.wrapSink(inner); got != event.Sink(inner) {
		t.Fatalf("wrapSink on nil trace did not return the inner sink unchanged")
	}
	if got := subagentOpenTraceFromContext(context.Background()); got != nil {
		t.Fatalf("trace from plain ctx = %v, want nil", got)
	}
}

func TestSubagentOpenTraceRequestPhases(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	tr := newTestOpenTrace()
	tr.describe("explore", "sub-20261031-1")

	// Local assembly marks land before the request phases.
	tr.mark("registry")
	tr.mark("transcript")
	tr.mark("runtime")
	tr.mark("slot")
	tr.mark("dispatch")

	// Connecting attempt=1: the request went out — closes "prepare".
	tr.observe(event.Event{Kind: event.Connecting, RetryAttempt: 1})
	// A duplicate attempt frame must not re-mark.
	tr.observe(event.Event{Kind: event.Connecting, RetryAttempt: 2})
	// Clear frame (attempt=0): headers arrived — closes "rtt".
	tr.observe(event.Event{Kind: event.Connecting, RetryAttempt: 0})
	// First content closes "first_out" and emits exactly once.
	tr.observe(event.Event{Kind: event.Reasoning, Text: "thinking"})
	tr.observe(event.Event{Kind: event.Text, Text: "answer"})

	tr.mu.Lock()
	emitted, segs := tr.emitted, tr.segs
	tr.mu.Unlock()
	if !emitted {
		t.Fatalf("trace did not emit on first content event")
	}
	summary := segs.summary()
	for _, want := range []string{"registry=", "transcript=", "runtime=", "slot=", "dispatch=", "prepare=", "rtt=", "first_out="} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q missing %q", summary, want)
		}
	}
	// Second finish (error paths) is a no-op after emission.
	tr.finish()
	tr.mu.Lock()
	emitted = tr.emitted
	tr.mu.Unlock()
	if !emitted {
		t.Fatalf("emitted flag lost")
	}
	if n := strings.Count(buf.String(), "subagent: open segments"); n != 1 {
		t.Fatalf("summary line count = %d, want 1\nlog: %s", n, buf.String())
	}
	if !strings.Contains(buf.String(), `name=explore`) || !strings.Contains(buf.String(), `ref=sub-20261031-1`) || !strings.Contains(buf.String(), `mode=fg`) {
		t.Fatalf("summary line missing identity fields\nlog: %s", buf.String())
	}
}

func TestSubagentOpenTraceClearFrameWithoutRequestIgnored(t *testing.T) {
	tr := newTestOpenTrace()
	// A stray clear frame (older emitter shape) must not fabricate an rtt
	// segment before the request went out.
	tr.observe(event.Event{Kind: event.Connecting, RetryAttempt: 0})
	tr.observe(event.Event{Kind: event.Text, Text: "answer"})
	tr.mu.Lock()
	defer tr.mu.Unlock()
	summary := tr.segs.summary()
	if strings.Contains(summary, "rtt=") {
		t.Fatalf("rtt fabricated without request-out: %q", summary)
	}
	if !strings.Contains(summary, "first_out=") {
		t.Fatalf("first_out missing: %q", summary)
	}
}

func TestSubagentOpenTraceFinishEmitsWhenNoOutput(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	tr := newTestOpenTrace()
	tr.describe("failing-run", "")
	tr.mark("registry")
	tr.finish()
	tr.finish() // idempotent
	if n := strings.Count(buf.String(), "subagent: open segments"); n != 1 {
		t.Fatalf("summary line count = %d, want 1\nlog: %s", n, buf.String())
	}
}

// failingSink fails EmitChecked so the test can see which path the wrapper
// takes; it records every event it observed.
type failingSink struct {
	events []event.Event
}

func (f *failingSink) Emit(e event.Event) { f.events = append(f.events, e) }

func (f *failingSink) EmitChecked(e event.Event) error {
	f.events = append(f.events, e)
	return errors.New("durability failure")
}

func TestSubagentOpenTraceSinkPassthrough(t *testing.T) {
	tr := newTestOpenTrace()
	inner := &failingSink{}
	wrapped := tr.wrapSink(inner)

	ev := event.Event{Kind: event.Text, Text: "hello"}
	wrapped.Emit(ev)
	if len(inner.events) != 1 || inner.events[0].Text != "hello" {
		t.Fatalf("Emit did not forward unchanged: %+v", inner.events)
	}
	if err := event.EmitChecked(wrapped, ev); err == nil {
		t.Fatalf("EmitChecked lost the inner sink's CheckedSink capability")
	}
	if len(inner.events) != 2 {
		t.Fatalf("EmitChecked did not reach inner: %+v", inner.events)
	}
	// Both paths observed the same event exactly once each.
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.sawFirstOutput {
		t.Fatalf("wrapper did not observe the content event")
	}
}

func TestSubagentOpenTraceContextRoundTrip(t *testing.T) {
	tr := newTestOpenTrace()
	ctx := withSubagentOpenTrace(context.Background(), tr)
	if got := subagentOpenTraceFromContext(ctx); got != tr {
		t.Fatalf("context round trip = %v, want the attached trace", got)
	}
}
