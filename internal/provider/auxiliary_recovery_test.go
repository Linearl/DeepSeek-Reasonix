package provider

import (
	"context"
	"strings"
	"testing"
	"time"
)

type auxiliaryScript struct {
	calls   int
	partial bool
}

func (*auxiliaryScript) Name() string { return "auxiliary-test" }
func (p *auxiliaryScript) Stream(context.Context, Request) (<-chan Chunk, error) {
	p.calls++
	if !p.partial && p.calls < 4 {
		return nil, &APIError{Status: 503}
	}
	ch := make(chan Chunk, 3)
	ch <- Chunk{Type: ChunkText, Text: "candidate"}
	ch <- Chunk{Type: ChunkUsage, Usage: &Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}}
	if !p.partial {
		ch <- Chunk{Type: ChunkDone}
	}
	close(ch)
	return ch, nil
}
func TestAuxiliaryRetriesShareFiniteBudgetAndDoNotLeakPartialText(t *testing.T) {
	for _, partial := range []bool{false, true} {
		p := &auxiliaryScript{partial: partial}
		var waits []time.Duration
		ctx := WithRecoverySleeper(context.Background(), func(_ context.Context, d time.Duration) bool { waits = append(waits, d); return true })
		ch, err := StreamAuxiliary(ctx, p, Request{})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		var usage *Usage
		var streamErr error
		for c := range ch {
			switch c.Type {
			case ChunkText:
				text.WriteString(c.Text)
			case ChunkUsage:
				usage = c.Usage
			case ChunkError:
				streamErr = c.Err
			}
		}
		if p.calls != 4 || len(waits) != 3 {
			t.Fatalf("calls=%d waits=%v", p.calls, waits)
		}
		for i, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
			if waits[i] != want {
				t.Fatalf("waits=%v", waits)
			}
		}
		if usage == nil || usage.RequestCount != 4 {
			t.Fatalf("usage=%+v", usage)
		}
		if partial {
			if text.Len() != 0 || streamErr == nil || usage.PromptTokens != 40 {
				t.Fatalf("partial text=%q err=%v usage=%+v", text.String(), streamErr, usage)
			}
		} else if text.String() != "candidate" || streamErr != nil || !usage.Unknown || usage.PromptTokens != 10 {
			t.Fatalf("text=%q err=%v usage=%+v", text.String(), streamErr, usage)
		}
	}
}

// bypassScript streams a failing partial attempt (text+reasoning, then a
// retryable mid-stream error) followed by a successful attempt — the exact
// shape the tl1751 decoupling must hold: the progress bypass sees the failed
// attempt's partial text live, while the out channel never does.
type bypassScript struct {
	calls int
}

func (*bypassScript) Name() string { return "auxiliary-bypass-test" }
func (p *bypassScript) Stream(context.Context, Request) (<-chan Chunk, error) {
	p.calls++
	ch := make(chan Chunk, 4)
	if p.calls == 1 {
		ch <- Chunk{Type: ChunkText, Text: "p1"}
		ch <- Chunk{Type: ChunkReasoning, Text: "p2"}
		ch <- Chunk{Type: ChunkError, Err: &APIError{Status: 503}}
	} else {
		ch <- Chunk{Type: ChunkText, Text: "full"}
		ch <- Chunk{Type: ChunkUsage, Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
		ch <- Chunk{Type: ChunkDone}
	}
	close(ch)
	return ch, nil
}

// tl1751: the WithStreamProgress bypass must feed chunks to the readout the
// moment they ARRIVE — including a failed attempt's partial text, which is
// the live signal the 130.8s compaction never showed — while the out channel
// keeps its retry atomicity (failed partial summaries never leak). One test
// nails both sides of the decoupling: bypass sees ["p1" "p2" "full"], out
// sees only "full".
func TestAuxiliaryStreamProgressBypassSeesLiveChunksWithoutLeaking(t *testing.T) {
	p := &bypassScript{}
	var waits []time.Duration
	var progress []string
	ctx := WithRecoverySleeper(
		WithStreamProgress(context.Background(), func(text string) { progress = append(progress, text) }),
		func(_ context.Context, d time.Duration) bool { waits = append(waits, d); return true },
	)
	ch, err := StreamAuxiliary(ctx, p, Request{})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var usage *Usage
	for c := range ch {
		switch c.Type {
		case ChunkText, ChunkReasoning:
			text.WriteString(c.Text)
		case ChunkUsage:
			usage = c.Usage
		case ChunkError:
			t.Fatalf("content channel leaked an error: %v", c.Err)
		}
	}
	if p.calls != 2 || len(waits) != 1 || waits[0] != 2*time.Second {
		t.Fatalf("calls=%d waits=%v", p.calls, waits)
	}
	// Bypass side: the failed attempt's partial text WAS fed live, before the
	// retry started — this is the readout feed the pre-tl1751 drain-only path
	// could never provide. Usage/done chunks stay off the bypass (rune count
	// of text only).
	if got, want := strings.Join(progress, "|"), "p1|p2|full"; got != want {
		t.Fatalf("progress=%q want=%q", got, want)
	}
	// Content side: only the successful attempt's summary leaves the buffer —
	// the failed attempt's "p1"/"p2" never did.
	if text.String() != "full" {
		t.Fatalf("out text=%q want=%q — failed attempt partials leaked", text.String(), "full")
	}
	// Usage merge: the failed attempt reported no usage (Unknown stays set,
	// baseline semantics), the success does — and RequestCount still reflects
	// both attempts.
	if usage == nil || usage.RequestCount != 2 || usage.PromptTokens != 10 || !usage.Unknown {
		t.Fatalf("usage=%+v want RequestCount=2 PromptTokens=10 Unknown=true", usage)
	}
}
